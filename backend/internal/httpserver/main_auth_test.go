package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/identity"
	"webfile/internal/persistence"
	"webfile/internal/session"
)

func TestLogoutPersistenceFailureIsReportedAndCanBeRetried(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.Current()
	if err != nil {
		t.Fatal(err)
	}
	sessions := session.New(time.Hour, false, state)
	created := httptest.NewRecorder()
	csrf, err := sessions.Create(created, id)
	if err != nil {
		t.Fatal(err)
	}
	cookie := created.Result().Cookies()[0]
	target := NewMain(MainOptions{AuthMode: "pam", Sessions: sessions, State: state})
	backup := filepath.Join(filepath.Dir(path), "backup.json")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/logout", nil)
	request.AddCookie(cookie)
	request.Header.Set("X-WebFile-CSRF", csrf)
	response := httptest.NewRecorder()
	target.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("logout acknowledged failed persistence: %d %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("failed logout discarded the cookie needed to retry")
	}
	if _, _, ok := sessions.Get(request); !ok {
		t.Fatal("failed mutation left memory inconsistent with persistent state")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	target.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("retry failed: %d %s", response.Code, response.Body.String())
	}
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("successful logout cookie: %v", cookies)
	}
	reopened, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := session.New(time.Hour, false, reopened).Get(request); ok {
		t.Fatal("successful logout was lost after reopening state")
	}
}

func proxyAuthTestServer(t *testing.T) (*MainServer, identity.Identity) {
	t.Helper()
	current, err := identity.Current()
	if err != nil {
		t.Fatal(err)
	}
	state, err := persistence.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return NewMain(MainOptions{
		AuthMode:        "proxy",
		Sessions:        session.New(time.Hour, false, state),
		ProxyUserHeader: "X-Authenticated-User",
		TrustedProxies:  []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		LogoutURL:       "/_auth/logout",
		State:           state,
		CurrentUser:     current,
		ElevationTTL:    5 * time.Minute,
	}), current
}

func TestHealthReportsBuildInformation(t *testing.T) {
	target := NewMain(MainOptions{Version: "1.0.0", Commit: "abc123", BuildDate: "2026-10-12T10:00:00Z"})
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()

	target.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		OK        bool   `json:"ok"`
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildDate string `json:"build_date"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Version != "1.0.0" || payload.Commit != "abc123" || payload.BuildDate != "2026-10-12T10:00:00Z" {
		t.Fatalf("unexpected health payload: %#v", payload)
	}
}

func TestBasePathMountsUIAndAPI(t *testing.T) {
	target := NewMain(MainOptions{BasePath: "/tools/webfile", Version: "test"})

	rootRequest := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rootResponse := httptest.NewRecorder()
	target.Handler().ServeHTTP(rootResponse, rootRequest)
	if rootResponse.Code != http.StatusNotFound {
		t.Fatalf("unprefixed status = %d", rootResponse.Code)
	}

	redirectRequest := httptest.NewRequest(http.MethodGet, "/tools/webfile", nil)
	redirectResponse := httptest.NewRecorder()
	target.Handler().ServeHTTP(redirectResponse, redirectRequest)
	if redirectResponse.Code != http.StatusPermanentRedirect || redirectResponse.Header().Get("Location") != "/tools/webfile/" {
		t.Fatalf("redirect = %d %q", redirectResponse.Code, redirectResponse.Header().Get("Location"))
	}

	healthRequest := httptest.NewRequest(http.MethodGet, "/tools/webfile/api/health", nil)
	healthResponse := httptest.NewRecorder()
	target.Handler().ServeHTTP(healthResponse, healthRequest)
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("prefixed health status = %d: %s", healthResponse.Code, healthResponse.Body.String())
	}

	uiRequest := httptest.NewRequest(http.MethodGet, "/tools/webfile/", nil)
	uiResponse := httptest.NewRecorder()
	target.Handler().ServeHTTP(uiResponse, uiRequest)
	if uiResponse.Code != http.StatusOK || !bytes.Contains(uiResponse.Body.Bytes(), []byte(`content="/tools/webfile/"`)) {
		t.Fatalf("prefixed UI response = %d: %s", uiResponse.Code, uiResponse.Body.String())
	}
}

func TestSameOriginUsesConfiguredOrigins(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/logout", nil)
	request.Header.Set("Origin", "https://files.example.test")
	if !sameOriginRequest(request, []string{"https://files.example.test"}) {
		t.Fatal("configured public origin was rejected")
	}
	if sameOriginRequest(request, []string{"https://other.example.test"}) {
		t.Fatal("unexpected public origin was accepted")
	}
}

func TestProxyAuthenticationCreatesLocalSessionForTrustedIdentity(t *testing.T) {
	target, current := proxyAuthTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("X-Authenticated-User", current.Username)
	response := httptest.NewRecorder()

	target.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Authenticated bool              `json:"authenticated"`
		Mode          string            `json:"mode"`
		User          identity.Identity `json:"user"`
		CSRFToken     string            `json:"csrf_token"`
		LogoutURL     string            `json:"logout_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Authenticated || payload.Mode != "proxy" || payload.User.Username != current.Username || payload.CSRFToken == "" {
		t.Fatalf("unexpected session payload: %#v", payload)
	}
	if payload.LogoutURL != "/_auth/logout" {
		t.Fatalf("logout URL = %q", payload.LogoutURL)
	}
	if len(response.Result().Cookies()) != 1 {
		t.Fatalf("session cookies = %d", len(response.Result().Cookies()))
	}
}

func TestProxyAuthenticationRejectsUntrustedHeader(t *testing.T) {
	target, current := proxyAuthTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	request.Header.Set("X-Authenticated-User", current.Username)
	response := httptest.NewRecorder()

	target.Handler().ServeHTTP(response, request)

	var payload struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Authenticated || len(response.Result().Cookies()) != 0 {
		t.Fatal("untrusted proxy header authenticated a session")
	}
}

func TestProxyAuthenticationRequiresHeaderAfterSessionCreation(t *testing.T) {
	target, current := proxyAuthTestServer(t)
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	bootstrap.RemoteAddr = "127.0.0.1:12345"
	bootstrap.Header.Set("X-Authenticated-User", current.Username)
	bootstrapResponse := httptest.NewRecorder()
	target.Handler().ServeHTTP(bootstrapResponse, bootstrap)
	cookies := bootstrapResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies = %d", len(cookies))
	}

	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	target.Handler().ServeHTTP(response, request)

	var payload struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Authenticated {
		t.Fatal("local session remained authenticated without the proxy identity header")
	}
}

func TestProxyAuthenticationRejectsDuplicateIdentityHeaders(t *testing.T) {
	target, current := proxyAuthTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Add("X-Authenticated-User", current.Username)
	request.Header.Add("X-Authenticated-User", current.Username)
	response := httptest.NewRecorder()
	target.Handler().ServeHTTP(response, request)

	var payload struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Authenticated {
		t.Fatal("duplicate proxy identity headers authenticated a session")
	}
}
