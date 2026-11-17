//go:build linux

package launcher

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"webfile/internal/auth"
	"webfile/internal/identity"
)

type Request struct {
	Action         string `json:"action"`
	Username       string `json:"username"`
	Password       string `json:"password,omitempty"`
	CacheToken     string `json:"cache_token,omitempty"`
	ElevationToken string `json:"elevation_token,omitempty"`
}

type Response struct {
	OK     bool   `json:"ok"`
	Socket string `json:"socket,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Server struct {
	Executable      string
	ConfigPath      string
	SocketPath      string
	WorkerDir       string
	BrokerGroup     string
	MinUserUID      uint32
	PAMService      string
	CacheSocket     string
	ElevationTTL    time.Duration
	ElevationGroups []string

	mu       sync.Mutex
	workers  map[uint32]workerProcess
	elevated map[string]workerProcess
}

type workerProcess struct {
	cmd        *exec.Cmd
	cacheToken string
	done       <-chan struct{}
}

func (worker workerProcess) alive() bool {
	if worker.cmd == nil || worker.cmd.Process == nil {
		return false
	}
	select {
	case <-worker.done:
		return false
	default:
		return true
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("launcher must run as root")
	}
	if s.SocketPath == "" || s.Executable == "" || s.ConfigPath == "" || s.WorkerDir == "" || s.CacheSocket == "" {
		return errors.New("launcher configuration is incomplete")
	}
	gid, err := groupID(s.BrokerGroup)
	if err != nil {
		return fmt.Errorf("launcher group: %w", err)
	}
	if s.MinUserUID == 0 {
		s.MinUserUID = 1000
	}
	s.workers = map[uint32]workerProcess{}
	s.elevated = map[string]workerProcess{}
	if s.ElevationTTL <= 0 {
		s.ElevationTTL = 5 * time.Minute
	}
	if err := os.MkdirAll(filepath.Dir(s.SocketPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.WorkerDir, 0o751); err != nil {
		return err
	}
	if err := os.Chmod(s.WorkerDir, 0o751); err != nil {
		return err
	}
	// Elevated workers are tied to this launcher process. Remove private state
	// left by an unclean prior launcher exit before accepting requests.
	_ = os.RemoveAll(filepath.Join(s.WorkerDir, "elevated"))
	_ = os.Remove(s.SocketPath)
	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(s.SocketPath)
	if err := os.Chown(s.SocketPath, 0, gid); err != nil {
		return err
	}
	if err := os.Chmod(s.SocketPath, 0o660); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		s.stopAll()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go s.handle(conn, gid)
	}
}

func (s *Server) handle(conn net.Conn, brokerGID int) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var req Request
	if err := json.NewDecoder(ioLimitReader(conn, 8<<10)).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: "invalid request"})
		return
	}
	if req.Action == "authenticate" {
		if req.Username == "" || req.Password == "" || s.PAMService == "" {
			_ = json.NewEncoder(conn).Encode(Response{Error: "invalid credentials"})
			return
		}
		if err := auth.AuthenticatePAM(context.Background(), s.PAMService, req.Username, req.Password); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{Error: "invalid credentials"})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true})
		return
	}
	if req.Action == "elevate" {
		if req.Username == "" || req.Password == "" || len(req.CacheToken) != 64 || !validToken(req.ElevationToken) || s.PAMService == "" {
			_ = json.NewEncoder(conn).Encode(Response{Error: "invalid elevation request"})
			return
		}
		id, err := identity.Lookup(req.Username)
		if err != nil || id.UID < s.MinUserUID || id.UID == 0 || !ElevationAllowed(id, s.ElevationGroups) {
			_ = json.NewEncoder(conn).Encode(Response{Error: "user is not allowed to elevate filesystem access"})
			return
		}
		if err := auth.AuthenticatePAM(context.Background(), s.PAMService, req.Username, req.Password); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{Error: "invalid credentials"})
			return
		}
		socket, err := s.startElevatedWorker(id, brokerGID, req.ElevationToken, req.CacheToken)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(Response{Error: err.Error()})
			return
		}
		slog.Info("filesystem elevation started", slog.String("user", id.Username), slog.Duration("ttl", s.ElevationTTL))
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Socket: socket})
		return
	}
	if req.Action == "drop-elevation" {
		if !validToken(req.ElevationToken) {
			_ = json.NewEncoder(conn).Encode(Response{Error: "invalid elevation request"})
			return
		}
		s.stopElevated(req.ElevationToken, "dropped")
		_ = json.NewEncoder(conn).Encode(Response{OK: true})
		return
	}
	if req.Action != "ensure" || req.Username == "" || len(req.CacheToken) != 64 {
		_ = json.NewEncoder(conn).Encode(Response{Error: "unsupported request"})
		return
	}
	id, err := identity.Lookup(req.Username)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: "unknown user"})
		return
	}
	if id.UID < s.MinUserUID || id.UID == 0 {
		_ = json.NewEncoder(conn).Encode(Response{Error: "user is not allowed to receive a filesystem worker"})
		return
	}
	socket, err := s.ensureWorker(id, brokerGID, s.CacheSocket, req.CacheToken)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(Response{OK: true, Socket: socket})
}

func (s *Server) ensureWorker(id identity.Identity, brokerGID int, cacheSocket, cacheToken string) (string, error) {
	uid, _, err := id.OwnershipIDs()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	userDir := filepath.Join(s.WorkerDir, strconv.FormatUint(uint64(id.UID), 10))
	socket := filepath.Join(userDir, "worker.sock")
	if worker := s.workers[id.UID]; worker.alive() && worker.cacheToken == cacheToken && socketReady(socket) {
		return socket, nil
	}
	if worker := s.workers[id.UID]; worker.alive() {
		_ = worker.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-worker.done:
		case <-time.After(5 * time.Second):
			_ = worker.cmd.Process.Kill()
			<-worker.done
		}
	}
	if err := os.MkdirAll(userDir, 0o710); err != nil {
		return "", err
	}
	if err := os.Chown(userDir, uid, brokerGID); err != nil {
		return "", err
	}
	if err := os.Chmod(userDir, 0o710); err != nil {
		return "", err
	}
	_ = os.Remove(socket)
	cmd := exec.Command(s.Executable, "worker", "-config", s.ConfigPath, "-socket", socket, "-username", id.Username, "-home", id.Home, "-thumbnail-cache-socket", cacheSocket, "-thumbnail-cache-token", cacheToken)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = id.Home
	cmd.Env = append(os.Environ(), "HOME="+id.Home, "USER="+id.Username, "LOGNAME="+id.Username)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: id.UID, Gid: id.GID, Groups: id.Groups},
		Pdeathsig:  syscall.SIGTERM,
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	if err := waitForSocket(socket, cmd, 5*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return "", err
	}
	if err := os.Chown(socket, uid, brokerGID); err != nil {
		_ = cmd.Process.Kill()
		return "", err
	}
	if err := os.Chmod(socket, 0o660); err != nil {
		_ = cmd.Process.Kill()
		return "", err
	}
	done := make(chan struct{})
	s.workers[id.UID] = workerProcess{cmd: cmd, cacheToken: cacheToken, done: done}
	go func(uid uint32, c *exec.Cmd, finished chan<- struct{}) {
		_ = c.Wait()
		close(finished)
		s.mu.Lock()
		if s.workers[uid].cmd == c {
			delete(s.workers, uid)
		}
		s.mu.Unlock()
	}(id.UID, cmd, done)
	return socket, nil
}

func (s *Server) startElevatedWorker(id identity.Identity, brokerGID int, token, cacheToken string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.elevated[token]; exists {
		return "", errors.New("elevation token already exists")
	}
	elevatedDir := filepath.Join(s.WorkerDir, "elevated")
	if err := os.MkdirAll(elevatedDir, 0o710); err != nil {
		return "", err
	}
	if err := os.Chown(elevatedDir, 0, brokerGID); err != nil {
		return "", err
	}
	if err := os.Chmod(elevatedDir, 0o710); err != nil {
		return "", err
	}
	userDir := filepath.Join(elevatedDir, token)
	stateDir := filepath.Join(userDir, "state")
	socket := filepath.Join(userDir, "worker.sock")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chown(userDir, 0, brokerGID); err != nil {
		return "", err
	}
	if err := os.Chmod(userDir, 0o710); err != nil {
		return "", err
	}
	cmd := exec.Command(s.Executable, "worker", "-config", s.ConfigPath, "-socket", socket, "-username", id.Username, "-home", id.Home, "-state-dir", stateDir, "-thumbnail-cache-socket", s.CacheSocket, "-thumbnail-cache-token", cacheToken)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = id.Home
	cmd.Env = append(os.Environ(), "HOME="+id.Home, "USER="+id.Username, "LOGNAME="+id.Username)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(userDir)
		return "", err
	}
	if err := waitForSocket(socket, cmd, 5*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(userDir)
		return "", err
	}
	if err := os.Chown(socket, 0, brokerGID); err != nil {
		stopStartedWorker(cmd, userDir)
		return "", err
	}
	if err := os.Chmod(socket, 0o660); err != nil {
		stopStartedWorker(cmd, userDir)
		return "", err
	}
	done := make(chan struct{})
	s.elevated[token] = workerProcess{cmd: cmd, cacheToken: cacheToken, done: done}
	go func(c *exec.Cmd, finished chan<- struct{}) {
		_ = c.Wait()
		close(finished)
		s.mu.Lock()
		if s.elevated[token].cmd == c {
			delete(s.elevated, token)
		}
		s.mu.Unlock()
		_ = os.RemoveAll(userDir)
	}(cmd, done)
	go func() {
		timer := time.NewTimer(s.ElevationTTL)
		defer timer.Stop()
		select {
		case <-timer.C:
			s.stopElevated(token, "expired")
		case <-done:
		}
	}()
	return socket, nil
}

func stopStartedWorker(cmd *exec.Cmd, workerDir string) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	_ = os.RemoveAll(workerDir)
}

func (s *Server) stopElevated(token, reason string) {
	s.mu.Lock()
	worker, ok := s.elevated[token]
	if ok {
		delete(s.elevated, token)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if worker.alive() {
		_ = worker.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-worker.done:
		case <-time.After(5 * time.Second):
			_ = worker.cmd.Process.Kill()
			<-worker.done
		}
	}
	slog.Info("filesystem elevation stopped", slog.String("reason", reason))
}

func (s *Server) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for uid, worker := range s.workers {
		if worker.alive() {
			_ = worker.cmd.Process.Signal(syscall.SIGTERM)
		}
		delete(s.workers, uid)
	}
	for token, worker := range s.elevated {
		if worker.alive() {
			_ = worker.cmd.Process.Signal(syscall.SIGTERM)
		}
		delete(s.elevated, token)
	}
}

func ClientEnsure(ctx context.Context, socketPath, username, cacheToken string) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(Request{Action: "ensure", Username: username, CacheToken: cacheToken}); err != nil {
		return "", err
	}
	var res Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return "", err
	}
	if !res.OK || res.Socket == "" {
		if res.Error == "" {
			res.Error = "launcher refused request"
		}
		return "", errors.New(res.Error)
	}
	return res.Socket, nil
}

func ClientAuthenticate(ctx context.Context, socketPath, username, password string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(conn).Encode(Request{Action: "authenticate", Username: username, Password: password}); err != nil {
		return err
	}
	var res Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return err
	}
	if !res.OK {
		if res.Error == "" {
			res.Error = "invalid credentials"
		}
		return errors.New(res.Error)
	}
	return nil
}

func ClientElevate(ctx context.Context, socketPath, username, password, elevationToken, cacheToken string) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := json.NewEncoder(conn).Encode(Request{Action: "elevate", Username: username, Password: password, ElevationToken: elevationToken, CacheToken: cacheToken}); err != nil {
		return "", err
	}
	var res Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return "", err
	}
	if !res.OK || res.Socket == "" {
		if res.Error == "" {
			res.Error = "launcher refused elevation"
		}
		return "", errors.New(res.Error)
	}
	return res.Socket, nil
}

func ClientDropElevation(ctx context.Context, socketPath, elevationToken string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(Request{Action: "drop-elevation", ElevationToken: elevationToken}); err != nil {
		return err
	}
	var res Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	return nil
}

func validToken(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func RandomElevationToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func groupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}

func socketReady(path string) bool {
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForSocket(socket string, cmd *exec.Cmd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil {
			return errors.New("worker exited before becoming ready")
		}
		if socketReady(socket) {
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("worker did not create %s within %s", socket, timeout)
}

type limitedReader struct {
	r net.Conn
	n int64
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("launcher request too large")
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	n, err := r.r.Read(p)
	r.n -= int64(n)
	return n, err
}

func ioLimitReader(conn net.Conn, n int64) *limitedReader { return &limitedReader{r: conn, n: n} }
