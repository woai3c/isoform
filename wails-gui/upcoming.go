package main

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"
)

// Upcoming is the one list of everything Isoform will do on its own
// (docs/muse-teardown.md ideas 16 and 17): the user's schedules, the morning
// and evening brief, and the Daily News run, ordered by next run, each with
// its last run, the result in plain words and the runs it missed this week.
// Schedules the app made for itself (surface "system") stay hidden unless
// their last run failed or was missed; one-time work behind a handoff lives
// on the Tasks page.

// Upcoming row kinds.
const (
	upcomingKindSchedule = "schedule"
	upcomingKindBrief    = "brief"
	upcomingKindNews     = "news"
)

// Last-run results, in the page's words.
const (
	upcomingResultDone    = "done"
	upcomingResultFailed  = "failed"
	upcomingResultSkipped = "skipped"
	upcomingResultMissed  = "missed"
	upcomingResultRunning = "running"
)

// Reasons a run failed, was skipped or was missed. The page turns each into
// a sentence; the raw error stays in the schedule's run history.
const (
	upcomingReasonAsleep     = "asleep"       // the Mac was asleep or Isoform was closed
	upcomingReasonNotReady   = "not_ready"    // the assistant was not ready yet
	upcomingReasonTimeout    = "timeout"      // the run took too long and was stopped
	upcomingReasonNoModel    = "no_model"     // no AI model is set up, or its sign-in failed
	upcomingReasonBusy       = "busy"         // the AI service was busy or the plan's limit was reached
	upcomingReasonOffline    = "offline"      // the internet could not be reached
	upcomingReasonEmpty      = "empty"        // the run finished without a result
	upcomingReasonRetrying   = "retrying"     // the brief failed and is being tried again
	upcomingReasonKeptFailed = "kept_failing" // the brief failed every try until its window closed
	upcomingReasonOther      = "other"
)

// upcomingBriefID names a brief slot's row.
func upcomingBriefID(slot string) string {
	return upcomingKindBrief + ":" + slot
}

// UpcomingItem is one row on the Upcoming page. Times are Unix milliseconds;
// zero means none.
type UpcomingItem struct {
	// ID is the schedule's id for a schedule or news row, and brief:<slot>
	// for a brief row.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// JobID is the schedule behind a schedule or news row; its detail pane
	// opens from the row.
	JobID string `json:"jobId"`
	// Slot is morning or evening for a brief row.
	Slot string `json:"slot"`
	// Name and Summary are the user's own words for a schedule. They are
	// empty for the brief and the news, which the page names itself.
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Enabled bool   `json:"enabled"`
	// ScheduleKind and ScheduleConfig say when a schedule runs (cron, every,
	// at, manual), as on the schedule itself; a brief row runs daily at Time.
	ScheduleKind   string `json:"scheduleKind"`
	ScheduleConfig any    `json:"scheduleConfig"`
	Time           string `json:"time"`
	NextRunAt      int64  `json:"nextRunAt"`
	LastRunAt      int64  `json:"lastRunAt"`
	// LastResult is done, failed, skipped, missed or running; empty before
	// the first run. LastReason says why a run failed, was skipped or was
	// missed.
	LastResult string `json:"lastResult"`
	LastReason string `json:"lastReason"`
	// FailedAttempts counts failures in a row.
	FailedAttempts int `json:"failedAttempts"`
	// MissedRuns counts scheduled runs in the last seven days that did not
	// happen because the Mac was asleep or Isoform was closed.
	MissedRuns int `json:"missedRuns"`
	// System marks a schedule the app made for itself, shown only because
	// its last run failed or was missed.
	System bool `json:"system"`
	// DidntRunAt is a brief's latest scheduled time, in Unix milliseconds,
	// when that run made no brief (missed, or failed and waiting for its
	// retry) and none was made on request after it; 0 otherwise. Only then does its row say it didn't run.
	DidntRunAt int64 `json:"didntRunAt,omitempty"`
	// TriedAttempts and TriedCause describe that run when it failed and Isoform
	// is still trying: its failed tries and why, when known (e.g. "timeout").
	TriedAttempts int    `json:"triedAttempts,omitempty"`
	TriedCause    string `json:"triedCause,omitempty"`
}

