package calendar

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeHistory creates a Chromium-shaped History database with the
// given urls (url, visit_count, last_visit_time as a time).
func writeFakeHistory(t *testing.T, path string, rows [][3]any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE urls (
		id INTEGER PRIMARY KEY, url LONGVARCHAR, title LONGVARCHAR,
		visit_count INTEGER DEFAULT 0, typed_count INTEGER DEFAULT 0,
		last_visit_time INTEGER NOT NULL, hidden INTEGER DEFAULT 0)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO urls (url, visit_count, last_visit_time) VALUES (?, ?, ?)`,
			row[0], row[1], ChromiumTime(row[2].(time.Time))); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
}

func TestReadWebCalendarVisits(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "History")
	now := mustTime("2026-10-14T10:00:00Z")
	writeFakeHistory(t, history, [][3]any{
		{"https://calendar.google.com/calendar/u/0/r/week", 12, now.Add(-time.Hour)},
		{"https://calendar.google.com/calendar/u/0/r/day/2026/10/10", 3, now.Add(-4 * 24 * time.Hour)},
		{"https://outlook.office.com/calendar/view/week", 2, now.Add(-20 * 24 * time.Hour)},
		{"https://outlook.office.com/mail/inbox", 50, now.Add(-time.Hour)},
		{"https://mail.google.com/mail/u/0/", 90, now.Add(-time.Minute)},
	})
	// A WAL file beside the database is copied too; an empty one is harmless.
	if err := os.WriteFile(history+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	visits, err := ReadWebCalendarVisits(context.Background(), history, dir, "Chrome Default", now.Add(-PeekRecentUse))
	if err != nil {
		t.Fatalf("ReadWebCalendarVisits: %v", err)
	}
	if len(visits) != 1 {
		t.Fatalf("visits = %+v, want only Google within 14 days", visits)
	}
	got := visits[0]
	if got.Provider != "google" || got.Visits != 15 || got.Browser != "Chrome Default" {
		t.Fatalf("visit %+v", got)
	}
	if !got.LastSeen.Equal(now.Add(-time.Hour)) || got.URL != "https://calendar.google.com/calendar/u/0/r/week" {
		t.Fatalf("newest sighting %+v", got)
	}
	// The copy is gone and the original untouched.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("scratch copies were not removed: %v", entries)
	}
}

func TestChromiumTimeRoundTrip(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	if got := FromChromiumTime(ChromiumTime(now)); !got.Equal(now) {
		t.Fatalf("round trip %v != %v", got, now)
	}
	// 2026-10-14T10:00:00Z in Chromium time, computed by hand from the 1601 epoch.
	if got := ChromiumTime(now); got != 13436445600000000 {
		t.Fatalf("ChromiumTime = %d", got)
	}
}

func TestChromiumHistoryFiles(t *testing.T) {
	home := t.TempDir()
	mk := func(parts ...string) {
		path := filepath.Join(append([]string{home, "Library", "Application Support"}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk("Google", "Chrome", "Default", "History")
	mk("Google", "Chrome", "Profile 2", "History")
	mk("Google", "Chrome", "System Profile", "History")
	mk("BraveSoftware", "Brave-Browser", "Default", "History")
	mk("Arc", "User Data", "Default", "History")
	files := ChromiumHistoryFiles(home)
	if len(files) != 4 {
		t.Fatalf("files = %v", files)
	}
	if files[filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Profile 2", "History")] != "Chrome Profile 2" {
		t.Fatalf("labels %v", files)
	}
}

func TestMergeWebVisits(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	merged := MergeWebVisits([]WebVisit{
		{Provider: "google", URL: "a", LastSeen: now.Add(-time.Hour), Visits: 2, Browser: "Chrome"},
		{Provider: "google", URL: "b", LastSeen: now, Visits: 3, Browser: "Arc"},
		{Provider: "outlook", URL: "c", LastSeen: now, Visits: 1, Browser: "Chrome"},
	})
	if len(merged) != 2 || merged[0].Provider != "google" || merged[0].Visits != 5 || merged[0].Browser != "Arc" || merged[0].URL != "b" {
		t.Fatalf("merged %+v", merged)
	}
}
