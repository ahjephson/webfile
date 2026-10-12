package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/identity"
	"webfile/internal/persistence"
	"webfile/internal/session"
)

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
