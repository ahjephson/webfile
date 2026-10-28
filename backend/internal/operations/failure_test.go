package operations

import (
	"context"
	"path/filepath"
	"testing"
)

func TestHandlerPanicFailsTaskAndReleasesSlot(t *testing.T) {
	for _, kind := range []string{"copy", "move", "delete", "extract", "archive"} {
		t.Run(kind, func(t *testing.T) {
			manager, err := Open(filepath.Join(t.TempDir(), "tasks.db"), 10, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close(context.Background())
			started, release := make(chan struct{}), make(chan struct{})
			op := manager.Start(kind, nil, "", func(context.Context, Reporter) error {
				close(started)
				<-release
				panic("unexpected filesystem failure")
			})
			<-started
			next := manager.Start(kind, nil, "", func(context.Context, Reporter) error { return nil })
			close(release)
			failed := waitForState(t, manager, op.ID, StateFailed)
			if failed.Error == "" || failed.FinishedAt == nil || failed.Cancellable {
				t.Fatalf("unfinished failure: %#v", failed)
			}
			waitForState(t, manager, next.ID, StateCompleted)
			stored, err := manager.store.List(10)
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range stored {
				if operation.ID == op.ID && operation.State == StateFailed {
					return
				}
			}
			t.Fatal("failed task was not persisted")
		})
	}
}

func TestCleanupPanicFailsCancellationAndReleasesSlot(t *testing.T) {
	manager := newManager(nil, 10, 1)
	defer manager.Close(context.Background())
	started := make(chan struct{})
	op := manager.StartTask("archive", nil, "", TaskOptions{
		Cancellable: true,
		Cleanup:     func(context.Context, Operation) error { panic("cleanup failed") },
	}, func(ctx context.Context, _ Reporter) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	next := manager.Start("copy", nil, "", func(context.Context, Reporter) error { return nil })
	if !manager.Cancel(op.ID) {
		t.Fatal("cancel failed")
	}
	failed := waitForState(t, manager, op.ID, StateFailed)
	if failed.CleanupError == "" {
		t.Fatal("missing cleanup failure")
	}
	waitForState(t, manager, next.ID, StateCompleted)
}