// upcomingBriefState is what the brief service tells Upcoming: whether the
// brief exists in this build, its schedule, its run records and which briefs
// are stored (a slot whose brief is stored does not run again that day).
type upcomingBriefState struct {
	Available bool
	Settings  mobileBriefSettings
	Runs      []mobileBriefRun
	Exists    func(briefID string) bool
	// NextRun is the scheduler's own next run for a slot (mobileBriefService.
	// slotNextRun). Without it the next run is read from Settings and Exists.
	NextRun func(now time.Time, slot string) time.Time
	// MadeOnRequest finds the first brief made on request (Make it now) at or
	// after from and before to: it stands in for a slot it came after.
	MadeOnRequest func(from, to time.Time) (briefID string, at time.Time, ok bool)
	// MissedReason says why a slot due at a time didn't run, only when Isoform
	// observed it (mobileBriefService.missedReason).
	MissedReason func(at time.Time) string
}

// upcomingBriefReader is implemented by the brief service.
type upcomingBriefReader interface {
	upcomingBrief() upcomingBriefState
}

func (s *mobileBriefService) upcomingBrief() upcomingBriefState {
	settings := s.config.load()
	runs := s.runs.list()
	for i := range runs {
		// Older adaptive runs recorded the preparation time; a late brief's
		// generation record retains the actual scheduled time independently.
		if brief, ok := s.store.load(runs[i].BriefID); ok && brief.Generation != nil && brief.Generation.ScheduledAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, brief.Generation.ScheduledAt); err == nil {
				runs[i].ScheduledAt = brief.Generation.ScheduledAt
				runs[i].ScheduledReady = true
			}
		}
	}
	return upcomingBriefState{Available: true, Settings: settings, Runs: runs, Exists: s.store.exists,
		NextRun: func(now time.Time, slot string) time.Time {
			at, _ := s.slotNextRun(now, settings, slot)
			return at
		},
		MadeOnRequest: s.onRequestBriefBetween,
		MissedReason:  s.missedReason,
	}
}

// GetUpcoming returns the Upcoming list, ordered by next run.
func (a *App) GetUpcoming() (perfValue []UpcomingItem) {
	defer logPageBinding("GetUpcoming", time.Now(), &perfValue)
	jobs := a.ListCronJobs()
	runs := make(map[string][]CronRunState, len(jobs))
	if a.store != nil {
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		for _, job := range jobs {
			if !upcomingListsSurface(job.SurfaceKind) || isTodayNewsJob(job) {
				continue
			}
			rows, err := a.store.ListCronRuns(ctx, job.ID, pruneKeep)
			if err != nil {
				log.Printf("[upcoming] runs for %s unavailable: %v", job.ID, err)
				continue
			}
			states := make([]CronRunState, len(rows))
			for i, row := range rows {
				states[i] = runRowToState(row)
			}
			runs[job.ID] = states
		}
	}
	var brief upcomingBriefState
	if reader, ok := a.mobileBriefSource().(upcomingBriefReader); ok {
		brief = reader.upcomingBrief()
	}
	return assembleUpcoming(time.Now(), time.Local, jobs, runs, brief)
}

// upcomingListsSurface reports whether a schedule's surface can appear on
// Upcoming at all: the user's schedules ("" is the legacy default) and the
// app's own. One-time work behind a handoff is listed on the Tasks page.
func upcomingListsSurface(surfaceKind string) bool {
	switch strings.ToLower(strings.TrimSpace(surfaceKind)) {
	case "handoff", openAnswerSurfaceKind:
		return false
	default:
		return true
	}
}

// upcomingSystemSurface reports whether a schedule is one the app made for
// itself rather than one the user asked for.
func upcomingSystemSurface(surfaceKind string) bool {
	return strings.EqualFold(strings.TrimSpace(surfaceKind), todayNewsSurfaceKind)
}

