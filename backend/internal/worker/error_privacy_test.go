//go:build linux

package worker

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyFailureDoesNotExposeWorkerSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "private-worker.sock")
	response := httptest.NewRecorder()
	proxyForSocket(socket).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/fs/operations", nil))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), socket) || strings.Contains(response.Body.String(), "dial unix") {
		t.Fatalf("worker diagnostic leaked: HTTP %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "file service is currently unavailable") {
		t.Fatalf("missing service message: %s", response.Body.String())
	}
}
