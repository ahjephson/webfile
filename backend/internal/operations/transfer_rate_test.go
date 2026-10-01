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

	// This sample moves the window forward. The sample at one second is kept as
	// the baseline immediately before the cutoff, producing 20 MiB/s.
	_, rate = appendTransferRateSample(samples, start.Add(5*time.Second), 90*1024*1024)
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

func TestTransferRateExpiry(t *testing.T) {
	lastProgress := time.Now()
	if transferRateExpired(lastProgress, lastProgress.Add(transferRateStaleAfter-time.Nanosecond)) {
		t.Fatal("rate expired before the stale threshold")
	}
	if !transferRateExpired(lastProgress, lastProgress.Add(transferRateStaleAfter)) {
		t.Fatal("rate did not expire at the stale threshold")
	}
	if transferRateExpired(time.Time{}, lastProgress.Add(transferRateStaleAfter)) {
		t.Fatal("zero progress time was treated as an expirable rate")
	}
}
