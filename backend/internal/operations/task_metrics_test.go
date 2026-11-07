package operations

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestTaskSpeedAndRemainingTime(t *testing.T) {
	start := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"copy", "move", "upload"} {
		t.Run(kind, func(t *testing.T) {
			m := New(10)
			rec := &record{op: Operation{Kind: kind, State: StateRunning, Phase: "copying", BytesTotal: 1000}, rateHeartbeat: true}
			m.updateTaskMetricsLocked(rec, start)
			if rec.op.ETASeconds != nil || rec.op.BytesPerSecond != nil {
				t.Fatal("estimate appeared before warmup")
			}
			rec.op.BytesDone = 500
			m.updateTaskMetricsLocked(rec, start.Add(2*time.Second))
			if rec.op.BytesPerSecond == nil || *rec.op.BytesPerSecond != 250 || rec.op.ETASeconds == nil || *rec.op.ETASeconds != 2 {
				t.Fatalf("wrong speed/ETA: %+v", rec.op)
			}
			m.updateTaskMetricsLocked(rec, start.Add(14*time.Second))
			if rec.op.BytesPerSecond == nil || *rec.op.BytesPerSecond != 0 || rec.op.ETASeconds != nil {
				t.Fatalf("stall retained ETA: %+v", rec.op)
			}
		})
	}
}

func TestDeletionUsesFractionalItemRatesAndRequiresKnownTotalForETA(t *testing.T) {
	start := time.Now()
	m := New(10)
	rec := &record{op: Operation{Kind: "delete", State: StateRunning, Phase: "deleting", ItemsTotal: 10}, rateHeartbeat: true}
	m.updateTaskMetricsLocked(rec, start)
	rec.op.ItemsDone = 2
	m.updateTaskMetricsLocked(rec, start.Add(4*time.Second))
	if rec.op.ItemsPerSecond == nil || *rec.op.ItemsPerSecond != 0.5 || rec.op.ETASeconds == nil || *rec.op.ETASeconds != 16 || rec.op.BytesPerSecond != nil {
		t.Fatalf("wrong deletion metrics: %+v", rec.op)
	}
	rec.op.ItemsTotal = 0 // A concurrent filesystem change invalidated the count.
	m.updateTaskMetricsLocked(rec, start.Add(5*time.Second))
	if rec.op.ETASeconds != nil || rec.op.ItemsPerSecond == nil {
		t.Fatalf("unknown total lost speed or retained ETA: %+v", rec.op)
	}
}

func TestTaskMetricsResetAndNeverEstimateArchiveProgress(t *testing.T) {
	start := time.Now()
	m := New(10)
	for _, condition := range []string{"paused", "scanning", "completed", "conflict", "counter reset", "extract", "archive"} {
		t.Run(condition, func(t *testing.T) {
			rec := &record{op: Operation{Kind: "copy", State: StateRunning, Phase: "copying", BytesTotal: 1000}, rateHeartbeat: true}
			m.updateTaskMetricsLocked(rec, start)
			rec.op.BytesDone = 500
			m.updateTaskMetricsLocked(rec, start.Add(2*time.Second))
			switch condition {
			case "paused":
				rec.op.Paused = true
			case "scanning":
				rec.op.Phase = "scanning"
			case "completed":
				rec.op.State = StateCompleted
			case "conflict":
				rec.op.State = StateAwaitingInput
			case "counter reset":
				rec.op.BytesDone = 100
			default:
				rec.op.Kind = condition
			}
			m.updateTaskMetricsLocked(rec, start.Add(3*time.Second))
			if rec.op.BytesPerSecond != nil || rec.op.ItemsPerSecond != nil || rec.op.ETASeconds != nil {
				t.Fatalf("stale or inferred archive metrics: %+v", rec.op)
			}
		})
	}
}

func TestRemainingTimeRejectsUnknownFinishedAndInvalidRates(t *testing.T) {
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if estimateRemaining(100, 50, rate) != nil {
			t.Fatalf("accepted invalid rate %v", rate)
		}
	}
	for _, counts := range [][2]int64{{0, 0}, {100, 100}, {100, 101}, {100, -1}} {
		if estimateRemaining(counts[0], counts[1], 1) != nil {
			t.Fatal("estimated unknown/completed work")
		}
	}
	if eta := estimateRemaining(10, 1, 2); eta == nil || *eta != 5 {
		t.Fatalf("ETA did not round up: %v", eta)
	}
}

func TestUploadChunkMetricsKeepTheirBaselineAndResetAtPauseAndRetry(t *testing.T) {
	m := New(10)
	now := time.Now()
	rec := &record{op: Operation{ID: "upload", Kind: "upload", AttemptID: "first", State: StateRunning, Phase: "running", BytesTotal: 1000, Cancellable: true}, external: true, cancel: func() {}, rateHeartbeat: true,
		rateSamples: []transferRateSample{{at: now.Add(-2 * time.Second), bytes: 0}}}
	m.records[rec.op.ID] = rec
	op := m.UpsertExternal("upload", "upload", nil, "target", StateRunning, 500, 1000, 0, 1, "Uploading", "", "first")
	if op.BytesPerSecond == nil || op.ETASeconds == nil {
		t.Fatalf("chunk discarded baseline: %+v", op)
	}
	paused, err := m.SetPaused("upload", true)
	if err != nil || paused.BytesPerSecond != nil || paused.ETASeconds != nil {
		t.Fatalf("pause retained metrics: %+v %v", paused, err)
	}
	resumed, err := m.SetPaused("upload", false)
	if err != nil || resumed.BytesPerSecond != nil || resumed.ETASeconds != nil {
		t.Fatalf("resume reused old rate: %+v %v", resumed, err)
	}
	op = m.UpsertExternal("upload", "upload", nil, "target", StateRunning, 600, 1000, 0, 1, "Retry", "", "second")
	if op.BytesPerSecond != nil || op.ETASeconds != nil {
		t.Fatalf("retry reused old rate: %+v", op)
	}
}

func TestUploadTelemetryDoesNotHideARealTransferFailure(t *testing.T) {
	m := New(10)
	op := m.UpsertExternal("upload", "upload", nil, "target", StateRunning, 0, 1000, 0, 1, "Uploading", "", "attempt")
	m.mu.Lock()
	rec := m.records[op.ID]
	rec.op.UpdatedAt = op.UpdatedAt.Add(time.Second) // A status heartbeat, not another chunk.
	m.mu.Unlock()
	m.FailExternal(op.ID, op.UpdatedAt, errors.New("connection failed"))
	failed, _ := m.Get(op.ID)
	if failed.State != StateFailed || failed.ETASeconds != nil || failed.BytesPerSecond != nil {
		t.Fatalf("heartbeat hid failure: %+v", failed)
	}
}
