package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestActivityHeatmapMatchesDayAcrossDST(t *testing.T) {
	app := newThreadTestApp(t)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	prior := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prior })
	for _, month := range []string{"2026-03", "2026-11"} {
		first, _ := time.ParseInLocation("2006-01", month, loc)
		for i := 0; i < 32; i++ {
			day := first.AddDate(0, 0, i)
			seedPresentSession(t, app, month+day.Format("2006-01-02")+"a", day.Add(30*time.Minute), day.Add(60*time.Minute))
			seedPresentSession(t, app, month+day.Format("2006-01-02")+"b", day.Add(65*time.Minute), day.Add(90*time.Minute))
		}
		got, err := app.GetActivityMonthStats(month)
		if err != nil || len(got) != 42 {
			t.Fatalf("month stats=%d err=%v", len(got), err)
		}
		for _, row := range got {
			day, _ := time.ParseInLocation("2006-01-02", row.Date, loc)
			want, _, err := app.activitySummaryDayStats(app.activity.store, day)
			if err != nil || row.ActiveMs != want.ActiveMs || row.SessionCount != want.SessionCount {
				t.Fatalf("%s got=%+v want=%+v err=%v", month, row, want, err)
			}
		}
	}
	if _, err := app.GetActivityMonthStats("bad"); err == nil {
		t.Fatal("invalid month accepted")
	}
}

func TestActivityStorageReadersDoNotQueueBehindCapture(t *testing.T) {
	app := newThreadTestApp(t)
	day, _ := threadTestDay(t)
	seedThreadMoment(t, app, threadFixtureMoment{id: "reader-task", title: "Public task", app: "Finder", start: at(day, 9, 0), end: at(day, 9, 5)})
	store := app.activity.store
	if _, err := app.AddActivityFollowUp("reader-task", "Check the public fixture", "fixture-followup"); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE activities SET title='Uncommitted' WHERE id='reader-task'`); err != nil {
		t.Fatal(err)
	}
	// An occupied writer slot and uncommitted WAL write are held until the
	// entire row assembly and summary have completed on separate readers.
	done := make(chan error, 1)
	go func() {
		rows, err := app.GetActivitiesInRange(day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli())
		if err == nil && (len(rows) != 1 || rows[0].Title != "Public task" || rows[0].FollowUp == nil) {
			err = context.Canceled
		}
		if err == nil {
			_, err = app.GetDailySummary(day.Format("2006-01-02"))
		}
		if err == nil {
			_, err = app.GetActivityMonthStats(day.Format("2006-01"))
		}
		if err == nil {
			_, err = app.GetDayThreads(day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli())
		}
		if err == nil {
			_, err = app.GetDayNoticed(day.Format("2006-01-02"))
		}
		if err == nil {
			_, err = app.GetRecentMeetingNotes(1)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		tx.Rollback()
		<-done
		t.Fatal("Activity reads queued behind capture's connection")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := app.GetActivitiesInRange(day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli())
	if err != nil || len(rows) != 1 || rows[0].Title != "Uncommitted" {
		t.Fatalf("committed rows=%+v err=%v", rows, err)
	}
	// Enforce read-only on every reopened connection, not only the first one.
	store.readDB.SetMaxIdleConns(0)
	if _, err := store.reader().Exec(`DELETE FROM activities`); err == nil {
		t.Fatal("reader allowed a write")
	}
	if _, err := store.reader().Exec(`DELETE FROM activities`); err == nil {
		t.Fatal("reopened reader allowed a write")
	}
}

func TestActivityStorageReaderEscapesDatabasePathAndCloses(t *testing.T) {
	name := "with # and spaces"
	if runtime.GOOS != "windows" {
		name += " and ? characters"
	}
	base := filepath.Join(t.TempDir(), name)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, base)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewActivityStore(relative)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.CheckpointTruncate(); err != nil {
		t.Fatal(err)
	}
	if err := store.reader().Ping(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.readDB.Ping(); err == nil {
		t.Fatal("reader was left open")
	}
	if err := store.db.Ping(); err == nil {
		t.Fatal("writer was left open")
	}
}

func TestActivityLearnedDeletionInvalidatesSessionCache(t *testing.T) {
	app := newThreadTestApp(t)
	app.eventBroadcaster = NewEventBroadcaster()
	events := app.eventBroadcaster.AddClient("activity-cache")
	defer app.eventBroadcaster.RemoveClient("activity-cache")
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "to-delete.md"), []byte("# Public fixture learning\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteKnowledgeEntry("to-delete"); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-events:
		var event BrowserEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.Event != "knowledge:updated" {
			t.Fatalf("withdrawal event=%s err=%v", raw, err)
		}
	default:
		t.Fatal("deleted learning stayed cached without an invalidation event")
	}
	if err := app.DeleteKnowledgeEntry("missing"); err == nil {
		t.Fatal("missing deletion unexpectedly succeeded")
	}
	select {
	case raw := <-events:
		t.Fatalf("failed deletion emitted withdrawal: %s", raw)
	default:
	}
}
