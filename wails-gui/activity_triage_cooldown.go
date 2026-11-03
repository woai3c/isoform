package main

import (
	"sync"
	"time"
)

// triageCooldownEntry tracks consecutive triage failures for one session.
//
// Cooldowns are keyed by session ID (not group ID) because sessionGroup
// boundaries are computed per tick and may shift if a new pending
// session arrives — session IDs are stable for the lifetime of the row.
//
// Stored in memory only. A process restart clears the map, which is
// the desired behaviour for a hotfix: the observed pathology is
// hundreds of retries within a single long-running process (a single
// group was retried 110 times in 6.5 hours), and a restart legitimately
// represents a chance for the user to recover. Persistence can be
// layered on later without changing the public surface.
type triageCooldownEntry struct {
	failedCount int
	retryAfter  time.Time
}

// triageCooldownDuration returns the cooldown window after `failedCount`
// consecutive triage failures. Exponential backoff capped at 24h:
//
//	1 failure  → 30 minutes  (transient hiccup, retry soon)
//	2 failures → 2 hours     (likely quota exhausted, give it time)
//	3 failures → 6 hours
//	4+         → 24 hours    (effectively give up for the day)
//
// Pure function so the schedule is directly unit-testable.
func triageCooldownDuration(failedCount int) time.Duration {
	switch {
	case failedCount <= 1:
		return 30 * time.Minute
	case failedCount == 2:
		return 2 * time.Hour
	case failedCount == 3:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// triageCooldownState owns the cooldown map for one ActivityTriager.
// Embedded as a field rather than freestanding methods so the cooldown
// surface is self-contained and trivially mockable in tests.
type triageCooldownState struct {
	mu      sync.Mutex
	entries map[string]triageCooldownEntry
}

// markFailed records a triage failure for every session in the group
// and computes the next retryAfter using exponential backoff. Repeated
// calls escalate the backoff per session.
func (s *triageCooldownState) markFailed(group sessionGroup, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]triageCooldownEntry)
	}
	for _, sess := range group.Sessions {
		e := s.entries[sess.ID]
		e.failedCount++
		e.retryAfter = now.Add(triageCooldownDuration(e.failedCount))
		s.entries[sess.ID] = e
	}
}

// clear wipes any cooldown entries for the group. Called on the success
// path so a session that eventually succeeds after a transient failure
// does not leave a stale entry behind.
func (s *triageCooldownState) clear(group sessionGroup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		return
	}
	for _, sess := range group.Sessions {
		delete(s.entries, sess.ID)
	}
}

// remaining returns the longest cooldown still pending across every
// session in the group, or 0 if the group is clear to retry.
//
// Conservative by design: any single session still inside its cooldown
// blocks the whole group, so a refreshed group containing one stale
// session cannot re-burn the same expensive prompt.
func (s *triageCooldownState) remaining(group sessionGroup, now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		return 0
	}
	var longest time.Duration
	for _, sess := range group.Sessions {
		e, ok := s.entries[sess.ID]
		if !ok {
			continue
		}
		if r := e.retryAfter.Sub(now); r > longest {
			longest = r
		}
	}
	return longest
}
