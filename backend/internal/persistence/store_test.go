package persistence

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestFavouritesForUnknownUserSerializesAsEmptyArray(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	favourites := store.Favourites("alex")
	if favourites == nil {
		t.Fatal("favourites must be a non-nil empty slice")
	}

	data, err := json.Marshal(favourites)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Fatalf("favourites JSON = %s", data)
	}
}

func TestStatePersistsPreferencesFavouritesAndSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPreferences("alex", Preferences{ViewMode: "grid", GridSize: "large", ShowHidden: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddFavourite("alex", Favourite{Path: "/Media/Movies", CreatedAt: time.Now().UTC()}); err != nil {
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
	if prefs.ViewMode != "grid" || prefs.GridSize != "large" || !prefs.ShowHidden {
		t.Fatalf("prefs=%+v", prefs)
	}
	if favs := reopened.Favourites("alex"); len(favs) != 1 || favs[0].Path != "/Media/Movies" {
		t.Fatalf("favourites=%+v", favs)
	}
	rec, ok := reopened.GetSession("hash")
	if !ok || rec.Username != "alex" || rec.CSRFToken != "csrf" {
		t.Fatalf("session=%+v ok=%v", rec, ok)
	}
}
