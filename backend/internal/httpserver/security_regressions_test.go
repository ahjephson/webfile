package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGIVEN_LocalProxyAddress_WHEN_CredentialIsMissingOrWrong_THEN_CannotCreateSession(t *testing.T) {
	for _, credentials := range [][]string{nil, {strings.Repeat("b", 64)}, {strings.Repeat("a", 64), strings.Repeat("a", 64)}} {
		target, current := proxyAuthTestServer(t)
		request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("X-Authenticated-User", current.Username)
		for _, credential := range credentials {
			request.Header.Add("X-WebFile-Proxy-Token", credential)
		}
		response := httptest.NewRecorder()
		target.Handler().ServeHTTP(response, request)
		var payload struct {
			Authenticated bool `json:"authenticated"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Authenticated || len(response.Result().Cookies()) != 0 {
			t.Fatal("local caller impersonated proxy")
		}
	}
}

func TestGIVEN_ExistingProxySession_WHEN_CredentialIsRemoved_THEN_RejectsSession(t *testing.T) {
	target, current := proxyAuthTestServer(t)
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	bootstrap.RemoteAddr = "127.0.0.1:12345"
	bootstrap.Header.Set("X-Authenticated-User", current.Username)
	bootstrap.Header.Set("X-WebFile-Proxy-Token", strings.Repeat("a", 64))
	created := httptest.NewRecorder()
	target.Handler().ServeHTTP(created, bootstrap)
	if len(created.Result().Cookies()) != 1 {
		t.Fatal("valid proxy session not created")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/preferences", nil)
	request.RemoteAddr = bootstrap.RemoteAddr
	request.Header.Set("X-Authenticated-User", current.Username)
	request.AddCookie(created.Result().Cookies()[0])
	response := httptest.NewRecorder()
	target.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("credential-free session accepted: %d", response.Code)
	}
}

func TestGIVEN_ApplicationResponse_WHEN_SettingCSP_THEN_AllowsOnlyConfiguredLogoutOrigin(t *testing.T) {
	target := NewMain(MainOptions{LogoutURL: "https://sso.example.test/logout"})
	response := httptest.NewRecorder()
	target.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	policy := response.Header().Get("Content-Security-Policy")
	for _, required := range []string{"script-src 'self'", "object-src 'none'", "frame-ancestors 'none'", "form-action 'self' https://sso.example.test"} {
		if !strings.Contains(policy, required) {
			t.Fatalf("missing %q: %s", required, policy)
		}
	}
}

func TestGIVEN_NoElevationPolicy_WHEN_AuthenticatedUserRequestsElevation_THEN_DeniesRootWorker(t *testing.T) {
	target, current := proxyAuthTestServer(t)
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	bootstrap.RemoteAddr = "127.0.0.1:12345"
	bootstrap.Header.Set("X-Authenticated-User", current.Username)
	bootstrap.Header.Set("X-WebFile-Proxy-Token", strings.Repeat("a", 64))
	created := httptest.NewRecorder()
	target.Handler().ServeHTTP(created, bootstrap)
	var session struct {
		CSRFToken          string `json:"csrf_token"`
		ElevationAvailable bool   `json:"elevation_available"`
	}
	if err := json.NewDecoder(created.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if session.ElevationAvailable {
		t.Fatal("disabled elevation advertised as available")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/elevation", strings.NewReader(`{"password":"Password"}`))
	request.RemoteAddr = bootstrap.RemoteAddr
	request.Header = bootstrap.Header.Clone()
	request.Header.Set("X-WebFile-CSRF", session.CSRFToken)
	request.AddCookie(created.Result().Cookies()[0])
	response := httptest.NewRecorder()
	target.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized elevation response: %d %s", response.Code, response.Body.String())
	}
}
