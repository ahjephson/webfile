package operations

import (
	"context"
	"encoding/json"
	"errors"
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
