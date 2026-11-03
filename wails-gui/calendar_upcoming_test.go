package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/storage"
)

func upcomingTestApp(t *testing.T) *App {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "yansu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{store: store}
}

func upcomingIDs(events []UpcomingCalendarEvent) []string {
	ids := []string{}
	for _, ev := range events {
		ids = append(ids, ev.ID)
	}
	return ids
}

func TestGetUpcomingCalendarEventsNextDayTimedOnly(t *testing.T) {
	app := upcomingTestApp(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	rows := []storage.CalendarEventRow{
		{ID: "ended", Title: "Ended", StartAt: ms(-3 * time.Hour), EndAt: ms(-2 * time.Hour), Source: "eventkit"},
		{ID: "now", Title: "In progress", StartAt: ms(-10 * time.Minute), EndAt: ms(20 * time.Minute), Source: "eventkit", ConferenceURL: "https://zoom.us/j/1"},
		{ID: "holiday", Title: "Holiday", StartAt: ms(-9 * time.Hour), EndAt: ms(15 * time.Hour), Source: "eventkit", AllDay: true},
		{ID: "soon", Title: "Design review", StartAt: ms(time.Hour), EndAt: ms(2 * time.Hour), Source: "eventkit", Location: "Room 4", CalendarTitle: "Work", ConferenceURL: "file:///etc/passwd"},
		{ID: "canceled", Title: "Canceled", StartAt: ms(90 * time.Minute), EndAt: ms(2 * time.Hour), Source: "eventkit", Status: "canceled"},
		{ID: "declined", Title: "Declined", StartAt: ms(100 * time.Minute), EndAt: ms(2 * time.Hour), Source: "eventkit", MyStatus: "declined"},
		{ID: "script", Title: "Odd link", StartAt: ms(5 * time.Hour), EndAt: ms(6 * time.Hour), Source: "eventkit", ConferenceURL: "javascript:alert(1)"},
		{ID: "tomorrow-late", Title: "Beyond a day", StartAt: ms(26 * time.Hour), EndAt: ms(27 * time.Hour), Source: "eventkit"},
		{ID: "gone", Title: "Removed", StartAt: ms(3 * time.Hour), EndAt: ms(4 * time.Hour), Source: "eventkit"},
	}
	if err := app.store.UpsertCalendarEvents(ctx, rows, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.MarkCalendarEventsVanished(ctx, "eventkit", "", ms(2*time.Hour+time.Minute), ms(4*time.Hour+time.Minute), map[string]bool{"script": true}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	got, err := app.upcomingCalendarEventsAt(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"now", "soon", "script"}
	if ids := upcomingIDs(got); len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if got[0].ConferenceURL != "https://zoom.us/j/1" {
		t.Fatalf("https link dropped: %q", got[0].ConferenceURL)
	}
	if got[1].ConferenceURL != "" || got[2].ConferenceURL != "" {
		t.Fatalf("file: or javascript: link kept: %q %q", got[1].ConferenceURL, got[2].ConferenceURL)
	}
	if got[1].Location != "Room 4" || got[1].CalendarTitle != "Work" {
		t.Fatalf("fields not carried: %+v", got[1])
	}
	limited, err := app.upcomingCalendarEventsAt(now, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("limit 2 = %+v err=%v", limited, err)
	}
	if _, err := (&App{}).GetUpcomingCalendarEvents(3); err == nil {
		t.Fatal("no store should be an error")
	}
}

func TestGetUpcomingCalendarEventsFallsBackToTomorrow(t *testing.T) {
	app := upcomingTestApp(t)
	ctx := context.Background()
	// 20:00 today, nothing in the next 24 hours except a meeting at 21:00
	// tomorrow (25 hours out), which is still before the end of tomorrow.
	now := time.Date(2026, 10, 15, 20, 0, 0, 0, time.Local)
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{
		{ID: "tomorrow-evening", Title: "Late sync", StartAt: ms(25 * time.Hour), EndAt: ms(26 * time.Hour), Source: "eventkit"},
		{ID: "day-after", Title: "Too far", StartAt: ms(40 * time.Hour), EndAt: ms(41 * time.Hour), Source: "eventkit"},
	}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, err := app.upcomingCalendarEventsAt(now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ids := upcomingIDs(got); len(ids) != 1 || ids[0] != "tomorrow-evening" {
		t.Fatalf("ids = %v, want [tomorrow-evening]", ids)
	}
}

func TestSafeConferenceURL(t *testing.T) {
	cases := map[string]string{
		"https://meet.google.com/abc-defg-hij": "https://meet.google.com/abc-defg-hij",
		"http://example.com/x":                 "http://example.com/x",
		"file:///etc/passwd":                   "",
		"javascript:alert(1)":                  "",
		"zoommtg://zoom.us/join?confno=1":      "",
		"https:///nohost":                      "",
		"  ":                                   "",
	}
	for in, want := range cases {
		if got := safeConferenceURL(in); got != want {
			t.Errorf("safeConferenceURL(%q) = %q, want %q", in, got, want)
		}
	}
}
