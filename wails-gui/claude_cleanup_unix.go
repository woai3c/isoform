//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// claudeWorkerStartTolerance bounds the gap between the start time Isoform
// recorded for a worker and the one ps reports (whole seconds, read a moment
// later).
const claudeWorkerStartTolerance = 5 * time.Second

// cleanupOrphanedClaudeSessionWorkers reaps stale Claude Code workers from
// the set this app tracked itself (isoformClaudeWorkers). A tracked pid is
// signaled only when the start time ps reports still matches what Isoform
// recorded, so a pid that was recycled by an unrelated process — including
// the user's own Claude Code — is never signaled.
func cleanupOrphanedClaudeSessionWorkers() {
	reapTrackedClaudeWorkers(time.Now(), claudeWorkerMaxAge)
}

func reapTrackedClaudeWorkers(now time.Time, maxAge time.Duration) {
	killed := 0
	for pid, startedAt := range trackedIsoformClaudeWorkers() {
		actual, err := osProcessStartTime(pid)
		if err != nil {
			// Already gone; drop it so the set stays the app's live workers.
			forgetIsoformClaudeWorker(pid)
			continue
		}
		if !sameClaudeWorkerStart(actual, startedAt) {
			forgetIsoformClaudeWorker(pid)
			continue
		}
		if now.Sub(startedAt) < maxAge {
			continue
		}
		// The worker leads its own process group (configureExternalProcess in
		// the agent, setResearchProcessGroup for research), so signal the
		// group first and fall back to the pid alone.
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				continue
			}
		}
		forgetIsoformClaudeWorker(pid)
		killed++
		log.Printf("[claude-cleanup] reaped stale Claude Code worker pid=%d started=%s", pid, startedAt.Format(time.RFC3339))
	}
	if killed > 0 {
		log.Printf("[claude-cleanup] reaped %d stale Claude Code worker(s)", killed)
	}
}

func sameClaudeWorkerStart(actual, recorded time.Time) bool {
	delta := actual.Sub(recorded)
	if delta < 0 {
		delta = -delta
	}
	return delta <= claudeWorkerStartTolerance
}

// osProcessStartTime returns when pid started, from ps lstart pinned to the C
// locale so month and weekday names never localize.
func osProcessStartTime(pid int) (time.Time, error) {
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	// lstart pads the day of month with a second space; normalize the fields.
	text := strings.Join(strings.Fields(string(out)), " ")
	if text == "" {
		return time.Time{}, fmt.Errorf("no start time for pid %d", pid)
	}
	return time.ParseInLocation("Mon Jan 2 15:04:05 2006", text, time.Local)
}
