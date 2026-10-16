package operations

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryLimitCountsOnlyFinishedTasks(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprintf("persistent=%v", persistent), func(t *testing.T) {
			manager := New(3)
			if persistent {
				var err error
				manager, err = Open(filepath.Join(t.TempDir(), "tasks.db"), 3, 2)
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { _ = manager.Close(context.Background()) })
			for index := 0; index < 3; index++ {
				manager.UpsertExternal(fmt.Sprintf("finished-%d", index), "upload", nil, "", StateCompleted, 1, 1, 1, 1, "done", "")
			}
			for index := 0; index < 7; index++ {
				manager.UpsertExternal(fmt.Sprintf("active-%d", index), "upload", nil, "", StateRunning, 0, 1, 0, 1, "active", "")
			}
			if got := len(manager.List()); got != 10 {
				t.Fatalf("active tasks consumed history allowance: got %d records, want 10", got)
			}
			manager.UpsertExternal("newest", "upload", nil, "", StateCompleted, 1, 1, 1, 1, "done", "")
			if _, exists := manager.Get("finished-0"); exists {
				t.Fatal("oldest finished task was not evicted")
			}
			if got := len(manager.List()); got != 10 {
				t.Fatalf("retained %d records, want 3 finished plus 7 active", got)
			}
			if persistent {
				stored, err := manager.store.List(100)
				if err != nil || len(stored) != 10 {
					t.Fatalf("persistent retention disagrees: %d %v", len(stored), err)
				}
			}
		})
	}
}

