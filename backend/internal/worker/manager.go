//go:build linux

package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"webfile/internal/identity"
	"webfile/internal/launcher"
	"webfile/internal/thumbnailcache"
)

type Process struct {
	Identity   identity.Identity
	Socket     string
	Cmd        *exec.Cmd
	Proxy      *httputil.ReverseProxy
	remote     bool
	cacheToken string
	done       <-chan struct{}
}

type Manager struct {
	mu             sync.Mutex
	workers        map[uint32]*Process
	elevatedTokens map[string]string
	executable     string
	configPath     string
	workerDir      string
	launcherSocket string
	cacheSocket    string
	cacheAuth      *thumbnailcache.Authorizer
	closed         bool
}

func NewManager(executable, configPath, workerDir, launcherSocket, cacheSocket string, cacheAuth *thumbnailcache.Authorizer) *Manager {
	return &Manager{
		workers:        map[uint32]*Process{},
		elevatedTokens: make(map[string]string),
		executable:     executable,
		configPath:     configPath,
		workerDir:      workerDir,
		launcherSocket: launcherSocket,
		cacheSocket:    cacheSocket,
		cacheAuth:      cacheAuth,
	}
}

func (m *Manager) Ensure(ctx context.Context, id identity.Identity) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("worker manager is closed")
	}
	if p := m.workers[id.UID]; p != nil && p.alive() {
		return p, nil
	}
	if previous := m.workers[id.UID]; previous != nil {
		m.cacheAuth.Revoke(previous.cacheToken)
		delete(m.workers, id.UID)
	}
	cacheToken, err := m.cacheAuth.Issue(id.UID)
	if err != nil {
		return nil, fmt.Errorf("issue thumbnail cache capability: %w", err)
	}

	var (
		p *Process
	)
	if uint32(os.Geteuid()) != id.UID && os.Geteuid() != 0 {
		p, err = m.startViaLauncherLocked(ctx, id, cacheToken)
	} else {
		p, err = m.startLocalLocked(ctx, id, cacheToken)
	}
	if err != nil {
		m.cacheAuth.Revoke(cacheToken)
		return nil, err
	}
	p.cacheToken = cacheToken
	m.workers[id.UID] = p
	return p, nil
}

func (m *Manager) Elevate(ctx context.Context, id identity.Identity, password string) (*Process, string, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, "", errors.New("worker manager is closed")
	}
	if m.launcherSocket == "" {
		return nil, "", errors.New("filesystem elevation requires the privileged launcher")
	}
	token, err := launcher.RandomElevationToken()
	if err != nil {
		return nil, "", fmt.Errorf("create elevation capability: %w", err)
	}
	cacheToken, err := m.cacheAuth.Issue(0)
	if err != nil {
		return nil, "", fmt.Errorf("issue elevated thumbnail cache capability: %w", err)
	}
	socket, err := launcher.ClientElevate(ctx, m.launcherSocket, id.Username, password, token, cacheToken)
	if err != nil {
		m.cacheAuth.Revoke(cacheToken)
		return nil, "", err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.cacheAuth.Revoke(cacheToken)
		_ = launcher.ClientDropElevation(ctx, m.launcherSocket, token)
		return nil, "", errors.New("worker manager is closed")
	}
	m.elevatedTokens[token] = cacheToken
	m.mu.Unlock()
	return &Process{Identity: id, Socket: socket, Proxy: proxyForSocket(socket), remote: true}, token, nil
}

func (m *Manager) DropElevation(ctx context.Context, token string) error {
	if token == "" || m.launcherSocket == "" {
		return nil
	}
	err := launcher.ClientDropElevation(ctx, m.launcherSocket, token)
	if err == nil {
		m.mu.Lock()
		cacheToken := m.elevatedTokens[token]
		delete(m.elevatedTokens, token)
		m.mu.Unlock()
		m.cacheAuth.Revoke(cacheToken)
	}
	return err
}

