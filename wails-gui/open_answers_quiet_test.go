package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func openAnswerQuietAround(now time.Time, radius time.Duration) engagement.QuietWindow {
	day := 24 * time.Hour
	offset := time.Duration(now.Hour())*time.Hour +
		time.Duration(now.Minute())*time.Minute +
		time.Duration(now.Second())*time.Second +
		time.Duration(now.Nanosecond())
	start, end := offset-radius, offset+radius
	if start < 0 {
		start += day
	}
	if end >= day {
		end -= day
	}
	return engagement.QuietWindow{Start: start, End: end}
}

func openAnswerQuietEndingSoon(now time.Time, after time.Duration) engagement.QuietWindow {
	day := 24 * time.Hour
	offset := time.Duration(now.Hour())*time.Hour +
		time.Duration(now.Minute())*time.Minute +
		time.Duration(now.Second())*time.Second +
		time.Duration(now.Nanosecond())
	start, end := offset-5*time.Minute, offset+after
	if start < 0 {
		start += day
	}
	if end >= day {
		end -= day
	}
	return engagement.QuietWindow{Start: start, End: end}
}

func openAnswerQuietTestApp(t *testing.T, quiet engagement.QuietWindow) (*App, *engagementTestRig, *mobileBriefService) {
	t.Helper()
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	app := mainChatTestApp(t)
	rig := newEngagementTestRig(t)
	rig.svc.mu.Lock()
	rig.svc.prefs.Quiet = quiet
	rig.svc.mu.Unlock()
	app.engagement = rig.svc
	app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	briefs := newMobileBriefService(app)
	app.mobileBridgeMu.Lock()
	app.mobileBriefSvc = briefs
	app.mobileBridgeMu.Unlock()
	return app, rig, briefs
}

func TestOpenAnswerHeldDuringQuietHoursAndRecoveredAfterQuiet(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	quiet := openAnswerQuietAround(now, 30*time.Minute)
	app, _, _ := openAnswerQuietTestApp(t, quiet)
	row := createOpenAnswerCheckFixture(t, app, "quiet-hold", now)
	if err := app.ensureOpenAnswerCheck(row, ""); err != nil {
		t.Fatal(err)
	}
	if jobs := openAnswerCheckJobs(t, app, row.ID); len(jobs) != 1 {
		t.Fatalf("initial check jobs = %d, want 1", len(jobs))
	}
	if err := app.postOpenAnswer(row.ID, "settled", "The result is final."); err != nil {
		t.Fatal(err)
	}
	held, err := app.store.GetOpenAnswer(ctx, row.ID)
	if err != nil || held.State != "open" || held.Posts != 0 ||
		held.HeldTransition != "settled" || held.HeldText != "The result is final." {
		t.Fatalf("held answer = %+v, %v", held, err)
	}
	if jobs := openAnswerCheckJobs(t, app, row.ID); len(jobs) != 0 {
		t.Fatalf("quiet-hour hold retained check jobs: %+v", jobs)
	}
	items, err := app.store.LoadStreamItems(ctx, row.ChatSessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.RunID == "open-answer:"+row.ID {
			t.Fatal("held answer was posted during quiet hours")
		}
	}

	app.recoverOpenAnswerChecks()
	stillHeld, err := app.store.GetOpenAnswer(ctx, row.ID)
	if err != nil || stillHeld.HeldTransition != "settled" || stillHeld.State != "open" {
		t.Fatalf("startup recovery released during quiet hours: %+v, %v", stillHeld, err)
	}
	if jobs := openAnswerCheckJobs(t, app, row.ID); len(jobs) != 0 {
		t.Fatalf("startup recovery recreated a held check: %+v", jobs)
	}

	app.engagement.mu.Lock()
	app.engagement.prefs.Quiet = engagement.QuietWindow{}
	app.engagement.mu.Unlock()
	restarted := &App{
		store: app.store, engagement: app.engagement, cronScheduler: app.cronScheduler,
		mobileBriefSvc: app.mobileBriefSvc, eventBroadcaster: NewEventBroadcaster(),
	}
	restarted.recoverOpenAnswerChecks()
	released, err := app.store.GetOpenAnswer(ctx, row.ID)
	if err != nil || released.State != "settled" || released.Posts != 1 ||
		released.HeldTransition != "" || released.HeldText != "" || released.HeldAt != 0 {
		t.Fatalf("restarted recovery did not release hold: %+v, %v", released, err)
	}
	if jobs := openAnswerCheckJobs(t, app, row.ID); len(jobs) != 0 {
		t.Fatalf("released answer retained a check: %+v", jobs)
	}
}

func TestOpenAnswerNamedWindowStartingInQuietPostsImmediately(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	app, _, _ := openAnswerQuietTestApp(t, openAnswerQuietAround(now, 30*time.Minute))
	row := createOpenAnswerCheckFixture(t, app, "quiet-window-exception", now, now.Add(10*time.Minute))
	if err := app.postOpenAnswer(row.ID, "settled", "The result is final."); err != nil {
		t.Fatal(err)
	}
	posted, err := app.store.GetOpenAnswer(ctx, row.ID)
	if err != nil || posted.State != "settled" || posted.Posts != 1 || posted.HeldTransition != "" {
		t.Fatalf("named quiet-window exception = %+v, %v", posted, err)
	}
}

