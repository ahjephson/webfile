package httpserver

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"webfile/internal/identity"
	"webfile/internal/persistence"
	"webfile/internal/session"
	"webfile/internal/worker"
)

type MainOptions struct {
	Version          string
	Commit           string
	BuildDate        string
	AuthMode         string
	Authenticate     func(context.Context, string, string) error
	Sessions         *session.Store
	ProxyUserHeader  string
	TrustedProxies   []netip.Prefix
	LogoutURL        string
	State            *persistence.Store
	Workers          *worker.Manager
	CurrentUser      identity.Identity
	CookieSecure     bool
	LoginMaxAttempts int
	LoginWindow      time.Duration
	LoginBlock       time.Duration
	ElevationTTL     time.Duration
}

type MainServer struct {
	opts           MainOptions
	mux            *http.ServeMux
	limiter        *loginLimiter
	elevationMu    sync.Mutex
	elevations     map[string]elevationState
	trustedProxies []netip.Prefix
}

type elevationState struct {
	token     string
	process   *worker.Process
	expiresAt time.Time
}

func NewMain(opts MainOptions) *MainServer {
	s := &MainServer{
		opts:           opts,
		mux:            http.NewServeMux(),
		limiter:        newLoginLimiter(opts.LoginMaxAttempts, opts.LoginWindow, opts.LoginBlock),
		elevations:     make(map[string]elevationState),
		trustedProxies: append([]netip.Prefix(nil), opts.TrustedProxies...),
	}
	if s.opts.ElevationTTL <= 0 {
		s.opts.ElevationTTL = 5 * time.Minute
	}
	s.routes()
	return s
}

func (s *MainServer) Handler() http.Handler {
	return securityHeaders(requestLog(recoverer(s.csrfGuard(s.mux))))
}

func (s *MainServer) routes() {
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"version":    s.opts.Version,
			"commit":     s.opts.Commit,
			"build_date": s.opts.BuildDate,
		})
	})
	s.mux.HandleFunc("GET /api/session", s.getSession)
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /api/logout", s.logout)
	s.mux.HandleFunc("POST /api/elevation", s.elevate)
	s.mux.HandleFunc("DELETE /api/elevation", s.dropElevation)
	s.mux.HandleFunc("GET /api/preferences", s.getPreferences)
	s.mux.HandleFunc("PUT /api/preferences", s.putPreferences)
	s.mux.HandleFunc("/api/fs/", s.proxyFilesystem)
	s.mux.Handle("/", spaHandler())
}

