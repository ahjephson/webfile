package operations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUndoSurvivesRestartAndCanOnlyBeClaimedOnce(t *testing.T) {
	database := filepath.Join(t.TempDir(), "tasks.db")
	m, err := Open(database, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	op := m.Start("copy", []string{"/Files/from/a"}, "/Files/to", func(_ context.Context, r Reporter) error {
		name, err := r.UndoJournalPath()
		if err != nil {
			return err
		}
		if err := os.WriteFile(name, []byte("test journal"), 0600); err != nil {
			return err
		}
		return r.SetUndo(map[string]string{"private": "journal"}, "")
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, _ := m.Get(op.ID)
		if current.State == StateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err = Open(database, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := m.Get(op.ID)
	if !current.UndoAvailable {
		t.Fatal("undo lost on restart")
	}
	encoded, _ := json.Marshal(current)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("private journal exposed to clients")
	}
	var claimed atomic.Int32
	var group sync.WaitGroup
	for range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := m.ClaimUndo(op.ID); err == nil {
				claimed.Add(1)
			}
		}()
	}
	group.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claims = %d", claimed.Load())
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err = Open(database, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	if _, err := m.ClaimUndo(op.ID); err == nil {
		t.Fatal("consumed undo revived after restart")
	}
	if removed, err := m.RemoveHistory(op.ID); err != nil || !removed {
		t.Fatal(removed, err)
	}
}

func TestUndoUnavailableForOtherKindsAndFailedTasks(t *testing.T) {
	m := New(50)
	defer m.Close(context.Background())
	for _, kind := range []string{"delete", "upload", "undo_copy"} {
		op := m.UpsertExternal(kind, kind, nil, "", StateCompleted, 0, 0, 0, 0, "", "")
		if _, err := m.ClaimUndo(op.ID); err == nil {
			t.Fatal("undo accepted for", kind)
		}
	}
	for _, state := range []State{StateFailed, StateCancelled} {
		op := m.Start("copy", nil, "", func(_ context.Context, r Reporter) error {
			if err := r.SetUndo(map[string]string{"private": "journal"}, ""); err != nil {
				return err
			}
			if state == StateCancelled {
				return context.Canceled
			}
			return errors.New("transfer failed after recording undo")
		})
		awaitTask(t, m, op.ID, state)
		current, _ := m.Get(op.ID)
		if current.UndoAvailable || len(current.UndoData) != 0 {
			t.Fatal("unfinished task retained undo")
		}
		if _, err := m.ClaimUndo(op.ID); err == nil {
			t.Fatal("undo accepted for unfinished transfer")
		}
	}
}

func completedUndo(t *testing.T, m *Manager, kind string) Operation {
	t.Helper()
	op := m.Start(kind, nil, "", func(_ context.Context, r Reporter) error {
		name, err := r.UndoJournalPath()
		if err != nil {
			return err
		}
		if name == "" {
			return nil
		}
		if err := os.WriteFile(name, []byte("receipt"), 0600); err != nil {
			return err
		}
		r.KeepUndoJournal(func() { _ = os.Remove(name) })
		return r.SetUndo(map[string]string{"journal": name}, "")
	})
	awaitTask(t, m, op.ID, StateCompleted)
	current, _ := m.Get(op.ID)
	if !current.UndoAvailable || current.UndoExpiresAt == nil {
		t.Fatal("undo not available", current)
	}
	if !current.UndoExpiresAt.Equal(current.FinishedAt.Add(time.Hour)) {
		t.Fatal("expiry is not one hour from completion")
	}
	return current
}

func TestUndoLatestOnlyAndReadOnlyTasksDoNotClearIt(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "tasks.db"), 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	first := completedUndo(t, m, "copy")
	for range 5 {
		archive := m.Start("archive", nil, "", func(context.Context, Reporter) error { return nil })
		awaitTask(t, m, archive.ID, StateCompleted)
	}
	current, ok := m.Get(first.ID)
	if !ok || !current.UndoAvailable || !current.UndoExpiresAt.Equal(*first.UndoExpiresAt) {
		t.Fatal("read-only download tasks removed or extended undo")
	}
	second := completedUndo(t, m, "move")
	if _, err := m.ClaimUndo(first.ID); err == nil {
		t.Fatal("older task remained undoable")
	}
	if _, err := os.Stat(filepath.Join(m.undoDir, first.ID+".undo")); !os.IsNotExist(err) {
		t.Fatal("older journal remains", err)
	}
	deletion := m.Start("delete", nil, "", func(context.Context, Reporter) error { return errors.New("delete failed") })
	awaitTask(t, m, deletion.ID, StateFailed)
	if _, err := m.ClaimUndo(second.ID); err == nil {
		t.Fatal("failed delete restored previous undo")
	}
	third := completedUndo(t, m, "copy")
	m.QueueExternal("upload-new", "upload", nil, "", 0, 1, "attempt")
	if _, err := m.ClaimUndo(third.ID); err == nil {
		t.Fatal("queued upload failed to invalidate undo")
	}
}

func TestUndoLateCompletionCannotResurrectSupersededSet(t *testing.T) {
	m := New(50)
	defer m.Close(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	var discarded atomic.Bool
	first := m.Start("copy", nil, "", func(_ context.Context, r Reporter) error {
		r.KeepUndoJournal(func() { discarded.Store(true) })
		close(started)
		<-release
		return r.SetUndo(map[string]string{"old": "receipt"}, "")
	})
	<-started
	second := completedUndo(t, m, "move")
	if !discarded.Load() {
		t.Fatal("active old writer was not discarded")
	}
	close(release)
	awaitTask(t, m, first.ID, StateCompleted)
	old, _ := m.Get(first.ID)
	current, _ := m.Get(second.ID)
	if old.UndoAvailable || len(old.UndoData) > 0 || !current.UndoAvailable {
		t.Fatal("late completion changed latest undo")
	}
}

func TestUndoExpiryCleanupAndRestartDoesNotExtendDeadline(t *testing.T) {
	database := filepath.Join(t.TempDir(), "tasks.db")
	m, err := Open(database, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	op := completedUndo(t, m, "copy")
	journal := filepath.Join(m.undoDir, op.ID+".undo")
	orphan := filepath.Join(m.undoDir, newID()+".undo")
	if err := os.WriteFile(orphan, []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err = Open(database, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	current, _ := m.Get(op.ID)
	if !current.UndoAvailable || !current.UndoExpiresAt.Equal(*op.UndoExpiresAt) {
		t.Fatal("restart extended or lost undo")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan remains", err)
	}
	m.mu.Lock()
	err = m.expireUndoLocked(op.UndoExpiresAt.Add(-time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if !m.records[op.ID].op.UndoAvailable {
		t.Fatal("expired early")
	}
	err = m.expireUndoLocked(*op.UndoExpiresAt)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimUndo(op.ID); err == nil {
		t.Fatal("expired undo accepted")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("expired journal remains", err)
	}
}

func TestUndoTimerExpiresWithoutARequest(t *testing.T) {
	m := New(50)
	defer m.Close(context.Background())
	op := completedUndo(t, m, "copy")
	m.mu.Lock()
	m.records[op.ID].op.UndoExpiresAt = timePointer(time.Now().Add(10 * time.Millisecond))
	m.armUndoTimerLocked()
	m.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		available := m.records[op.ID].op.UndoAvailable
		m.mu.Unlock()
		if !available {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expiry timer did not run")
}

func TestUndoOnFinishRunsForQueuedCancellation(t *testing.T) {
	m := newManager(nil, 50, 1)
	defer m.Close(context.Background())
	release := make(chan struct{})
	first := m.Start("archive", nil, "", func(context.Context, Reporter) error { <-release; return nil })
	var closed atomic.Bool
	queued := m.StartTask("undo_copy", nil, "", TaskOptions{Cancellable: true, OnFinish: func() { closed.Store(true) }}, func(context.Context, Reporter) error { t.Error("cancelled task ran"); return nil })
	if !m.Cancel(queued.ID) || !closed.Load() {
		t.Fatal("queued undo reader was not closed")
	}
	close(release)
	awaitTask(t, m, first.ID, StateCompleted)
}
