package main

import (
	"testing"

	"wails-gui/internal/platform/activitymon"
)

// visual-churn pacing state machine. These exercise the pure helpers extracted
// from captureSnapshotForEvent so the throttle/streak logic can be verified
// without the screen-capture pipeline.

func TestVisualChurnEligible(t *testing.T) {
	exempt := []string{
		string(activitymon.EventAppSwitch), "focus", // window-state: must capture promptly
		"heartbeat", "session_end", // session keepalives
		"slide_change", "calendar_keyboard", "calendar_save", "calendar_save_followup", // forced
	}
	for _, trig := range exempt {
		if visualChurnEligible(trig) {
			t.Errorf("trigger %q must be exempt from visual-churn pacing", trig)
		}
	}
	paced := []string{"keyboard", "visual_change", "click", "scroll"}
	for _, trig := range paced {
		if !visualChurnEligible(trig) {
			t.Errorf("trigger %q must be subject to visual-churn pacing", trig)
		}
	}
}

func TestVisualChurnThrottles(t *testing.T) {
	const floor = int64(visualChurnMinCaptureIntervalMs)
	const now = int64(1_000_000)
	tests := []struct {
		name        string
		churnUntil  int64
		lastCapture int64
		want        bool
	}{
		{"not armed", 0, now - floor - 1, false},
		{"armed, within floor", now + 1000, now - floor + 1, true},
		{"armed, exactly floor elapsed", now + 1000, now - floor, false},
		{"armed, beyond floor", now + 1000, now - floor - 1, false},
		{"lapsed pacing", now - 1, now - 10, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := visualChurnThrottles(tc.churnUntil, tc.lastCapture, now); got != tc.want {
				t.Errorf("visualChurnThrottles(until=%d,last=%d,now=%d)=%v want %v",
					tc.churnUntil, tc.lastCapture, now, got, tc.want)
			}
		})
	}
}

func TestVisualChurnStreakBroken(t *testing.T) {
	const window = int64(visualChurnStreakWindowMs)
	const now = int64(1_000_000)
	tests := []struct {
		name       string
		trigger    string
		lastBundle string
		bundle     string
		lastFrame  int64
		want       bool
	}{
		{"first frame (no prior)", "visual_change", "com.app", "com.app", 0, false},
		{"same app, within window", "visual_change", "com.app", "com.app", now - window + 1, false},
		{"same app, at window edge", "visual_change", "com.app", "com.app", now - window, false},
		{"same app, beyond window", "visual_change", "com.app", "com.app", now - window - 1, true},
		{"app changed, recent", "visual_change", "com.app", "com.other", now - 100, true},
		{"app changed, also stale", "visual_change", "com.app", "com.other", now - window - 1, true},
		// Window-state triggers are an explicit context shift and break the
		// streak even within the same app (two windows of one app share a bundle).
		{"focus, same app, within window", "focus", "com.app", "com.app", now - 100, true},
		{"app_switch, same app", string(activitymon.EventAppSwitch), "com.app", "com.app", now - 100, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := visualChurnStreakBroken(tc.trigger, tc.lastBundle, tc.bundle, tc.lastFrame, now); got != tc.want {
				t.Errorf("visualChurnStreakBroken(%q,%q,%q,last=%d,now=%d)=%v want %v",
					tc.trigger, tc.lastBundle, tc.bundle, tc.lastFrame, now, got, tc.want)
			}
		})
	}
}

func TestNextVisualChurnStreak_EngagesOnlyAfterFullStreak(t *testing.T) {
	const high = visualChurnDistThreshold + 0.1
	const now = int64(1_000_000)
	streak, churnUntil := 0, int64(0)
	for i := 1; i < visualChurnStreakThreshold; i++ {
		streak, churnUntil = nextVisualChurnStreak(streak, churnUntil, high, now, false)
		if churnUntil != 0 {
			t.Fatalf("pacing armed too early at streak=%d (need %d)", streak, visualChurnStreakThreshold)
		}
	}
	streak, churnUntil = nextVisualChurnStreak(streak, churnUntil, high, now, false)
	if streak != visualChurnStreakThreshold {
		t.Fatalf("streak=%d want %d", streak, visualChurnStreakThreshold)
	}
	if churnUntil != now+int64(visualChurnCooldownMs) {
		t.Fatalf("churnUntil=%d want %d", churnUntil, now+int64(visualChurnCooldownMs))
	}
}

func TestNextVisualChurnStreak_LowFrameResetsButKeepsCooldown(t *testing.T) {
	const low = visualChurnDistThreshold - 0.05
	const now = int64(1_000_000)
	startUntil := now + int64(visualChurnCooldownMs)
	streak, churnUntil := nextVisualChurnStreak(visualChurnStreakThreshold, startUntil, low, now, false)
	if streak != 0 {
		t.Fatalf("a low-distance frame must reset the streak, got %d", streak)
	}
	// A single still frame inside a video must not flap pacing off; the cooldown
	// is left to expire on its own.
	if churnUntil != startUntil {
		t.Fatalf("a low-distance frame must not clear the cooldown: got %d want %d", churnUntil, startUntil)
	}
}

func TestNextVisualChurnStreak_BrokenStreakCannotArmOnSingleFrame(t *testing.T) {
	const high = visualChurnDistThreshold + 0.1
	const now = int64(1_000_000)
	// A leftover streak (even threshold-1 or more) plus one high-distance frame
	// must NOT arm pacing when the streak is broken (stale gap or app switch).
	for _, prior := range []int{visualChurnStreakThreshold - 1, visualChurnStreakThreshold + 2} {
		streak, churnUntil := nextVisualChurnStreak(prior, now-1, high, now, true)
		if streak >= visualChurnStreakThreshold {
			t.Fatalf("broken streak (prior=%d) must restart, got %d", prior, streak)
		}
		if churnUntil > now {
			t.Fatalf("broken streak (prior=%d) must not arm pacing, churnUntil=%d now=%d", prior, churnUntil, now)
		}
	}
}

func TestNextVisualChurnStreak_ActivePacingReExtendsCooldown(t *testing.T) {
	const high = visualChurnDistThreshold + 0.1
	const now = int64(1_000_000)
	later := now + 6000
	churnUntil := later + 5000 // pacing still active at `later`
	streak, churnUntil := nextVisualChurnStreak(visualChurnStreakThreshold, churnUntil, high, later, false)
	if streak != visualChurnStreakThreshold+1 {
		t.Fatalf("active pacing should keep growing the streak, got %d", streak)
	}
	if churnUntil != later+int64(visualChurnCooldownMs) {
		t.Fatalf("active pacing must re-extend the cooldown: got %d want %d", churnUntil, later+int64(visualChurnCooldownMs))
	}
}
