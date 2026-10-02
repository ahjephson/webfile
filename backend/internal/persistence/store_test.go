package persistence

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStatePersistsPreferencesAndSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lineNumbers := false
	wantPreferences := Preferences{
		ViewMode: "grid", GridSize: "large", ShowHidden: true, Theme: "dark",
		EditorWordWrap: true, EditorFontSize: 18, EditorLineNumbers: &lineNumbers,
		EditorShowWhitespace: true, EditorMinimap: true, EditorToolbar: "rich",
		EditorMarkdownPreview: true, CopyChunkMiB: 16,
	}
	if err := store.SetPreferences("alex", wantPreferences); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).UTC()
	if err := store.PutSession("hash", SessionRecord{Username: "alex", ExpiresAt: expires, CSRFToken: "csrf"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	prefs := reopened.Preferences("alex")
	if prefs.EditorLineNumbers == nil || *prefs.EditorLineNumbers || !reflect.DeepEqual(prefs, wantPreferences) {
		t.Fatalf("prefs=%+v, want %+v", prefs, wantPreferences)
	}
	rec, ok := reopened.GetSession("hash")
	if !ok || rec.Username != "alex" || rec.CSRFToken != "csrf" {
		t.Fatalf("session=%+v ok=%v", rec, ok)
	}
}

func TestLegacyFavouritesAreDroppedOnNextStateWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := []byte(`{"version":1,"users":{"alex":{"preferences":{},"favourites":[{"path":"/Media"}]}}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPreferences("alex", Preferences{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("favourites")) {
		t.Fatalf("legacy favourites remained in state: %s", data)
	}
}
