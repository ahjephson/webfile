package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"webfile/internal/identity"
	"webfile/internal/persistence"
)

const CookieName = "webfile_session"
const SecureCookieName = "__Host-webfile_session"

type Store struct {
	state  *persistence.Store
	ttl    time.Duration
	secure bool
}

func New(ttl time.Duration, secure bool, state *persistence.Store) *Store {
	return &Store{state: state, ttl: ttl, secure: secure}
}

func (s *Store) Create(w http.ResponseWriter, id identity.Identity) (string, error) {
	tokenValue, err := randomToken(32)
	if err != nil {
		return "", err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(s.ttl)
	if err := s.state.PutSession(tokenHash(tokenValue), persistence.SessionRecord{
		Username:  id.Username,
		ExpiresAt: expires,
		CSRFToken: csrf,
	}); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    tokenValue,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(s.ttl.Seconds()),
	})
	return csrf, nil
}

func (s *Store) Get(r *http.Request) (identity.Identity, string, bool) {
	cookie, err := r.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return identity.Identity{}, "", false
	}
	rec, ok := s.state.GetSession(tokenHash(cookie.Value))
	if !ok || !time.Now().Before(rec.ExpiresAt) {
		if ok {
			_ = s.state.DeleteSession(tokenHash(cookie.Value))
		}
		return identity.Identity{}, "", false
	}
	id, err := identity.Lookup(rec.Username)
	if err != nil {
		return identity.Identity{}, "", false
	}
	return id, rec.CSRFToken, true
}

// Key returns a non-secret, stable key for the current browser session.
func (s *Store) Key(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return tokenHash(cookie.Value), true
}

func (s *Store) VerifyCSRF(r *http.Request) bool {
	_, expected, ok := s.Get(r)
	if !ok || expected == "" {
		return false
	}
	provided := r.Header.Get("X-WebFile-CSRF")
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (s *Store) Delete(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.cookieName()); err == nil && cookie.Value != "" {
		_ = s.state.DeleteSession(tokenHash(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
	})
}

func (s *Store) cookieName() string {
	if s.secure {
		return SecureCookieName
	}
	return CookieName
}

func (s *Store) Sweep() int {
	removed, _ := s.state.SweepSessions(time.Now())
	return removed
}

func randomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(b)
	if value == "" {
		return "", errors.New("could not generate token")
	}
	return value, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
