package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

func TestWebCalendarDetectorRecordsSources(t *testing.T) {
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()

	d := &webCalendarDetector{
		store: st,
		now:   func() time.Time { return now },
		history: func(context.Context, time.Time) []calendar.WebVisit {
			return []calendar.WebVisit{
				{Provider: "google", URL: "https://calendar.google.com/calendar/u/0/r/week", LastSeen: now.Add(-2 * time.Hour), Visits: 4},
				// Too old to count as in use.
				{Provider: "outlook", URL: "https://outlook.office.com/calendar/view/week", LastSeen: now.Add(-40 * 24 * time.Hour), Visits: 1},
			}
		},
		screen: func(time.Time) []calendar.WebVisit {
			return []calendar.WebVisit{
				{Provider: "google", URL: "https://calendar.google.com/calendar/u/0/r/day", LastSeen: now.Add(-time.Hour), Visits: 1},
				{Provider: "notion", URL: "https://calendar.notion.so/", LastSeen: now.Add(-time.Hour), Visits: 1},
			}
		},
	}
	n, err := d.detect(ctx)
	if err != nil || n != 2 {
		t.Fatalf("detect: %d %v", n, err)
	}
	sources, err := st.ListCalendarSources(ctx)
	if err != nil || len(sources) != 2 {
		t.Fatalf("sources %+v %v", sources, err)
	}
	google := sources[0]
	if google.ID != "web:google" || google.Kind != "web" || google.Title != "calendar.google.com" {
		t.Fatalf("google %+v", google)
	}
	// The newest sighting (the screen one) wins for the time and the signal.
	if google.LastSeenAt != now.Add(-time.Hour).UnixMilli() || google.DetectedBy != calendar.DetectedByScreen {
		t.Fatalf("google sighting %+v", google)
	}
	if sources[1].ID != "web:notion" {
		t.Fatalf("notion %+v", sources[1])
	}

	// A second pass with older data never moves a sighting back, and the
	// first detection signal is kept.
	d.screen = func(time.Time) []calendar.WebVisit { return nil }
	if _, err := d.detect(ctx); err != nil {
		t.Fatal(err)
	}
	sources, _ = st.ListCalendarSources(ctx)
	if sources[0].LastSeenAt != now.Add(-time.Hour).UnixMilli() || sources[0].DetectedBy != calendar.DetectedByScreen {
		t.Fatalf("after second pass %+v", sources[0])
	}
}

func TestWebCalendarDetectorIgnoresThePeeksOwnVisits(t *testing.T) {
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	peekAt := now.Add(-3 * time.Hour)
	if err := recordCalendarPeekWindow(ctx, st, peekAt, peekAt.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	d := &webCalendarDetector{
		store: st,
		now:   func() time.Time { return now },
		history: func(context.Context, time.Time) []calendar.WebVisit {
			return []calendar.WebVisit{
				// The peek's own tab: ignored.
				{Provider: "google", URL: "https://calendar.google.com/calendar/u/0/r/week", LastSeen: peekAt.Add(5 * time.Second), Visits: 1},
				// The user's real visit a day earlier: counts.
				{Provider: "outlook", URL: "https://outlook.office.com/calendar/view/week", LastSeen: now.Add(-24 * time.Hour), Visits: 2},
			}
		},
	}
	if _, err := d.detect(ctx); err != nil {
		t.Fatal(err)
	}
	sources, _ := st.ListCalendarSources(ctx)
	if len(sources) != 1 || sources[0].Provider != "outlook" {
		t.Fatalf("only the user's visit counts: %+v", sources)
	}
	// Old windows are pruned when a new one is recorded.
	if err := recordCalendarPeekWindow(ctx, st, now.Add(20*24*time.Hour), now.Add(20*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if windows := loadCalendarPeekWindows(ctx, st); len(windows) != 1 {
		t.Fatalf("pruned windows %+v", windows)
	}
}

func TestScreenCalendarVisitsClassifiesSightings(t *testing.T) {
	a := &App{}
	if got := a.screenCalendarVisits(time.Now()); got != nil {
		t.Fatalf("no activity store means no visits, got %+v", got)
	}
}

func TestReadChromiumCalendarVisitsCopiesOnlyChangedFilesAndAtMostHourly(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "History")
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	writeHistory := func(visitAt time.Time) {
		_ = os.Remove(history)
		db, err := sql.Open("sqlite", history)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE urls (id INTEGER PRIMARY KEY, url LONGVARCHAR, title LONGVARCHAR, visit_count INTEGER DEFAULT 0, typed_count INTEGER DEFAULT 0, last_visit_time INTEGER NOT NULL, hidden INTEGER DEFAULT 0)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO urls (url, visit_count, last_visit_time) VALUES (?, ?, ?)`, "https://calendar.google.com/calendar/u/0/r/week", 1, calendar.ChromiumTime(visitAt)); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	writeHistory(now.Add(-time.Hour))
	chromiumHistoryMu.Lock()
	delete(chromiumHistoryCache, history)
	chromiumHistoryMu.Unlock()
	files := map[string]string{history: "Chrome Default"}
	since := now.Add(-calendar.PeekRecentUse)

	first := readChromiumCalendarVisitsFrom(context.Background(), files, since, now)
	if len(first) != 1 || !first[0].LastSeen.Equal(now.Add(-time.Hour)) {
		t.Fatalf("first read %+v", first)
	}
	// The file changed, but it was read less than an hour ago: the cached
	// result is reused.
	writeHistory(now.Add(-time.Minute))
	second := readChromiumCalendarVisitsFrom(context.Background(), files, since, now.Add(10*time.Minute))
	if len(second) != 1 || !second[0].LastSeen.Equal(now.Add(-time.Hour)) {
		t.Fatalf("second read should be cached %+v", second)
	}
	// An hour later the changed file is copied again.
	third := readChromiumCalendarVisitsFrom(context.Background(), files, since, now.Add(61*time.Minute))
	if len(third) != 1 || !third[0].LastSeen.Equal(now.Add(-time.Minute)) {
		t.Fatalf("third read should see the change %+v", third)
	}
	// Unchanged file, hours later: still no copy (the cached visits come back).
	chromiumHistoryMu.Lock()
	entry := chromiumHistoryCache[history]
	chromiumHistoryMu.Unlock()
	fourth := readChromiumCalendarVisitsFrom(context.Background(), files, since, now.Add(5*time.Hour))
	chromiumHistoryMu.Lock()
	after := chromiumHistoryCache[history]
	chromiumHistoryMu.Unlock()
	if len(fourth) != 1 || !after.readAt.Equal(entry.readAt) {
		t.Fatalf("an unchanged file is not copied again: %+v %v", fourth, after.readAt)
	}
}
