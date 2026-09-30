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
)

type Process struct {
	Identity identity.Identity
	Socket   string
	Cmd      *exec.Cmd
	Proxy    *httputil.ReverseProxy
	remote   bool
}

type Manager struct {
	mu             sync.Mutex
	workers        map[uint32]*Process
	executable     string
	configPath     string
	workerDir      string
	launcherSocket string
	closed         bool
}

func NewManager(executable, configPath, workerDir, launcherSocket string) *Manager {
	return &Manager{
		workers:        map[uint32]*Process{},
		executable:     executable,
		configPath:     configPath,
		workerDir:      workerDir,
		launcherSocket: launcherSocket,
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
	delete(m.workers, id.UID)

	var (
		p   *Process
		err error
	)
	if uint32(os.Geteuid()) != id.UID && os.Geteuid() != 0 {
		p, err = m.startViaLauncherLocked(ctx, id)
	} else {
		p, err = m.startLocalLocked(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	m.workers[id.UID] = p
	return p, nil
}

func (p *Process) alive() bool {
	if p == nil {
		return false
	}
	if p.Cmd != nil {
		return p.Cmd.ProcessState == nil
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
		if p.Cmd != nil && p.Cmd.Process != nil && p.Cmd.ProcessState == nil {
			if err := p.Cmd.Process.Signal(syscall.SIGTERM); err != nil && first == nil {
				first = err
			}
		}
		delete(m.workers, uid)
	}
	return first
}

func (m *Manager) startViaLauncherLocked(ctx context.Context, id identity.Identity) (*Process, error) {
	if m.launcherSocket == "" {
		return nil, errors.New("broker is unprivileged and no launcher socket is configured")
	}
	socket, err := launcher.ClientEnsure(ctx, m.launcherSocket, id.Username)
	if err != nil {
		return nil, fmt.Errorf("start worker via launcher: %w", err)
	}
	return &Process{Identity: id, Socket: socket, Proxy: proxyForSocket(socket), remote: true}, nil
}

func (m *Manager) startLocalLocked(ctx context.Context, id identity.Identity) (*Process, error) {
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

	p := &Process{Identity: id, Socket: socket, Cmd: cmd, Proxy: proxyForSocket(socket)}
	go func(uid uint32, process *Process) {
		_ = cmd.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if current := m.workers[uid]; current == process {
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
