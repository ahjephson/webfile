package operations

import (
	"math"
	"time"
)

// Metrics use actual byte/item counters only. In particular, never infer speed
// or remaining time from 7-Zip percentages or synthetic archive byte counters.
func taskMetricCounters(op Operation) (ProgressUnit, int64, int64, bool) {
	if op.State != StateRunning || op.Paused {
		return "", 0, 0, false
	}
	switch op.Kind {
	case "copy", "move":
		if transferIOPhase(op.Phase) && op.BytesTotal > 0 {
			return ProgressUnitBytes, op.BytesDone, op.BytesTotal, true
		}
	case "upload":
		if op.BytesTotal > 0 {
			return ProgressUnitBytes, op.BytesDone, op.BytesTotal, true
		}
	case "delete":
		if op.Phase == "deleting" {
			return ProgressUnitItems, op.ItemsDone, op.ItemsTotal, true
		}
	}
	return "", 0, 0, false
}

func (m *Manager) updateTaskMetricsLocked(rec *record, now time.Time) {
	unit, done, total, ok := taskMetricCounters(rec.op)
	if !ok {
		resetTransferRate(rec)
		return
	}
	if (rec.rateUnit != "" && rec.rateUnit != unit) || (len(rec.rateSamples) > 0 && done < rec.rateSamples[len(rec.rateSamples)-1].bytes) {
		resetTransferRate(rec)
	}
	rec.rateUnit = unit
	var rate *int64
	rec.rateSamples, rate = appendTransferRateSample(rec.rateSamples, now, done)
	rec.op.BytesPerSecond, rec.op.ItemsPerSecond, rec.op.ETASeconds = nil, nil, nil
	if rate != nil {
		// Preserve fractional item rates for slow deletions (e.g. 0.5 items/s).
		first, last := rec.rateSamples[0], rec.rateSamples[len(rec.rateSamples)-1]
		perSecond := float64(last.bytes-first.bytes) / last.at.Sub(first.at).Seconds()
		if unit == ProgressUnitItems {
			rec.op.ItemsPerSecond = &perSecond
		} else {
			rec.op.BytesPerSecond = rate
			perSecond = float64(*rate)
		}
		rec.op.ETASeconds = estimateRemaining(total, done, perSecond)
	}
	if len(rec.rateSamples) > 0 && !rec.rateHeartbeat {
		rec.rateHeartbeat = true
		go m.updateTransferRate(rec.op.ID)
	}
}

func estimateRemaining(total, done int64, perSecond float64) *int64 {
	if total <= 0 || done < 0 || done >= total || perSecond <= 0 || math.IsNaN(perSecond) || math.IsInf(perSecond, 0) {
		return nil
	}
	seconds := math.Ceil(float64(total-done) / perSecond)
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds >= float64(math.MaxInt64) {
		return nil
	}
	remaining := int64(seconds)
	return &remaining
}
