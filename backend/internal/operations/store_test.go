package operations

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStoreMigratesLegacyProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    sources_json TEXT NOT NULL,
    destination TEXT NOT NULL,
    state TEXT NOT NULL,
    phase TEXT NOT NULL,
    message TEXT NOT NULL,
    error TEXT NOT NULL,
    bytes_done INTEGER NOT NULL,
    bytes_total INTEGER NOT NULL,
    items_done INTEGER NOT NULL,
    items_total INTEGER NOT NULL,
    cancellable INTEGER NOT NULL,
    cleanup_policy TEXT NOT NULL,
    cleanup_error TEXT NOT NULL,
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT
);
INSERT INTO tasks (
    id, kind, sources_json, destination, state, phase, message, error,
    bytes_done, bytes_total, items_done, items_total, cancellable,
    cleanup_policy, cleanup_error, started_at, updated_at, finished_at
) VALUES (
    'legacy', 'copy', '["/source"]', '/destination', 'running', 'transferring', 'Copying', '',
    5, 10, 1, 2, 1, '', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL
);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	operations, err := store.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 {
		t.Fatalf("operations=%d", len(operations))
	}
	operation := operations[0]
	if operation.ProgressDone != 5 || operation.ProgressTotal != 10 || operation.ProgressUnit != ProgressUnitBytes {
		t.Fatalf("progress=%d/%d %q", operation.ProgressDone, operation.ProgressTotal, operation.ProgressUnit)
	}
	operation.UpdatedAt = time.Now().UTC()
	if err := store.Upsert(operation, false); err != nil {
		t.Fatalf("upsert migrated task: %v", err)
	}
}