func TestRestartRecoversAllActiveTasksBeyondHistoryLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	states := []State{StateQueued, StatePreparing, StateAwaitingInput, StateRunning, StateCancelling, StateCleaningUp}
	for index := 0; index < 115; index++ {
		state := StateCompleted
		if index < 105 {
			state = states[index%len(states)]
		}
		started := now.Add(time.Duration(index) * time.Second)
		op := Operation{ID: fmt.Sprintf("task-%d", index), Kind: "upload", State: state, StartedAt: started, UpdatedAt: started, Cancellable: true}
		if state == StateAwaitingInput {
			op.Input = []byte(`{"revision":1}`)
		}
		if err := store.Upsert(op, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	stored, err := manager.store.List(1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range stored {
		if activeState(operation.State) {
			t.Fatalf("task %s was left %s after restart", operation.ID, operation.State)
		}
		if operation.State == StateInterrupted && (operation.Cancellable || operation.FinishedAt == nil || len(operation.Input) != 0) {
			t.Fatalf("invalid interrupted task: %#v", operation)
		}
	}
	if len(stored) != 100 || len(manager.List()) != 100 {
		t.Fatalf("finished retention after recovery: database=%d memory=%d", len(stored), len(manager.List()))
	}
	if manager.running != 0 || len(manager.queue) != 0 {
		t.Fatal("recovered tasks were scheduled for replay")
	}
}

func TestHistoryCleanupFailureRetainsPersistentRecordForRetry(t *testing.T) {
	manager, err := Open(filepath.Join(t.TempDir(), "tasks.db"), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	failed := true
	manager.SetHistoryCleanup(func(operation Operation) error {
		if failed {
			return errors.New("temporary cleanup failure")
		}
		return nil
	})
	manager.UpsertExternal("old", "archive", nil, "", StateCompleted, 1, 1, 1, 1, "done", "")
	manager.UpsertExternal("new", "upload", nil, "", StateCompleted, 1, 1, 1, 1, "done", "")
	if _, ok := manager.Get("old"); !ok {
		t.Fatal("failed cleanup lost its record")
	}
	stored, err := manager.store.List(10)
	if err != nil || len(stored) != 2 {
		t.Fatalf("persistent retry records: %v %v", stored, err)
	}
	failed = false
	manager.UpsertExternal("new", "upload", nil, "", StateCompleted, 1, 1, 1, 1, "done", "")
	if _, ok := manager.Get("old"); ok {
		t.Fatal("successful retry did not evict old record")
	}
	stored, err = manager.store.List(10)
	if err != nil || len(stored) != 1 || stored[0].ID != "new" {
		t.Fatalf("persistent eviction: %v %v", stored, err)
	}
}

func TestOperationCompletesAndReportsProgress(t *testing.T) {
	manager := New(10)
	updates, cancel := manager.Subscribe()
	defer cancel()

	op := manager.Start("copy", []string{"/a"}, "/b", func(ctx context.Context, reporter Reporter) error {
		reporter.Update(5, 10, 1, 2, "copying")
		reporter.Update(10, 10, 2, 2, "done")
		return nil
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.ID == op.ID && update.State == StateCompleted {
				if update.BytesDone != 10 || update.ItemsDone != 2 {
					t.Fatalf("unexpected progress: %#v", update)
				}
				return
			}
		case <-deadline:
			t.Fatal("operation did not complete")
		}
	}
}

func TestCopyingOperationReportsAndClearsTransferRate(t *testing.T) {
	manager := New(10)
	updates, cancel := manager.Subscribe()
	defer cancel()

	op := manager.Start("copy", []string{"/a"}, "/b", func(_ context.Context, reporter Reporter) error {
		reporter.UpdatePhase("copying", 0, 100, 0, 1, "Copying a")
		reporter.UpdatePhase("copying", 50, 100, 0, 1, "Copying a")
		// Simulate the next broadcast interval without a wall-clock sleep.
		manager.mu.Lock()
		manager.records[reporter.ID()].lastBroadcast = time.Time{}
		manager.mu.Unlock()
		reporter.UpdatePhase("copying", 100, 100, 0, 1, "Copying a")
		manager.mu.Lock()
		manager.records[reporter.ID()].lastBroadcast = time.Time{}
		manager.mu.Unlock()
		reporter.UpdatePhase("flushing", 100, 100, 0, 1, "Copying a")
		return nil
	})

	foundRate := false
	foundClearedRate := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.ID != op.ID {
				continue
			}
			if update.Phase == "copying" && update.BytesPerSecond != nil {
				foundRate = *update.BytesPerSecond > 0
			}
			if update.Phase == "flushing" && update.BytesPerSecond == nil {
				foundClearedRate = true
			}
			if update.State == StateCompleted {
				if !foundRate || !foundClearedRate {
					t.Fatalf("rate lifecycle missing: rate=%v cleared=%v", foundRate, foundClearedRate)
				}
				return
			}
		case <-deadline:
			t.Fatal("operation did not complete")
		}
	}
}

func TestProgressUnitAndPhaseChangesBroadcastAtomically(t *testing.T) {
	manager := New(10)
	updates, cancel := manager.Subscribe()
	defer cancel()
	manager.Start("extract", []string{"/archive"}, "/output", func(_ context.Context, reporter Reporter) error {
		reporter.Report(ProgressUpdate{
			Phase: "extracting", Message: "Extracting archive",
			ProgressDone: 64, ProgressTotal: 100, ProgressUnit: ProgressUnitPercent,
		})
		reporter.Report(ProgressUpdate{Phase: "validating", Message: "Validating extracted files"})
		return nil
	})
	foundPercent := false
	foundValidation := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Phase == "extracting" {
				foundPercent = true
				if update.ProgressDone != 64 || update.ProgressTotal != 100 || update.ProgressUnit != ProgressUnitPercent {
					t.Fatalf("unexpected extraction progress: %#v", update)
				}
				if update.BytesDone != 0 || update.BytesTotal != 0 {
					t.Fatalf("percentage was reported as bytes: %#v", update)
				}
			}
			if update.Phase == "validating" {
				foundValidation = true
				if update.ProgressTotal != 0 || update.ProgressUnit != "" {
					t.Fatalf("validation retained stale progress: %#v", update)
				}
			}
			if update.State == StateCompleted {
				if !foundPercent || !foundValidation {
					t.Fatalf("missing phase broadcasts: percent=%v validation=%v", foundPercent, foundValidation)
				}
				return
			}
		case <-deadline:
			t.Fatal("operation did not complete")
		}
	}
}