func (p *Process) alive() bool {
	if p == nil {
		return false
	}
	if p.Cmd != nil {
		select {
		case <-p.done:
			return false
		default:
			return p.Cmd.Process != nil
		}
	}
	conn, err := net.DialTimeout("unix", p.Socket, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	var first error
	for uid, p := range m.workers {
		m.cacheAuth.Revoke(p.cacheToken)
		if p.Cmd != nil && p.alive() {
			if err := p.Cmd.Process.Signal(syscall.SIGTERM); err != nil && first == nil {
				first = err
			}
		}
		delete(m.workers, uid)
	}
	for token, cacheToken := range m.elevatedTokens {
		m.cacheAuth.Revoke(cacheToken)
		delete(m.elevatedTokens, token)
	}
	return first
}

func (m *Manager) startViaLauncherLocked(ctx context.Context, id identity.Identity, cacheToken string) (*Process, error) {
	if m.launcherSocket == "" {
		return nil, errors.New("broker is unprivileged and no launcher socket is configured")
	}
	socket, err := launcher.ClientEnsure(ctx, m.launcherSocket, id.Username, cacheToken)
	if err != nil {
		return nil, fmt.Errorf("start worker via launcher: %w", err)
	}
	return &Process{Identity: id, Socket: socket, Proxy: proxyForSocket(socket), remote: true}, nil
}

func (m *Manager) startLocalLocked(ctx context.Context, id identity.Identity, cacheToken string) (*Process, error) {
	if m.executable == "" {
		return nil, errors.New("worker executable is empty")
	}
	if err := os.MkdirAll(m.workerDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(m.workerDir, 0o755); err != nil {
		return nil, err
	}
	userDir := filepath.Join(m.workerDir, strconv.FormatUint(uint64(id.UID), 10))
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(userDir, 0o700); err != nil {
		return nil, err
	}
	if uint32(os.Geteuid()) != id.UID {
		if err := os.Chown(userDir, int(id.UID), int(id.GID)); err != nil {
			return nil, fmt.Errorf("chown worker dir: %w", err)
		}
	}
	socket := filepath.Join(userDir, "worker.sock")
	_ = os.Remove(socket)

	cmd := exec.Command(m.executable,
		"worker",
		"-config", m.configPath,
		"-socket", socket,
		"-username", id.Username,
		"-home", id.Home,
		"-thumbnail-cache-socket", m.cacheSocket,
		"-thumbnail-cache-token", cacheToken,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = id.Home
	cmd.Env = append(os.Environ(), "HOME="+id.Home, "USER="+id.Username, "LOGNAME="+id.Username)
	if uint32(os.Geteuid()) != id.UID {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: id.UID, Gid: id.GID, Groups: id.Groups},
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start worker for %s: %w", id.Username, err)
	}
	if err := waitForSocket(ctx, socket, cmd, 5*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, err
	}

	done := make(chan struct{})
	p := &Process{Identity: id, Socket: socket, Cmd: cmd, Proxy: proxyForSocket(socket), done: done}
	go func(uid uint32, process *Process) {
		_ = cmd.Wait()
		close(done)
		m.mu.Lock()
		defer m.mu.Unlock()
		if current := m.workers[uid]; current == process {
			m.cacheAuth.Revoke(process.cacheToken)
			delete(m.workers, uid)
		}
	}(id.UID, p)
	return p, nil
}

func proxyForSocket(socket string) *httputil.ReverseProxy {
	transport := &http.Transport{
		DisableCompression: false,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	target, _ := url.Parse("http://worker")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "filesystem worker unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return proxy
}

func waitForSocket(ctx context.Context, socket string, cmd *exec.Cmd, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("worker did not create %s within %s", socket, timeout)
		case <-ticker.C:
			if cmd.ProcessState != nil {
				return errors.New("worker exited before becoming ready")
			}
			conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}
