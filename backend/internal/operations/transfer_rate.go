package operations

import "time"

const (
	transferRateWindow      = 10 * time.Second
	transferRateMinimum     = time.Second
	transferRateSampleEvery = 100 * time.Millisecond
	transferRateHeartbeat   = time.Second
)

type transferRateSample struct {
	at    time.Time
	bytes int64
}

func appendTransferRateSample(samples []transferRateSample, at time.Time, bytes int64) ([]transferRateSample, *int64) {
	// Keep the first sample as a baseline and bound sample density during fast
	// buffered writes. A short burst must not become an instantaneous speed.
	if len(samples) > 1 && at.Truncate(transferRateSampleEvery).Equal(samples[len(samples)-1].at.Truncate(transferRateSampleEvery)) {
		samples[len(samples)-1] = transferRateSample{at: at, bytes: bytes}
	} else {
		samples = append(samples, transferRateSample{at: at, bytes: bytes})
	}
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
	if elapsed < transferRateMinimum || delta < 0 {
		return samples, nil
	}
	rate := int64(float64(delta) / elapsed.Seconds())
	return samples, &rate
}
