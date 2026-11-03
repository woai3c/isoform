package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMobileBriefTimingPauseExclusionsSurviveRetention(t *testing.T) {
	app, service, today := mobileBriefTimingSignalFixture(t)
	clock := today
	observer := &contextSignalService{
		store: app.store, loc: today.Location(), now: func() time.Time { return clock },
		idle: func() time.Duration { return 0 }, inputTimingSupported: true,
	}
	ctx := context.Background()
	// Exercise the persisted pause log and actual idle poll, which continues
	// maintaining day marks while Closed Eyes suppresses observation reads.
	for _, offset := range []int{-14, -10, -9, -8} {
		day := today.AddDate(0, 0, offset)
		clock = time.Date(day.Year(), day.Month(), day.Day(), 7, 0, 0, 0, day.Location())
		app.closedEyes.enabled.Store(true)
		app.recordObservationPause(true, clock.Add(-time.Minute))
		observer.pollIdle(ctx)
		clock = clock.Add(8 * time.Hour)
		observer.pollIdle(ctx)
		app.recordObservationPause(false, clock.Add(time.Minute))
		app.closedEyes.enabled.Store(false)
	}
	assertWithheld := func(stage string) {
		t.Helper()
		input := service.timingInput(today)
		for _, day := range input.Days {
			if !day.FirstActivity.IsZero() || !day.LastActivity.IsZero() {
				t.Errorf("%s: paused input re-entered history: %+v", stage, day)
			}
		}
		settings := mobileBriefSettings{Enabled: true, Morning: "10:00", Evening: "18:00"}
		prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(today, today.Location(), settings, input), mobileBriefSlotMorning)
		want := time.Date(today.Year(), today.Month(), today.Day(), 10, 0, 0, 0, today.Location())
		assertBriefTimingPrediction(t, prediction, want, "fixed_slot")
	}
	assertWithheld("before retention")
	app.recordObservationPause(true, today.Add(-time.Minute))
	app.recordObservationPause(false, today)
	assertWithheld("after retention")
}

func TestObservationPauseCompactionPreservesExclusions(t *testing.T) {
	app, service, now := mobileBriefTimingSignalFixture(t)
	start := now.AddDate(0, 0, -3)
	pauses := make([]observationPause, observationPausesMax)
	for index := range pauses {
		at := start.Add(time.Duration(index) * time.Minute)
		pauses[index] = observationPause{Start: at.UnixMilli(), End: at.Add(30 * time.Second).UnixMilli()}
	}
	app.saveObservationPauses(pauses)
	putMobileBriefTimingMarks(t, app, start, start.Add(10*time.Minute))
	app.recordObservationPause(true, now.Add(-time.Minute))
	app.recordObservationPause(false, now)
	kept, err := app.observationPauses()
	if err != nil || len(kept) > observationPausesMax {
		t.Fatalf("compaction = %d, %v", len(kept), err)
	}
	for _, pause := range pauses {
		if !pausedAt(kept, time.UnixMilli(pause.Start)) {
			t.Fatalf("compaction forgot excluded time %d", pause.Start)
		}
	}
	for _, day := range service.timingInput(now).Days {
		if !day.FirstActivity.IsZero() {
			t.Fatalf("compacted pause admitted input: %+v", day)
		}
	}
}

func TestMobileBriefTimingRejectsHistoryWithoutPrivacyCoverage(t *testing.T) {
	for _, coverage := range []string{"missing", "malformed", "future"} {
		t.Run(coverage, func(t *testing.T) {
			app, service, now := mobileBriefTimingSignalFixture(t, false)
			if coverage != "missing" {
				value := "not-a-timestamp"
				if coverage == "future" {
					value = "9999999999999"
				}
				if err := app.store.SetUIState(context.Background(), "mobile_brief_timing_privacy_since", value); err != nil {
					t.Fatal(err)
				}
			}
			// The old log has already forgotten these pauses. An empty log is
			// not proof that old marks were observed with permission.
			for offset := -10; offset <= -8; offset++ {
				first := now.AddDate(0, 0, offset).Add(-11 * time.Hour)
				putMobileBriefTimingMarks(t, app, first, first.Add(time.Hour))
			}
			for _, day := range service.timingInput(now).Days {
				if !day.FirstActivity.IsZero() || !day.LastActivity.IsZero() {
					t.Fatalf("%s coverage admitted unproven history: %+v", coverage, day)
				}
			}
		})
	}
}

