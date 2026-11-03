package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

// Today-review v1.1.10 restores the pending slot while retaining a link to
// the last brief and the run's retry details.
func TestTodayHidesAnEarlierSlotsBriefWhileThisSlotsIsMissing(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(18, 30).AddDate(0, 0, -1)}
	svc, _ := missedBriefRig(t, clock)
	app := svc.app
	app.mobileBriefSvc = svc
	svc.app = app
	previous := todayBriefPermissionStatus
	todayBriefPermissionStatus = func(*App) PlatformPermissions { return PlatformPermissions{} }
	t.Cleanup(func() { todayBriefPermissionStatus = previous })
	ctx := context.Background()
	earlier, err := svc.generate(ctx, mobileBriefSlotOnRequest, mobileBriefReasonSettings)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(mobileBriefTestDay(7, 5))
	svc.gather = func(context.Context, string, time.Time, time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{}, context.DeadlineExceeded
	}
	svc.checkDue(ctx)
	view, err := app.GetLatestBrief()
	if err != nil {
		t.Fatal(err)
	}
	if view.Brief != nil || view.LastBriefID != earlier.BriefID || view.LastAt == "" {
		t.Fatalf("pending slot should link to the last brief: %+v", view)
	}
	if view.PendingSlot != "morning" || view.PendingAttempts != 1 || view.RetryAt == "" {
		t.Fatalf("pending = %q attempts = %d retry = %q", view.PendingSlot, view.PendingAttempts, view.RetryAt)
	}
	// The earlier brief still opens by its id (the panel, Chat's card).
	byID, err := app.GetTodayBriefByID(earlier.BriefID)
	if err != nil || byID.Brief == nil || byID.Brief.BriefID != earlier.BriefID {
		t.Fatalf("by id = %+v, %v", byID.Brief, err)
	}
}

// With no failed try on record the pending view carries no count.
func TestPendingAttemptsComeOnlyFromTheRunRecord(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 5)}
	svc, _ := missedBriefRig(t, clock)
	if health := svc.Health(); health.PendingSlot != "morning" || health.PendingAttempts != 0 {
		t.Fatalf("before any try: %+v", health)
	}
	svc.gather = func(context.Context, string, time.Time, time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{}, context.DeadlineExceeded
	}
	for i := 1; i <= 2; i++ {
		svc.checkDue(context.Background())
		if health := svc.Health(); health.PendingAttempts != i {
			t.Fatalf("after %d failures attempts = %d", i, health.PendingAttempts)
		}
		clock.Advance(mobileBriefRetryBackoff(i))
	}
}

// tasks-review v1.0.8 #1: a failed run waiting for its retry made no brief,
// so the row says it didn't run, retry or not.
func TestBriefRowSaysDidntRunWhileAFailedRunWaitsForItsRetry(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	failed := mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: "2026-10-09T07:00:00Z", Status: mobileBriefRunFailed, At: "2026-10-09T08:07:00Z", Attempts: 6}
	state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{failed}}
	if got := upcomingBriefItem(mobileBriefTestDay(8, 10), time.UTC, state, "morning").DidntRunAt; got != mobileBriefTestDay(7, 0).UnixMilli() {
		t.Fatalf("didntRunAt = %d, want the 7:00 run", got)
	}
	state.Exists = func(id string) bool { return id == failed.BriefID }
	if got := upcomingBriefItem(mobileBriefTestDay(8, 10), time.UTC, state, "morning").DidntRunAt; got != 0 {
		t.Fatalf("a stored brief still says didn't run: %d", got)
	}
}

// tasks-review v1.0.8 #2: today's row comes first with what the record
// proves: the failed tries and the next try.
func TestBriefDetailListsTodaysFailedRunWithItsTriesAndNextTry(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	yesterday := mobileBriefRun{BriefID: "2026-10-08-morning", Slot: "morning", ScheduledAt: "2026-10-08T07:00:00Z", Status: mobileBriefRunOK, At: "2026-10-08T07:00:10Z"}
	failed := mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: "2026-10-09T07:00:00Z", Status: mobileBriefRunFailed, At: "2026-10-09T08:07:00Z", Attempts: 6}
	retry := mobileBriefTestDay(8, 37)
	state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{yesterday, failed},
		NextRun: func(time.Time, string) time.Time { return retry }}
	runs := briefScheduleDetailFrom(mobileBriefTestDay(8, 10), time.UTC, state, "morning").Runs
	if len(runs) != 2 || runs[0].BriefID != failed.BriefID || runs[0].Status != mobileBriefRunFailed || runs[0].Attempts != 6 || runs[0].NextTryAt != formatMobileBriefTime(retry) {
		t.Fatalf("runs = %+v", runs)
	}
	// Tomorrow's run is not this slot's next try.
	state.NextRun = func(time.Time, string) time.Time { return mobileBriefTestDay(7, 0).AddDate(0, 0, 1) }
	if runs := briefScheduleDetailFrom(mobileBriefTestDay(8, 10), time.UTC, state, "morning").Runs; runs[0].NextTryAt != "" {
		t.Fatalf("next try = %q, want none", runs[0].NextTryAt)
	}
}

// today-review v1.1.5 #6: the brief names a meeting the way people do, with
// §9.1's title cleanup, and never asks about something a person can't place.
func TestBriefNamesMeetingsByTheirCleanedNames(t *testing.T) {
	oneToOne := calendar.Event{Title: "Priya and Alex: recurring tuesdays 1:1", Attendees: []calendar.Attendee{{Name: "Priya"}, {Name: "Alex", IsMe: true}}}
	if got := mobileBriefMeetingName(oneToOne); got != "1:1 with Priya" {
		t.Fatalf("1:1 = %q", got)
	}
	group := calendar.Event{Title: "Isoform, NMG Weekly Check In", Attendees: []calendar.Attendee{{Name: "Ann"}, {Name: "Raj"}, {Name: "Alex", IsMe: true}}}
	if got := mobileBriefMeetingName(group); got != "NMG Check In" {
		t.Fatalf("group = %q", got)
	}
	brief := MobileBriefV1{AsOf: "2026-10-30T17:50:00Z", Timezone: "UTC"}
	item := MobileBriefItem{Kind: mobileBriefAttendanceSource, Status: "unknown"}
	en := mobileBriefTemplateItem(brief, item, "Review", "1:1 with Priya", "en", "en")
	if !strings.Contains(en, "your 1:1 with Priya") || strings.Contains(en, "device") || strings.Contains(en, "recurring tuesdays") {
		t.Fatalf("en = %q", en)
	}
	zh := mobileBriefTemplateItem(brief, item, "Review", "1:1 with Priya", "zh", "zh-CN")
	if !strings.Contains(zh, "1:1 with Priya") || strings.Contains(zh, "设备") {
		t.Fatalf("zh = %q", zh)
	}
}
