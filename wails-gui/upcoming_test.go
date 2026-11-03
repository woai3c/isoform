package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wails-gui/storage"
)

func upcomingTestTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", value, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func upcomingItemIDs(items []UpcomingItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func TestAssembleUpcomingOrdersByNextRun(t *testing.T) {
	now := upcomingTestTime(t, "2026-10-15 09:00")
	ms := func(value string) int64 { return upcomingTestTime(t, value).UnixMilli() }
	jobs := []CronJobState{
		{ID: "weekly", Name: "Weekly update", Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-19 09:00")},
		{ID: "paused", Name: "Paused report", Enabled: false, ScheduleKind: "cron", NextRunAt: ms("2026-10-15 10:00"), LastRunAt: ms("2026-10-10 10:00")},
		{ID: "news", Name: todayNewsJobName, SurfaceKind: todayNewsSurfaceKind, Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-16 08:00")},
		{ID: "mine-news", Name: todayNewsJobName, Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-17 08:00")},
		{ID: "soon", Name: "Inbox sweep", Enabled: true, ScheduleKind: "every", NextRunAt: ms("2026-10-15 09:30")},
		{ID: "manual", Name: "On demand", Enabled: true, ScheduleKind: "manual"},
		{ID: "task", Name: "One-time work", SurfaceKind: "handoff", Enabled: true, ScheduleKind: "at", NextRunAt: ms("2026-10-15 09:10")},
	}
	brief := upcomingBriefState{Available: true, Settings: mobileBriefSettings{Enabled: true, Morning: "07:30", Evening: "18:00"},
		Exists: func(id string) bool { return id == "2026-10-15-morning" }}

	items := assembleUpcoming(now, time.UTC, jobs, nil, brief)

	want := []string{"soon", "brief:evening", "brief:morning", "mine-news", "weekly", "manual", "paused"}
	got := upcomingItemIDs(items)
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
	byID := map[string]UpcomingItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if byID["paused"].NextRunAt != 0 {
		t.Fatalf("a paused schedule shows a next run: %+v", byID["paused"])
	}
	if got := byID["brief:morning"].NextRunAt; got != ms("2026-10-16 07:30") {
		t.Fatalf("morning brief next run = %v", time.UnixMilli(got).UTC())
	}
	if _, ok := byID["news"]; ok {
		t.Fatal("retired Daily News is still listed")
	}
}

func TestAssembleUpcomingHidesSystemJobsUnlessFailed(t *testing.T) {
	now := upcomingTestTime(t, "2026-10-15 09:00")
	ms := func(value string) int64 { return upcomingTestTime(t, value).UnixMilli() }
	jobs := []CronJobState{
		{ID: "quiet", Name: "Index refresh", SurfaceKind: "system", Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-15 12:00")},
		{ID: "broken", Name: "Cache cleanup", SurfaceKind: "system", Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-15 13:00")},
		{ID: "slept", Name: "Archive", SurfaceKind: "system", Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-15 14:00")},
		{ID: "news", Name: todayNewsJobName, SurfaceKind: todayNewsSurfaceKind, Enabled: true, ScheduleKind: "cron", NextRunAt: ms("2026-10-16 08:00")},
	}
	runs := map[string][]CronRunState{
		"quiet":  {{ID: "q1", Status: "ok", StartedAt: ms("2026-10-14 12:00"), FinishedAt: ms("2026-10-14 12:01")}},
		"broken": {{ID: "b1", Status: "error", Error: "context deadline exceeded", StartedAt: ms("2026-10-14 13:00"), FinishedAt: ms("2026-10-14 14:00")}},
		"slept":  {{ID: "s1", Status: cronRunMissed, StartedAt: ms("2026-10-14 14:00"), FinishedAt: ms("2026-10-14 14:00")}},
		"news":   {{ID: "n1", Status: "ok", StartedAt: ms("2026-10-15 08:00"), FinishedAt: ms("2026-10-15 08:02")}},
	}

	items := assembleUpcoming(now, time.UTC, jobs, runs, upcomingBriefState{})

	got := map[string]UpcomingItem{}
	for _, item := range items {
		got[item.ID] = item
	}
	if _, ok := got["quiet"]; ok {
		t.Fatal("a healthy system job is shown")
	}
	broken, ok := got["broken"]
	if !ok || !broken.System || broken.LastResult != upcomingResultFailed || broken.LastReason != upcomingReasonTimeout {
		t.Fatalf("failed system job = %+v (shown %v)", broken, ok)
	}
	slept, ok := got["slept"]
	if !ok || !slept.System || slept.LastResult != upcomingResultMissed {
		t.Fatalf("missed system job = %+v (shown %v)", slept, ok)
	}
	if news, ok := got["news"]; ok {
		t.Fatalf("retired Daily News is still listed: %+v", news)
	}
}

func TestAssembleUpcomingScheduleHealth(t *testing.T) {
	now := upcomingTestTime(t, "2026-10-15 09:00")
	ms := func(value string) int64 { return upcomingTestTime(t, value).UnixMilli() }
	job := CronJobState{
		ID: "daily", Name: "Daily digest", Description: "Summarize my inbox\nand calendar", Enabled: true,
		ScheduleKind: "cron", NextRunAt: ms("2026-10-16 08:00"), ConsecutiveErrors: 1,
	}
	runs := []CronRunState{
		{ID: "r4", Status: "error", Error: "Post \"https://api\": dial tcp: lookup api: no such host", StartedAt: ms("2026-10-15 08:00"), FinishedAt: ms("2026-10-15 08:00")},
		{ID: "r3", Status: cronRunMissed, StartedAt: ms("2026-10-14 08:00"), FinishedAt: ms("2026-10-14 08:00")},
		{ID: "r2", Status: cronRunMissed, StartedAt: ms("2026-10-13 08:00"), FinishedAt: ms("2026-10-13 08:00")},
		{ID: "r1", Status: cronRunMissed, StartedAt: ms("2026-10-01 08:00"), FinishedAt: ms("2026-10-01 08:00")},
	}

	items := assembleUpcoming(now, time.UTC, []CronJobState{job}, map[string][]CronRunState{"daily": runs}, upcomingBriefState{})

	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	item := items[0]
	if item.Summary != "Summarize my inbox" {
		t.Fatalf("summary = %q", item.Summary)
	}
	if item.LastResult != upcomingResultFailed || item.LastReason != upcomingReasonOffline || item.LastRunAt != ms("2026-10-15 08:00") {
		t.Fatalf("last run = %+v", item)
	}
	if item.MissedRuns != 2 {
		t.Fatalf("missed runs = %d, want 2 (one is older than a week)", item.MissedRuns)
	}
	if item.FailedAttempts != 1 {
		t.Fatalf("failed attempts = %d", item.FailedAttempts)
	}
}

func TestAssembleUpcomingBriefHealth(t *testing.T) {
	now := upcomingTestTime(t, "2026-10-15 07:15")
	ms := func(value string) int64 { return upcomingTestTime(t, value).UnixMilli() }
	brief := upcomingBriefState{
		Available: true,
		Settings:  mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"},
		Runs: []mobileBriefRun{
			{BriefID: "2026-10-13-evening", Slot: "evening", ScheduledAt: "2026-10-13T18:00:00Z", Status: mobileBriefRunMissed, At: "2026-10-14T00:00:00Z"},
			{BriefID: "2026-10-14-morning", Slot: "morning", ScheduledAt: "2026-10-14T07:00:00Z", Status: mobileBriefRunOK, At: "2026-10-14T07:02:00Z"},
			{BriefID: "2026-10-14-evening", Slot: "evening", ScheduledAt: "2026-10-14T18:00:00Z", Status: mobileBriefRunMissed, At: "2026-10-15T00:00:00Z", Attempts: 3},
			{BriefID: "2026-10-15-morning", Slot: "morning", ScheduledAt: "2026-10-15T07:00:00Z", Status: mobileBriefRunFailed, At: "2026-10-15T07:10:00Z", Attempts: 2},
		},
	}

	items := assembleUpcoming(now, time.UTC, nil, nil, brief)

	got := map[string]UpcomingItem{}
	for _, item := range items {
		got[item.ID] = item
	}
	morning := got["brief:morning"]
	if morning.LastResult != upcomingResultFailed || morning.LastReason != upcomingReasonRetrying || morning.FailedAttempts != 2 {
		t.Fatalf("morning = %+v", morning)
	}
	// Today's morning brief failed and is still inside its catch-up window,
	// so it is tried again now.
	if morning.NextRunAt != now.UnixMilli() || morning.Time != "07:00" {
		t.Fatalf("morning next = %v time %q", time.UnixMilli(morning.NextRunAt).UTC(), morning.Time)
	}
	evening := got["brief:evening"]
	if evening.LastResult != upcomingResultMissed || evening.LastReason != upcomingReasonKeptFailed || evening.MissedRuns != 2 {
		t.Fatalf("evening = %+v", evening)
	}
	if evening.NextRunAt != ms("2026-10-15 18:00") {
		t.Fatalf("evening next = %v", time.UnixMilli(evening.NextRunAt).UTC())
	}

	// Today's morning brief is stored: the slot waits for tomorrow even when
	// moved to a later time today. A slot within its catch-up grace without a
	// brief is built now.
	stored := map[string]bool{"2026-10-15-morning": true}
	brief.Exists = func(id string) bool { return stored[id] }
	brief.Settings.Morning = "10:00"
	for _, item := range assembleUpcoming(now, time.UTC, nil, nil, brief) {
		if item.ID == "brief:morning" && item.NextRunAt != ms("2026-10-16 10:00") {
			t.Fatalf("stored morning brief next = %v", time.UnixMilli(item.NextRunAt).UTC())
		}
	}
	stored = map[string]bool{}
	brief.Settings.Morning = "07:30"
	catchUpNow := upcomingTestTime(t, "2026-10-15 07:45")
	for _, item := range assembleUpcoming(catchUpNow, time.UTC, nil, nil, brief) {
		if item.ID == "brief:morning" && item.NextRunAt != catchUpNow.UnixMilli() {
			t.Fatalf("catch-up morning brief next = %v, want now", time.UnixMilli(item.NextRunAt).UTC())
		}
	}

	brief.Settings.Enabled = false
	for _, item := range assembleUpcoming(now, time.UTC, nil, nil, brief) {
		if item.Enabled || item.NextRunAt != 0 {
			t.Fatalf("a paused brief shows a next run: %+v", item)
		}
	}
}

func TestUpcomingFailureReason(t *testing.T) {
	cases := map[string]string{
		"":                          upcomingReasonOther,
		"agent not ready":           upcomingReasonNotReady,
		"context deadline exceeded": upcomingReasonTimeout,
		"run produced no deliverable: agent returned empty summary with no files or delivery target": upcomingReasonEmpty,
		"API error 429: rate limit":         upcomingReasonBusy,
		"401 Unauthorized: invalid api key": upcomingReasonNoModel,
		"dial tcp: connection refused":      upcomingReasonOffline,
		"unknown target kind: shell":        upcomingReasonOther,
	}
	for input, want := range cases {
		if got := upcomingFailureReason(input); got != want {
			t.Errorf("upcomingFailureReason(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMissedOccurrences(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)
	daily, _ := json.Marshal(scheduleCron{Expr: "0 8 * * *", TZ: "UTC"})
	job := &storage.CronJobRow{ID: "d", ScheduleKind: "cron", ScheduleConfig: string(daily),
		NextRunAt: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC).UnixMilli()}

	covered := missedOccurrences(job, now, true)
	if len(covered) != 3 || covered[0] != job.NextRunAt || covered[2] != time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("covered = %v", covered)
	}
	if all := missedOccurrences(job, now, false); len(all) != 4 {
		t.Fatalf("uncovered = %v", all)
	}

	job.NextRunAt = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC).UnixMilli()
	old := missedOccurrences(job, now, true)
	floor := now.Add(-cronMissedWindow).UnixMilli()
	if len(old) == 0 || old[0] < floor || len(old) > 7 {
		t.Fatalf("times older than a week = %v", old)
	}

	every, _ := json.Marshal(scheduleEvery{EveryMs: (15 * time.Minute).Milliseconds()})
	frequent := &storage.CronJobRow{ID: "e", ScheduleKind: "every", ScheduleConfig: string(every),
		NextRunAt: now.Add(-10 * time.Hour).UnixMilli()}
	if got := missedOccurrences(frequent, now, true); len(got) != cronMissedPerGap {
		t.Fatalf("a frequent schedule recorded %d times, want the cap %d", len(got), cronMissedPerGap)
	}

	// A very short interval over a week-long gap still records the times
	// just before now, not the start of the window.
	tiny, _ := json.Marshal(scheduleEvery{EveryMs: 10_000})
	fast := &storage.CronJobRow{ID: "t", ScheduleKind: "every", ScheduleConfig: string(tiny),
		NextRunAt: now.Add(-6 * 24 * time.Hour).UnixMilli()}
	got := missedOccurrences(fast, now, true)
	if len(got) != cronMissedPerGap || now.UnixMilli()-got[len(got)-1] > 20_000 {
		t.Fatalf("short interval times = %d, newest %v before now", len(got), time.Duration(now.UnixMilli()-got[len(got)-1])*time.Millisecond)
	}

	future := &storage.CronJobRow{ID: "f", ScheduleKind: "cron", ScheduleConfig: string(daily), NextRunAt: now.Add(time.Hour).UnixMilli()}
	if got := missedOccurrences(future, now, false); got != nil {
		t.Fatalf("a schedule not yet due has missed times: %v", got)
	}
	manual := &storage.CronJobRow{ID: "m", ScheduleKind: "manual", NextRunAt: now.Add(-time.Hour).UnixMilli()}
	if got := missedOccurrences(manual, now, false); got != nil {
		t.Fatalf("a manual schedule has missed times: %v", got)
	}
}

func TestRecordMissedSkipsTimesAlreadyRecorded(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	daily, _ := json.Marshal(scheduleCron{Expr: "0 8 * * *", TZ: "UTC"})
	row := storage.CronJobRow{ID: "job-missed", Name: "Daily digest", Enabled: true, ScheduleKind: "cron",
		ScheduleConfig: string(daily), TargetKind: "agent", TargetConfig: `{"prompt":"x"}`}
	if err := store.SaveCronJob(ctx, row); err != nil {
		t.Fatal(err)
	}
	var events int
	scheduler := NewCronScheduler(store, func(event string, _ any) {
		if event == "cron:run" {
			events++
		}
	}, func(string, string) {}, nil)

	times := []int64{1_000, 2_000}
	scheduler.recordMissed(ctx, &row, times, false)
	scheduler.recordMissed(ctx, &row, append(times, 3_000), false)

	runs, err := store.ListCronRuns(ctx, row.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || events != 3 {
		t.Fatalf("runs = %+v, events = %d; want three missed rows", runs, events)
	}
	for _, run := range runs {
		if run.Status != cronRunMissed || run.TriggerKind != "scheduled" || run.ItemSortStart != -1 || run.Error != "" {
			t.Fatalf("missed row = %+v", run)
		}
	}

	// Pruning dropped the rows; the same times are still not recorded again.
	if err := store.PruneCronRuns(ctx, row.ID, 0); err != nil {
		t.Fatal(err)
	}
	scheduler.recordMissed(ctx, &row, []int64{1_000, 2_000, 3_000}, false)
	if runs, _ := store.ListCronRuns(ctx, row.ID, 10); len(runs) != 0 {
		t.Fatalf("pruned times recorded again: %+v", runs)
	}

	// Times that passed while the job's runs were being skipped say so.
	row.LastRunStatus = "skipped"
	scheduler.recordMissed(ctx, &row, []int64{4_000}, false)
	runs, _ = store.ListCronRuns(ctx, row.ID, 10)
	if len(runs) != 1 || runs[0].Error != errAgentNotReady.Error() {
		t.Fatalf("skipped-time row = %+v", runs)
	}
	result, reason := upcomingRunResult(runRowToState(runs[0]))
	if result != upcomingResultMissed || reason != upcomingReasonNotReady {
		t.Fatalf("skipped-time result = %s/%s", result, reason)
	}
}

func TestCronAPISurfaceKindKeepsSchedulesOnUpcoming(t *testing.T) {
	cases := map[string]string{"": "", "scheduled": "scheduled", "Scheduled": "scheduled", "system": "", "handoff": "", "schedule_permission": ""}
	for input, want := range cases {
		if got := cronAPISurfaceKind(input); got != want {
			t.Errorf("cronAPISurfaceKind(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUpdateJobResumeWaitsForNextTime(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	daily, _ := json.Marshal(scheduleCron{Expr: "0 8 * * *"})
	stale := time.Now().Add(-48 * time.Hour).UnixMilli()
	row := storage.CronJobRow{ID: "job-paused", Name: "Daily digest", Enabled: false, ScheduleKind: "cron",
		ScheduleConfig: string(daily), TargetKind: "agent", TargetConfig: `{"prompt":"x"}`, NextRunAt: stale}
	if err := store.SaveCronJob(ctx, row); err != nil {
		t.Fatal(err)
	}
	scheduler := NewCronScheduler(store, func(string, any) {}, func(string, string) {}, nil)
	if err := scheduler.loadJobs(ctx); err != nil {
		t.Fatal(err)
	}
	close(scheduler.loadedCh)

	on := true
	state, err := scheduler.UpdateJob(ctx, row.ID, CronJobPatch{Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if state.NextRunAt <= time.Now().UnixMilli() {
		t.Fatalf("resumed schedule next run = %v, want a future time", time.UnixMilli(state.NextRunAt))
	}
	saved, err := store.LoadCronJob(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.NextRunAt != state.NextRunAt || !saved.Enabled {
		t.Fatalf("stored job = %+v", saved)
	}
}

func TestDueMissedTimesOnRetryAfterSleep(t *testing.T) {
	// A daily-hourly schedule failed at 08:00 and set a retry for 09:05; the
	// Mac slept from 09:01 to 11:30. The retry covers 11:00; 10:00 is missed.
	day := func(h, m int) time.Time { return time.Date(2026, 10, 15, h, m, 0, 0, time.UTC) }
	hourly, _ := json.Marshal(scheduleCron{Expr: "0 * * * *", TZ: "UTC"})
	job := &storage.CronJobRow{ID: "h", ScheduleKind: "cron", ScheduleConfig: string(hourly),
		NextRunAt: day(10, 0).UnixMilli(), RetryAttempt: 1, RetryRunAt: day(9, 5).UnixMilli()}
	got := dueMissedTimes(job, day(11, 30), true)
	if len(got) != 1 || got[0] != day(10, 0).UnixMilli() {
		t.Fatalf("retry after sleep missed = %v", got)
	}
	// A retry that fires before the next time comes due leaves nothing missed.
	if got := dueMissedTimes(job, day(9, 6), true); len(got) != 0 {
		t.Fatalf("on-time retry missed = %v", got)
	}
}

func TestTodayNewsIgnoresMissedRows(t *testing.T) {
	runs := []CronRunState{{ID: "m1", Status: cronRunMissed, StartedAt: 1}}
	if got := todayNewsDataFromRuns("2026-10-15", runs); got.Status != "generating" {
		t.Fatalf("news status with only missed rows = %q, want generating", got.Status)
	}
}

func TestUpcomingShowsAScheduleKeptInUTCInLocalTime(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no zone data")
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, la)
	job := CronJobState{ID: "utc", Name: "Digest", Enabled: true, ScheduleKind: "cron",
		ScheduleConfig: map[string]any{"expr": "0 16 * * 1-5", "tz": "UTC"}}
	items := assembleUpcoming(now, la, []CronJobState{job}, nil, upcomingBriefState{})
	config, _ := items[0].ScheduleConfig.(map[string]any)
	if config["expr"] != "0 9 * * 1-5" {
		t.Fatalf("shown schedule = %+v, want 09:00 local on weekdays", items[0].ScheduleConfig)
	}
}

func TestCalendarRunsLeaveOutMissedTimes(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SaveCronJob(ctx, storage.CronJobRow{ID: "hourly", Name: "Hourly sweep", Enabled: true, ScheduleKind: "cron",
		ScheduleConfig: `{"expr":"0 * * * *"}`, TargetKind: "agent", TargetConfig: `{"prompt":"x"}`}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, status := range []string{"ok", cronRunMissed, cronRunMissed} {
		at := now.Add(-time.Duration(i+1) * time.Hour).UnixMilli()
		if err := store.InsertCronRun(ctx, storage.CronRunRow{ID: "r" + strconv.Itoa(i), JobID: "hourly", StartedAt: at, FinishedAt: at,
			Status: status, TriggerKind: "scheduled", ItemSortStart: -1}); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{store: store}
	runs, err := app.GetCronRunsByDateRange(now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != "r0" || runs[0].JobName != "Hourly sweep" {
		t.Fatalf("calendar runs = %+v, want only the real run", runs)
	}
	// The job's own history keeps the missed times.
	if history, _ := store.ListCronRuns(ctx, "hourly", 10); len(history) != 3 {
		t.Fatalf("history = %d rows, want 3", len(history))
	}
}
