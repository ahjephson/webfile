package session

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/identity"
	"webfile/internal/persistence"
)

func TestSessionSurvivesStateReopen(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state, err := persistence.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.Current()
	if err != nil {
		t.Skipf("current identity unavailable: %v", err)
	}
	store := New(time.Hour, false, state)
	recorder := httptest.NewRecorder()
	csrf, err := store.Create(recorder, id)
	if err != nil {
		t.Fatal(err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}

	reopened, err := persistence.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	store2 := New(time.Hour, false, reopened)
	req := httptest.NewRequest("GET", "http://example/api/session", nil)
	req.AddCookie(cookies[0])
	got, gotCSRF, ok := store2.Get(req)
	if !ok || got.Username != id.Username || gotCSRF != csrf {
		t.Fatalf("got=%+v csrf=%q ok=%v", got, gotCSRF, ok)
	}
	key, ok := store2.Key(req)
	if !ok || key == "" || key == cookies[0].Value {
		t.Fatalf("key=%q ok=%v", key, ok)
	}
}
