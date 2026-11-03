package eval

import (
	"sort"
	"time"
)

// PerformanceMetrics captures runtime cost for a detector evaluation run.
// Detection time is measured per sample after any model initialization, so it
// reflects steady-state suitability for the background masking pipeline.
type PerformanceMetrics struct {
	SampleCount      int
	InitMs           float64
	TotalDetectionMs float64
	MeanSampleMs     float64
	P95SampleMs      float64
	MaxSampleMs      float64
	SamplesPerSecond float64
}

func PerformanceFromDurations(init time.Duration, sampleDurations []time.Duration) PerformanceMetrics {
	pm := PerformanceMetrics{
		SampleCount: len(sampleDurations),
		InitMs:      durationMs(init),
	}
	if len(sampleDurations) == 0 {
		return pm
	}

	sorted := make([]time.Duration, len(sampleDurations))
	copy(sorted, sampleDurations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	var total, maxDuration time.Duration
	for _, d := range sampleDurations {
		total += d
		if d > maxDuration {
			maxDuration = d
		}
	}

	pm.TotalDetectionMs = durationMs(total)
	pm.MeanSampleMs = durationMs(total) / float64(len(sampleDurations))
	pm.P95SampleMs = durationMs(sorted[p95Index(len(sorted))])
	pm.MaxSampleMs = durationMs(maxDuration)
	if total > 0 {
		pm.SamplesPerSecond = float64(len(sampleDurations)) / total.Seconds()
	}
	return pm
}

func p95Index(n int) int {
	if n <= 1 {
		return 0
	}
	idx := int(0.95*float64(n)+0.999999999) - 1
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

func durationMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
