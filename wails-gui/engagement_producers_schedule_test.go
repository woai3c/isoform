package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestScheduleMissedTwiceInARow(t *testing.T) {
	run := func(status, trigger string) CronRunState {
		return CronRunState{Status: status, TriggerKind: trigger}
	}
	// A time that passed while Isoform was open but not ready; one with no
	// reason passed while the Mac was asleep or Isoform was closed.
	notReady := CronRunState{Status: cronRunMissed, TriggerKind: "scheduled", Error: errAgentNotReady.Error()}
	asleep := run(cronRunMissed, "scheduled")
	cases := []struct {
		name string
		runs []CronRunState
		want bool
	}{
		{"two failures", []CronRunState{run("error", "scheduled"), run("error", "scheduled")}, true},
		{"missed then failed", []CronRunState{notReady, run("error", "catchup")}, true},
		{"a run in progress does not hide two misses", []CronRunState{run("running", "catchup"), notReady, notReady}, true},
		{"times passed asleep do not count", []CronRunState{asleep, asleep, asleep}, false},
		{"times passed asleep do not break a streak either", []CronRunState{run("error", "catchup"), asleep, asleep, run("error", "scheduled")}, true},
		{"a failure and its failed retry are one outcome", []CronRunState{run("error", cronTriggerRetry), run("error", "scheduled"), run("ok", "scheduled")}, false},
		{"two retries of one time are one outcome", []CronRunState{run("error", cronTriggerRetry), run("error", cronTriggerRetry), run("error", "scheduled")}, false},
		{"a retry that worked clears its time", []CronRunState{run("error", "scheduled"), run("ok", cronTriggerRetry), run("error", "scheduled")}, false},
		{"two times that failed after their retries", []CronRunState{run("error", cronTriggerRetry), run("error", "scheduled"), run("error", cronTriggerRetry), run("error", "scheduled")}, true},
		{"one failure", []CronRunState{run("error", "scheduled"), run("ok", "scheduled")}, false},
		{"an ok run breaks the streak", []CronRunState{run("error", "scheduled"), run("ok", "scheduled"), run("error", "scheduled")}, false},
		{"runs by hand do not count", []CronRunState{run("error", "manual"), run("error", "scheduled"), run("ok", "scheduled")}, false},
		{"skipped runs do not count", []CronRunState{run("skipped", "scheduled"), run("skipped", "scheduled"), run("error", "scheduled")}, false},
		{"nothing yet", nil, false},
	}
	for _, tc := range cases {
		if got := scheduleMissedTwiceInARow(tc.runs); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// scheduleMissedRig is an app whose schedules and engagement moments share
// one store.
func scheduleMissedRig(t *testing.T, jobs ...storage.CronJobRow) (*App, *engagementTestRig) {
	t.Helper()
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
	for _, job := range jobs {
		if err := rig.store.SaveCronJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	return app, rig
}

func addScheduleRun(t *testing.T, store *storage.Store, jobID, id, status, trigger string, at time.Time) {
	t.Helper()
	if err := store.InsertCronRun(context.Background(), storage.CronRunRow{
		ID: id, JobID: jobID, StartedAt: at.UnixMilli(), FinishedAt: at.UnixMilli(),
		Status: status, TriggerKind: trigger, ItemSortStart: -1,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleMissedTwiceRaisesOneMomentPerDay(t *testing.T) {
	job := upcomingTestJob("weekly", "Weekly update", "cron", `{"expr":"0 9 * * 1"}`, true)
	app, rig := scheduleMissedRig(t, job)
	day := rig.clock

	addScheduleRun(t, rig.store, "weekly", "r1", "error", "scheduled", day.Add(-48*time.Hour))
	app.noteScheduleRunHealth("weekly")
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 0 {
		t.Fatalf("one failure raised a moment: %+v", moments)
	}

	addScheduleRun(t, rig.store, "weekly", "asleep", cronRunMissed, "scheduled", day.Add(-36*time.Hour))
	app.noteScheduleRunHealth("weekly")
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 0 {
		t.Fatalf("a time missed while the Mac slept raised a moment: %+v", moments)
	}

	if err := rig.store.InsertCronRun(context.Background(), storage.CronRunRow{
		ID: "r2", JobID: "weekly", StartedAt: day.Add(-24 * time.Hour).UnixMilli(), FinishedAt: day.Add(-24 * time.Hour).UnixMilli(),
		Status: cronRunMissed, TriggerKind: "scheduled", Error: errAgentNotReady.Error(), ItemSortStart: -1,
	}); err != nil {
		t.Fatal(err)
	}
	app.noteScheduleRunHealth("weekly")
	app.noteScheduleRunHealth("weekly")
	moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed))
	if len(moments) != 1 || moments[0].SubjectID != "weekly" || moments[0].DedupeKey != "schedule_missed:weekly:"+day.Format("2006-01-02") {
		t.Fatalf("moments = %+v, want one for today", moments)
	}
	var params map[string]string
	_ = json.Unmarshal([]byte(moments[0].Params), &params)
	if params["title"] != "Weekly update" {
		t.Fatalf("params = %v", params)
	}

	// The next day the same streak may be said once more.
	rig.clock = day.Add(24 * time.Hour)
	app.noteScheduleRunHealth("weekly")
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 2 {
		t.Fatalf("moments on the next day = %d, want 2", len(moments))
	}
}

func TestScheduleMissedSkipsHandoffWorkAndHealthySchedules(t *testing.T) {
	handoffJob := upcomingTestJob("task", "One-time work", "at", `{"at_ms":1}`, true)
	handoffJob.SurfaceKind = "handoff"
	healthy := upcomingTestJob("daily", "Daily digest", "cron", `{"expr":"0 8 * * *"}`, true)
	app, rig := scheduleMissedRig(t, handoffJob, healthy)
	for i, id := range []string{"h1", "h2"} {
		addScheduleRun(t, rig.store, "task", id, "error", "scheduled", rig.clock.Add(-time.Duration(i+1)*time.Hour))
	}
	addScheduleRun(t, rig.store, "daily", "d1", "ok", "scheduled", rig.clock.Add(-time.Hour))
	addScheduleRun(t, rig.store, "daily", "d2", "error", "scheduled", rig.clock.Add(-2*time.Hour))
	app.noteScheduleRunHealth("task")
	app.noteScheduleRunHealth("daily")
	app.noteScheduleRunHealth("missing")
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 0 {
		t.Fatalf("moments = %+v, want none", moments)
	}
}

func TestScheduleMissedFiresFromAFailedRunAndFromRecordedMissedTimes(t *testing.T) {
	job := upcomingTestJob("weekly", "Weekly update", "cron", `{"expr":"0 9 * * 1"}`, true)
	app, rig := scheduleMissedRig(t, job)

	// A failed run reaches the check through the run callback.
	addScheduleRun(t, rig.store, "weekly", "r1", "error", "scheduled", rig.clock.Add(-2*time.Hour))
	addScheduleRun(t, rig.store, "weekly", "r2", "error", "scheduled", rig.clock.Add(-time.Hour))
	app.handleCronRunLifecycle(CronRunLifecycleEvent{JobID: "weekly", JobName: "Weekly update", Status: "error", RunID: "r2",
		StartedAt: rig.clock.Add(-time.Hour).UnixMilli(), FinishedAt: rig.clock.Add(-time.Hour).UnixMilli()})
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 1 {
		t.Fatalf("moments after a second failure = %d, want 1", len(moments))
	}

	// Recorded missed times reach it through the scheduler's callback.
	var told []string
	scheduler := NewCronScheduler(rig.store, func(string, any) {}, func(string, string) {}, nil)
	scheduler.SetOnMissed(func(jobID string) { told = append(told, jobID) })
	row, err := rig.store.LoadCronJob(context.Background(), "weekly")
	if err != nil {
		t.Fatal(err)
	}
	// Times a dispatched run covers tell no one; that run decides.
	scheduler.recordMissed(context.Background(), row, []int64{500}, false)
	if len(told) != 0 {
		t.Fatalf("covered missed times told %v", told)
	}
	scheduler.recordMissed(context.Background(), row, []int64{1_000}, true)
	scheduler.recordMissed(context.Background(), row, []int64{1_000}, true)
	if len(told) != 1 || told[0] != "weekly" {
		t.Fatalf("told = %v, want the job once (nothing new the second time)", told)
	}
}

func TestScheduleMissedSkipsManualRunsAndPendingRetries(t *testing.T) {
	retrying := upcomingTestJob("retrying", "Retrying job", "cron", `{"expr":"0 9 * * *"}`, true)
	retrying.RetryAttempt = 1
	retrying.RetryRunAt = time.Now().Add(time.Minute).UnixMilli()
	plain := upcomingTestJob("plain", "Plain job", "cron", `{"expr":"0 9 * * *"}`, true)
	app, rig := scheduleMissedRig(t, retrying, plain)
	for _, id := range []string{"retrying", "plain"} {
		addScheduleRun(t, rig.store, id, id+"-1", "error", "scheduled", rig.clock.Add(-2*time.Hour))
		addScheduleRun(t, rig.store, id, id+"-2", "error", "scheduled", rig.clock.Add(-time.Hour))
	}
	app.noteScheduleRunHealth("retrying")
	app.handleCronRunLifecycle(CronRunLifecycleEvent{JobID: "plain", Status: "error", TriggerKind: "manual",
		StartedAt: rig.clock.UnixMilli(), FinishedAt: rig.clock.UnixMilli()})
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 0 {
		t.Fatalf("moments = %+v, want none for a pending retry or a run by hand", moments)
	}
}

func TestScheduleMissedRetiresWhenTheScheduleRecovers(t *testing.T) {
	job := upcomingTestJob("recovers", "Recovering job", "cron", `{"expr":"0 9 * * *"}`, true)
	app, rig := scheduleMissedRig(t, job)
	addScheduleRun(t, rig.store, "recovers", "r1", "error", "scheduled", rig.clock.Add(-2*time.Hour))
	addScheduleRun(t, rig.store, "recovers", "r2", "error", "scheduled", rig.clock.Add(-time.Hour))
	app.noteScheduleRunHealth("recovers")
	if len(openMoments(t, rig.store, string(engagement.KindScheduleMissed))) != 1 {
		t.Fatal("no moment raised")
	}
	app.handleCronRunLifecycle(CronRunLifecycleEvent{JobID: "recovers", Status: "ok", TriggerKind: "scheduled",
		StartedAt: rig.clock.UnixMilli(), FinishedAt: rig.clock.UnixMilli()})
	for _, row := range openMoments(t, rig.store, string(engagement.KindScheduleMissed)) {
		if row.State != storage.EngagementStateSent {
			t.Fatalf("a recovered schedule's notice is still waiting: %+v", row)
		}
	}
}

func TestScheduleMissedTitleIsPlainShortText(t *testing.T) {
	cases := map[string]string{
		"Weekly update":                "Weekly update",
		"Apple ID locked\" call now":   "Apple ID locked call now",
		"line one\nline two\u202eevil": "line one line two evil",
		"  ":                           "Untitled schedule",
		strings.Repeat("x", 80):        strings.Repeat("x", 60) + "...",
	}
	for input, want := range cases {
		if got := scheduleMissedTitle(input); got != want {
			t.Errorf("scheduleMissedTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestScheduleMissedNotificationOpensItsCard(t *testing.T) {
	m := engagement.Moment{ID: "schedule-moment", Kind: engagement.KindScheduleMissed, SubjectID: "weekly", Params: map[string]string{scheduleMissedCauseParam: scheduleMissedCauseMissed}}
	if got := engagementClickData(m); got != "main-chat-item:schedule-moment" {
		t.Fatalf("click data = %q", got)
	}
	app := mainChatTestApp(t)
	app.postEngagementReport(m, engagementText{Title: "A schedule did not run."})
	reports := mainChatProactiveItems(t, app)
	if len(reports) != 1 {
		t.Fatalf("reports = %+v", reports)
	}
	if report := reports[0]; report.Target.Kind != mainChatTargetAutomation || report.Target.ID != "weekly" {
		t.Fatalf("report target = %+v", report.Target)
	}

	// A two-failure streak is actionable even though the first run folded into status.
	failed := engagement.Moment{Kind: engagement.KindScheduleMissed, SubjectID: "other", Params: map[string]string{scheduleMissedCauseParam: scheduleMissedCauseFailed}}
	app.postEngagementReport(failed, engagementText{Title: "A schedule did not run."})
	if reports := mainChatProactiveItems(t, app); len(reports) != 2 {
		t.Fatalf("a failure-completed streak did not post a card: %d reports", len(reports))
	}
}

func TestSingleMissedScheduleFoldsStatusWithoutMoment(t *testing.T) {
	job := upcomingTestJob("weekly", "Weekly update", "cron", `{"expr":"0 9 * * 1"}`, true)
	app, rig := scheduleMissedRig(t, job)
	if err := rig.store.InsertCronRun(context.Background(), storage.CronRunRow{
		ID: "missed-once", JobID: job.ID, Status: cronRunMissed, TriggerKind: "scheduled",
		Error: errAgentNotReady.Error(), StartedAt: rig.clock.Add(-time.Hour).UnixMilli(), ItemSortStart: -1,
	}); err != nil {
		t.Fatal(err)
	}
	app.noteScheduleRunHealth(job.ID)
	app.noteScheduleRunHealth(job.ID)
	entries := mainChatStatusEntries(t, app)
	if len(entries) != 1 || entries[0].Kind != "schedule_missed_once" || entries[0].Target.RunID != "missed-once" {
		t.Fatalf("single missed run did not fold: %+v", entries)
	}
	if len(openMoments(t, rig.store, string(engagement.KindScheduleMissed))) != 0 {
		t.Fatal("one missed time created an actionable moment")
	}
}

func TestScheduleMissedIgnoresTimesMissedWhileClosed(t *testing.T) {
	// Isoform was closed overnight: catch-up records the passed times of a
	// schedule past its first five as missed and tells the check, which
	// says nothing, since closing the app is not a silent failure.
	job := upcomingTestJob("hourly", "Hourly sweep", "cron", `{"expr":"0 * * * *"}`, true)
	app, rig := scheduleMissedRig(t, job)
	scheduler := NewCronScheduler(rig.store, func(string, any) {}, func(string, string) {}, nil)
	scheduler.SetOnMissed(app.noteScheduleRunHealth)
	row, err := rig.store.LoadCronJob(context.Background(), "hourly")
	if err != nil {
		t.Fatal(err)
	}
	var times []int64
	for i := 8; i >= 1; i-- {
		times = append(times, rig.clock.Add(-time.Duration(i)*time.Hour).UnixMilli())
	}
	scheduler.recordMissed(context.Background(), row, times, true)
	if runs, _ := rig.store.ListCronRuns(context.Background(), "hourly", 20); len(runs) != len(times) {
		t.Fatalf("missed rows = %d, want %d", len(runs), len(times))
	}
	if moments := openMoments(t, rig.store, string(engagement.KindScheduleMissed)); len(moments) != 0 {
		t.Fatalf("times missed while Isoform was closed raised a moment: %+v", moments)
	}
}

func TestCronRetryRunIsRecordedAsARetry(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	job := upcomingTestJob("flaky", "Flaky job", "cron", `{"expr":"0 9 * * *"}`, true)
	job.RetryAttempt = 1
	job.RetryRunAt = time.Now().Add(-time.Second).UnixMilli()
	job.RetryPolicy = `{"max_attempts":1}`
	if err := store.SaveCronJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 1)
	scheduler := NewCronScheduler(store, func(string, any) {}, func(string, string) {},
		func(context.Context, *storage.CronJobRow) (*cronExecResult, error) {
			ran <- struct{}{}
			return &cronExecResult{Summary: "done"}, nil
		})
	if err := scheduler.loadJobs(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.tick(ctx)
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("the retry did not run")
	}
	scheduler.wg.Wait()
	runs, err := store.ListCronRuns(ctx, "flaky", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].TriggerKind != cronTriggerRetry {
		t.Fatalf("runs = %+v, want one retry", runs)
	}
	saved, _ := store.LoadCronJob(ctx, "flaky")
	if saved.LastRunAt <= 0 {
		t.Fatal("a retry no longer counts as the schedule's last run")
	}
}

func TestEngagementQuietScheduleMissedSuppressesSeparateStatus(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(fmt.Sprint(quiet), func(t *testing.T) {
			job := upcomingTestJob("quiet-job", "Weekly update", "cron", `{"expr":"0 9 * * 1"}`, true)
			app, rig := scheduleMissedRig(t, job)
			if err := rig.svc.SetKindQuiet(t.Context(), engagement.KindScheduleMissed, quiet); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 2; i++ {
				at := rig.clock.Add(-time.Duration(i) * time.Hour)
				if err := rig.store.InsertCronRun(t.Context(), storage.CronRunRow{ID: fmt.Sprint(i), JobID: job.ID, Status: cronRunMissed, Error: errAgentNotReady.Error(), TriggerKind: "scheduled", StartedAt: at.UnixMilli(), FinishedAt: at.UnixMilli(), ItemSortStart: -1}); err != nil {
					t.Fatal(err)
				}
			}
			app.noteScheduleRunHealth(job.ID)
			statuses := mainChatStatusEntries(t, app)
			if quiet && len(statuses) != 0 {
				t.Fatalf("quiet missed schedule posted status: %+v", statuses)
			}
			if !quiet && len(statuses) != 1 {
				t.Fatalf("normal missed schedule lost status: %+v", statuses)
			}
			if quiet {
				rig.svc.Pass(t.Context())
				lines, err := app.GetTodayQuietLines()
				if err != nil || len(lines) != 1 || lines[0].Count != 1 {
					t.Fatalf("quiet schedule did not reach Today: %+v %v", lines, err)
				}
			}
		})
	}
}