// isTodayNewsJob reports whether job is the app's Daily News run.
func isTodayNewsJob(job CronJobState) bool {
	return job.SurfaceKind == todayNewsSurfaceKind && job.Name == todayNewsJobName
}

// assembleUpcoming builds the list from the schedules, their runs (newest
// first, as the store returns them) and the brief. It reads no clock of its
// own, so ordering and health are table-tested.
func assembleUpcoming(
	now time.Time,
	loc *time.Location,
	jobs []CronJobState,
	runs map[string][]CronRunState,
	brief upcomingBriefState,
) []UpcomingItem {
	if loc == nil {
		loc = time.Local
	}
	items := make([]UpcomingItem, 0, len(jobs)+2)
	for _, job := range jobs {
		if !upcomingListsSurface(job.SurfaceKind) || isTodayNewsJob(job) {
			continue
		}
		item := upcomingScheduleItem(now, job, runs[job.ID])
		if upcomingSystemSurface(job.SurfaceKind) {
			// A schedule the app made for itself is shown only while
			// something is wrong with it.
			if item.LastResult != upcomingResultFailed && item.LastResult != upcomingResultMissed {
				continue
			}
			item.System = true
		}
		items = append(items, item)
	}
	if brief.Available {
		for _, entry := range mobileBriefSlotClock(brief.Settings) {
			items = append(items, upcomingBriefItem(now, loc, brief, entry.slot))
		}
	}
	sortUpcoming(items)
	return items
}

// upcomingScheduleItem describes one schedule and its runs.
func upcomingScheduleItem(now time.Time, job CronJobState, runs []CronRunState) UpcomingItem {
	item := UpcomingItem{
		ID:             job.ID,
		Kind:           upcomingKindSchedule,
		JobID:          job.ID,
		Name:           strings.TrimSpace(job.Name),
		Summary:        upcomingSummaryLine(job),
		Enabled:        job.Enabled,
		ScheduleKind:   job.ScheduleKind,
		ScheduleConfig: upcomingLocalScheduleConfig(job, now),
		FailedAttempts: job.ConsecutiveErrors,
	}
	if isTodayNewsJob(job) {
		item.Kind = upcomingKindNews
		item.Name = ""
		item.Summary = ""
	}
	if job.Enabled {
		item.NextRunAt = job.NextRunAt
		if job.RetryAttempt > 0 && job.RetryRunAt > 0 && (item.NextRunAt == 0 || job.RetryRunAt < item.NextRunAt) {
			item.NextRunAt = job.RetryRunAt
		}
	}
	since := now.Add(-cronMissedWindow).UnixMilli()
	for i, run := range runs {
		if i == 0 {
			item.LastResult, item.LastReason = upcomingRunResult(run)
			item.LastRunAt = run.FinishedAt
			if item.LastRunAt <= 0 || run.Status == "running" {
				item.LastRunAt = run.StartedAt
			}
		}
		if run.Status == cronRunMissed && run.StartedAt >= since {
			item.MissedRuns++
		}
	}
	if item.LastResult == "" && job.LastRunStatus == "running" {
		item.LastResult = upcomingResultRunning
	}
	return item
}

// upcomingLocalScheduleConfig is the schedule as the page shows it: a cron
// schedule kept in another time zone (the automation extractor stores UTC)
// is shown in local time, since the page says times without a zone. One it
// cannot rewrite keeps its zone, which the page ignores.
func upcomingLocalScheduleConfig(job CronJobState, now time.Time) any {
	if job.ScheduleKind != "cron" {
		return job.ScheduleConfig
	}
	config, ok := job.ScheduleConfig.(map[string]any)
	if !ok {
		return job.ScheduleConfig
	}
	expr, _ := config["expr"].(string)
	tz, _ := config["tz"].(string)
	if strings.TrimSpace(tz) == "" {
		return job.ScheduleConfig
	}
	local, err := cronInLocalTime(scheduleCron{Expr: expr, TZ: tz}, now)
	if err != nil {
		return job.ScheduleConfig
	}
	return map[string]any{"expr": local.Expr}
}