func TestOperationCanBeCancelled(t *testing.T) {
	manager := New(10)
	started := make(chan struct{})
	op := manager.Start("copy", nil, "", func(ctx context.Context, reporter Reporter) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	if !manager.Cancel(op.ID) {
		t.Fatal("expected cancel to succeed")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, current := range manager.List() {
			if current.ID == op.ID && current.State == StateCancelled {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not become cancelled")
}

func TestCancellationCleanupFailureReportsRecoveryInsteadOfCancelled(t *testing.T) {
	manager, err := Open(filepath.Join(t.TempDir(), "tasks.db"), 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	started := make(chan struct{})
	recovery := errors.New("rollback incomplete; recovery files retained at /private/stage")
	op := manager.StartTask("extract", nil, "", TaskOptions{
		Cancellable: true,
		Cleanup:     func(context.Context, Operation) error { return recovery },
	}, func(ctx context.Context, reporter Reporter) error {
		close(started)
		<-ctx.Done()
		return errors.Join(ctx.Err(), recovery)
	})
	<-started
	if !manager.Cancel(op.ID) {
		t.Fatal("cancel failed")
	}
	finished := waitForState(t, manager, op.ID, StateFailed)
	if finished.Error != recovery.Error() || finished.CleanupError != recovery.Error() {
		t.Fatalf("recovery details hidden: %#v", finished)
	}
	stored, err := manager.store.List(10)
	if err != nil || len(stored) != 1 || stored[0].Error != finished.Error || stored[0].CleanupError != finished.CleanupError {
		t.Fatalf("recovery details not persisted: %#v %v", stored, err)
	}
}

func TestFailedOperationStoresError(t *testing.T) {
	manager := New(10)
	want := errors.New("boom")
	op := manager.Start("copy", nil, "", func(context.Context, Reporter) error { return want })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, current := range manager.List() {
			if current.ID == op.ID && current.State == StateFailed {
				if current.Error != want.Error() {
					t.Fatalf("got error %q", current.Error)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not fail")
}

func TestTasksQueueAtConfiguredConcurrency(t *testing.T) {
	manager := newManager(nil, 10, 1)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		close(firstStarted)
		<-firstRelease
		return nil
	})
	<-firstStarted
	second := manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		close(secondStarted)
		return nil
	})

	for _, operation := range manager.List() {
		if operation.ID == second.ID && operation.State != StateQueued {
			t.Fatalf("second task state is %q, want %q", operation.State, StateQueued)
		}
	}
	select {
	case <-secondStarted:
		t.Fatal("second task started before the first completed")
	default:
	}

	close(firstRelease)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second task did not start after capacity became available")
	}
}

func TestTasksRunAtMostOneOfEachKind(t *testing.T) {
	manager := newManager(nil, 10, 2)
	firstCopyStarted := make(chan struct{})
	firstCopyRelease := make(chan struct{})
	secondCopyStarted := make(chan struct{})
	extractStarted := make(chan struct{})
	extractRelease := make(chan struct{})

	manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		close(firstCopyStarted)
		<-firstCopyRelease
		return nil
	})
	<-firstCopyStarted
	second := manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		close(secondCopyStarted)
		return nil
	})
	manager.Start("extract", nil, "", func(context.Context, Reporter) error {
		close(extractStarted)
		<-extractRelease
		return nil
	})

	select {
	case <-extractStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("different task kind did not use the second slot")
	}
	select {
	case <-secondCopyStarted:
		t.Fatal("second copy ran concurrently with the first")
	default:
	}
	for _, operation := range manager.List() {
		if operation.ID == second.ID && (operation.State != StateQueued || operation.QueuePosition != 1) {
			t.Fatalf("second copy was not queued first: %#v", operation)
		}
	}

	close(firstCopyRelease)
	select {
	case <-secondCopyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second copy did not start when its kind became available")
	}
	close(extractRelease)
}

func TestQueuedTasksCanBeReordered(t *testing.T) {
	manager := newManager(nil, 10, 2)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	thirdStarted := make(chan struct{})
	manager.Start("extract", nil, "", func(context.Context, Reporter) error {
		close(firstStarted)
		<-firstRelease
		return nil
	})
	<-firstStarted
	second := manager.Start("extract", []string{"/second"}, "", func(context.Context, Reporter) error {
		close(secondStarted)
		return nil
	})
	third := manager.Start("extract", []string{"/third"}, "", func(context.Context, Reporter) error {
		close(thirdStarted)
		return nil
	})

	if _, moved := manager.MoveQueued(third.ID, -1); !moved {
		t.Fatal("third task was not moved up")
	}
	for _, operation := range manager.List() {
		if operation.ID == third.ID && operation.QueuePosition != 1 {
			t.Fatalf("third queue position=%d, want 1", operation.QueuePosition)
		}
		if operation.ID == second.ID && operation.QueuePosition != 2 {
			t.Fatalf("second queue position=%d, want 2", operation.QueuePosition)
		}
	}
	close(firstRelease)
	select {
	case <-thirdStarted:
	case <-secondStarted:
		t.Fatal("second task started before reordered third task")
	case <-time.After(2 * time.Second):
		t.Fatal("reordered task did not start")
	}
}