func TestMobileBriefTimingPrivacyCoverageSurvivesRestart(t *testing.T) {
	app, service, start := mobileBriefTimingSignalFixture(t, false)
	service.timingInput(start)
	coverage, err := app.store.GetUIState(context.Background(), "mobile_brief_timing_privacy_since")
	if err != nil || coverage == "" {
		t.Fatalf("privacy coverage was not persisted: %q, %v", coverage, err)
	}
	for offset := -3; offset <= 3; offset++ {
		day := start.AddDate(0, 0, offset)
		hour := 7
		if offset > 0 {
			hour = 9
		}
		first := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, day.Location())
		putMobileBriefTimingMarks(t, app, first, first.Add(time.Hour))
	}
	now := start.AddDate(0, 0, 4)
	restarted := &mobileBriefService{app: app, location: service.location}
	input := restarted.timingInput(now)
	settings := mobileBriefSettings{Enabled: true, Morning: "10:00", Evening: "18:00"}
	prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(now, now.Location(), settings, input), mobileBriefSlotMorning)
	want := time.Date(now.Year(), now.Month(), now.Day(), 8, 50, 0, 0, now.Location())
	assertBriefTimingPrediction(t, prediction, want, "learned_activity")
	after, _ := app.store.GetUIState(context.Background(), "mobile_brief_timing_privacy_since")
	if after != coverage {
		t.Fatalf("restart moved privacy coverage from %q to %q", coverage, after)
	}
}

func TestMobileBriefTimingPrivacyCoverageGatesOriginalSourceTimes(t *testing.T) {
	app, service, since := mobileBriefTimingSignalFixture(t, false)
	service.timingInput(since)
	now := since.AddDate(0, 0, 1)
	ctx := context.Background()
	// A receipt on the uncovered date must not become training evidence.
	moment, err := app.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{Kind: "brief_ready", DedupeKey: "uncovered-open"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.RecordEngagementReceipt(ctx, storage.EngagementReceiptRow{
		MomentID: moment.ID, Channel: "push", State: storage.EngagementReceiptOpened, At: since.Add(-time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, day := range service.timingInput(now).Days {
		if !day.FirstOpen.IsZero() {
			t.Fatalf("uncovered open entered timing: %+v", day)
		}
	}
	// The meeting overlaps the covered date, but its real start precedes
	// coverage. Clipping it to the query would lose that provenance.
	if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{{
		ID: "uncovered-start", Source: "eventkit", Provider: "google",
		StartAt: since.Add(-time.Hour).UnixMilli(), EndAt: now.Add(time.Hour).UnixMilli(),
		Status: "confirmed", MyStatus: "accepted", Availability: "busy",
	}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	privacy, err := mobileBriefTimingPrivacy(ctx, app, now)
	if err != nil {
		t.Fatal(err)
	}
	day := &mobileBriefTimingDay{Date: now.Format("2006-01-02")}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	collectMobileBriefCalendarTiming(ctx, app, privacy, midnight, midnight.AddDate(0, 0, 1), true, map[string]*mobileBriefTimingDay{day.Date: day})
	if !day.FirstMeeting.IsZero() || !day.LastMeeting.IsZero() {
		t.Fatalf("uncovered original meeting start entered timing: %+v", day)
	}
	// Even after the first-use floor ages away, expiring exclusions cannot
	// authorize an old event with a long interval extending into today.
	later := now.Add(observationPausesKept + time.Hour)
	privacy, err = mobileBriefTimingPrivacy(ctx, app, later)
	if err != nil {
		t.Fatal(err)
	}
	if privacy.timeAllowed(now.UnixMilli(), later.UnixMilli()) {
		t.Fatal("an interval older than retained pause coverage became readable")
	}
	if !privacy.timeAllowed(later.Add(-time.Hour).UnixMilli(), later.UnixMilli()) {
		t.Fatal("covered current observations became unreadable")
	}
}
