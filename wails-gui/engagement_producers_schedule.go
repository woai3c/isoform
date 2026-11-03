package main

import (
	"context"
	"log"
	"strings"
	"time"
	"unicode"

	"wails-gui/internal/engagement"
)

// The missed-schedule notice (docs/notifications-design.md section 6). A
// schedule that did not run as planned twice in a row raises one
// schedule_missed moment per schedule per day, the way the brief raises
// brief_missed, so a silent failure is seen. "Did not run as planned" is a
// scheduled time whose run failed (after its retries), or that passed
// without a run while Isoform was open but not ready. A time that passed
// while the Mac was asleep or Isoform was closed is not a silent failure
// and does not count either way. Runs started by hand do not count either,
// and a run skipped while the assistant was getting ready is retried, so it
// does not count either. The brief has its own notice; one-time work behind
// a handoff reports through Tasks. Missed times that a catch-up run covers
// do not raise it on their own: the run decides (see
// CronScheduler.recordMissed).

const (
	engagementScheduleMissedStakes = 0.5
	engagementScheduleMissedExpiry = 12 * time.Hour
	// scheduleMissedRunsChecked is how many recent runs are read to find
	// the two newest scheduled outcomes.
	scheduleMissedRunsChecked = 20
	// scheduleMissedTitleMax caps the schedule's name in the notice.
	scheduleMissedTitleMax = 60
	// scheduleMissedCauseParam says what completed the streak: a failed run
	// (which posts its own main-chat line) or missed times.
	scheduleMissedCauseParam  = "cause"
	scheduleMissedCauseFailed = "failed"
	scheduleMissedCauseMissed = "missed"
)

// scheduleMissedTwiceInARow reports whether the two newest scheduled
// outcomes in runs (newest first, as the store returns them) both failed
// or were missed while Isoform was not ready. A scheduled time and its
// retries are one outcome: the newest retry's.
func scheduleMissedTwiceInARow(runs []CronRunState) bool {
	bad := 0
	retried := "" // the newest retry's status, until its scheduled run is read
	for _, run := range runs {
		if run.TriggerKind == "manual" {
			continue
		}
		if run.Status == cronRunMissed && run.Error == "" {
			// The Mac was asleep or Isoform was closed.
			continue
		}
		status := run.Status
		if run.TriggerKind == cronTriggerRetry {
			if retried == "" {
				retried = status
			}
			continue
		}
		if retried != "" {
			status, retried = retried, ""
		}
		switch status {
		case "error", cronRunMissed:
			bad++
			if bad == 2 {
				return true
			}
		case "ok":
			return false
		default:
			// running or skipped: not an outcome yet.
		}
	}
	return false
}

// noteScheduleRunHealth checks a schedule after a failed run or after the
// scheduler recorded missed times, and raises the notice when it did not
// run as planned twice in a row.
func (a *App) noteScheduleRunHealth(jobID string) {
	if !a.engagementActive() || a.store == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	ctx := context.Background()
	job, err := a.store.LoadCronJob(ctx, jobID)
	if err != nil || job == nil || !upcomingListsSurface(job.SurfaceKind) {
		return
	}
	if job.RetryAttempt > 0 && job.RetryRunAt > 0 {
		// A retry is coming: this scheduled time has not finished yet.
		return
	}
	rows, err := a.store.ListCronRuns(ctx, jobID, scheduleMissedRunsChecked)
	if err != nil {
		return
	}
	runs := make([]CronRunState, len(rows))
	for i, row := range rows {
		runs[i] = runRowToState(row)
	}
	// The health check already has the latest outcomes. Surface one missed
	// time quietly without creating a moment or reading another source; the
	// existing two-outcome threshold below still governs an actionable card.
	for _, run := range runs {
		if run.TriggerKind == "manual" || run.Status == "running" || run.Status == "skipped" {
			continue
		}
		if run.Status == cronRunMissed && run.Error != "" && !a.engagement.kindQuiet(engagement.KindScheduleMissed) {
			a.postMainChatStatus(mainChatStatusEntry{
				ID: mainChatReportKey(mainChatSourceSchedule, job.ID, run.ID), Kind: "schedule_missed_once",
				Text:   engagement.StatusReport(a.currentUILanguage(), "schedule_missed_once", job.Name),
				Target: mainChatTarget{Kind: mainChatTargetAutomation, ID: job.ID, RunID: run.ID},
			}, time.UnixMilli(run.StartedAt).In(a.engagement.now().Location()))
		}
		break
	}
	if !scheduleMissedTwiceInARow(runs) {
		return
	}
	cause := scheduleMissedCauseMissed
	if len(runs) > 0 && runs[0].Status == "error" {
		cause = scheduleMissedCauseFailed
	}
	a.produceScheduleMissed(job.ID, job.Name, cause)
}

// retireScheduleMissed drops today's notice for a schedule that ran fine
// again before the notice went out.
func (a *App) retireScheduleMissed(jobID string) {
	if !a.engagementActive() || strings.TrimSpace(jobID) == "" {
		return
	}
	key := engagement.DedupeKey(engagement.KindScheduleMissed, jobID, a.engagement.now().Format("2006-01-02"))
	if err := a.engagement.Retire(context.Background(), key, "recovered"); err != nil {
		log.Printf("[engagement] missed-schedule notice for %s not retired: %v", jobID, err)
	}
}

// scheduleMissedTitle is the schedule's name as the notice says it: one
// line of plain printable text, without quotes that would break out of the
// quoted name, and short.
func scheduleMissedTitle(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\u201c' || r == '\u201d':
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	title := strings.Join(strings.Fields(b.String()), " ")
	if runes := []rune(title); len(runes) > scheduleMissedTitleMax {
		title = strings.TrimSpace(string(runes[:scheduleMissedTitleMax])) + "..."
	}
	if title == "" {
		return "Untitled schedule"
	}
	return title
}

// produceScheduleMissed submits schedule_missed for a schedule. The key
// names the schedule and the local day, so each schedule is said at most
// once a day. It reports whether the moment was accepted.
func (a *App) produceScheduleMissed(jobID, name, cause string) bool {
	if !a.engagementActive() || strings.TrimSpace(jobID) == "" {
		return false
	}
	now := a.engagement.now()
	key := engagement.DedupeKey(engagement.KindScheduleMissed, jobID, now.Format("2006-01-02"))
	// A schedule that keeps failing is submitted once a day, not once per
	// failure.
	a.scheduleMissedMu.Lock()
	raised := a.scheduleMissedRaised[key]
	a.scheduleMissedMu.Unlock()
	if raised {
		return true
	}
	accepted := a.submitEngagementMoment("schedule missed", engagement.Moment{
		Kind:      engagement.KindScheduleMissed,
		SubjectID: jobID,
		DedupeKey: key,
		Stakes:    engagementScheduleMissedStakes,
		ExpiresAt: now.Add(engagementScheduleMissedExpiry),
		Params:    map[string]string{"title": scheduleMissedTitle(name), scheduleMissedCauseParam: cause},
	})
	if accepted {
		a.scheduleMissedMu.Lock()
		if a.scheduleMissedRaised == nil {
			a.scheduleMissedRaised = map[string]bool{}
		}
		a.scheduleMissedRaised[key] = true
		a.scheduleMissedMu.Unlock()
	}
	return accepted
}
