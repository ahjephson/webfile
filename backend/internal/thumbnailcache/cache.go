package thumbnailcache

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxThumbnailBytes = 16 << 20

var keyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Item struct {
	Data        []byte
	ContentType string
}

type Cache interface {
	Get(context.Context, string) (Item, bool, error)
	Put(context.Context, string, Item) error
	Move(context.Context, string, string) error
	Delete(context.Context, string) error
}

type Store struct {
	dir         string
	maxBytes    int64
	mu          sync.Mutex
	indexMu     sync.Mutex
	index       *locationIndex
	indexErr    error
	indexClosed bool
}

func Open(dir string, maxBytes int64) (*Store, error) {
	if !filepath.IsAbs(dir) || maxBytes <= 0 {
		return nil, errors.New("invalid thumbnail cache configuration")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: filepath.Clean(dir), maxBytes: maxBytes}, nil
}

func (s *Store) Get(ctx context.Context, key string) (Item, bool, error) {
	if err := validateKey(key); err != nil {
		return Item{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return Item{}, false, err
	}
	encoded, err := os.ReadFile(s.dataPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	separator := bytes.IndexByte(encoded, '\n')
	if separator <= 0 {
		return Item{}, false, errors.New("invalid cached thumbnail")
	}
	now := time.Now()
	_ = os.Chtimes(s.dataPath(key), now, now)
	return Item{Data: encoded[separator+1:], ContentType: string(encoded[:separator])}, true, nil
}

func (s *Store) Put(ctx context.Context, key string, item Item) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateItem(item); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded := make([]byte, 0, len(item.ContentType)+1+len(item.Data))
	encoded = append(encoded, item.ContentType...)
	encoded = append(encoded, '\n')
	encoded = append(encoded, item.Data...)
	if err := atomicWrite(s.dataPath(key), encoded); err != nil {
		return err
	}
	return s.pruneLocked()
}

func (s *Store) Move(ctx context.Context, from, to string) error {
	if err := validateKey(from); err != nil {
		return err
	}
	if err := validateKey(to); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if from == to {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Rename(s.dataPath(from), s.dataPath(to)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return err
	}
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.dataPath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /cache/locations", s.locationHandler)
	mux.HandleFunc("GET /cache/{key}", func(w http.ResponseWriter, r *http.Request) {
		item, ok, err := s.Get(r.Context(), r.PathValue("key"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", item.ContentType)
		w.Header().Set("Content-Length", fmt.Sprint(len(item.Data)))
		_, _ = w.Write(item.Data)
	})
	mux.HandleFunc("PUT /cache/{key}", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		data, err := io.ReadAll(io.LimitReader(r.Body, maxThumbnailBytes+1))
		if err != nil || len(data) > maxThumbnailBytes {
			http.Error(w, "thumbnail is too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := s.Put(r.Context(), r.PathValue("key"), Item{Data: data, ContentType: r.Header.Get("Content-Type")}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /cache/{key}/move", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request); err != nil {
			http.Error(w, "invalid move request", http.StatusBadRequest)
			return
		}
		if err := s.Move(r.Context(), r.PathValue("key"), request.Key); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /cache/{key}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Delete(r.Context(), r.PathValue("key")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

type peerUIDContextKey struct{}

type Authorizer struct {
	mu     sync.RWMutex
	tokens map[string]uint32
	scopes map[string]map[string]string
}

func NewAuthorizer() *Authorizer {
	return &Authorizer{tokens: make(map[string]uint32), scopes: make(map[string]map[string]string)}
}

// IssueScoped separates persistent ownership from temporary worker tokens.
// An elevated grant uses the originating account and an elevated authority ID.
func (a *Authorizer) IssueScoped(uid uint32, owner string, roots []string) (string, error) {
	for _, root := range roots {
		if err := validateKey(root); err != nil {
			return "", err
		}
	}
	token, err := a.Issue(uid)
	if err != nil {
		return "", err
	}
	scopes := make(map[string]string, len(roots))
	for _, root := range roots {
		scopes[root] = HashName(owner + ":" + root)
	}
	a.mu.Lock()
	a.scopes[token] = scopes
	a.mu.Unlock()
	return token, nil
}

func (a *Authorizer) Issue(uid uint32) (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	token := hex.EncodeToString(data)
	a.mu.Lock()
	a.tokens[token] = uid
	a.mu.Unlock()
	return token, nil
}

func (a *Authorizer) Revoke(token string) {
	a.mu.Lock()
	delete(a.tokens, token)
	delete(a.scopes, token)
	a.mu.Unlock()
}

func (a *Authorizer) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := r.Context().Value(peerUIDContextKey{}).(uint32)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		a.mu.RLock()
		expectedUID, issued := a.tokens[token]
		scopes, scoped := a.scopes[token]
		a.mu.RUnlock()
		if !ok || !issued || uid != expectedUID {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if scoped {
			r = r.WithContext(context.WithValue(r.Context(), namespaceContextKey{}, scopes))
		}
		next.ServeHTTP(w, r)
	})
}

func Listen(socketPath string) (net.Listener, error) {
	if !filepath.IsAbs(socketPath) {
		return nil, errors.New("thumbnail cache socket must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	// Local workers need only connect/write permission; every request is also
	// bound to its kernel-reported peer UID and a per-worker capability token.
	if err := os.Chmod(socketPath, 0o622); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func ConnContext(ctx context.Context, conn net.Conn) context.Context {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return ctx
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return ctx
	}
	var uid uint32
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			credentialErr = err
			return
		}
		uid = credentials.Uid
	}); err != nil || credentialErr != nil {
		return ctx
	}
	return context.WithValue(ctx, peerUIDContextKey{}, uid)
}

func validateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return errors.New("invalid thumbnail cache key")
	}
	return nil
}

func validateItem(item Item) error {
	if len(item.Data) == 0 || len(item.Data) > maxThumbnailBytes {
		return errors.New("invalid thumbnail data size")
	}
	switch item.ContentType {
	case "image/jpeg", "image/png", "image/webp":
		return nil
	default:
		return errors.New("unsupported thumbnail content type")
	}
}

func (s *Store) dataPath(key string) string { return filepath.Join(s.dir, key+".thumb") }

func atomicWrite(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".webfile-thumbnail-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *Store) pruneLocked() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	type cachedFile struct {
		path    string
		key     string
		size    int64
		modTime time.Time
	}
	files := make([]cachedFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".thumb") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".thumb")
		files = append(files, cachedFile{path: filepath.Join(s.dir, entry.Name()), key: key, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files {
		if total <= s.maxBytes {
			break
		}
		if err := os.Remove(file.path); err == nil || errors.Is(err, os.ErrNotExist) {
			total -= file.size
		}
	}
	return nil
}
