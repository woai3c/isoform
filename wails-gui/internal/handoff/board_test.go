package handoff

import "testing"

func TestPlacementMapsEveryStateToItsBoardGroup(t *testing.T) {
	for _, tc := range []struct {
		name         string
		entry        Entry
		group, cause string
	}{
		{"waiting to start", Entry{Status: StatusActive}, BoardNeedsYou, ""},
		{"running", Entry{Status: StatusInProgress}, BoardRunning, ""},
		{"finished", Entry{Status: StatusCompleted}, BoardDone, ""},
		{"check not met", Entry{Status: StatusNeedsReview}, BoardNeedsYou, ""},
		{"restarted", Entry{Status: StatusBlocked, BlockedReason: BlockedInterrupted}, BoardNeedsYou, CauseRestarted},
		{"no activity", Entry{Status: StatusBlocked, BlockedReason: BlockedTimeout}, BoardNeedsYou, CauseTimeout},
		{"blocked without a reason", Entry{Status: StatusBlocked}, BoardNeedsYou, CauseAssistantError},
		{"you stopped it", Entry{Status: StatusAborted, StoppedBy: StoppedByPerson}, BoardDone, ""},
		{"no AI", Entry{Status: StatusAborted, StoppedBy: StoppedBySystem, StopCause: CauseNoAssistant}, BoardNeedsYou, CauseNoAssistant},
		{"signed out", Entry{Status: StatusAborted, StoppedBy: StoppedBySystem, StopCause: CauseSignedOut}, BoardNeedsYou, CauseSignedOut},
		{"assistant error", Entry{Status: StatusAborted, StoppedBy: StoppedBySystem, StopCause: CauseAssistantError}, BoardNeedsYou, CauseAssistantError},
		{"system stop with an unknown cause", Entry{Status: StatusAborted, StoppedBy: StoppedBySystem, StopCause: "other"}, BoardNeedsYou, CauseAssistantError},
		{"aborted before stops were recorded", Entry{Status: StatusAborted}, BoardDone, ""},
		{"stale suggestion never started", Entry{Status: StatusStale}, BoardNone, ""},
		{"unavailable agency suggestion never started", Entry{Status: StatusStale, Source: SourceAgency}, BoardNone, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group, cause := Placement(tc.entry)
			if group != tc.group || cause != tc.cause {
				t.Fatalf("Placement = %q, %q; want %q, %q", group, cause, tc.group, tc.cause)
			}
		})
	}
}

func TestClearStopForgetsTheLastStop(t *testing.T) {
	e := Entry{Status: StatusAborted, StoppedBy: StoppedByPerson, StopCause: CauseTimeout, StopAssistant: "codex", StoppedAt: 5,
		Thread: &Thread{Outcome: &Outcome{Kind: OutcomeStopped, By: OutcomeByYou}}}
	e.ClearStop()
	if e.StoppedBy != "" || e.StopCause != "" || e.StopAssistant != "" || e.StoppedAt != 0 || e.Thread.Outcome != nil {
		t.Fatalf("stop kept: %+v", e)
	}
}
