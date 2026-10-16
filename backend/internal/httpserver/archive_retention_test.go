package httpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/operations"
)

func TestHistoryEvictionRemovesPreparedArchive(t *testing.T) {
	manager := operations.New(1)
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	directory := t.TempDir()
	_ = NewWorker(nil, nil, nil, nil, manager, directory)
	manager.UpsertExternal("old", "archive", nil, "", operations.StateCompleted, 1, 1, 1, 1, "ready", "")
	path := filepath.Join(directory, "old.zip")
	if err := os.WriteFile(path, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	manager.UpsertExternal("new", "upload", nil, "", operations.StateCompleted, 1, 1, 1, 1, "done", "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("evicted archive remains: %v", err)
	}
}

func TestWorkerStartupCleansOrphansAndPreservesRetainedArchives(t *testing.T) {
	manager := operations.New(10)
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	directory := t.TempDir()
	manager.UpsertExternal("retained", "archive", nil, "", operations.StateCompleted, 1, 1, 1, 1, "ready", "")
	manager.UpsertExternal("active", "archive", nil, "", operations.StateRunning, 0, 1, 0, 1, "working", "")
	manager.UpsertExternal("interrupted", "archive", nil, "", operations.StateInterrupted, 0, 1, 0, 1, "interrupted", "")
	manager.UpsertExternal("failed", "archive", nil, "", operations.StateFailed, 0, 1, 0, 1, "failed", "")
	for _, name := range []string{"orphan.zip", "orphan.partial.zip", "interrupted.partial.zip", "failed.zip", "retained.zip", "active.partial.zip", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_ = NewWorker(nil, nil, nil, nil, manager, directory)
	for _, name := range []string{"orphan.zip", "orphan.partial.zip", "interrupted.partial.zip", "failed.zip"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("orphan %s remains: %v", name, err)
		}
	}
	for _, name := range []string{"retained.zip", "active.partial.zip", "unrelated.txt"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("preserved %s: %v", name, err)
		}
	}
}