// upcomingSummaryLine is one line on what a schedule does: its description,
// or the first line of what it asks Isoform to do.
func upcomingSummaryLine(job CronJobState) string {
	text := strings.TrimSpace(job.Description)
	if text == "" {
		if cfg, ok := job.TargetConfig.(map[string]any); ok {
			if prompt, ok := cfg["prompt"].(string); ok {
				text = strings.TrimSpace(prompt)
			}
		}
	}
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	const maxRunes = 160
	if runes := []rune(text); len(runes) > maxRunes {
		text = strings.TrimSpace(string(runes[:maxRunes])) + "…"
	}
	return text
}

// upcomingRunResult turns a run into the page's result and reason.
func upcomingRunResult(run CronRunState) (string, string) {
	switch run.Status {
	case "ok":
		return upcomingResultDone, ""
	case "error":
		return upcomingResultFailed, upcomingFailureReason(run.Error)
	case "skipped":
		return upcomingResultSkipped, upcomingReasonNotReady
	case cronRunMissed:
		if strings.TrimSpace(run.Error) != "" {
			return upcomingResultMissed, upcomingFailureReason(run.Error)
		}
		return upcomingResultMissed, upcomingReasonAsleep
	case "running":
		return upcomingResultRunning, ""
	default:
		return "", ""
	}
}

// upcomingFailureReasons maps fragments of a run's error to a reason, in
// the order they are checked.
var upcomingFailureReasons = []struct {
	reason string
	parts  []string
}{
	{upcomingReasonNotReady, []string{errAgentNotReady.Error()}},
	{upcomingReasonEmpty, []string{"no deliverable"}},
	{upcomingReasonTimeout, []string{"deadline exceeded", "timed out", "timeout"}},
	{upcomingReasonBusy, []string{"429", "rate limit", "rate_limit", "overloaded", "quota", "usage limit"}},
	{upcomingReasonNoModel, []string{"401", "403", "unauthorized", "api key", "no ai provider", "authentication", "not signed in", "sign in"}},
	{upcomingReasonOffline, []string{"no such host", "connection refused", "connection reset", "network is unreachable", "dial tcp", "offline"}},
}

// upcomingFailureReason sorts a run's error into a reason the page can say
// in plain words.
func upcomingFailureReason(errText string) string {
	text := strings.ToLower(strings.TrimSpace(errText))
	if text == "" {
		return upcomingReasonOther
	}
	for _, entry := range upcomingFailureReasons {
		for _, part := range entry.parts {
			if strings.Contains(text, part) {
				return entry.reason
			}
		}
	}
	return upcomingReasonOther
}

