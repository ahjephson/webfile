package operations

import (
	"testing"
	"time"
)

func TestTransferRateUsesRollingWindow(t *testing.T) {
	start := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	var samples []transferRateSample
	var rate *int64
	samples, rate = appendTransferRateSample(samples, start, 0)
	if rate != nil {
		t.Fatalf("first sample produced rate %d", *rate)
	}
	samples, _ = appendTransferRateSample(samples, start.Add(time.Second), 10*1024*1024)
	samples, rate = appendTransferRateSample(samples, start.Add(2*time.Second), 30*1024*1024)
	if rate == nil || *rate != 15*1024*1024 {
		t.Fatalf("rate = %v, want %d", rate, 15*1024*1024)
	}

	// This sample moves the window forward. The sample at two seconds is kept as
	// the baseline immediately before the cutoff, producing 20 MiB/s.
	_, rate = appendTransferRateSample(samples, start.Add(12100*time.Millisecond), 232*1024*1024)
	if rate == nil || *rate != 20*1024*1024 {
		t.Fatalf("trimmed rate = %v, want %d", rate, 20*1024*1024)
	}
}

func TestTransferRateRejectsInvalidSamples(t *testing.T) {
	start := time.Now()
	samples := []transferRateSample{{at: start, bytes: 100}}
	_, rate := appendTransferRateSample(samples, start, 200)
	if rate != nil {
		t.Fatalf("zero-duration samples produced rate %d", *rate)
	}
	_, rate = appendTransferRateSample(samples, start.Add(time.Second), 50)
	if rate != nil {
		t.Fatalf("decreasing byte count produced rate %d", *rate)
	}
}

func TestTransferRateIncludesStallsAndResumesWithoutBurstSpike(t *testing.T) {
	start := time.Now()
	samples, _ := appendTransferRateSample(nil, start, 0)
	samples, rate := appendTransferRateSample(samples, start.Add(time.Second), 100<<20)
	if rate == nil || *rate != 100<<20 {
		t.Fatalf("initial rate = %v", rate)
	}
	samples, rate = appendTransferRateSample(samples, start.Add(2*time.Second), 100<<20)
	if rate == nil || *rate != 50<<20 {
		t.Fatalf("flush wait was excluded: %v", rate)
	}
	for second := 3; second <= 13; second++ {
		samples, rate = appendTransferRateSample(samples, start.Add(time.Duration(second)*time.Second), 100<<20)
	}
	if rate == nil || *rate != 0 {
		t.Fatalf("stalled rate = %v, want zero", rate)
	}
	_, rate = appendTransferRateSample(samples, start.Add(13100*time.Millisecond), 110<<20)
	if rate == nil || *rate > 1<<20 {
		t.Fatalf("resume discarded stall history: %v", rate)
	}
}

func TestTransferRateWarmsUpAndBoundsSamples(t *testing.T) {
	start := time.Now()
	var samples []transferRateSample
	for millisecond := 0; millisecond <= 30000; millisecond++ {
		var rate *int64
		samples, rate = appendTransferRateSample(samples, start.Add(time.Duration(millisecond)*time.Millisecond), int64(millisecond)<<20)
		if millisecond < 1000 && rate != nil {
			t.Fatal("short cache burst produced a rate before warmup")
		}
		if len(samples) > 105 {
			t.Fatalf("unbounded sample density: %d", len(samples))
		}
	}
}

func TestTransferRateRetainsBaselineAcrossFilesAndResetsOutsideTransfer(t *testing.T) {
	manager := New(10)
	started := time.Now().Add(-5 * time.Second)
	rec := &record{op: Operation{ID: "move", Kind: "move", State: StateRunning, Phase: "copying", BytesTotal: 1000},
		rateSamples: []transferRateSample{{at: started, bytes: 0}}, rateHeartbeat: true}
	manager.records[rec.op.ID] = rec
	for _, phase := range []string{"copying", "flushing", "publishing", "removing_source", "copying"} {
		manager.updateProgress(rec.op.ID, ProgressUpdate{Phase: phase, BytesDone: 100, BytesTotal: 1000})
		if len(rec.rateSamples) < 2 || !rec.rateSamples[0].at.Equal(started) || rec.op.BytesPerSecond == nil || *rec.op.BytesPerSecond > 20 {
			t.Fatalf("%s lost elapsed transfer time: %#v", phase, rec)
		}
	}
	manager.updateProgress(rec.op.ID, ProgressUpdate{Phase: "scanning"})
	if len(rec.rateSamples) != 0 || rec.op.BytesPerSecond != nil {
		t.Fatal("non-transfer phase retained transfer speed")
	}
}

func TestTransferRateNewBaselineExcludesPreviouslyCopiedBytes(t *testing.T) {
	manager := New(10)
	rec := &record{op: Operation{ID: "resumed", Kind: "move", State: StateRunning, Phase: "copying", BytesTotal: 1000}, rateHeartbeat: true}
	manager.records[rec.op.ID] = rec
	manager.updateProgress(rec.op.ID, ProgressUpdate{Phase: "copying", BytesDone: 100, BytesTotal: 1000})
	if rec.rateSamples[0].bytes != 100 {
		t.Fatal("fresh rate baseline counted earlier work")
	}
	rec.rateSamples[0].at = time.Now().Add(-5 * time.Second)
	manager.updateProgress(rec.op.ID, ProgressUpdate{Phase: "copying", BytesDone: 200, BytesTotal: 1000})
	if rec.op.BytesPerSecond == nil || *rec.op.BytesPerSecond > 20 {
		t.Fatalf("resumed rate includes earlier bytes: %v", rec.op.BytesPerSecond)
	}
}

func TestTransferRateHeartbeatIncludesBlockedFlushTime(t *testing.T) {
	manager := New(10)
	now := time.Now()
	rate := int64(100)
	rec := &record{op: Operation{ID: "flushing", Kind: "move", State: StateRunning, Phase: "flushing", BytesDone: 100, BytesTotal: 1000, BytesPerSecond: &rate},
		rateSamples: []transferRateSample{{at: now.Add(-2 * time.Second), bytes: 0}, {at: now.Add(-time.Second), bytes: 100}}, rateHeartbeat: true}
	manager.records[rec.op.ID] = rec
	updates, cancel := manager.Subscribe()
	defer cancel()
	stopped := make(chan struct{})
	go func() { manager.updateTransferRate(rec.op.ID); close(stopped) }()
	defer func() {
		manager.mu.Lock()
		rec.op.State = StateCompleted
		manager.mu.Unlock()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("rate heartbeat did not stop after completion")
		}
	}()
	select {
	case update := <-updates:
		if update.BytesDone != 100 || update.Phase != "flushing" || update.BytesPerSecond == nil || *update.BytesPerSecond >= 50 {
			t.Fatalf("flush wait was not sampled: %#v", update)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked flush never updated its speed")
	}
}
