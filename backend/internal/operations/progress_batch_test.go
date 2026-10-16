package operations

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTransferFilePhasesDoNotCreateDurableTaskEvents(t *testing.T) {
	for _, kind := range []string{"copy", "move"} {
		t.Run(kind, func(t *testing.T) {
			manager, err := Open(filepath.Join(t.TempDir(), "tasks.db"), 10, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.store.Close()
			now := time.Now().UTC()
			rec := &record{op: Operation{ID: "transfer", Kind: kind, State: StateRunning, Phase: "copying",
				ProgressUnit: ProgressUnitBytes, ProgressTotal: 100, BytesTotal: 100, StartedAt: now, UpdatedAt: now},
				lastBroadcast: time.Now(), lastPersistence: time.Now()}
			manager.records[rec.op.ID] = rec
			if err := manager.store.Upsert(rec.op, false); err != nil {
				t.Fatal(err)
			}
			updates, cancel := manager.Subscribe()
			defer cancel()
			reporter := Reporter{manager: manager, id: rec.op.ID}
			started := time.Now()
			for item := int64(1); item <= 100; item++ {
				for _, phase := range []string{"copying", "flushing", "publishing"} {
					reporter.UpdatePhase(phase, item, 100, item, 100, "Copying file")
				}
			}
			var events int
			if err := manager.store.db.QueryRow("SELECT COUNT(*) FROM task_events WHERE task_id = ?", rec.op.ID).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if events != 0 {
				t.Fatalf("file phases created %d durable events", events)
			}
			if rec.op.BytesDone != 100 || rec.op.ItemsDone != 100 {
				t.Fatalf("throttling lost current progress: %#v", rec.op)
			}
			if broadcasts, limit := len(updates), int(time.Since(started)/(100*time.Millisecond))+1; broadcasts > limit {
				t.Fatalf("file phases flooded the browser: %d broadcasts, limit %d", broadcasts, limit)
			}
			// An expired persistence interval saves the latest counters without
			// recording the file's transient phase as a task lifecycle event.
			rec.lastPersistence = time.Now().Add(-2 * time.Second)
			rec.lastBroadcast = time.Now().Add(-time.Second)
			reporter.UpdatePhase("flushing", 100, 100, 100, 100, "Copying file")
			stored, err := manager.store.List(10)
			if err != nil || len(stored) != 1 || stored[0].BytesDone != 100 {
				t.Fatalf("periodic progress not saved: %#v %v", stored, err)
			}
			select {
			case <-updates:
			default:
				t.Fatal("periodic progress was not broadcast")
			}
			manager.setStatus(rec.op.ID, StateCancelling, "cancelling", "Cancelling")
			stored, err = manager.store.List(10)
			if err != nil || stored[0].State != StateCancelling {
				t.Fatalf("cancellation was not immediately persisted: %#v %v", stored, err)
			}
		})
	}
}
