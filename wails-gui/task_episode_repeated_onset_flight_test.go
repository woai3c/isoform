package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Callers that arrive while the all-time repeated-onset scan runs share it: one
// scan, the same answer for each caller.
func TestRepeatedOnsetScanIsSharedWhileInFlight(t *testing.T) {
	store := newTestStore(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	repeatedOnsetScanGate = func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	t.Cleanup(func() { repeatedOnsetScanGate = nil })
	before := repeatedOnsetScans.Load()

	const callers = 8
	results := make([]map[string]struct{}, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = store.taskEpisodeRepeatedOnsetSetContext(context.Background())
		}(i)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no scan started")
	}
	// Give the other callers time to join the flight before it completes.
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := repeatedOnsetScans.Load() - before; got != 1 {
		t.Fatalf("scans run = %d, want 1 shared scan for %d callers", got, callers)
	}
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if len(results[i]) != len(results[0]) {
			t.Fatalf("caller %d got a different set: %v vs %v", i, results[i], results[0])
		}
	}
}
