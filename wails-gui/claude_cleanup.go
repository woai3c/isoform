package main

import (
	"context"
	"sync"
	"time"
)

const (
	claudeWorkerReapInterval = 60 * time.Second
	// Every Claude Code run Isoform starts is bounded in minutes (a chat
	// attempt gets two, research eight), so a worker still alive after this
	// is a leak its run failed to tear down.
	claudeWorkerMaxAge = time.Hour
)

// isoformClaudeWorkers is the set of Claude Code processes this app instance
// started itself: pid to when Isoform started it. The janitor reaps stale
// entries from this set only — it never reads ~/.claude and never signals a
// process that is not tracked here.
var isoformClaudeWorkers = struct {
	sync.Mutex
	byPID map[int]time.Time
}{byPID: make(map[int]time.Time)}

// trackIsoformClaudeWorker records a Claude Code process Isoform just
// started. It is the Started half of the agent.ProcessHooks the app registers
// in app.go; research_cli.go calls it directly.
func trackIsoformClaudeWorker(pid int, startedAt time.Time) {
	if pid <= 0 {
		return
	}
	isoformClaudeWorkers.Lock()
	isoformClaudeWorkers.byPID[pid] = startedAt
	isoformClaudeWorkers.Unlock()
}

// forgetIsoformClaudeWorker stops tracking a worker: its run is done with it,
// the janitor reaped it, or its pid was recycled by an unrelated process.
func forgetIsoformClaudeWorker(pid int) {
	isoformClaudeWorkers.Lock()
	delete(isoformClaudeWorkers.byPID, pid)
	isoformClaudeWorkers.Unlock()
}

func trackedIsoformClaudeWorkers() map[int]time.Time {
	isoformClaudeWorkers.Lock()
	defer isoformClaudeWorkers.Unlock()
	tracked := make(map[int]time.Time, len(isoformClaudeWorkers.byPID))
	for pid, startedAt := range isoformClaudeWorkers.byPID {
		tracked[pid] = startedAt
	}
	return tracked
}

func startClaudeSessionWorkerCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(claudeWorkerReapInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupOrphanedClaudeSessionWorkers()
			}
		}
	}()
}