func TestOpenAnswerHoldTimerReleasesAtQuietEnd(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	offset := time.Duration(now.Hour())*time.Hour + time.Duration(now.Minute())*time.Minute +
		time.Duration(now.Second())*time.Second + time.Duration(now.Nanosecond())
	if offset < 5*time.Minute || offset+1500*time.Millisecond >= 24*time.Hour {
		t.Skip("the test clock is too close to midnight for a short quiet window")
	}
	app, _, _ := openAnswerQuietTestApp(t, openAnswerQuietEndingSoon(now, time.Second))
	row := createOpenAnswerCheckFixture(t, app, "quiet-timer", now)
	if err := app.postOpenAnswer(row.ID, "settled", "The result is final."); err != nil {
		t.Fatal(err)
	}
	held, err := app.store.GetOpenAnswer(context.Background(), row.ID)
	if err != nil || held.HeldTransition != "settled" {
		t.Fatalf("timer fixture was not held: %+v, %v", held, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		released, err := app.store.GetOpenAnswer(context.Background(), row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if released.State == "settled" && released.Posts == 1 {
			if released.HeldTransition != "" || released.HeldText != "" || released.HeldAt != 0 {
				t.Fatalf("timer release retained held fields: %+v", released)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("quiet-end timer did not release held post")
}

func TestOpenAnswerHoldReleasesAtBriefStartBeforeGather(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	clock := &fakeMobileBriefClock{now: now}
	briefs, _ := newTestMobileBriefService(t, clock)
	app := briefs.app
	app.eventBroadcaster = NewEventBroadcaster()
	rig := newEngagementTestRig(t)
	rig.svc.mu.Lock()
	rig.svc.prefs.Quiet = openAnswerQuietAround(now, 30*time.Minute)
	rig.svc.mu.Unlock()
	app.engagement = rig.svc
	app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	app.mobileBridgeMu.Lock()
	app.mobileBriefSvc = briefs
	app.mobileBridgeMu.Unlock()
	row := createOpenAnswerCheckFixture(t, app, "quiet-brief", now)
	if err := app.postOpenAnswer(row.ID, "settled", "The result is final."); err != nil {
		t.Fatal(err)
	}

	gather := briefs.gather
	sawReleased := false
	briefs.gather = func(ctx context.Context, slot string, windowStart, asOf time.Time) (mobileBriefInput, error) {
		current, err := app.store.GetOpenAnswer(ctx, row.ID)
		if err != nil {
			return mobileBriefInput{}, err
		}
		sawReleased = current.State == "settled" && current.Posts == 1 && current.HeldTransition == ""
		return gather(ctx, slot, windowStart, asOf)
	}
	if _, err := briefs.generateWithMetadata(ctx, mobileBriefSlotOnRequest, "quiet-hold-test", nil); err != nil {
		t.Fatalf("generate brief: %v", err)
	}
	if !sawReleased {
		t.Fatal("brief gather ran before the quiet-hour hold was released")
	}
}

func TestOpenAnswerQuietHoldReleaseRechecksPrivacyGates(t *testing.T) {
	for _, gate := range []string{"Closed Eyes", "muted kind"} {
		t.Run(gate, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().Truncate(time.Millisecond)
			app, _, _ := openAnswerQuietTestApp(t, openAnswerQuietAround(now, 30*time.Minute))
			row := createOpenAnswerCheckFixture(t, app, "quiet-gate-"+gate, now)
			if err := app.postOpenAnswer(row.ID, "settled", "The result is final."); err != nil {
				t.Fatal(err)
			}
			if gate == "Closed Eyes" {
				app.closedEyes.enabled.Store(true)
			} else if err := app.store.SetOpenAnswerKindMuted(ctx, row.Topic, true); err != nil {
				t.Fatal(err)
			}
			app.engagement.mu.Lock()
			app.engagement.prefs.Quiet = engagement.QuietWindow{}
			app.engagement.mu.Unlock()
			app.releaseOpenAnswerHolds(ctx, now.Add(time.Hour), false)
			current, err := app.store.GetOpenAnswer(ctx, row.ID)
			if err != nil || current.State != "open" || current.Posts != 0 || current.HeldTransition != "settled" {
				t.Fatalf("privacy gate released held post: %+v, %v", current, err)
			}
		})
	}
}

func TestReleasedOpenAnswerNotifiesOnlyWhenBriefScheduleIsDisabled(t *testing.T) {
	for _, tc := range []struct {
		name          string
		briefsEnabled bool
		wantMoments   int
	}{
		{name: "enabled", briefsEnabled: true, wantMoments: 0},
		{name: "disabled", briefsEnabled: false, wantMoments: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().Truncate(time.Millisecond)
			app, rig, briefs := openAnswerQuietTestApp(t, openAnswerQuietAround(now, 30*time.Minute))
			row := createOpenAnswerCheckFixture(t, app, "quiet-notify-"+tc.name, now)
			if err := app.postOpenAnswer(row.ID, "settled", "The result is final."); err != nil {
				t.Fatal(err)
			}
			if !tc.briefsEnabled {
				settings := briefs.config.load()
				settings.Enabled = false
				if err := briefs.config.save(settings); err != nil {
					t.Fatal(err)
				}
			}
			app.engagement.mu.Lock()
			app.engagement.prefs.Quiet = engagement.QuietWindow{}
			app.engagement.mu.Unlock()
			app.releaseOpenAnswerHolds(ctx, now.Add(time.Hour), false)
			moments, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStatePending, 0)
			if err != nil || len(moments) != tc.wantMoments {
				t.Fatalf("released answer moments = %+v, %v; want %d", moments, err, tc.wantMoments)
			}
		})
	}
}