func TestQueuedTaskCanBeCancelledWithoutRunning(t *testing.T) {
	manager := newManager(nil, 10, 1)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		close(firstStarted)
		<-firstRelease
		return nil
	})
	<-firstStarted
	second := manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		close(secondStarted)
		return nil
	})

	if !manager.Cancel(second.ID) {
		t.Fatal("expected queued cancellation to succeed")
	}
	close(firstRelease)
	time.Sleep(50 * time.Millisecond)
	select {
	case <-secondStarted:
		t.Fatal("cancelled queued task was run")
	default:
	}
	for _, operation := range manager.List() {
		if operation.ID == second.ID && operation.State != StateCancelled {
			t.Fatalf("cancelled task state is %q, want %q", operation.State, StateCancelled)
		}
	}
}

func TestTaskHistoryPersistsAndRunningTasksBecomeInterrupted(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tasks.db")
	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	operation := Operation{
		ID:          "ID",
		Kind:        "copy",
		Sources:     []string{"/Source"},
		Destination: "/Destination",
		State:       StateRunning,
		Phase:       "transferring",
		Message:     "Copying",
		Cancellable: true,
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.Upsert(operation, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	manager, err := Open(databasePath, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	operations := manager.List()
	if len(operations) != 1 {
		t.Fatalf("got %d operations, want 1", len(operations))
	}
	if operations[0].State != StateInterrupted {
		t.Fatalf("state is %q, want %q", operations[0].State, StateInterrupted)
	}
	if operations[0].FinishedAt == nil {
		t.Fatal("interrupted task has no finished time")
	}
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestClearHistoryPreservesActiveTasks(t *testing.T) {
	manager := newManager(nil, 10, 1)
	manager.Start("copy", nil, "", func(context.Context, Reporter) error {
		return nil
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		operations := manager.List()
		if len(operations) == 1 && operations[0].State == StateCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	active := manager.Start("move", nil, "", func(context.Context, Reporter) error {
		close(started)
		<-release
		return nil
	})
	<-started

	removed, err := manager.ClearHistory()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d tasks, want 1", removed)
	}
	operations := manager.List()
	if len(operations) != 1 {
		t.Fatalf("got %d tasks after clearing history, want 1", len(operations))
	}
	if operations[0].ID != active.ID || operations[0].State != StateRunning {
		t.Fatalf("active task was not preserved: %#v", operations[0])
	}
	removedActive, err := manager.RemoveHistory(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removedActive {
		t.Fatal("active task was removed individually")
	}
	close(release)
}

func TestStoreClearHistoryDeletesTerminalTasksOnly(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	finished := now
	completed := Operation{ID: "Completed", Kind: "copy", State: StateCompleted, StartedAt: now, UpdatedAt: now, FinishedAt: &finished}
	cancelled := Operation{ID: "Cancelled", Kind: "extract", State: StateCancelled, StartedAt: now, UpdatedAt: now, FinishedAt: &finished}
	running := Operation{ID: "Running", Kind: "move", State: StateRunning, Cancellable: true, StartedAt: now, UpdatedAt: now}
	if err := store.Upsert(completed, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(running, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(cancelled, true); err != nil {
		t.Fatal(err)
	}
	removedOne, err := store.RemoveHistory(completed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !removedOne {
		t.Fatal("completed task was not removed individually")
	}
	removedActive, err := store.RemoveHistory(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removedActive {
		t.Fatal("active task was removed individually")
	}

	removed, err := store.ClearHistory()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d tasks, want 1", removed)
	}
	operations, err := store.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].ID != running.ID {
		t.Fatalf("active task was not preserved: %#v", operations)
	}
}