func (s *MainServer) getSession(w http.ResponseWriter, r *http.Request) {
	var id identity.Identity
	var csrf string
	var ok bool
	if s.opts.AuthMode == "proxy" {
		externalID, externalOK := s.proxyIdentity(r)
		if externalOK {
			sessionID, sessionCSRF, sessionOK := s.opts.Sessions.Get(r)
			if sessionOK && sessionID.Username == externalID.Username {
				id, csrf, ok = externalID, sessionCSRF, true
			} else {
				if sessionOK {
					s.dropElevationForRequest(r)
					s.opts.Sessions.Delete(w, r)
				}
				createdCSRF, err := s.opts.Sessions.Create(w, externalID)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
					return
				}
				id, csrf, ok = externalID, createdCSRF, true
			}
		}
	} else {
		id, csrf, ok = s.identityForRequest(r)
	}
	response := map[string]any{
		"authenticated": ok,
		"mode":          s.opts.AuthMode,
	}
	if ok {
		response["user"] = id
		if s.opts.AuthMode == "pam" || s.opts.AuthMode == "proxy" {
			response["csrf_token"] = csrf
			response["elevation_available"] = true
			if state, elevated := s.elevationForRequest(r); elevated {
				response["elevated"] = true
				response["elevation_expires_at"] = state.expiresAt
			} else {
				response["elevated"] = false
			}
		}
		if s.opts.AuthMode == "proxy" && s.opts.LogoutURL != "" {
			response["logout_url"] = s.opts.LogoutURL
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *MainServer) login(w http.ResponseWriter, r *http.Request) {
	if s.opts.AuthMode != "pam" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password login is disabled"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid credentials"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if retryAfter := s.limiter.RetryAfter(r, req.Username); retryAfter > 0 {
		seconds := int64((retryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts; try again later"})
		return
	}
	if s.opts.Authenticate == nil || s.opts.Authenticate(r.Context(), req.Username, req.Password) != nil {
		s.limiter.Failure(r, req.Username)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	id, err := identity.Lookup(req.Username)
	if err != nil {
		s.limiter.Failure(r, req.Username)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	csrf, err := s.opts.Sessions.Create(w, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	s.limiter.Success(r, req.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":       true,
		"mode":                s.opts.AuthMode,
		"user":                id,
		"csrf_token":          csrf,
		"elevation_available": true,
		"elevated":            false,
	})
}

func (s *MainServer) logout(w http.ResponseWriter, r *http.Request) {
	if s.opts.AuthMode == "pam" || s.opts.AuthMode == "proxy" {
		s.dropElevationForRequest(r)
		s.opts.Sessions.Delete(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *MainServer) proxyFilesystem(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	process := (*worker.Process)(nil)
	if state, elevated := s.elevationForRequest(r); elevated {
		process = state.process
	}
	var err error
	if process == nil {
		process, err = s.opts.Workers.Ensure(r.Context(), id)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	process.Proxy.ServeHTTP(w, r)
}

func (s *MainServer) elevate(w http.ResponseWriter, r *http.Request) {
	if s.opts.AuthMode != "pam" && s.opts.AuthMode != "proxy" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filesystem elevation is unavailable"})
		return
	}
	id, csrf, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Password == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if retryAfter := s.limiter.RetryAfter(r, id.Username); retryAfter > 0 {
		seconds := int64((retryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many authentication attempts; try again later"})
		return
	}
	process, token, err := s.opts.Workers.Elevate(r.Context(), id, req.Password)
	if err != nil {
		if err.Error() == "invalid credentials" {
			s.limiter.Failure(r, id.Username)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not start elevated filesystem worker"})
		}
		return
	}
	s.limiter.Success(r, id.Username)
	key, ok := s.opts.Sessions.Key(r)
	if !ok {
		_ = s.opts.Workers.DropElevation(context.Background(), token)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	s.dropElevationForKey(key)
	state := elevationState{token: token, process: process, expiresAt: time.Now().Add(s.opts.ElevationTTL)}
	s.elevationMu.Lock()
	s.elevations[key] = state
	s.elevationMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":        true,
		"mode":                 s.opts.AuthMode,
		"user":                 id,
		"csrf_token":           csrf,
		"elevation_available":  true,
		"elevated":             true,
		"elevation_expires_at": state.expiresAt,
	})
}

func (s *MainServer) dropElevation(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.identityForRequest(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	s.dropElevationForRequest(r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *MainServer) elevationForRequest(r *http.Request) (elevationState, bool) {
	if s.opts.AuthMode != "pam" && s.opts.AuthMode != "proxy" {
		return elevationState{}, false
	}
	key, ok := s.opts.Sessions.Key(r)
	if !ok {
		return elevationState{}, false
	}
	s.elevationMu.Lock()
	state, ok := s.elevations[key]
	if ok && !time.Now().Before(state.expiresAt) {
		delete(s.elevations, key)
		ok = false
	}
	s.elevationMu.Unlock()
	if !ok && state.token != "" {
		go func() { _ = s.opts.Workers.DropElevation(context.Background(), state.token) }()
	}
	return state, ok
}

func (s *MainServer) dropElevationForRequest(r *http.Request) {
	key, ok := s.opts.Sessions.Key(r)
	if ok {
		s.dropElevationForKey(key)
	}
}

func (s *MainServer) dropElevationForKey(key string) {
	s.elevationMu.Lock()
	state, ok := s.elevations[key]
	if ok {
		delete(s.elevations, key)
	}
	s.elevationMu.Unlock()
	if ok {
		_ = s.opts.Workers.DropElevation(context.Background(), state.token)
	}
}

func (s *MainServer) identityForRequest(r *http.Request) (identity.Identity, string, bool) {
	if s.opts.AuthMode == "none" {
		return s.opts.CurrentUser, "", true
	}
	id, csrf, ok := s.opts.Sessions.Get(r)
	if !ok || s.opts.AuthMode != "proxy" {
		return id, csrf, ok
	}
	externalID, externalOK := s.proxyIdentity(r)
	if !externalOK || externalID.Username != id.Username {
		return identity.Identity{}, "", false
	}
	return externalID, csrf, true
}

func (s *MainServer) proxyIdentity(r *http.Request) (identity.Identity, bool) {
	if s.opts.AuthMode != "proxy" || !s.isTrustedProxy(r.RemoteAddr) {
		return identity.Identity{}, false
	}
	values := r.Header.Values(s.opts.ProxyUserHeader)
	if len(values) != 1 {
		return identity.Identity{}, false
	}
	username := strings.TrimSpace(values[0])
	if username == "" || username != values[0] || strings.ContainsAny(username, "\r\n,") {
		return identity.Identity{}, false
	}
	id, err := identity.Lookup(username)
	if err != nil {
		return identity.Identity{}, false
	}
	return id, true
}

func (s *MainServer) isTrustedProxy(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	address = address.Unmap()
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (s *MainServer) getPreferences(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, s.opts.State.Preferences(id.Username))
}

func (s *MainServer) putPreferences(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var prefs persistence.Preferences
	if err := decodeJSON(r, &prefs); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if prefs.ViewMode != "" && prefs.ViewMode != "list" && prefs.ViewMode != "grid" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "view_mode must be list or grid"})
		return
	}
	if prefs.GridSize != "" && prefs.GridSize != "small" && prefs.GridSize != "medium" && prefs.GridSize != "large" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "grid_size must be small, medium or large"})
		return
	}
	if prefs.Theme != "" && prefs.Theme != "system" && prefs.Theme != "dark" && prefs.Theme != "light" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "theme must be system, dark or light"})
		return
	}
	if prefs.EditorFontSize != 0 && (prefs.EditorFontSize < 10 || prefs.EditorFontSize > 28) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "editor_font_size must be between 10 and 28"})
		return
	}
	if prefs.EditorToolbar != "" && prefs.EditorToolbar != "compact" && prefs.EditorToolbar != "rich" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "editor_toolbar must be compact or rich"})
		return
	}
	if prefs.CopyChunkMiB != 0 && prefs.CopyChunkMiB != 1 && prefs.CopyChunkMiB != 4 && prefs.CopyChunkMiB != 16 && prefs.CopyChunkMiB != 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "copy_chunk_mib must be 1, 4, 16 or 64"})
		return
	}
	if prefs.ExtractExistingFiles != "" && prefs.ExtractExistingFiles != "skip" && prefs.ExtractExistingFiles != "overwrite" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "extract_existing_files must be skip or overwrite"})
		return
	}
	if err := s.opts.State.SetPreferences(id.Username, prefs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save preferences"})
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *MainServer) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMutation(r.Method) {
			if !sameOriginRequest(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request refused"})
				return
			}
			if s.opts.AuthMode != "none" && r.URL.Path != "/api/login" && !s.opts.Sessions.VerifyCSRF(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid csrf token"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(u.Host)), []byte(strings.ToLower(r.Host))) == 1
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
