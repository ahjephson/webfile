//go:build linux

package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Action     string `json:"action"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	CacheToken string `json:"cache_token,omitempty"`
}

type Response struct {
	OK     bool   `json:"ok"`
	Socket string `json:"socket,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Server struct {
	Executable  string
	ConfigPath  string
	SocketPath  string
	WorkerDir   string
	BrokerGroup string
	MinUserUID  uint32
	PAMService  string
	CacheSocket string

	mu      sync.Mutex
	workers map[uint32]workerProcess
}

type workerProcess struct {
	cmd        *exec.Cmd
	cacheToken string
	done       <-chan struct{}
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
	if err := os.MkdirAll(filepath.Dir(s.SocketPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.WorkerDir, 0o751); err != nil {
		return err
	}
	if err := os.Chmod(s.WorkerDir, 0o751); err != nil {
		return err
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	userDir := filepath.Join(s.WorkerDir, strconv.FormatUint(uint64(id.UID), 10))
	socket := filepath.Join(userDir, "worker.sock")
	if worker := s.workers[id.UID]; worker.cmd != nil && worker.cmd.ProcessState == nil && worker.cacheToken == cacheToken && socketReady(socket) {
		return socket, nil
	}
	if worker := s.workers[id.UID]; worker.cmd != nil && worker.cmd.ProcessState == nil {
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
	if err := os.Chown(userDir, int(id.UID), brokerGID); err != nil {
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
	if err := os.Chown(socket, int(id.UID), brokerGID); err != nil {
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

func (s *Server) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for uid, worker := range s.workers {
		if worker.cmd.Process != nil && worker.cmd.ProcessState == nil {
			_ = worker.cmd.Process.Signal(syscall.SIGTERM)
		}
		delete(s.workers, uid)
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
