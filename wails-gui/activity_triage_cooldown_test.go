package main

import (
	"testing"
	"time"
)

func TestTriageCooldownDuration(t *testing.T) {
	cases := []struct {
		failedCount int
		want        time.Duration
	}{
		{0, 30 * time.Minute},
		{1, 30 * time.Minute},
		{2, 2 * time.Hour},
		{3, 6 * time.Hour},
		{4, 24 * time.Hour},
		{10, 24 * time.Hour},
	}
	for _, tc := range cases {
		if got := triageCooldownDuration(tc.failedCount); got != tc.want {
			t.Errorf("triageCooldownDuration(%d) = %v, want %v", tc.failedCount, got, tc.want)
		}
	}
}

func TestTriageCooldown_FreshGroupHasNoRemaining(t *testing.T) {
	var s triageCooldownState
	g := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}}}
	if rem := s.remaining(g, time.Now()); rem != 0 {
		t.Fatalf("fresh group should have no cooldown; got %v", rem)
	}
}

func TestTriageCooldown_BlocksWithinWindowReleasesAfter(t *testing.T) {
	var s triageCooldownState
	g := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}, {ID: "s2"}}}
	now := time.Now()

	s.markFailed(g, now)

	if rem := s.remaining(g, now.Add(time.Minute)); rem <= 0 {
		t.Fatalf("group should still be in cooldown 1 minute after first failure; got %v", rem)
	}
	if rem := s.remaining(g, now.Add(31*time.Minute)); rem != 0 {
		t.Fatalf("group should be clear after first cooldown elapses; got %v", rem)
	}
}

func TestTriageCooldown_BackoffEscalatesAndCaps(t *testing.T) {
	var s triageCooldownState
	g := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}}}
	now := time.Now()

	durations := make([]time.Duration, 0, 5)
	for range 5 {
		s.markFailed(g, now)
		durations = append(durations, s.remaining(g, now))
	}

	// Strictly increasing for the first four failures.
	for i := 1; i < 4; i++ {
		if !(durations[i-1] < durations[i]) {
			t.Errorf("expected cooldown[%d]=%v < cooldown[%d]=%v", i-1, durations[i-1], i, durations[i])
		}
	}
	// 5th failure should hit the 24h cap, identical to the 4th.
	if durations[3] != durations[4] {
		t.Errorf("expected 4+ failures to share the cap; got 4=%v 5=%v", durations[3], durations[4])
	}
}

func TestTriageCooldown_ClearDropsEntry(t *testing.T) {
	var s triageCooldownState
	g := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}}}
	now := time.Now()

	s.markFailed(g, now)
	if rem := s.remaining(g, now); rem == 0 {
		t.Fatalf("expected cooldown after failure")
	}

	s.clear(g)
	if rem := s.remaining(g, now); rem != 0 {
		t.Fatalf("expected cooldown cleared after success; got %v", rem)
	}
}

// Group remaining must reflect the worst-case session, not the first.
// A refreshed group that pulls in one already-deeply-failed session
// must not be retried just because a fresh session sits next to it.
func TestTriageCooldown_GroupTakesLongestSession(t *testing.T) {
	var s triageCooldownState
	now := time.Now()

	s1 := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}}}
	s2 := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s2"}}}

	// s1: one failure → 30 min.
	s.markFailed(s1, now)
	// s2: three failures → 6 h.
	s.markFailed(s2, now)
	s.markFailed(s2, now)
	s.markFailed(s2, now)

	combined := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}, {ID: "s2"}}}
	rem := s.remaining(combined, now)
	if rem < 5*time.Hour {
		t.Errorf("expected combined cooldown to take s2's 6h window; got %v", rem)
	}
}

func TestTriageCooldown_NilSafe(t *testing.T) {
	// Zero-value triageCooldownState must answer remaining() and clear()
	// safely without a panic. markFailed lazy-inits the map.
	var s triageCooldownState
	g := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s1"}}}

	if rem := s.remaining(g, time.Now()); rem != 0 {
		t.Errorf("nil-state remaining should be 0, got %v", rem)
	}
	s.clear(g) // must not panic
}
