package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestTodayBriefMissingNamesWhatIsoformCannotSee(t *testing.T) {
	perms := PlatformPermissions{Items: []PermissionItem{
		{Key: "screen_recording", Granted: false},
		{Key: "accessibility", Granted: true},
		{Key: "calendars", Granted: true},
		{Key: "microphone", Granted: false},
	}}
	switchOff := func(key string) bool { return key != "calendars" }
	got := todayBriefMissing(perms, switchOff)
	if want := []string{"screen_recording", "calendars"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing = %v, want %v (microphone is not a brief source)", got, want)
	}
	if got := todayBriefMissing(PlatformPermissions{}, nil); len(got) != 0 {
		t.Fatalf("a platform without these permissions reported %v", got)
	}
}

func TestTodayBriefViewShowsOnlyTodaysBrief(t *testing.T) {
	now := time.Now()
	today := now.In(time.Local).Format("2006-01-02")
	health := MobileBriefHealth{Enabled: true, NextRunAt: "2026-10-16T07:00:00Z", NextSlot: "morning"}

	full := MobileBriefV1{BriefID: today + "-morning", LocalDate: today, Sections: MobileBriefSections{
		Meetings: []MobileBriefItem{{ID: "meetings-1", Title: "Design review"}},
	}}
	view := todayBriefView(now, time.Local, full, true, health, false, nil)
	if view.Brief == nil || view.Brief.BriefID != full.BriefID || view.Empty {
		t.Fatalf("today's brief = %+v", view)
	}
	if view.NextAt != health.NextRunAt || view.NextSlot != "morning" || !view.Enabled || view.Missing == nil {
		t.Fatalf("next brief = %+v", view)
	}

	weekOnly := MobileBriefV1{
		BriefID: today + "-evening", LocalDate: today,
		YourWeek: &MobileBriefWeek{Headline: "A quiet week."},
	}
	if view := todayBriefView(now, time.Local, weekOnly, true, health, false, nil); view.Brief == nil || view.Empty {
		t.Fatalf("a Your week brief was marked empty: %+v", view)
	}

	// An earlier day's brief stays current until today's arrives (design
	// section 1): Today shows it, and it is not marked as today's.
	yesterday := full
	yesterday.BriefID = now.In(time.Local).AddDate(0, 0, -1).Format("2006-01-02") + "-evening"
	yesterday.LocalDate = now.In(time.Local).AddDate(0, 0, -1).Format("2006-01-02")
	if view := todayBriefView(now, time.Local, yesterday, true, health, true, []string{"calendars"}); view.Brief == nil || view.Brief.BriefID != yesterday.BriefID || !view.Building || len(view.Missing) != 1 {
		t.Fatalf("the current brief from an earlier day was not shown: %+v", view)
	}

	empty := MobileBriefV1{BriefID: today + "-on_request-090000", LocalDate: today}
	if view := todayBriefView(now, time.Local, empty, true, health, false, nil); view.Brief == nil || !view.Empty {
		t.Fatalf("an empty brief was not marked empty: %+v", view)
	}
}

func TestGetLatestBriefReadsTheStoredBrief(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	app := svc.app
	app.mobileBriefSvc = svc
	svc.app = app
	previous := todayBriefPermissionStatus
	todayBriefPermissionStatus = func(*App) PlatformPermissions {
		return PlatformPermissions{Items: []PermissionItem{{Key: "screen_recording", Granted: false}, {Key: "accessibility", Granted: true}}}
	}
	t.Cleanup(func() { todayBriefPermissionStatus = previous })

	view, err := app.GetLatestBrief()
	if err != nil || view.Brief != nil {
		t.Fatalf("before any brief: %+v, %v", view, err)
	}
	if !reflect.DeepEqual(view.Missing, []string{"screen_recording"}) {
		t.Fatalf("missing = %v", view.Missing)
	}

	if _, err := svc.generate(context.Background(), mobileBriefSlotOnRequest, mobileBriefReasonSettings); err != nil {
		t.Fatal(err)
	}
	view, err = app.GetLatestBrief()
	if err != nil || view.Brief == nil || view.Brief.Slot != mobileBriefSlotOnRequest || view.Empty {
		t.Fatalf("after a brief: %+v, %v", view, err)
	}
}

