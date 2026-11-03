//go:build live

package agent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestLive_TTFBDistribution measures where first-token latency actually
// comes from. Runs N cold requests and reports p50/p95 for: DNS+connect
// (handled by Go http transport internally, not measured here), HTTP
// TTFB (time to 200 status), first-text-delta (time to first assistant
// content byte). The gap between TTFB and first-text reveals how much
// of the wait is "upstream thinking" vs "my code".
//
// Run with:
//
//	YANSU_LIVE_TEST=1 go test -tags=live -v -run TestLive_TTFBDistribution ./agent/
func TestLive_TTFBDistribution(t *testing.T) {
	_ = liveSkipIfUnconfigured(t)
	n := 5
	if v := os.Getenv("YANSU_LIVE_BENCH_N"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}

	runner := NewNativeAgentRunner(NativeRunnerConfig{
		DefaultModel: liveModel(),
		ModelAliases: liveModelAliases(),
	})
	_ = runner.Init(context.Background())
	defer runner.Shutdown()

	type sample struct {
		ttfbMs  int64 // run_created → first text delta
		totalMs int64
	}
	samples := make([]sample, 0, n)

	for i := 0; i < n; i++ {
		var (
			mu        sync.Mutex
			firstText time.Time
		)
		start := time.Now()
		cb := func(ev *StreamEvent) error {
			if ev.Type != "assistant" || ev.Content == "" {
				return nil
			}
			mu.Lock()
			if firstText.IsZero() {
				firstText = time.Now()
			}
			mu.Unlock()
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := runner.ExecuteTask(ctx, &TaskInput{
			Prompt:   "Say the single word 'pong' and nothing else.",
			Model:    liveModel(),
			WorkDir:  t.TempDir(),
			NoTools:  true, // isolate model speed from tool-schema overhead
			NoSystem: true,
		}, cb)
		cancel()
		total := time.Since(start)
		if err != nil {
			t.Errorf("run %d failed: %v", i, err)
			continue
		}
		mu.Lock()
		ft := firstText
		mu.Unlock()
		if ft.IsZero() {
			t.Errorf("run %d: no assistant delta", i)
			continue
		}
		samples = append(samples, sample{
			ttfbMs:  ft.Sub(start).Milliseconds(),
			totalMs: total.Milliseconds(),
		})
	}

	if len(samples) == 0 {
		t.Fatal("no successful samples")
	}

	// Report
	sort.Slice(samples, func(i, j int) bool { return samples[i].ttfbMs < samples[j].ttfbMs })
	ttfbs := make([]int64, len(samples))
	totals := make([]int64, len(samples))
	for i, s := range samples {
		ttfbs[i] = s.ttfbMs
		totals[i] = s.totalMs
	}

	p := func(xs []int64, pct float64) int64 {
		idx := int(float64(len(xs)-1) * pct)
		return xs[idx]
	}

	t.Logf("")
	t.Logf("=== TTFB distribution over %d runs (model=%s, no tools, no system) ===", len(samples), liveModel())
	t.Logf("  ttfb (ms):   min=%d  p50=%d  p95=%d  max=%d", ttfbs[0], p(ttfbs, 0.5), p(ttfbs, 0.95), ttfbs[len(ttfbs)-1])
	t.Logf("  total (ms):  min=%d  p50=%d  p95=%d  max=%d", totals[0], p(totals, 0.5), p(totals, 0.95), totals[len(totals)-1])
	t.Logf("")
	t.Logf("per-run:")
	for i, s := range samples {
		t.Logf("  #%d  ttfb=%dms  total=%dms", i, s.ttfbMs, s.totalMs)
	}
}
