package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/persistence"
	"wails-gui/storage"
)

func TestParseOpenAnswerCheckResultStrictly(t *testing.T) {
	valid := `{"settled":true,"answer":"It passed.","value":"passed","source":"Official results"}`
	result, ok := parseOpenAnswerCheckResult(valid)
	if !ok || !result.Settled || result.Answer != "It passed." || result.Value != "passed" || result.Source != "Official results" {
		t.Fatalf("valid result = %+v, %v", result, ok)
	}
	wrapped := strings.TrimSpace(stripResearchMeta(withResearchMeta(valid, researchMeta{})))
	if result, ok := parseOpenAnswerCheckResult(wrapped); !ok || !result.Settled || result.Answer != "It passed." {
		t.Fatalf("meta-wrapped result = %+v, %v", result, ok)
	}
	for _, raw := range []string{
		`not json`,
		`{"settled":true,"answer":"It passed.","value":"passed"}`,
		`{"settled":"true","answer":"It passed.","value":"passed","source":"Official results"}`,
		`{"settled":true,"answer":"It passed.","value":"passed","source":"Official results","extra":1}`,
		`{"settled":true,"answer":"It passed.","value":"passed","source":"Official results"} trailing`,
		`{"settled":true,"settled":false,"answer":"It passed.","value":"passed","source":"Official results"}`,
		"```json\n" + valid + "\n```",
	} {
		if parsed, ok := parseOpenAnswerCheckResult(raw); ok {
			t.Errorf("accepted %q as %+v", raw, parsed)
		}
	}
}

func TestOpenAnswerCheckJobsAreHiddenAndGrantedResearch(t *testing.T) {
	job := &storage.CronJobRow{ID: "answer", Name: openAnswerJobName("answer"), SurfaceKind: openAnswerSurfaceKind}
	if upcomingListsSurface(job.SurfaceKind) {
		t.Fatal("open answer check is exposed on Upcoming")
	}
	if !isAppResearchJob(job) || len(appResearchAuthorityGrants(job)) == 0 {
		t.Fatal("open answer check did not receive app research authority")
	}
	if isAppResearchJob(&storage.CronJobRow{ID: "user", Name: job.Name, SurfaceKind: "scheduled"}) {
		t.Fatal("user schedule received app research authority")
	}
}