func TestTodayKeepsWrittenBriefAfterEmptyRequest(t *testing.T) {
	app, svc, older := keptTextFixture(t, "safe-recall")
	now := svc.now().Add(time.Hour)
	svc.now = func() time.Time { return now }
	empty := buildMobileBriefV1(mobileBriefInput{AsOf: now.Add(-time.Minute), Slot: mobileBriefSlotOnRequest, Location: time.UTC})
	empty.NarrativeStatus = "skipped"
	if err := svc.store.save(empty, now); err != nil {
		t.Fatal(err)
	}
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil || view.Brief.BriefID != older.BriefID || !view.NothingNew {
		t.Fatalf("Today should keep the written brief after an empty request: %+v, %v", view, err)
	}
	failed := buildMobileBriefV1(mobileBriefInput{AsOf: now, Slot: mobileBriefSlotOnRequest, Location: time.UTC})
	failed.NarrativeStatus = "unavailable"
	if err := svc.store.save(failed, now); err != nil {
		t.Fatal(err)
	}
	afterFailure, err := app.GetLatestBrief()
	if err != nil || afterFailure.Brief == nil || afterFailure.Brief.BriefID != older.BriefID || afterFailure.NothingNew {
		t.Fatalf("a later failed request must not claim nothing new: %+v, %v", afterFailure, err)
	}
	menu, ok := app.menuBarLatestBrief(svc)
	if !ok || menu.BriefID != view.Brief.BriefID {
		t.Fatalf("menu brief = %s, Today = %s", menu.BriefID, view.Brief.BriefID)
	}
}

func TestTodayV110DoesNotShowQuietSlotAsFirstBrief(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.mobileBriefSvc = svc
	quiet := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 9)
	quiet.NarrativeStatus = "quiet"
	if err := svc.store.save(quiet, clock.Now()); err != nil {
		t.Fatal(err)
	}
	view, err := svc.app.GetLatestBrief()
	if err != nil || view.Brief != nil || view.Quiet {
		t.Fatalf("quiet slot should not replace the first-brief state: %+v, %v", view, err)
	}
}

func TestTodayV110SlotBoundaryKeepsLastBriefOnlyBeforeDue(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 10, 9, 17, 59, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.mobileBriefSvc = svc
	morning := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 9)
	morning.AsOf = "2026-10-09T09:23:00Z"
	if err := svc.store.save(morning, clock.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := svc.app.GetLatestBrief()
	if err != nil || before.Brief == nil || before.Brief.BriefID != morning.BriefID {
		t.Fatalf("before slot: %+v, %v", before, err)
	}
	clock.Set(time.Date(2026, 10, 9, 18, 0, 0, 0, time.UTC))
	after, err := svc.app.GetLatestBrief()
	if err != nil || after.Brief != nil || after.PendingSlot != "evening" || after.LastBriefID != morning.BriefID {
		t.Fatalf("at slot: %+v, %v", after, err)
	}
	clock.Set(time.Date(2026, 10, 9, 18, 2, 0, 0, time.UTC))
	evening := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotEvening, 18)
	evening.AsOf = "2026-10-09T18:02:00Z"
	if err := svc.store.save(evening, clock.Now()); err != nil {
		t.Fatal(err)
	}
	ready, err := svc.app.GetLatestBrief()
	if err != nil || ready.Brief == nil || ready.Brief.BriefID != evening.BriefID || ready.PendingSlot != "" {
		t.Fatalf("after brief ready: %+v, %v", ready, err)
	}
}

func TestGetTodayBriefByIDSelectsStoredBriefAndFallsBackWhenMissing(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	app := svc.app
	app.mobileBriefSvc = svc
	svc.app = app

	previous := todayBriefPermissionStatus
	todayBriefPermissionStatus = func(*App) PlatformPermissions {
		return PlatformPermissions{Items: []PermissionItem{
			{Key: "screen_recording", Granted: true},
			{Key: "accessibility", Granted: true},
			{Key: "calendars", Granted: true},
		}}
	}
	t.Cleanup(func() { todayBriefPermissionStatus = previous })

	source := MobileBriefV1{
		BriefID: "2026-10-14-evening", LocalDate: "2026-10-14", Slot: mobileBriefSlotEvening,
		AsOf: "2026-10-14T18:00:00Z", NarrativeStatus: "ok",
		YourWeek: &MobileBriefWeek{Start: "2026-10-12", End: "2026-10-14", Headline: "The week in review."},
	}
	if err := svc.store.save(source, clock.Now()); err != nil {
		t.Fatal(err)
	}
	latest, err := svc.generate(context.Background(), mobileBriefSlotOnRequest, mobileBriefReasonSettings)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := app.GetTodayBriefByID(source.BriefID)
	if err != nil || selected.Brief == nil || selected.Brief.BriefID != source.BriefID || selected.Brief.LocalDate != source.LocalDate {
		t.Fatalf("selected source brief = %+v, %v", selected, err)
	}
	fallback, err := app.GetTodayBriefByID("2026-10-14-morning")
	if err != nil || fallback.Brief == nil || fallback.Brief.BriefID != latest.BriefID {
		t.Fatalf("missing source fallback = %+v, %v; want latest %q", fallback, err, latest.BriefID)
	}
}

