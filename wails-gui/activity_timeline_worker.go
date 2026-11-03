package main

import (
	"context"
	"errors"
	"log"
	"time"
)

const (
	// timelineSummaryCheckInterval is the fallback wake-up cadence. The worker
	// is primarily kick-driven (a session save that changed the run table
	// wakes it immediately); the ticker only exists to pick up scheduled
	// retries (next_retry_at) and quiet open runs without a save event.
	timelineSummaryCheckInterval = 2 * time.Minute
	// timelineSummaryQueueBatch is the fetch page size while draining the
	// queue — a pass keeps pulling pages until no due runs remain, so this
	// bounds memory per query, not throughput.
	timelineSummaryQueueBatch = 16
	// timelineAutoSummarizeHorizon bounds background quota: only runs that
	// ended within this window are auto-summarized. Older runs (including the
	// migration backfill) generate on-demand when the user selects them.
	timelineAutoSummarizeHorizon = 48 * time.Hour
	// timelineOpenRunQuiet is how long the open run must sit without new
	// events before the worker summarizes it early; growth after that makes
	// it eligible again, so the summary catches up when the run closes.
	timelineOpenRunQuiet = 10 * time.Minute
	// timelineSummaryMaxAttempts caps transient retries before a run is
	// parked as failed_permanent.
	timelineSummaryMaxAttempts = 4
)

// KickTimelineSummaries wakes the summary worker without waiting for the
// fallback ticker. Non-blocking: a pending kick already covers the change.
func (c *ActivityTriager) KickTimelineSummaries() {
	if c.timelineSummaryKick == nil {
		return
	}
	select {
	case c.timelineSummaryKick <- struct{}{}:
	default:
	}
}

// runTimelineSummaryLoop drives the timeline-run summary worker on its own
// goroutine, decoupled from triage evaluate()/runMu and from the
// semantic-event pass, so neither can starve it. Wakes on session-save kicks
// and on the fallback ticker; each pass drains the whole due queue. Exits
// when ctx is cancelled.
func (c *ActivityTriager) runTimelineSummaryLoop(ctx context.Context) {
	c.runTimelineSummaryPassGuarded(ctx)
	ticker := time.NewTicker(timelineSummaryCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runTimelineSummaryPassGuarded(ctx)
		case <-c.timelineSummaryKick:
			c.runTimelineSummaryPassGuarded(ctx)
		}
	}
}

// runTimelineSummaryPassGuarded runs one worker pass under timelineSummaryMu
// (non-blocking — a still-running pass is already draining the queue and will
// pick up whatever prompted this wake-up on its next page fetch).
func (c *ActivityTriager) runTimelineSummaryPassGuarded(ctx context.Context) {
	if !c.timelineSummaryMu.TryLock() {
		return
	}
	defer c.timelineSummaryMu.Unlock()
	c.runTimelineSummaryPass(ctx)
}

// notifyTimelineGroupsUpdated tells the UI to refetch timeline groups.
func (c *ActivityTriager) notifyTimelineGroupsUpdated() {
	if c.app != nil && c.app.ctx != nil {
		c.app.emitBrowserEvent("activity:timeline-groups-updated")
	}
}

// runTimelineSummaryPass self-heals the run table, then drains the due queue
// newest-first: every eligible per-app run is summarized serially (one model
// call at a time) until nothing is due. The UI is notified after each run so
// titles stream into the sidebar as the queue drains. Failures are recorded
// on the run (with backoff) and never abort the pass.
func (c *ActivityTriager) runTimelineSummaryPass(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[timeline-summary] recovered from panic: %v", r)
		}
	}()
	store := c.activityStore()
	if store == nil || c.app == nil {
		return
	}

	// Self-heal: catch up runs for any events persisted without a reconcile
	// (crash between session commit and reconcile, sessions written outside
	// extractSession). The reconcile's fast path makes this free when the
	// table is already current.
	reconciled, err := store.ReconcileTimelineRuns()
	if err != nil {
		log.Printf("[timeline-summary] self-heal reconcile: %v", err)
	} else if reconciled {
		c.notifyTimelineGroupsUpdated()
	}

	// The runner is assigned asynchronously after app startup; without it no
	// generation can succeed, so skip the pass without consuming attempts.
	c.app.mu.Lock()
	runner := c.app.runner
	c.app.mu.Unlock()
	if runner == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		now := time.Now().UnixMilli()
		due, err := store.GetTimelineRunsDueForSummary(
			now,
			timelineAutoSummarizeHorizon.Milliseconds(),
			timelineOpenRunQuiet.Milliseconds(),
			timelineSummaryQueueBatch,
		)
		if err != nil {
			log.Printf("[timeline-summary] load due runs: %v", err)
			return
		}
		if len(due) == 0 {
			return
		}
		progressed := false
		for _, run := range due {
			select {
			case <-ctx.Done():
				return
			default:
			}
			claimed, err := store.ClaimTimelineRunForSummary(run.ID, []string{
				timelineSummaryStatusNone,
				timelineSummaryStatusFailedTransient,
				timelineSummaryStatusDone,
				timelineSummaryStatusSkipped,
			})
			if err != nil {
				log.Printf("[timeline-summary] claim run %s: %v", run.ID, err)
				continue
			}
			if !claimed {
				continue
			}
			progressed = true
			// Protected-app runs are skipped inside generateTimelineRunSummary
			// (the shared core); the sentinel is an expected outcome, not an
			// error worth logging.
			if _, err := c.app.generateTimelineRunSummary(ctx, run); err != nil && !errors.Is(err, errTimelineRunProtected) {
				log.Printf("[timeline-summary] generate for run %s (app %s): %v", run.ID, run.AppName, err)
			}
			c.notifyTimelineGroupsUpdated()
		}
		// Every fetched run was claimed by the on-demand path in the
		// meantime — nothing left for this pass to make progress on.
		if !progressed {
			return
		}
	}
}
