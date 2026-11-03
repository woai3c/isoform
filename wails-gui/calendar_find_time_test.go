package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestFindTimeWindowsExcludesBusyAndQuiet(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 12, 0, 0, time.UTC)
	events := []calendar.Event{
		{Start: now.Add(2*time.Hour - 12*time.Minute), End: now.Add(3*time.Hour - 12*time.Minute)},
		{Start: now.Add(4*time.Hour - 12*time.Minute), End: now.Add(5*time.Hour - 12*time.Minute), Availability: "free"},
	}
	quiet := engagement.QuietWindow{Start: 15 * time.Hour, End: 17 * time.Hour}
	got, err := findTimeWindows(events, now, quiet)
	if err != nil || len(got) != 3 || got[0].Start != "2026-10-30T09:30:00Z" || got[0].End != "2026-10-30T11:00:00Z" ||
		got[1].Start != "2026-10-30T12:00:00Z" || got[1].End != "2026-10-30T15:00:00Z" ||
		got[2].Start != "2026-10-30T17:00:00Z" {
		t.Fatalf("free windows=%+v err=%v", got, err)
	}
}

func TestFindTimeReadsCachedEventsOnly(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	storeEvent(t, app.store, calendar.Event{ID: "busy", Source: calendar.SourceEventKit,
		Title: "Meeting", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)})
	got, err := app.findTime(context.Background(), now)
	if err != nil || len(got) < 2 || got[0].Start != "2026-10-30T09:00:00Z" || got[0].End != "2026-10-30T10:00:00Z" ||
		got[1].Start != "2026-10-30T11:00:00Z" {
		t.Fatalf("free windows=%+v err=%v", got, err)
	}
}

func TestFindTimeWithholdsWhenCalendarIsUnavailable(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if ranges, err := app.findTime(context.Background(), now); err == nil || len(ranges) != 0 {
		t.Fatalf("unconnected calendar gave free time: %+v %v", ranges, err)
	}
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, now.Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if ranges, err := app.findTime(context.Background(), now); err == nil || len(ranges) != 0 {
		t.Fatalf("stale calendar gave free time: %+v %v", ranges, err)
	}
}

func TestFindTimeRequiresExistingAccess(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if ranges, err := app.findTimeWithAccess(context.Background(), now, false); err == nil || len(ranges) != 0 {
		t.Fatalf("calendar permission denied: %+v %v", ranges, err)
	}
	if ranges, err := app.findTimeWithAccess(context.Background(), now, true); err != nil || len(ranges) == 0 {
		t.Fatalf("calendar permission granted: %+v %v", ranges, err)
	}
}

func TestFindTimeNamesBrowserOnlyCalendarWhileHoldsRequireCoverage(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpsertCalendarSource(context.Background(), storage.CalendarSourceRow{
		ID: "web:google", Kind: calendar.SourceKindWeb, Provider: "google", LastSeenAt: now.UnixMilli(), LastReadAt: now.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	if ranges, err := app.findTime(context.Background(), now); err == nil || len(ranges) != 0 {
		t.Fatalf("hold check accepted uncovered browser calendar: %+v %v", ranges, err)
	}
	details, err := app.findTimeDetails(context.Background(), now, true, []calendar.RawCalendar{{Title: "Work"}, {Title: "Home"}})
	if err != nil || len(details.Ranges) == 0 || len(details.CheckedCalendars) != 2 || len(details.BrowserOnly) != 1 || details.BrowserOnly[0] != "Google" {
		t.Fatalf("partial answer = %+v, %v", details, err)
	}
}

func TestMobileFindTimeRequiresBriefScope(t *testing.T) {
	bridge := &mobileBridge{}
	for _, path := range []string{"/mobile/v1/calendar/free-time", "/mobile/v1/calendar/free-time-details"} {
		recorder := httptest.NewRecorder()
		bridge.handleAuthorized(recorder, httptest.NewRequest(http.MethodGet, path, nil), mobileTestDevice())
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s unscoped response = %d", path, recorder.Code)
		}
	}
}

func TestFindTimeWindowsSkipsBusyAllDayAndWeekend(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	events := []calendar.Event{{Start: now.Add(-9 * time.Hour), End: now.Add(15 * time.Hour), AllDay: true}}
	got, err := findTimeWindows(events, now, engagement.QuietWindow{})
	if err != nil || len(got) == 0 || got[0].Start != "2026-11-02T09:00:00Z" {
		t.Fatalf("free windows=%+v err=%v", got, err)
	}
}

func TestFindTimeRangeWindowsUsesRequestedWeekAndOneBlockPerDay(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 30, 16, 0, 0, 0, loc) // Friday
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 5)
	events := []calendar.Event{
		{Start: start.Add(11 * time.Hour), End: start.Add(12 * time.Hour), Availability: "busy"},
		{Start: start.AddDate(0, 0, 1).Add(9 * time.Hour), End: start.AddDate(0, 0, 1).Add(17 * time.Hour), Availability: "busy"},
	}
	got, err := findTimeRangeWindows(events, now, start, end, time.Hour, engagement.QuietWindow{}, inferHoldWorkHours(nil, now))
	if err != nil || len(got) != 3 {
		t.Fatalf("ranges = %+v, %v", got, err)
	}
	if got[0].Start != "2026-11-02T09:00:00Z" || got[0].End != "2026-11-02T11:00:00Z" || got[1].Start != "2026-11-03T17:00:00Z" || got[2].Start != "2026-11-04T09:00:00Z" {
		t.Fatalf("unexpected ranges: %+v", got)
	}
}

func TestFindTimeRangeWindowsWithholdsShortBlocks(t *testing.T) {
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	events := []calendar.Event{{Start: day.Add(9*time.Hour + 30*time.Minute), End: day.Add(18 * time.Hour), Availability: "busy"}}
	got, err := findTimeRangeWindows(events, day, day, day.AddDate(0, 0, 1), time.Hour, engagement.QuietWindow{}, inferHoldWorkHours(nil, day))
	if err != nil || len(got) != 0 {
		t.Fatalf("ranges = %+v, %v", got, err)
	}
}

func TestFindTimeRangeRejectsDatesOutsideTheSyncedWindow(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.findTimeEvents(context.Background(), now, now.AddDate(0, 0, 15), now.AddDate(0, 0, 16), false); err == nil {
		t.Fatal("unsynced calendar window was treated as readable")
	}
}