// A brief made in California after 17:00 is already tomorrow in UTC. Its
// local date and Today's "today" must both be California's, on either side
// of the UTC date line.
func TestTodaysBriefSurvivesTheUTCDateLine(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database")
	}
	for _, at := range []time.Time{
		time.Date(2026, 10, 15, 16, 30, 0, 0, la), // 23:30 UTC, same date
		time.Date(2026, 10, 15, 18, 0, 0, 0, la),  // 01:00 UTC on the 16th
		time.Date(2026, 10, 15, 23, 59, 0, 0, la), // 06:59 UTC on the 16th
	} {
		clock := &fakeMobileBriefClock{now: at}
		svc, _ := newTestMobileBriefService(t, clock)
		svc.location = la // the gather still says UTC, as an old source might
		app := &App{mobileBriefSvc: svc}
		svc.app = app
		brief, err := svc.generate(context.Background(), mobileBriefSlotEvening, mobileBriefReasonSettings)
		if err != nil {
			t.Fatal(err)
		}
		if brief.LocalDate != "2026-10-15" {
			t.Fatalf("at %s the brief is dated %s, want 2026-10-15", at.Format(time.RFC3339), brief.LocalDate)
		}
		view, err := app.GetLatestBrief()
		if err != nil || view.Brief == nil {
			t.Fatalf("at %s Today hid the brief: %+v, %v", at.Format(time.RFC3339), view, err)
		}
	}
}

// The production path: the real service and the real gather stamp the
// date in the Mac's zone.
func TestTheRealServiceDatesTheBriefInTheMacsZone(t *testing.T) {
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	app := &App{}
	svc := newMobileBriefService(app)
	svc.app = app
	svc.power = func(string) (func(), error) { return func() {}, nil }
	svc.speaker = mobileBriefSpeaker{}
	evening := time.Date(2026, 10, 15, 18, 0, 0, 0, time.Local)
	svc.now = func() time.Time { return evening }
	app.mobileBriefSvc = svc
	brief, err := svc.generate(context.Background(), mobileBriefSlotEvening, mobileBriefReasonSettings)
	if err != nil {
		t.Fatal(err)
	}
	if brief.LocalDate != "2026-10-15" {
		t.Fatalf("the evening brief is dated %s, want the Mac's 2026-10-15", brief.LocalDate)
	}
	if view, err := app.GetLatestBrief(); err != nil || view.Brief == nil {
		t.Fatalf("Today hid the evening brief: %+v, %v", view, err)
	}
}

// Launched 20 minutes after the morning slot, the first due check builds it once.
func TestMobileBriefLaunchBuildsTheMorningWithinGraceOnce(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 20)}
	svc, gathers := newTestMobileBriefService(t, clock)
	svc.runs.firstSeen(mobileBriefTestDay(0, 0))
	ctx := context.Background()

	svc.checkDue(ctx)
	if !svc.store.exists("2026-10-09-morning") || gathers.Load() != 1 {
		t.Fatalf("the morning brief was not built at launch (gathers %d)", gathers.Load())
	}
	// The due check that follows finds nothing missed or due.
	svc.checkDue(ctx)
	if got := gathers.Load(); got != 1 {
		t.Fatalf("gather ran %d times, want once", got)
	}
	var morning *mobileBriefRun
	for _, run := range svc.runs.list() {
		if run.BriefID == "2026-10-09-morning" {
			r := run
			morning = &r
		}
	}
	if morning == nil || morning.Status != mobileBriefRunOK {
		t.Fatalf("the caught-up slot was recorded as %+v", morning)
	}
	if health := svc.Health(); health.MissedRuns != 0 {
		t.Fatalf("a caught-up slot counted as missed: %+v", health)
	}
}

func TestMobileBriefLateSlotSkipsWhenABriefCameLater(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(13, 0)}
	svc, gathers := newTestMobileBriefService(t, clock)
	svc.runs.firstSeen(mobileBriefTestDay(0, 0))
	if _, err := svc.generate(context.Background(), mobileBriefSlotOnRequest, mobileBriefReasonSettings); err != nil {
		t.Fatal(err)
	}
	clock.Set(mobileBriefTestDay(14, 0))
	svc.checkDue(context.Background())
	if got := gathers.Load(); got != 1 {
		t.Fatalf("an on-request brief after the slot did not count: %d gathers", got)
	}
}

