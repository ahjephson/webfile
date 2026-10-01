package operations

import "time"

const (
	transferRateWindow      = 3 * time.Second
	transferRateStaleAfter  = 2 * time.Second
	transferRateHeartbeat   = time.Second
	transferRateActivePhase = "copying"
)

type transferRateSample struct {
	at    time.Time
	bytes int64
}

func appendTransferRateSample(samples []transferRateSample, at time.Time, bytes int64) ([]transferRateSample, *int64) {
	samples = append(samples, transferRateSample{at: at, bytes: bytes})
	cutoff := at.Add(-transferRateWindow)
	first := 0
	for first+1 < len(samples) && samples[first+1].at.Before(cutoff) {
		first++
	}
	samples = samples[first:]
	if len(samples) < 2 {
		return samples, nil
	}

	elapsed := samples[len(samples)-1].at.Sub(samples[0].at)
	delta := samples[len(samples)-1].bytes - samples[0].bytes
	if elapsed <= 0 || delta <= 0 {
		return samples, nil
	}
	rate := int64(float64(delta) / elapsed.Seconds())
	if rate <= 0 {
		return samples, nil
	}
	return samples, &rate
}

func transferRateExpired(lastProgress, now time.Time) bool {
	return !lastProgress.IsZero() && now.Sub(lastProgress) >= transferRateStaleAfter
}
