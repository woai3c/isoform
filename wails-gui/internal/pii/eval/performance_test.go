package eval

import (
	"math"
	"testing"
	"time"
)

func TestPerformanceFromDurations(t *testing.T) {
	pm := PerformanceFromDurations(150*time.Millisecond, []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
	})

	if pm.SampleCount != 4 {
		t.Fatalf("SampleCount = %d, want 4", pm.SampleCount)
	}
	assertFloat(t, "InitMs", pm.InitMs, 150)
	assertFloat(t, "TotalDetectionMs", pm.TotalDetectionMs, 100)
	assertFloat(t, "MeanSampleMs", pm.MeanSampleMs, 25)
	assertFloat(t, "P95SampleMs", pm.P95SampleMs, 40)
	assertFloat(t, "MaxSampleMs", pm.MaxSampleMs, 40)
	assertFloat(t, "SamplesPerSecond", pm.SamplesPerSecond, 40)
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("%s = %.4f, want %.4f", name, got, want)
	}
}
