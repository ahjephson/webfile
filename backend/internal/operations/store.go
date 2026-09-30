package operations

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const taskSchema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS tasks (
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

CREATE INDEX IF NOT EXISTS tasks_started_at_idx ON tasks(started_at DESC);
CREATE INDEX IF NOT EXISTS tasks_state_idx ON tasks(state);

CREATE TABLE IF NOT EXISTS task_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    phase TEXT NOT NULL,
    message TEXT NOT NULL,
    error TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS task_events_task_idx ON task_events(task_id, sequence);

CREATE TABLE IF NOT EXISTS task_artifacts (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    kind TEXT NOT NULL,
    cleanup_state TEXT NOT NULL,
    PRIMARY KEY(task_id, path)
);

CREATE TABLE IF NOT EXISTS task_checkpoints (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(task_id, name)
);`

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("task database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(taskSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize task database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Upsert(operation Operation, recordEvent bool) error {
	sources, err := json.Marshal(operation.Sources)
	if err != nil {
		return err
	}
	finished := any(nil)
	if operation.FinishedAt != nil {
		finished = formatTime(*operation.FinishedAt)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
INSERT INTO tasks (
    id, kind, sources_json, destination, state, phase, message, error,
    bytes_done, bytes_total, items_done, items_total, cancellable,
    cleanup_policy, cleanup_error, started_at, updated_at, finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    kind = excluded.kind,
    sources_json = excluded.sources_json,
    destination = excluded.destination,
    state = excluded.state,
    phase = excluded.phase,
    message = excluded.message,
    error = excluded.error,
    bytes_done = excluded.bytes_done,
    bytes_total = excluded.bytes_total,
    items_done = excluded.items_done,
    items_total = excluded.items_total,
    cancellable = excluded.cancellable,
    cleanup_policy = excluded.cleanup_policy,
    cleanup_error = excluded.cleanup_error,
    updated_at = excluded.updated_at,
    finished_at = excluded.finished_at`,
		operation.ID, operation.Kind, string(sources), operation.Destination, operation.State, operation.Phase,
		operation.Message, operation.Error, operation.BytesDone, operation.BytesTotal, operation.ItemsDone,
		operation.ItemsTotal, operation.Cancellable, operation.CleanupPolicy, operation.CleanupError,
		formatTime(operation.StartedAt), formatTime(operation.UpdatedAt), finished)
	if err != nil {
		return err
	}
	if recordEvent {
		if _, err := tx.Exec(`INSERT INTO task_events (task_id, state, phase, message, error, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			operation.ID, operation.State, operation.Phase, operation.Message, operation.Error, formatTime(operation.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) List(limit int) ([]Operation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT id, kind, sources_json, destination, state, phase, message, error,
       bytes_done, bytes_total, items_done, items_total, cancellable,
       cleanup_policy, cleanup_error, started_at, updated_at, finished_at
FROM tasks
ORDER BY started_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := make([]Operation, 0)
	for rows.Next() {
		var operation Operation
		var sources string
		var state string
		var startedAt string
		var updatedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(
			&operation.ID, &operation.Kind, &sources, &operation.Destination, &state, &operation.Phase,
			&operation.Message, &operation.Error, &operation.BytesDone, &operation.BytesTotal,
			&operation.ItemsDone, &operation.ItemsTotal, &operation.Cancellable, &operation.CleanupPolicy,
			&operation.CleanupError, &startedAt, &updatedAt, &finishedAt,
		); err != nil {
			return nil, err
		}
		operation.State = State(state)
		if err := json.Unmarshal([]byte(sources), &operation.Sources); err != nil {
			return nil, err
		}
		operation.StartedAt, err = parseTime(startedAt)
		if err != nil {
			return nil, err
		}
		operation.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			finished, err := parseTime(finishedAt.String)
			if err != nil {
				return nil, err
			}
			operation.FinishedAt = &finished
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (s *Store) Trim(maxKeep int) error {
	if maxKeep <= 0 {
		return nil
	}
	_, err := s.db.Exec(`
DELETE FROM tasks
WHERE id IN (
    SELECT id FROM tasks
    WHERE state IN ('completed', 'failed', 'cancelled', 'interrupted')
    ORDER BY updated_at DESC
    LIMIT -1 OFFSET ?
)`, maxKeep)
	return err
}

func (s *Store) ClearHistory() (int64, error) {
	result, err := s.db.Exec(`
DELETE FROM tasks
WHERE state IN ('completed', 'failed', 'cancelled', 'interrupted')`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) RemoveHistory(id string) (bool, error) {
	result, err := s.db.Exec(`
DELETE FROM tasks
WHERE id = ? AND state IN ('completed', 'failed', 'cancelled', 'interrupted')`, id)
	if err != nil {
		return false, err
	}
	removed, err := result.RowsAffected()
	return removed == 1, err
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