func TestMobileBriefPatchLatestKeepsTheSpokenVersionSafe(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(8, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatal(err)
	}
	patched, ok := svc.PatchLatestMobileBrief(func(b MobileBriefV1) (MobileBriefV1, bool) {
		b.Sections.Watching = append(b.Sections.Watching, MobileBriefItem{ID: "watching-find-1", Title: "Wails 2.12.1", Kind: "find", Refs: []string{"e1"}})
		return b, true
	}, nil)
	if !ok || len(patched.Sections.Watching) != 1 {
		t.Fatalf("patch = %+v (ok=%v)", patched, ok)
	}
	stored, found := svc.GetMobileBrief(brief.BriefID)
	if !found || len(stored.Sections.Watching) != 1 || stored.AsOf != brief.AsOf {
		t.Fatalf("stored = %+v", stored)
	}
	if _, ok := svc.PatchLatestMobileBrief(func(b MobileBriefV1) (MobileBriefV1, bool) { return b, false }, nil); ok {
		t.Fatal("a patch that changed nothing reported a change")
	}
}

// Today can read before the brief service has started: it then reads the
// stored brief and schedule from disk instead of saying briefs are off.
func TestTodayReadsTheStoredBriefBeforeTheServiceStarts(t *testing.T) {
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	clock := &fakeMobileBriefClock{now: time.Now()}
	svc := newMobileBriefService(&App{})
	svc.now = clock.Now
	svc.location = time.Local
	svc.power = func(string) (func(), error) { return func() {}, nil }
	svc.speaker = mobileBriefSpeaker{}
	svc.gather = func(_ context.Context, slot string, windowStart, asOf time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{Slot: slot, WindowStart: windowStart, AsOf: asOf, Location: time.Local,
			Moved: []mobileBriefCandidate{{SourceKind: "history", SourceID: "h-1", Title: "Shipped the plan", At: asOf.Add(-time.Hour)}}}, nil
	}
	if _, err := svc.generate(context.Background(), mobileBriefSlotOnRequest, mobileBriefReasonSettings); err != nil {
		t.Fatal(err)
	}
	previous := todayBriefPermissionStatus
	todayBriefPermissionStatus = func(*App) PlatformPermissions { return PlatformPermissions{} }
	t.Cleanup(func() { todayBriefPermissionStatus = previous })

	view, err := (&App{}).GetLatestBrief()
	if err != nil || view.Brief == nil || !view.Enabled {
		t.Fatalf("before the service started Today read %+v, %v; want the stored brief with briefs on", view, err)
	}
}

// A due brief that has not arrived keeps the current brief on screen and says
// it is on its way (design section 2: "will come when your Mac wakes").
func TestTodayBriefViewCarriesTheBriefOnItsWay(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 15, 0, 0, time.UTC)
	health := MobileBriefHealth{Enabled: true, PendingSlot: "morning", PendingAt: "2026-10-27T07:00:00Z", RetryAt: "2026-10-27T10:20:00Z",
		LatestBriefID: "2026-10-26-evening", LastSlot: "evening", LastRunAt: "2026-10-26T19:30:00Z"}
	yesterday := MobileBriefV1{BriefID: "2026-10-26-evening", Slot: "evening", LocalDate: "2026-10-26"}
	view := todayBriefView(now, time.UTC, yesterday, true, health, false, nil)
	if view.Brief == nil || view.Brief.BriefID != yesterday.BriefID || view.PendingSlot != "morning" || view.PendingAt != health.PendingAt || view.RetryAt != health.RetryAt {
		t.Fatalf("the current brief or the pending brief lost: %+v", view)
	}
	// The shown brief is the newest stored one, so there is no other last brief to offer.
	if view.LastBriefID != "" {
		t.Fatalf("the open brief is offered again: %+v", view)
	}
	// A brief older than the newest stored one is offered as the last brief.
	older := MobileBriefV1{BriefID: "2026-10-25-evening", Slot: "evening", LocalDate: "2026-10-25"}
	if view := todayBriefView(now, time.UTC, older, true, health, false, nil); view.LastBriefID != "2026-10-26-evening" || view.LastSlot != "evening" || view.LastAt != health.LastRunAt {
		t.Fatalf("the last brief is not offered: %+v", view)
	}
}
