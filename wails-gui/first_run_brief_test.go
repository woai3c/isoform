package main

import (
	"sync/atomic"
	"testing"
	"time"
)

type firstBriefTestSource struct {
	mobileTestBriefSource
	requests atomic.Int32
}

func (s *firstBriefTestSource) RequestMobileBrief(reason string) (MobileBriefRequestResult, error) {
	s.requests.Add(1)
	return MobileBriefRequestResult{Status: "started"}, nil
}

// The first brief is made as soon as a fresh install finishes setup, once,
// through Make my brief now (Isoform design, first run 1.0, section 3); an
// upgrade, whose setup finished long ago, never gets one.
func TestFirstBriefRightAfterSetup(t *testing.T) {
	app := mainChatTestApp(t)
	source := &firstBriefTestSource{}
	app.mobileBridgeMu.Lock()
	app.mobileBriefSvc = source
	app.mobileBridgeMu.Unlock()
	ctx := t.Context()
	if err := app.store.SetUIState(ctx, firstWeekPendingKey, "true"); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveUIState(onboardingStateKey, `{"completed":true}`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for source.requests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := source.requests.Load(); got != 1 {
		t.Fatalf("first brief requests = %d, want 1", got)
	}
	// Saving the state again (another step, a restart) asks for nothing more.
	if err := app.SaveUIState(onboardingStateKey, `{"completed":true}`); err != nil {
		t.Fatal(err)
	}
	app.requestFirstBrief()
	time.Sleep(50 * time.Millisecond)
	if got := source.requests.Load(); got != 1 {
		t.Fatalf("first brief asked for again: %d", got)
	}
}

func TestNoFirstBriefOnUpgrade(t *testing.T) {
	app := mainChatTestApp(t)
	source := &firstBriefTestSource{}
	app.mobileBridgeMu.Lock()
	app.mobileBriefSvc = source
	app.mobileBridgeMu.Unlock()
	if err := app.SaveUIState(onboardingStateKey, `{"completed":true}`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := source.requests.Load(); got != 0 {
		t.Fatalf("an upgrade asked for a first brief: %d", got)
	}
}
