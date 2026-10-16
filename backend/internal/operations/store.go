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
    progress_done INTEGER NOT NULL DEFAULT 0,
    progress_total INTEGER NOT NULL DEFAULT 0,
    progress_unit TEXT NOT NULL DEFAULT '',
    bytes_done INTEGER NOT NULL,
    bytes_total INTEGER NOT NULL,
    items_done INTEGER NOT NULL,
    items_total INTEGER NOT NULL,
    queue_position INTEGER NOT NULL DEFAULT 0,
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
	if err := ensureTaskColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate task progress: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func ensureTaskColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	added := false
	for name, definition := range map[string]string{
		"progress_done":  "INTEGER NOT NULL DEFAULT 0",
		"progress_total": "INTEGER NOT NULL DEFAULT 0",
		"progress_unit":  "TEXT NOT NULL DEFAULT ''",
		"queue_position": "INTEGER NOT NULL DEFAULT 0",
	} {
		if !columns[name] {
			if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN " + name + " " + definition); err != nil {
				return err
			}
			added = true
		}
	}
	if added {
		_, err = db.Exec(`UPDATE tasks SET
			progress_done = CASE WHEN bytes_total > 0 THEN bytes_done ELSE items_done END,
			progress_total = CASE WHEN bytes_total > 0 THEN bytes_total ELSE items_total END,
			progress_unit = CASE WHEN bytes_total > 0 THEN 'bytes' WHEN items_total > 0 THEN 'items' ELSE '' END`)
	}
	return err
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
    progress_done, progress_total, progress_unit, bytes_done, bytes_total, items_done, items_total, queue_position, cancellable,
    cleanup_policy, cleanup_error, started_at, updated_at, finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    kind = excluded.kind,
    sources_json = excluded.sources_json,
    destination = excluded.destination,
    state = excluded.state,
    phase = excluded.phase,
    message = excluded.message,
    error = excluded.error,
    progress_done = excluded.progress_done,
    progress_total = excluded.progress_total,
    progress_unit = excluded.progress_unit,
    bytes_done = excluded.bytes_done,
    bytes_total = excluded.bytes_total,
    items_done = excluded.items_done,
    items_total = excluded.items_total,
    queue_position = excluded.queue_position,
    cancellable = excluded.cancellable,
    cleanup_policy = excluded.cleanup_policy,
    cleanup_error = excluded.cleanup_error,
    updated_at = excluded.updated_at,
    finished_at = excluded.finished_at`,
		operation.ID, operation.Kind, string(sources), operation.Destination, operation.State, operation.Phase,
		operation.Message, operation.Error, operation.ProgressDone, operation.ProgressTotal, operation.ProgressUnit,
		operation.BytesDone, operation.BytesTotal, operation.ItemsDone, operation.ItemsTotal, operation.QueuePosition,
		operation.Cancellable, operation.CleanupPolicy, operation.CleanupError,
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
	if len(operation.Input) > 0 {
		_, err = tx.Exec(`INSERT INTO task_checkpoints(task_id,name,value_json,updated_at) VALUES(?,'input',?,?) ON CONFLICT(task_id,name) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at`, operation.ID, string(operation.Input), formatTime(operation.UpdatedAt))
	} else {
		_, err = tx.Exec(`DELETE FROM task_checkpoints WHERE task_id=? AND name='input'`, operation.ID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) List(limit int) ([]Operation, error) {
	return s.list(`ORDER BY started_at DESC LIMIT ?`, limit)
}

func (s *Store) listForRecovery(limit int) ([]Operation, error) {
	return s.list(`
WHERE tasks.state NOT IN ('completed', 'failed', 'cancelled', 'interrupted')
   OR tasks.id IN (
       SELECT id FROM tasks
       WHERE state IN ('completed', 'failed', 'cancelled', 'interrupted')
       ORDER BY updated_at DESC LIMIT ?
   )
ORDER BY started_at DESC`, limit)
}

func (s *Store) list(tail string, limit int) ([]Operation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT id, kind, sources_json, destination, state, phase, message, error,
       progress_done, progress_total, progress_unit, bytes_done, bytes_total, items_done, items_total, queue_position, cancellable,
       cleanup_policy, cleanup_error, started_at, tasks.updated_at, finished_at, COALESCE(checkpoint.value_json,'')
FROM tasks LEFT JOIN task_checkpoints checkpoint ON tasks.id=checkpoint.task_id AND checkpoint.name='input'
`+tail, limit)
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
		var input string
		if err := rows.Scan(
			&operation.ID, &operation.Kind, &sources, &operation.Destination, &state, &operation.Phase,
			&operation.Message, &operation.Error, &operation.ProgressDone, &operation.ProgressTotal,
			&operation.ProgressUnit, &operation.BytesDone, &operation.BytesTotal,
			&operation.ItemsDone, &operation.ItemsTotal, &operation.QueuePosition, &operation.Cancellable, &operation.CleanupPolicy,
			&operation.CleanupError, &startedAt, &updatedAt, &finishedAt, &input,
		); err != nil {
			return nil, err
		}
		operation.State = State(state)
		if input != "" {
			operation.Input = json.RawMessage(input)
		}
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
