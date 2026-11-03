package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func TestTodayChatCalendarEveningUsesWholeDayAndTomorrow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		day      int
		hour     int
		upcoming bool
	}{
		{name: "earlier today before last brief", hour: 9},
		{name: "tomorrow morning", day: 1, hour: 9, upcoming: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, store := newLinkTestApp(t)
			day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local)
			now := day.Add(18 * time.Hour)
			event := prepEvent("selected", "Atlas review", day.AddDate(0, 0, tc.day).Add(time.Duration(tc.hour)*time.Hour), 30, "Zoom", 1)
			event.Attendees = []calendar.Attendee{{Name: "Tim", Email: "tim@example.com"}}
			storeEvent(t, store, event)
			in := mobileBriefInput{Slot: mobileBriefSlotEvening, WindowStart: day.Add(15 * time.Hour), AsOf: now, Location: time.Local, ClockLayout: "15:04"}
			collectMobileBriefCalendarContext(context.Background(), app, &in)
			if len(in.Meetings) != 1 || in.Meetings[0].SourceID != event.ID {
				t.Fatalf("collector did not produce the selected evening card: %+v", in.Meetings)
			}

			// Test the stored-source read after authorization, without depending on
			// the test process having native Calendar access on the host computer.
			candidate, prefill, found := app.currentTodayCalendarCandidateWithPrefill(context.Background(), event.ID, in)
			if !found {
				t.Fatal("Today chat rejected a meeting included in the evening brief")
			}
			if !reflect.DeepEqual(candidate, in.Meetings[0]) {
				t.Fatalf("draft candidate = %+v, collector = %+v", candidate, in.Meetings[0])
			}
			if prefill.Kind != "meeting" || !reflect.DeepEqual(prefill.People, []string{"Tim"}) {
				t.Fatalf("prefill lost exact source metadata: %+v", prefill)
			}
			if tc.upcoming {
				if prefill.StartsAt != event.Start.UnixMilli() || prefill.OccurredAt != 0 {
					t.Fatalf("upcoming prefill = %+v", prefill)
				}
			} else if prefill.StartsAt != 0 || prefill.OccurredAt != event.Start.UnixMilli() || strings.Contains(candidate.Detail, "09:00") {
				t.Fatalf("past meeting exposed a clock time: candidate=%+v prefill=%+v", candidate, prefill)
			}
		})
	}
}

func TestTodayChatCalendarWindowMatchesCollectorEligibility(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// Tomorrow includes the autumn clock change; use local dates, not 48 hours.
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)
	for _, tc := range []struct {
		name   string
		slot   string
		start  time.Time
		end    time.Time
		status string
		mine   string
		want   bool
	}{
		{name: "midnight start", slot: mobileBriefSlotEvening, start: day, end: day.Add(time.Hour), want: true},
		{name: "overlaps day start", slot: mobileBriefSlotEvening, start: day.Add(-time.Hour), end: day.Add(time.Hour), want: true},
		{name: "ends at day start", slot: mobileBriefSlotEvening, start: day.Add(-time.Hour), end: day},
		{name: "tomorrow late after clock change", slot: mobileBriefSlotEvening, start: day.AddDate(0, 0, 1).Add(24 * time.Hour), end: day.AddDate(0, 0, 2), want: true},
		{name: "starts after tomorrow", slot: mobileBriefSlotEvening, start: day.AddDate(0, 0, 2), end: day.AddDate(0, 0, 2).Add(time.Hour)},
		{name: "morning excludes tomorrow", slot: mobileBriefSlotMorning, start: day.AddDate(0, 0, 1), end: day.AddDate(0, 0, 1).Add(time.Hour)},
		{name: "canceled", slot: mobileBriefSlotEvening, start: day.Add(19 * time.Hour), end: day.Add(20 * time.Hour), status: "canceled"},
		{name: "declined", slot: mobileBriefSlotEvening, start: day.Add(19 * time.Hour), end: day.Add(20 * time.Hour), mine: calendar.StatusDeclined},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, store := newLinkTestApp(t)
			event := prepEvent("selected", "Atlas review", tc.start, 30, "Zoom", 1)
			event.End, event.Status, event.MyStatus = tc.end, tc.status, tc.mine
			storeEvent(t, store, event)
			in := mobileBriefInput{Slot: tc.slot, WindowStart: day.Add(15 * time.Hour), AsOf: day.Add(18 * time.Hour), Location: loc}
			collectMobileBriefCalendarContext(context.Background(), app, &in)
			collected := false
			for _, candidate := range in.Meetings {
				if candidate.SourceID == event.ID {
					collected = true
				}
			}
			_, _, found := app.currentTodayCalendarCandidateWithPrefill(context.Background(), event.ID, in)
			if found != tc.want || found != collected {
				t.Fatalf("resolved=%v, collected=%v, want=%v", found, collected, tc.want)
			}
		})
	}
}
