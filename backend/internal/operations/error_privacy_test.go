package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"webfile/internal/clienterror"
)

func TestTaskErrorPrivacyAcrossEventsHistoryAndRestart(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	database := filepath.Join(t.TempDir(), "tasks.db")
	manager, err := Open(database, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()
	private := "/private/server-only/staging/file"
	op := manager.Start("copy", []string{"/Files/file"}, "/Files/target", func(context.Context, Reporter) error {
		return &os.PathError{Op: "lstat", Path: private, Err: fs.ErrNotExist}
	})
	failed := waitForState(t, manager, op.ID, StateFailed)
	if failed.Error != string(clienterror.NotFound) {
		t.Fatalf("failure: %#v", failed)
	}
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("missing failure event")
	default:
		found := false
		for len(events) > 0 {
			event := <-events
			encoded, _ := json.Marshal(event)
			if strings.Contains(string(encoded), private) || strings.Contains(string(encoded), "lstat") {
				t.Fatalf("event leaked: %s", encoded)
			}
			found = found || event.State == StateFailed
		}
		if !found {
			t.Fatal("missing failure event")
		}
	}
	if !strings.Contains(logs.String(), private) {
		t.Fatal("server diagnostic was not logged")
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(database, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if history := reopened.List(); len(history) != 1 || history[0].Error != string(clienterror.NotFound) {
		t.Fatalf("history: %#v", history)
	}
}

func TestLegacyTaskHistoryDoesNotExposeDiagnostics(t *testing.T) {
	database := filepath.Join(t.TempDir(), "tasks.db")
	store, err := OpenStore(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Upsert(Operation{ID: "legacy", Kind: "extract", State: StateFailed, StartedAt: now, UpdatedAt: now,
		Error: "lstat /private/server/file: permission denied", CleanupError: "remove /private/stage: failed"}, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(database, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	legacy, _ := manager.Get("legacy")
	if legacy.Error != string(clienterror.Unknown) || legacy.CleanupError != string(clienterror.Cleanup) {
		t.Fatalf("legacy task leaked: %#v", legacy)
	}
}
