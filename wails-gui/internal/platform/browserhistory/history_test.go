package browserhistory

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeVisit is one row for writeFakeHistory.
type fakeVisit struct {
	url, title string
	at         time.Time
	transition int
}

// writeFakeHistory creates a Chromium-shaped History database with urls and
// visits tables.
func writeFakeHistory(t *testing.T, path string, visits []fakeVisit) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE urls (id INTEGER PRIMARY KEY, url LONGVARCHAR, title LONGVARCHAR,
			visit_count INTEGER DEFAULT 0, typed_count INTEGER DEFAULT 0,
			last_visit_time INTEGER NOT NULL DEFAULT 0, hidden INTEGER DEFAULT 0)`,
		`CREATE TABLE visits (id INTEGER PRIMARY KEY, url INTEGER NOT NULL, visit_time INTEGER NOT NULL,
			from_visit INTEGER DEFAULT 0, transition INTEGER DEFAULT 0, visit_duration INTEGER DEFAULT 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	for i, v := range visits {
		res, err := db.Exec(`INSERT INTO urls (url, title, visit_count, last_visit_time) VALUES (?, ?, 1, ?)`, v.url, v.title, ChromiumTime(v.at))
		if err != nil {
			t.Fatalf("insert url: %v", err)
		}
		urlID, _ := res.LastInsertId()
		if _, err := db.Exec(`INSERT INTO visits (id, url, visit_time, transition) VALUES (?, ?, ?, ?)`, i+1, urlID, ChromiumTime(v.at), v.transition); err != nil {
			t.Fatalf("insert visit: %v", err)
		}
	}
}

func TestProfilesFindsChromiumProfilesOnly(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "Library", "Application Support")
	for _, dir := range []string{
		"Google/Chrome/Default", "Google/Chrome/Profile 2", "Google/Chrome/System Profile",
		"BraveSoftware/Brave-Browser/Default", "Arc/User Data/Default",
	} {
		full := filepath.Join(base, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "History"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]bool{}
	for _, p := range Profiles(home) {
		got[p.Label()] = true
	}
	for _, want := range []string{"Chrome Default", "Chrome Profile 2", "Brave Default", "Arc Default"} {
		if !got[want] {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if got["Chrome System Profile"] || len(got) != 4 {
		t.Fatalf("unexpected profiles %v", got)
	}
}

func TestWithCopyReadsVisitsAndSkipsSubframes(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "History")
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	writeFakeHistory(t, history, []fakeVisit{
		{url: "https://old.example.com/", title: "Old", at: now.Add(-48 * time.Hour)},
		{url: "https://github.com/example/isoform", title: "isoform", at: now.Add(-time.Hour)},
		{url: "https://ads.example.com/frame", title: "", at: now.Add(-50 * time.Minute), transition: 3},
		{url: "https://news.ycombinator.com/", title: "Hacker News", at: now.Add(-10 * time.Minute), transition: 0x30000001},
	})
	if err := os.WriteFile(history+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var visits []Visit
	err := WithCopy(context.Background(), history, dir, func(db *sql.DB) error {
		var err error
		visits, err = ReadVisits(context.Background(), db, now.Add(-24*time.Hour), 100)
		return err
	})
	if err != nil {
		t.Fatalf("WithCopy: %v", err)
	}
	if len(visits) != 2 || visits[0].Title != "isoform" || visits[1].URL != "https://news.ycombinator.com/" {
		t.Fatalf("visits %+v", visits)
	}
	if !visits[0].At.Equal(now.Add(-time.Hour)) || visits[0].ID != 2 {
		t.Fatalf("visit time or id %+v", visits[0])
	}
	// The copy is gone.
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("scratch copy left behind: %s", entry.Name())
		}
	}
}

func TestReadVisitsLeavesOutOtherDevices(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "History")
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	writeFakeHistory(t, history, []fakeVisit{
		{url: "https://here.example.com/", title: "On this Mac", at: now.Add(-time.Hour)},
		{url: "https://phone.example.com/", title: "On the phone", at: now.Add(-time.Hour + time.Minute)},
	})
	db, err := sql.Open("sqlite", history)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE visits ADD COLUMN originator_cache_guid TEXT DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE visits SET originator_cache_guid = 'phone-guid' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var visits []Visit
	err = WithCopy(context.Background(), history, dir, func(db *sql.DB) error {
		var err error
		visits, err = ReadVisits(context.Background(), db, now.Add(-24*time.Hour), 100)
		return err
	})
	if err != nil || len(visits) != 1 || visits[0].Title != "On this Mac" {
		t.Fatalf("visits %+v %v", visits, err)
	}
}

func TestModTimeFollowsTheWAL(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "History")
	if err := os.WriteFile(history, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(history, old, old); err != nil {
		t.Fatal(err)
	}
	first, err := ModTime(history)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history+"-wal", []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, _ := ModTime(history)
	if !second.After(first) {
		t.Fatalf("a newer WAL must move the time: %v then %v", first, second)
	}
}

func TestCopyFileReportsAMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := copyFile(filepath.Join(dir, "History-wal"), filepath.Join(dir, "copy-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing WAL must read as not existing: %v", err)
	}
	src := filepath.Join(dir, "History")
	if err := os.WriteFile(src, []byte("visits"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "visits" {
		t.Fatalf("copy holds %q", got)
	}
}
