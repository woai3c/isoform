package engagement

import (
	"testing"
	"time"
)

func TestHoldWhileLocked(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	interrupt := Verdict{Mode: ModeInterrupt, Interruption: InterruptionActive, Reason: "ok"}
	locked := Presence{ScreenLocked: true}

	held := HoldWhileLocked(Moment{}, interrupt, locked, now)
	if held.Mode != ModeHold || held.Reason != "screen locked" || !held.HoldUntil.Equal(now.Add(lockedHold)) || held.Interruption != InterruptionActive {
		t.Fatalf("held %+v", held)
	}
	if got := HoldWhileLocked(Moment{}, interrupt, Presence{}, now); got != interrupt {
		t.Fatalf("unlocked screen leaves the verdict alone: %+v", got)
	}
	urgent := Verdict{Mode: ModeInterrupt, Interruption: InterruptionTimeSensitive, Reason: "ok"}
	if got := HoldWhileLocked(Moment{}, urgent, locked, now); got != urgent {
		t.Fatalf("time-sensitive moments are not held: %+v", got)
	}
	digest := Verdict{Mode: ModeDigest, Interruption: InterruptionPassive}
	if got := HoldWhileLocked(Moment{}, digest, locked, now); got != digest {
		t.Fatalf("only interrupting verdicts are held: %+v", got)
	}
	expiring := Moment{Kind: KindMeetingPrep, ExpiresAt: now.Add(3 * time.Minute)}
	if got := HoldWhileLocked(expiring, interrupt, locked, now); got != interrupt {
		t.Fatalf("a moment that expires during the hold is not held: %+v", got)
	}
	later := Moment{Kind: KindMeetingPrep, ExpiresAt: now.Add(time.Hour)}
	if got := HoldWhileLocked(later, interrupt, locked, now); got.Mode != ModeHold {
		t.Fatalf("a moment with time left is held: %+v", got)
	}
}
