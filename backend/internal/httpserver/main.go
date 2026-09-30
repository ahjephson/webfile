package httpserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"webfile/internal/identity"
	"webfile/internal/persistence"
	"webfile/internal/session"
	"webfile/internal/worker"
)

type MainOptions struct {
	AuthMode         string
	Authenticate     func(context.Context, string, string) error
	Sessions         *session.Store
	State            *persistence.Store
	Workers          *worker.Manager
	CurrentUser      identity.Identity
	CookieSecure     bool
	LoginMaxAttempts int
	LoginWindow      time.Duration
	LoginBlock       time.Duration
}

type MainServer struct {
	opts    MainOptions
	mux     *http.ServeMux
	limiter *loginLimiter
}

func NewMain(opts MainOptions) *MainServer {
	s := &MainServer{
		opts:    opts,
		mux:     http.NewServeMux(),
		limiter: newLoginLimiter(opts.LoginMaxAttempts, opts.LoginWindow, opts.LoginBlock),
	}
	s.routes()
	return s
}

func (s *MainServer) Handler() http.Handler {
	return securityHeaders(requestLog(recoverer(s.csrfGuard(s.mux))))
}

func (s *MainServer) routes() {
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	s.mux.HandleFunc("GET /api/session", s.getSession)
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /api/logout", s.logout)
	s.mux.HandleFunc("GET /api/preferences", s.getPreferences)
	s.mux.HandleFunc("PUT /api/preferences", s.putPreferences)
	s.mux.HandleFunc("GET /api/favourites", s.listFavourites)
	s.mux.HandleFunc("POST /api/favourites", s.addFavourite)
	s.mux.HandleFunc("DELETE /api/favourites", s.removeFavourite)
	s.mux.HandleFunc("/api/fs/", s.proxyFilesystem)
	s.mux.Handle("/", spaHandler())
}

func (s *MainServer) getSession(w http.ResponseWriter, r *http.Request) {
	id, csrf, ok := s.identityForRequest(r)
	response := map[string]any{
		"authenticated": ok,
		"mode":          s.opts.AuthMode,
	}
	if ok {
		response["user"] = id
		if s.opts.AuthMode == "pam" {
			response["csrf_token"] = csrf
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
		"authenticated": true,
		"mode":          s.opts.AuthMode,
		"user":          id,
		"csrf_token":    csrf,
	})
}

func (s *MainServer) logout(w http.ResponseWriter, r *http.Request) {
	if s.opts.AuthMode == "pam" {
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
	process, err := s.opts.Workers.Ensure(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	process.Proxy.ServeHTTP(w, r)
}

func (s *MainServer) identityForRequest(r *http.Request) (identity.Identity, string, bool) {
	if s.opts.AuthMode == "none" {
		return s.opts.CurrentUser, "", true
	}
	return s.opts.Sessions.Get(r)
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
	if err := s.opts.State.SetPreferences(id.Username, prefs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save preferences"})
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *MainServer) listFavourites(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favourites": s.opts.State.Favourites(id.Username)})
}

func (s *MainServer) addFavourite(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var favourite persistence.Favourite
	if err := decodeJSON(r, &favourite); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	favourite.Path = strings.TrimSpace(favourite.Path)
	if favourite.Path == "" || !strings.HasPrefix(favourite.Path, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	favourite.CreatedAt = time.Now().UTC()
	if err := s.opts.State.AddFavourite(id.Username, favourite); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save favourite"})
		return
	}
	writeJSON(w, http.StatusCreated, favourite)
}

func (s *MainServer) removeFavourite(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.identityForRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	if err := s.opts.State.RemoveFavourite(id.Username, path); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove favourite"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *MainServer) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMutation(r.Method) {
			if !sameOriginRequest(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request refused"})
				return
			}
			if s.opts.AuthMode == "pam" && r.URL.Path != "/api/login" && !s.opts.Sessions.VerifyCSRF(r) {
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
