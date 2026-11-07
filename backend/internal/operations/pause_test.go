package operations

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/taskcontrol"
)

func TestPausedTaskDoesNotBlockOtherTasksAndCanCancel(t *testing.T) {
	m := New(20)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := m.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	started := make(chan struct{})
	step := make(chan struct{})
	op := m.Start("copy", nil, "", func(ctx context.Context, r Reporter) error {
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-step:
		}
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		r.Update(1, 2, 0, 1, "Copied a chunk")
		return nil
	})
	<-started
	if _, err := m.SetPaused(op.ID, true); err != nil {
		t.Fatal(err)
	}
	close(step)
	other := m.Start("delete", nil, "", func(context.Context, Reporter) error { return nil })
	awaitTask(t, m, other.ID, StateCompleted)
	paused, _ := m.Get(op.ID)
	if !paused.Paused || paused.BytesDone != 0 {
		t.Fatalf("paused task progressed: %+v", paused)
	}
	if !m.Cancel(op.ID) {
		t.Fatal("cancel rejected")
	}
	awaitTask(t, m, op.ID, StateCancelled)
}

func TestQueuedPauseSkipsTaskUntilResume(t *testing.T) {
	m := newManager(nil, 20, 1)
	block := make(chan struct{})
	first := m.Start("copy", nil, "", func(context.Context, Reporter) error { <-block; return nil })
	paused := m.Start("copy", nil, "", func(context.Context, Reporter) error { return nil })
	if _, err := m.SetPaused(paused.ID, true); err != nil {
		t.Fatal(err)
	}
	next := m.Start("copy", nil, "", func(context.Context, Reporter) error { return nil })
	close(block)
	awaitTask(t, m, first.ID, StateCompleted)
	awaitTask(t, m, next.ID, StateCompleted)
	op, _ := m.Get(paused.ID)
	if !op.Paused || op.State != StateQueued {
		t.Fatalf("paused queue task started: %+v", op)
	}
	if _, err := m.SetPaused(paused.ID, false); err != nil {
		t.Fatal(err)
	}
	awaitTask(t, m, paused.ID, StateCompleted)
	if _, err := m.SetPaused(paused.ID, true); err == nil {
		t.Fatal("completed task was paused")
	}
}

func TestPausedTaskPersistenceAndRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tasks.db")
	s, err := OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.Upsert(Operation{ID: "paused", Kind: "extract", State: StateRunning, Paused: true, Cancellable: true, StartedAt: now, UpdatedAt: now, ProgressDone: 42, ProgressTotal: 100, ProgressUnit: ProgressUnitPercent}, true); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(10)
	if err != nil || len(list) != 1 || !list[0].Paused {
		t.Fatalf("paused state not persisted: %+v %v", list, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := Open(db, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	op, _ := m.Get("paused")
	if op.State != StateInterrupted || op.Paused {
		t.Fatalf("restart kept task paused: %+v", op)
	}
}

func awaitTask(t *testing.T, m *Manager, id string, state State) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, _ := m.Get(id)
		if op.State == state {
			if op.Paused {
				t.Fatal("terminal task is paused")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %s: %+v", state, op)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPauseColumnMigrationPreservesProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.Upsert(Operation{ID: "percent", Kind: "extract", State: StateCompleted, ProgressDone: 42, ProgressTotal: 100, ProgressUnit: ProgressUnitPercent, StartedAt: now, UpdatedAt: now}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE tasks DROP COLUMN paused"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.List(10)
	if err != nil || len(list) != 1 || list[0].ProgressDone != 42 || list[0].ProgressUnit != ProgressUnitPercent {
		t.Fatalf("migration changed progress: %+v %v", list, err)
	}
}
