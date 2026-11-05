package main

import (
	"context"
	"testing"
	"time"
)

// A store still opening is waited for: a gate that withholds only because a store is
// not ready is run again, and the brief is shown once the store answers (R64).
func TestNotReadyGateIsRetriedUntilTheStoreAnswers(t *testing.T) {
	calls := 0
	once := func() (MobileBriefV1, mobileBriefWithholding) {
		calls++
		if calls < 3 {
			return MobileBriefV1{}, mobileBriefWithholding{Reason: mobileBriefWithheldNotReady}
		}
		return MobileBriefV1{BriefID: "2026-11-05-morning", NarrativeStatus: "ok"}, mobileBriefWithholding{}
	}
	brief, withheld := mobileBriefRetryNotReady(context.Background(), []time.Duration{0, 0}, once)
	if withheld.Reason != "" || brief.BriefID != "2026-11-05-morning" {
		t.Fatalf("a store that opened late still withheld the brief: %+v %+v", brief, withheld)
	}
	if calls != 3 {
		t.Fatalf("gate ran %d times, want 3", calls)
	}
}

// A store that never opens is still withheld, after the bounded waits.
func TestNotReadyGateGivesUpAfterTheBoundedWaits(t *testing.T) {
	calls := 0
	once := func() (MobileBriefV1, mobileBriefWithholding) {
		calls++
		return MobileBriefV1{}, mobileBriefWithholding{Reason: mobileBriefWithheldNotReady}
	}
	_, withheld := mobileBriefRetryNotReady(context.Background(), []time.Duration{0, 0}, once)
	if withheld.Reason != mobileBriefWithheldNotReady {
		t.Fatalf("a store that never opens was not withheld: %+v", withheld)
	}
	if calls != 3 {
		t.Fatalf("gate ran %d times, want 1 + 2 waits", calls)
	}
}

// A real privacy reason is never waited on: it is withheld at once.
func TestPrivacyWithholdingIsNotRetried(t *testing.T) {
	calls := 0
	once := func() (MobileBriefV1, mobileBriefWithholding) {
		calls++
		return MobileBriefV1{}, mobileBriefWithholding{Reason: mobileBriefWithheldSource, SourceKind: "memory"}
	}
	_, withheld := mobileBriefRetryNotReady(context.Background(), []time.Duration{0, 0}, once)
	if withheld.Reason != mobileBriefWithheldSource || calls != 1 {
		t.Fatalf("a privacy reason was retried: calls=%d withheld=%+v", calls, withheld)
	}
}

// A cancelled read does not wait for the store.
func TestNotReadyGateStopsWhenTheReadIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	once := func() (MobileBriefV1, mobileBriefWithholding) {
		calls++
		return MobileBriefV1{}, mobileBriefWithholding{Reason: mobileBriefWithheldNotReady}
	}
	_, withheld := mobileBriefRetryNotReady(ctx, []time.Duration{time.Hour}, once)
	if withheld.Reason != mobileBriefWithheldNotReady || calls != 1 {
		t.Fatalf("a cancelled read waited: calls=%d withheld=%+v", calls, withheld)
	}
}
