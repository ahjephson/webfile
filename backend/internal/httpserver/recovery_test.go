package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecovererPreservesStreamAbort(t *testing.T) {
	const partial = "{\"entry\":{},\"done\":false}\n"
	response := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(partial))
			panic(http.ErrAbortHandler)
		})).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/fs/search", nil))
	}()
	if recovered != http.ErrAbortHandler {
		t.Fatalf("stream abort = %v; want http.ErrAbortHandler", recovered)
	}
	if got := response.Body.String(); got != partial {
		t.Fatalf("stream body was modified during abort: %q", got)
	}
}

func TestRecovererHandlesOrdinaryPanic(t *testing.T) {
	response := httptest.NewRecorder()
	recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("unexpected failure")
	})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/fs/dir", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", response.Code)
	}
	if got := response.Body.String(); got != "{\"error\":\"internal server error\"}\n" {
		t.Fatalf("error body = %q", got)
	}
}
