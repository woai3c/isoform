package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMobileBriefTimingOvernightMeetingDoesNotDelayEvening(t *testing.T) {
	for _, endClock := range []string{"00:00", "00:30", "09:00"} {
		t.Run(endClock, func(t *testing.T) {
			app, _, now := mobileBriefTimingSignalFixture(t)
			start := now
			end := briefTimingFixtureTime(t, now.Location(), now.AddDate(0, 0, 1).Format("2006-01-02"), endClock)
			ctx := context.Background()
			if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{{
				ID: "overnight", Source: "eventkit", Provider: "google", StartAt: start.UnixMilli(), EndAt: end.UnixMilli(),
				Status: "confirmed", MyStatus: "accepted", Availability: "busy",
			}}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			privacy, err := app.mcpReadPrivacy(ctx)
			if err != nil {
				t.Fatal(err)
			}
			date := now.Format("2006-01-02")
			day := &mobileBriefTimingDay{Date: date}
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			collectMobileBriefCalendarTiming(ctx, app, privacy, midnight, midnight.AddDate(0, 0, 1), true, map[string]*mobileBriefTimingDay{date: day})
			if !day.FirstMeeting.Equal(start) || !day.LastMeetingStart.Equal(start) || !day.LastMeeting.Equal(end) {
				t.Fatalf("overnight range was lost: %+v", day)
			}
			settings := mobileBriefSettings{Enabled: true, Adaptive: true, Morning: "07:00", Evening: "18:00"}
			prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(now, now.Location(), settings, mobileBriefTimingInput{Today: *day}), mobileBriefSlotEvening)
			assertBriefTimingPrediction(t, prediction, briefTimingFixtureTime(t, now.Location(), date, "18:00"), "fixed_slot")
		})
	}
}

func TestMobileBriefTimingCalendarProjectsEveryOverlappingDay(t *testing.T) {
	for _, tc := range []struct {
		name           string
		startDays      int
		endDays        int
		endClock       string
		queryStartDays int
		wantClock      string
		wantReason     string
		wantOverlap    bool
		pauseStart     bool
	}{
		{name: "ending day", startDays: -1, endClock: "18:30", queryStartDays: -14, wantClock: "18:00", wantReason: "fixed_slot", wantOverlap: true},
		{name: "ending day starts before query", startDays: -15, endClock: "18:30", queryStartDays: -14, wantClock: "18:00", wantReason: "fixed_slot", wantOverlap: true},
		{name: "intermediate day", startDays: -1, endDays: 1, endClock: "18:30", queryStartDays: -14, wantClock: "18:00", wantReason: "fixed_slot", wantOverlap: true},
		{name: "midnight end excludes ending date", startDays: -1, endClock: "00:00", queryStartDays: -14, wantClock: "18:00", wantReason: "fixed_slot"},
		{name: "original start excluded before query", startDays: -15, endClock: "18:30", queryStartDays: -14, wantClock: "18:00", wantReason: "fixed_slot", pauseStart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _, now := mobileBriefTimingSignalFixture(t)
			ctx := context.Background()
			date := now.Format("2006-01-02")
			start := briefTimingFixtureTime(t, now.Location(), now.AddDate(0, 0, tc.startDays).Format("2006-01-02"), "23:00")
			end := briefTimingFixtureTime(t, now.Location(), now.AddDate(0, 0, tc.endDays).Format("2006-01-02"), tc.endClock)
			if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{{
				ID: "spanning-meeting", Source: "eventkit", Provider: "google", StartAt: start.UnixMilli(), EndAt: end.UnixMilli(),
				Status: "confirmed", MyStatus: "accepted", Availability: "busy",
			}}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if tc.pauseStart {
				raw, err := json.Marshal([]observationPause{{Start: start.UnixMilli(), End: start.Add(time.Hour).UnixMilli()}})
				if err != nil {
					t.Fatal(err)
				}
				if err := app.store.SetUIState(ctx, observationPausesKey, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			privacy, err := app.mcpReadPrivacy(ctx)
			if err != nil {
				t.Fatal(err)
			}
			day := &mobileBriefTimingDay{Date: date}
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			collectMobileBriefCalendarTiming(ctx, app, privacy, midnight.AddDate(0, 0, tc.queryStartDays), midnight.AddDate(0, 0, 1), true, map[string]*mobileBriefTimingDay{date: day})
			if tc.wantOverlap {
				if !day.FirstMeeting.IsZero() || !day.LastMeetingStart.Equal(start) || !day.LastMeeting.Equal(end) {
					t.Errorf("overlapping day lost the original interval: %+v, want %s-%s", day, start, end)
				}
			} else if !day.FirstMeeting.IsZero() || !day.LastMeetingStart.IsZero() || !day.LastMeeting.IsZero() {
				t.Errorf("nonoverlapping date acquired meeting: %+v", day)
			}

			clock := &fakeMobileBriefClock{now: now}
			svc, gathers := newTestMobileBriefService(t, clock)
			svc.location = now.Location()
			input := mobileBriefTimingInput{Today: *day}
			enableTestAdaptiveBrief(t, svc, &input)
			prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(now, now.Location(), svc.config.load(), input), mobileBriefSlotEvening)
			want := briefTimingFixtureTime(t, now.Location(), date, tc.wantClock)
			if tc.wantReason == "fixed_slot" {
				want = want.Add(-mobileBriefBuildLead)
			}
			if !prediction.ScheduledAt.Equal(want) || prediction.Reason != tc.wantReason {
				t.Errorf("evening prediction = %s (%s), want %s (%s)", prediction.ScheduledAt, prediction.Reason, want, tc.wantReason)
			}
			svc.checkDue(ctx)
			if gathers.Load() == 0 {
				clock.Set(want)
				svc.checkDue(ctx)
			}
			if gathers.Load() != 1 || !svc.store.exists(date+"-evening") {
				t.Fatal("evening brief did not run at its configured time")
			}
		})
	}
}

func TestMobileBriefTimingCalendarKeepsFirstMeetingOnItsStartDate(t *testing.T) {
	app, _, now := mobileBriefTimingSignalFixture(t)
	ctx := context.Background()
	date := now.Format("2006-01-02")
	first := briefTimingFixtureTime(t, now.Location(), date, "09:00")
	end := first.Add(time.Hour)
	if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{
		{ID: "overnight", Source: "eventkit", StartAt: first.Add(-10 * time.Hour).UnixMilli(), EndAt: first.Add(-8*time.Hour - 30*time.Minute).UnixMilli(), Availability: "busy"},
		{ID: "morning", Source: "eventkit", StartAt: first.UnixMilli(), EndAt: end.UnixMilli(), Availability: "busy"},
	}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	privacy, err := app.mcpReadPrivacy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	day := &mobileBriefTimingDay{Date: date}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	collectMobileBriefCalendarTiming(ctx, app, privacy, midnight.AddDate(0, 0, -1), midnight.AddDate(0, 0, 1), true, map[string]*mobileBriefTimingDay{date: day})
	if !day.FirstMeeting.Equal(first) || !day.LastMeetingStart.Equal(first) || !day.LastMeeting.Equal(end) {
		t.Errorf("overnight meeting hid the day's first start: %+v", day)
	}
	settings := mobileBriefSettings{Enabled: true, Adaptive: true, Morning: "10:00", Evening: "18:00"}
	prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(now, now.Location(), settings, mobileBriefTimingInput{Today: *day}), mobileBriefSlotMorning)
	assertBriefTimingPrediction(t, prediction, first.Add(-mobileBriefTimingLead), "before_meeting")
}