func TestRecoverOpenAnswerCheckJob(t *testing.T) {
	// No quiet hours: the default 22:00-08:00 window would hold the closing line.
	app, _, _ := openAnswerQuietTestApp(t, engagement.QuietWindow{})
	ctx := context.Background()
	session, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	row := storage.OpenAnswerRow{
		ID: "recover-check", ChatSessionID: session.LocalID, AssistantMessageID: "promise",
		Question: "When will results be final?", Topic: "results", Settles: "result", Source: "Official results",
		WindowStart: now.Add(-time.Minute).UnixMilli(), WindowEnd: now.Add(time.Hour).UnixMilli(),
		Delivery: "around", ExpiresAt: now.Add(24 * time.Hour).UnixMilli(), State: "open", CreatedAt: now.UnixMilli(),
	}
	if err := app.store.CreateOpenAnswer(ctx, row); err != nil {
		t.Fatal(err)
	}
	expiredMail := storage.OpenAnswerRow{
		ID: "expired-mail-check", ChatSessionID: session.LocalID, AssistantMessageID: "mail-promise",
		Question: "Will Priya reply?", Topic: "Plans", Settles: "reply", Source: "Mail", WatchedEvent: `{"who":"Priya","label":"Plans"}`,
		Delivery: "around", ExpiresAt: now.Add(-time.Hour).UnixMilli(), State: "open", CreatedAt: now.Add(-8 * 24 * time.Hour).UnixMilli(),
	}
	if err := app.store.CreateOpenAnswer(ctx, expiredMail); err != nil {
		t.Fatal(err)
	}
	app.recoverOpenAnswerChecks()

	jobs, err := app.store.ListCronJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *storage.CronJobRow
	for i := range jobs {
		if jobs[i].Name == openAnswerJobName(row.ID) {
			found = &jobs[i]
			break
		}
	}
	if found == nil || found.SurfaceKind != openAnswerSurfaceKind || found.ScheduleKind != "at" || !found.DeleteAfterRun {
		t.Fatalf("recovered job = %+v", found)
	}
	expired, err := app.store.GetOpenAnswer(ctx, expiredMail.ID)
	if err != nil || expired.State != "gave_up" || expired.Posts != 1 {
		t.Fatalf("expired Mail record = %+v, %v", expired, err)
	}
	if openAnswerTransitionNotifies("gave_up", expiredMail.Delivery) {
		t.Fatal("gave-up transition produced an engagement moment")
	}
	items, err := app.store.LoadStreamItems(ctx, session.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	foundMailPost := false
	for _, item := range items {
		if item.RunID == "open-answer:"+expiredMail.ID && strings.Contains(item.Content, "hasn't replied in a week") {
			foundMailPost = true
		}
	}
	if !foundMailPost {
		t.Fatal("expired Mail record did not post its closing line")
	}
}

func createOpenAnswerCheckFixture(t *testing.T, app *App, id string, now time.Time, windowStarts ...time.Time) storage.OpenAnswerRow {
	t.Helper()
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row := storage.OpenAnswerRow{
		ID: id, ChatSessionID: session.LocalID, AssistantMessageID: "promise-" + id,
		Question: "When will the results be final?", Topic: "results", Settles: "result", Source: "Official results",
		WindowStart: now.Add(-time.Hour).UnixMilli(), WindowEnd: now.Add(time.Hour).UnixMilli(),
		Delivery: "around", ExpiresAt: now.Add(24 * time.Hour).UnixMilli(), State: "open", CreatedAt: now.UnixMilli(),
	}
	if len(windowStarts) > 0 {
		row.WindowStart = windowStarts[0].UnixMilli()
	}
	if err := app.store.CreateOpenAnswer(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	if app.cronScheduler == nil {
		app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	}
	return row
}

func openAnswerCheckJobs(t *testing.T, app *App, recordID string) []storage.CronJobRow {
	t.Helper()
	jobs, err := app.store.ListCronJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []storage.CronJobRow
	for _, job := range jobs {
		if job.Name == openAnswerJobName(recordID) && job.SurfaceKind == openAnswerSurfaceKind && job.Enabled {
			out = append(out, job)
		}
	}
	return out
}

func assertOneOpenAnswerCheckAt(t *testing.T, app *App, recordID string, at int64) storage.CronJobRow {
	t.Helper()
	jobs := openAnswerCheckJobs(t, app, recordID)
	if len(jobs) != 1 {
		t.Fatalf("pending open-answer jobs = %+v, want exactly one", jobs)
	}
	var schedule map[string]any
	if err := json.Unmarshal([]byte(jobs[0].ScheduleConfig), &schedule); err != nil {
		t.Fatal(err)
	}
	for _, value := range schedule {
		if got, ok := value.(float64); ok && int64(got) == at {
			return jobs[0]
		}
	}
	t.Fatalf("pending schedule = %s, want at %d", jobs[0].ScheduleConfig, at)
	return storage.CronJobRow{}
}

func TestCompleteOpenAnswerCheckInterimLifecycle(t *testing.T) {
	app := mainChatTestApp(t)
	pinOpenAnswerQuietOff(t, app)
	now := time.Now().Truncate(time.Millisecond)
	row := createOpenAnswerCheckFixture(t, app, "interim-lifecycle", now)
	if err := app.ensureOpenAnswerCheck(row, ""); err != nil {
		t.Fatal(err)
	}
	current := openAnswerCheckJobs(t, app, row.ID)[0]
	first := `{"settled":false,"answer":"The count is up now.","value":"count up","source":"Official results"}`
	app.completeOpenAnswerCheck(row.ID, first, now, current.ID)
	if err := app.cronScheduler.RemoveJob(t.Context(), current.ID); err != nil {
		t.Fatal(err)
	}
	afterFirst, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || afterFirst.Posts != 1 || afterFirst.State != "open" {
		t.Fatalf("first interim record = %+v, %v", afterFirst, err)
	}
	pending := assertOneOpenAnswerCheckAt(t, app, row.ID, afterFirst.LastCheckAt+int64(openAnswerCheckPeriod/time.Millisecond))
	items, err := app.store.LoadStreamItems(t.Context(), row.ChatSessionID)
	if err != nil {
		t.Fatal(err)
	}
	interims := 0
	for _, item := range items {
		if item.RunID == "open-answer:"+row.ID {
			interims++
			if item.Content != "The count is up now." {
				t.Errorf("interim content = %q", item.Content)
			}
		}
	}
	if interims != 1 {
		t.Fatalf("interim posts = %d, want 1", interims)
	}

	second := `{"settled":false,"answer":"A further update arrived.","value":"different state","source":"Official results"}`
	app.completeOpenAnswerCheck(row.ID, second, now.Add(time.Minute), pending.ID)
	if err := app.cronScheduler.RemoveJob(t.Context(), pending.ID); err != nil {
		t.Fatal(err)
	}
	afterSecond, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || afterSecond.Posts != 1 || afterSecond.LastValue != "different state" {
		t.Fatalf("second interim record = %+v, %v", afterSecond, err)
	}
	pending = assertOneOpenAnswerCheckAt(t, app, row.ID, afterSecond.LastCheckAt+int64(openAnswerCheckPeriod/time.Millisecond))

	settled := `{"settled":true,"answer":"The result is final.","value":"final state","source":"Official results"}`
	app.completeOpenAnswerCheck(row.ID, settled, now.Add(2*time.Minute), pending.ID)
	if err := app.cronScheduler.RemoveJob(t.Context(), pending.ID); err != nil {
		t.Fatal(err)
	}
	final, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || final.State != "settled" || final.Posts != 2 {
		t.Fatalf("settled record = %+v, %v", final, err)
	}
	app.completeOpenAnswerCheck(row.ID, settled, now.Add(3*time.Minute), "")
	final, err = app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || final.Posts != 2 {
		t.Fatalf("post-terminal check changed record = %+v, %v", final, err)
	}
	if jobs := openAnswerCheckJobs(t, app, row.ID); len(jobs) != 0 {
		t.Fatalf("terminal record retained pending jobs: %+v", jobs)
	}
	items, err = app.store.LoadStreamItems(t.Context(), row.ChatSessionID)
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	for _, item := range items {
		if item.RunID == "open-answer:"+row.ID {
			posts++
		}
	}
	if posts != 2 {
		t.Fatalf("post count = %d, want 2", posts)
	}
}

func TestOpenAnswerCheckLifecycleRecoveryExcludesRunningJob(t *testing.T) {
	for _, status := range []string{"error", "skipped"} {
		t.Run(status, func(t *testing.T) {
			app := mainChatTestApp(t)
			now := time.Now().Truncate(time.Millisecond)
			row := createOpenAnswerCheckFixture(t, app, "lifecycle-"+status, now)
			if err := app.ensureOpenAnswerCheck(row, ""); err != nil {
				t.Fatal(err)
			}
			current := openAnswerCheckJobs(t, app, row.ID)[0]
			app.handleCronRunLifecycle(CronRunLifecycleEvent{
				JobID: current.ID, JobName: openAnswerJobName(row.ID),
				SurfaceKind: openAnswerSurfaceKind, Status: status,
			})
			jobs := openAnswerCheckJobs(t, app, row.ID)
			if len(jobs) != 2 {
				t.Fatalf("jobs before current deletion = %+v, want current and successor", jobs)
			}
			for _, job := range jobs {
				if job.ID != current.ID && job.NextRunAt < row.LastCheckAt+int64(openAnswerCheckPeriod/time.Millisecond) {
					t.Fatalf("successor runs before the next check period: %+v", job)
				}
			}
		})
	}
}

func TestCompleteOpenAnswerCheckStripsResearchMeta(t *testing.T) {
	app := mainChatTestApp(t)
	pinOpenAnswerQuietOff(t, app)
	now := time.Now().Truncate(time.Millisecond)
	row := createOpenAnswerCheckFixture(t, app, "meta-result", now)
	raw := withResearchMeta(
		`{"settled":true,"answer":"The result is final.","value":"final","source":"Official results"}`,
		researchMeta{},
	)
	app.completeOpenAnswerCheck(row.ID, raw, now, "")
	updated, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || updated.State != "settled" || updated.Posts != 1 {
		t.Fatalf("meta-wrapped completion = %+v, %v", updated, err)
	}
}

func TestOpenAnswerCheckSkipDoesNotRunGenericAgent(t *testing.T) {
	for _, closedEyes := range []bool{false, true} {
		name := "research unavailable"
		if closedEyes {
			name = "Closed Eyes"
		}
		t.Run(name, func(t *testing.T) {
			app := mainChatTestApp(t)
			now := time.Now().Truncate(time.Millisecond)
			row := createOpenAnswerCheckFixture(t, app, "skip-"+name, now)
			app.closedEyes.enabled.Store(closedEyes)
			if !closedEyes {
				if err := app.store.SetUIState(t.Context(), permissionUIStateKey("screen_recording"), "false"); err != nil {
					t.Fatal(err)
				}
				if err := app.store.SetUIState(t.Context(), permissionUIStateKey("accessibility"), "false"); err != nil {
					t.Fatal(err)
				}
			}
			if app.dailyTopicSearchAllowed() {
				t.Fatal("test did not close the automatic research gate")
			}
			job := &storage.CronJobRow{
				ID: "running-" + row.ID, Name: openAnswerJobName(row.ID), SurfaceKind: openAnswerSurfaceKind,
				TargetConfig: `{"prompt":"open-answer-check"}`,
			}
			result, err := app.executeAgentJob(t.Context(), job)
			if err != nil || result == nil {
				t.Fatalf("restricted check result = %+v, %v", result, err)
			}
		})
	}
}

func TestAnswerReadyEngagementDedupeAndInterruption(t *testing.T) {
	spec, ok := engagement.Spec(engagement.KindAnswerReady)
	if !ok || !spec.DefaultOn || answerReadyDedupeKey("record-1") != "answer_ready:record-1" {
		t.Fatalf("answer-ready spec = %+v, %v", spec, ok)
	}
	actions := engagement.ChatActions(engagement.KindAnswerReady)
	if len(actions) != 1 || actions[0].ID != "open" {
		t.Fatalf("answer-ready actions = %+v", actions)
	}
	click := engagementClickData(engagement.Moment{
		Kind: engagement.KindAnswerReady,
		Params: map[string]string{
			"session_id": "session-1",
			"message_id": "answer-1",
		},
	})
	if click != "chat-message:session-1:answer-1" {
		t.Fatalf("answer-ready click = %q", click)
	}
	now := time.Now()
	if got := spec.Interruption(engagement.Moment{HardDeadline: true}, now); got != engagement.InterruptionTimeSensitive {
		t.Fatalf("window interruption = %q", got)
	}
	if got := spec.Interruption(engagement.Moment{}, now); got != engagement.InterruptionActive {
		t.Fatalf("mail interruption = %q", got)
	}
}

func TestOpenAnswerMailReplyCopyUsesHeadersOnly(t *testing.T) {
	event := persistence.OpenAnswerEvent{Who: "Priya", Label: "Plans"}
	at := time.Date(2026, 10, 27, 8, 5, 0, 0, time.Local)
	for _, tc := range []struct {
		language string
		want     string
	}{
		{language: "English", want: `Priya replied to "Plans" · 8:05 AM`},
		{language: "Simplified Chinese", want: "Priya 回复了「Plans」 · 8:05 AM"},
	} {
		got := openAnswerMailReplyPostCopy(event, at, tc.language)
		if got != tc.want || strings.Contains(got, "private message body") {
			t.Errorf("%s copy = %q", tc.language, got)
		}
	}
}

func TestRecoverExpiredMailOpenAnswerHoldsClosingLineDuringQuietHours(t *testing.T) {
	now := time.Now()
	app, _, _ := openAnswerQuietTestApp(t, openAnswerQuietAround(now, 30*time.Minute))
	ctx := context.Background()
	session, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expiredMail := storage.OpenAnswerRow{
		ID: "quiet-expired-mail", ChatSessionID: session.LocalID, AssistantMessageID: "mail-promise",
		Question: "Will Priya reply?", Topic: "Plans", Settles: "reply", Source: "Mail", WatchedEvent: `{"who":"Priya","label":"Plans"}`,
		Delivery: "around", ExpiresAt: now.Add(-time.Hour).UnixMilli(), State: "open", CreatedAt: now.Add(-8 * 24 * time.Hour).UnixMilli(),
	}
	if err := app.store.CreateOpenAnswer(ctx, expiredMail); err != nil {
		t.Fatal(err)
	}
	app.recoverOpenAnswerChecks()
	held, err := app.store.GetOpenAnswer(ctx, expiredMail.ID)
	if err != nil || held.State != "open" || held.HeldTransition != "gave_up" || held.Posts != 0 {
		t.Fatalf("quiet-hours expired Mail record = %+v, %v", held, err)
	}

	app.engagement.mu.Lock()
	app.engagement.prefs.Quiet = engagement.QuietWindow{}
	app.engagement.mu.Unlock()
	app.recoverOpenAnswerChecks()
	released, err := app.store.GetOpenAnswer(ctx, expiredMail.ID)
	if err != nil || released.State != "gave_up" || released.Posts != 1 || released.HeldTransition != "" {
		t.Fatalf("released expired Mail record = %+v, %v", released, err)
	}
}

// pinOpenAnswerQuietOff makes the quiet window empty for a test. Quiet hours
// default to 21:00-09:00 and 23:00-07:00, so without this the tests held every
// post when they ran at night, and passed by day: they must not depend on the
// clock.
func pinOpenAnswerQuietOff(t *testing.T, app *App) {
	t.Helper()
	svc := newEngagementService(app.store, nil, engagementDelivery{})
	rules := svc.rulesRow()
	rules.QuietStartMinutes, rules.QuietEndMinutes = 0, 0
	if err := app.store.SetEngagementRules(t.Context(), rules); err != nil {
		t.Fatal(err)
	}
}
