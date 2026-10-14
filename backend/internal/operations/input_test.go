package operations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func waitForState(t *testing.T, manager *Manager, id string, state State) Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, _ := manager.Get(id)
		if op.State == state {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatal("state did not settle", op, state)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAwaitingDecisionReleasesWorkerSlotAndResumesExactlyOnce(t *testing.T) {
	manager := newManager(nil, 20, 1)
	defer manager.Close(context.Background())
	accepted := false
	first := manager.StartTask("copy", []string{"source"}, "destination", TaskOptions{Cancellable: true, InputHandler: func(data json.RawMessage) error { accepted = true; return nil }}, func(ctx context.Context, r Reporter) error {
		if !accepted {
			if err := r.SetInput(map[string]string{"revision": "review-1"}); err != nil {
				return err
			}
			return ErrAwaitingInput
		}
		return nil
	})
	awaiting := waitForState(t, manager, first.ID, StateAwaitingInput)
	if len(awaiting.Input) == 0 || !awaiting.Cancellable {
		t.Fatal(awaiting)
	}
	second := manager.Start("copy", nil, "", func(context.Context, Reporter) error { return nil })
	waitForState(t, manager, second.ID, StateCompleted)
	if _, err := manager.SubmitInput(first.ID, json.RawMessage(`{"choice":"skip"}`)); err != nil {
		t.Fatal(err)
	}
	completed := waitForState(t, manager, first.ID, StateCompleted)
	if len(completed.Input) != 0 {
		t.Fatal("completed task retained input", completed)
	}
	if _, err := manager.SubmitInput(first.ID, json.RawMessage(`{}`)); err == nil {
		t.Fatal("duplicate decision accepted")
	}
}

func TestAwaitingDecisionCanBeCancelledOrInterruptedWithoutReplay(t *testing.T) {
	database := filepath.Join(t.TempDir(), "tasks.db")
	manager, err := Open(database, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	start := func() Operation {
		return manager.StartTask("copy", nil, "", TaskOptions{Cancellable: true, InputHandler: func(json.RawMessage) error { t.Error("decision replayed"); return nil }}, func(ctx context.Context, r Reporter) error {
			r.SetInput(map[string]string{"revision": "review-1"})
			return ErrAwaitingInput
		})
	}
	cancelled := start()
	waitForState(t, manager, cancelled.ID, StateAwaitingInput)
	if !manager.Cancel(cancelled.ID) {
		t.Fatal("waiting task could not be cancelled")
	}
	waitForState(t, manager, cancelled.ID, StateCancelled)
	interrupted := start()
	waitForState(t, manager, interrupted.ID, StateAwaitingInput)
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(database, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	op := waitForState(t, reopened, interrupted.ID, StateInterrupted)
	if len(op.Input) != 0 || op.Cancellable {
		t.Fatal("interrupted task kept stale decision", op)
	}
	if _, err := reopened.SubmitInput(interrupted.ID, json.RawMessage(`{}`)); err == nil {
		t.Fatal("interrupted decision accepted")
	}
}