// upcomingBriefItem describes one brief slot from the schedule and the run
// records.
func upcomingBriefItem(now time.Time, loc *time.Location, brief upcomingBriefState, slot string) UpcomingItem {
	settings := brief.Settings
	item := UpcomingItem{
		ID:           upcomingBriefID(slot),
		Kind:         upcomingKindBrief,
		Slot:         slot,
		Enabled:      settings.Enabled,
		ScheduleKind: "daily",
	}
	switch slot {
	case mobileBriefSlotMorning:
		item.Time = settings.Morning
	case mobileBriefSlotEvening:
		item.Time = settings.Evening
	default:
	}
	if settings.Enabled {
		item.NextRunAt = upcomingBriefNextRun(now, loc, brief, slot)
	}
	since := now.Add(-mobileBriefHealthWindow)
	var latest *mobileBriefRun
	var latestAt time.Time
	for i := range brief.Runs {
		run := brief.Runs[i]
		if run.Slot != slot {
			continue
		}
		scheduled, err := time.Parse(time.RFC3339Nano, run.ScheduledAt)
		if err != nil {
			continue
		}
		if run.Status == mobileBriefRunMissed && !scheduled.Before(since) {
			item.MissedRuns++
		}
		if latest == nil || scheduled.After(latestAt) {
			latest = &brief.Runs[i]
			latestAt = scheduled
		}
	}
	if latest == nil {
		return item
	}
	// A failed try still waiting for its retry made no brief either
	// (tasks-review v1.0.8 #1): the row says so, retry or not.
	if (latest.Status == mobileBriefRunMissed || latest.Status == mobileBriefRunFailed) &&
		(brief.Exists == nil || !brief.Exists(latest.BriefID)) &&
		!upcomingBriefStoodIn(loc, brief, mobileBriefSlotOccurrence{BriefID: latest.BriefID, Slot: slot, ScheduledAt: latestAt}) {
		item.DidntRunAt = latestAt.UnixMilli()
		if latest.Status == mobileBriefRunFailed && latest.Attempts > 0 && item.NextRunAt > now.UnixMilli() {
			item.TriedAttempts, item.TriedCause = latest.Attempts, latest.FailureCause
		}
	}
	item.LastRunAt = latestAt.UnixMilli()
	if at, err := time.Parse(time.RFC3339Nano, latest.At); err == nil && latest.Status == mobileBriefRunOK {
		item.LastRunAt = at.UnixMilli()
	}
	switch latest.Status {
	case mobileBriefRunOK:
		item.LastResult = upcomingResultDone
	case mobileBriefRunFailed:
		item.LastResult = upcomingResultFailed
		item.LastReason = upcomingReasonRetrying
		item.FailedAttempts = latest.Attempts
	case mobileBriefRunMissed:
		item.LastResult = upcomingResultMissed
		item.LastReason = upcomingReasonAsleep
		if latest.Attempts >= mobileBriefMissedAfterFailures {
			item.LastReason = upcomingReasonKeptFailed
			item.FailedAttempts = latest.Attempts
		}
	default:
	}
	return item
}

// upcomingBriefNextRun is when a brief slot runs next, the way the brief
// service decides it (mobileBriefPendingSlot): a slot whose brief is already
// stored waits for the next day, and a slot still inside its catch-up grace
// without a brief is built now, so its next run is now.
func upcomingBriefNextRun(now time.Time, loc *time.Location, brief upcomingBriefState, slot string) int64 {
	var at time.Time
	if brief.NextRun != nil {
		at = brief.NextRun(now, slot)
	} else {
		at, _ = mobileBriefSlotNextRun(now, loc, brief.Settings, slot, mobileBriefScheduleFacts{Exists: brief.Exists})
	}
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

// upcomingBriefStoodIn reports a brief made on request inside a slot's
// window, after its time: the person has that slot's brief.
func upcomingBriefStoodIn(loc *time.Location, brief upcomingBriefState, occurrence mobileBriefSlotOccurrence) bool {
	if brief.MadeOnRequest == nil {
		return false
	}
	_, _, ok := brief.MadeOnRequest(occurrence.ScheduledAt, mobileBriefSlotCloses(occurrence, loc, brief.Settings))
	return ok
}

// upcomingKindOrder breaks ties between rows due at the same time: the
// brief, then the news, then the user's schedules.
func upcomingKindOrder(kind string) int {
	switch kind {
	case upcomingKindBrief:
		return 0
	case upcomingKindNews:
		return 1
	default:
		return 2
	}
}

// sortUpcoming orders rows by next run. Rows that will run come first,
// soonest first; rows with no next run follow: the ones that run when asked
// before the paused or finished ones, then the most recently run first.
func sortUpcoming(items []UpcomingItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		aNext, bNext := a.NextRunAt > 0, b.NextRunAt > 0
		if aNext != bNext {
			return aNext
		}
		if aNext && a.NextRunAt != b.NextRunAt {
			return a.NextRunAt < b.NextRunAt
		}
		if !aNext && a.Enabled != b.Enabled {
			return a.Enabled
		}
		if !aNext && a.LastRunAt != b.LastRunAt {
			return a.LastRunAt > b.LastRunAt
		}
		if ka, kb := upcomingKindOrder(a.Kind), upcomingKindOrder(b.Kind); ka != kb {
			return ka < kb
		}
		if a.Name != b.Name {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return a.ID < b.ID
	})
}
