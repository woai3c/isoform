package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// captureCountsFixture seeds an activity store around one fixed day: a
// session eight days back (outside both windows), one three days back (week
// only), one that starts at 23:50 yesterday and runs past midnight (split by
// each row's own time), and one this morning.
type captureCountsFixture struct {
	store      *ActivityStore
	todayStart time.Time
	weekStart  time.Time
}

func newCaptureCountsFixture(t *testing.T) captureCountsFixture {
	t.Helper()
	s := newTestStore(t)
	today := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	f := captureCountsFixture{store: s, todayStart: today, weekStart: today.AddDate(0, 0, -6)}

	at := func(day, hour, minute, second int) int64 {
		return today.AddDate(0, 0, day).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second).UnixMilli()
	}
	speech := func(ts int64) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{Timestamp: ts, Kind: activitymon.EventSpeech, SpeechText: "hello"}
	}
	typed := func(ts int64, text string) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{Timestamp: ts, Kind: activitymon.EventKeyboard, TypedText: text}
	}
	peek := func(ts int64) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{Timestamp: ts, Kind: activitymon.EventCalendarPeek, Provider: "google", Count: 3}
	}
	click := func(ts int64) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{Timestamp: ts, Kind: activitymon.EventClick}
	}

	type seeded struct {
		id        string
		start     int64
		meeting   string
		events    []activitymon.ActivityEvent
		snapshots []int64
		ocr       []int64 // frame times as absolute ms; one box-cache row is added per session too
	}
	sessions := []seeded{
		{
			id: "old", start: at(-8, 10, 0, 0), meeting: "/transcripts/old.md",
			events:    []activitymon.ActivityEvent{speech(at(-8, 10, 0, 5)), typed(at(-8, 10, 1, 0), "old"), peek(at(-8, 10, 2, 0))},
			snapshots: []int64{at(-8, 10, 0, 1)},
			ocr:       []int64{at(-8, 10, 0, 1)},
		},
		{
			id: "mid", start: at(-3, 14, 0, 0), meeting: "/transcripts/mid.md",
			// Two batches in the same minute count as one minute.
			events:    []activitymon.ActivityEvent{speech(at(-3, 14, 0, 5)), speech(at(-3, 14, 0, 15)), speech(at(-3, 14, 1, 5)), typed(at(-3, 14, 2, 0), "mid"), typed(at(-3, 14, 3, 0), ""), click(at(-3, 14, 4, 0))},
			snapshots: []int64{at(-3, 14, 0, 1), at(-3, 14, 5, 0)},
			ocr:       []int64{at(-3, 14, 0, 1), at(-3, 14, 5, 0)},
		},
		{
			id: "cross", start: at(-1, 23, 50, 0),
			events:    []activitymon.ActivityEvent{speech(at(-1, 23, 55, 0)), speech(at(0, 0, 5, 0)), typed(at(-1, 23, 56, 0), "late"), typed(at(0, 0, 6, 0), "early"), peek(at(0, 0, 7, 0))},
			snapshots: []int64{at(-1, 23, 55, 0), at(0, 0, 5, 0)},
			ocr:       []int64{at(-1, 23, 55, 0), at(0, 0, 5, 0)},
		},
		{
			id: "today", start: at(0, 9, 0, 0), meeting: "/transcripts/today.md",
			events:    []activitymon.ActivityEvent{speech(at(0, 9, 0, 5)), speech(at(0, 9, 0, 50)), speech(at(0, 9, 2, 0)), typed(at(0, 9, 3, 0), "now"), click(at(0, 9, 4, 0))},
			snapshots: []int64{at(0, 9, 0, 1), at(0, 9, 1, 0), at(0, 9, 2, 0)},
			ocr:       []int64{at(0, 9, 0, 1)},
		},
		// A meeting that has no transcript yet is not counted.
		{id: "untranscribed", start: at(0, 11, 0, 0), meeting: "-"},
	}

	for _, seed := range sessions {
		row := ActivitySessionRow{
			ID: seed.id, StartTime: seed.start, EndTime: seed.start + int64(20*time.Minute/time.Millisecond),
			TriggerKind: "idle_transition", AppNames: "[]", EventCount: len(seed.events), TriageStatus: "pending",
			CreatedAt: seed.start,
		}
		if seed.meeting != "" {
			row.IsMeeting = true
			row.MeetingApp = "Zoom"
			if seed.meeting != "-" {
				row.TranscriptPath = seed.meeting
			}
		}
		if err := s.SaveSession(row, seed.events, nil); err != nil {
			t.Fatalf("SaveSession(%s): %v", seed.id, err)
		}
		var snapshotIDs []int64
		for i, ts := range seed.snapshots {
			res, err := s.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path) VALUES (?, ?, ?)`,
				seed.id, ts, fmt.Sprintf("%s/%d.jpg", seed.id, i))
			if err != nil {
				t.Fatalf("insert snapshot: %v", err)
			}
			id, _ := res.LastInsertId()
			snapshotIDs = append(snapshotIDs, id)
		}
		for i, ts := range seed.ocr {
			if err := s.SaveOCRFrame(OCRFrameRow{
				SessionID: seed.id, SegmentID: snapshotIDs[i], FrameTime: float64(ts-seed.start) / 1000, Text: "frame text", Confidence: 0.9,
			}); err != nil {
				t.Fatalf("SaveOCRFrame: %v", err)
			}
		}
		if len(snapshotIDs) > 0 {
			// The text-less box-cache row on-demand OCR writes.
			if err := s.SaveOCRBoxesForSnapshot(snapshotOCRIdentity{SessionID: seed.id, SnapshotID: snapshotIDs[0] + 100000, FrameTime: 1}, "[]"); err != nil {
				t.Fatalf("SaveOCRBoxesForSnapshot: %v", err)
			}
		}
	}
	return f
}

func TestCaptureTallies_CountsEachSourceForTodayAndTheWeek(t *testing.T) {
	f := newCaptureCountsFixture(t)
	got, err := f.store.captureTallies(f.todayStart.UnixMilli(), f.weekStart.UnixMilli())
	if err != nil {
		t.Fatalf("captureTallies: %v", err)
	}
	want := map[string]captureTally{
		// mid 2 + cross 2 + today 3; today: cross 1 + today 3.
		captureSourceScreens: {today: 4, week: 7},
		// mid 2 + cross 2 + today 1, plus a text-less on-demand row in each of
		// those sessions; a frame counts on the day its session started, so
		// today has only the morning session's 1 + 1.
		captureSourceTextRead: {today: 2, week: 8},
		// mid: 14:00 and 14:01; cross: 23:55 and 00:05; today: 09:00 (twice) and 09:02.
		captureSourceAudioMinutes: {today: 3, week: 6},
		// mid and today; the meeting without a transcript is left out.
		captureSourceMeetings: {today: 1, week: 2},
		// mid "mid" (the empty burst is left out), cross "late" and "early", today "now".
		captureSourceTyping:        {today: 2, week: 4},
		captureSourceCalendarPeeks: {today: 1, week: 1},
	}
	for source, tally := range want {
		if got[source] != tally {
			t.Errorf("%s = %+v, want %+v", source, got[source], tally)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d sources, want %d: %+v", len(got), len(want), got)
	}
}

// TestCaptureCountQueries_UseIndexes guards the cost of the counts on a large
// activity.db: every table is reached through an index, never scanned.
func TestCaptureCountQueries_UseIndexes(t *testing.T) {
	s := newTestStore(t)
	wantIndex := map[string]string{
		captureSourceScreens:       "INDEX idx_activity_snapshots_timestamp",
		captureSourceTextRead:      "COVERING INDEX idx_ocr_frames_session",
		captureSourceAudioMinutes:  "INDEX idx_activity_events_kind_time",
		captureSourceMeetings:      "INDEX idx_activity_sessions_start_time",
		captureSourceTyping:        "INDEX idx_activity_events_kind_time",
		captureSourceCalendarPeeks: "INDEX idx_activity_events_kind_time",
	}
	for _, q := range captureCountQueries {
		plan := explainQueryPlan(t, s.db, q.query, int64(1), int64(0))
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") {
				t.Errorf("%s scans a table: %q\nplan: %q", q.source, line, plan)
			}
		}
		if want := wantIndex[q.source]; !strings.Contains(strings.Join(plan, "\n"), want) {
			t.Errorf("%s does not use %s: %q", q.source, want, plan)
		}
	}
}

func explainQueryPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	return plan
}

func TestCountCalendarEventsRead(t *testing.T) {
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	today := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	weekStart := today.AddDate(0, 0, -6)
	row := func(id, source string) storage.CalendarEventRow {
		return storage.CalendarEventRow{ID: id, UID: id, Source: source, Title: id, StartAt: today.UnixMilli(), EndAt: today.Add(time.Hour).UnixMilli()}
	}
	// Read eight days ago and never again: outside both windows.
	if err := st.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row("stale", calendar.SourceEventKit)}, today.AddDate(0, 0, -8).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// Read three days ago: week only.
	if err := st.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row("tuesday", calendar.SourceEventKit)}, today.AddDate(0, 0, -3).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// Read last week and again today: counted once, as today.
	if err := st.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row("standup", calendar.SourceEventKit)}, today.AddDate(0, 0, -10).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row("standup", calendar.SourceEventKit), row("web", calendar.SourceWeb)}, today.Add(8*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// Read last week, found missing today: finding it gone is not a read.
	if err := st.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row("cancelled", calendar.SourceEventKit)}, today.AddDate(0, 0, -9).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if n, err := st.MarkCalendarEventsVanished(ctx, calendar.SourceEventKit, "", today.UnixMilli(), today.Add(time.Hour).UnixMilli(),
		map[string]bool{"standup": true, "tuesday": true, "stale": true}, today.Add(9*time.Hour).UnixMilli()); err != nil || n != 1 {
		t.Fatalf("MarkCalendarEventsVanished = %d, %v; want the cancelled event marked", n, err)
	}
	gotToday, gotWeek, err := st.CountCalendarEventsRead(ctx, calendar.SourceEventKit, today.UnixMilli(), weekStart.UnixMilli())
	if err != nil {
		t.Fatalf("CountCalendarEventsRead: %v", err)
	}
	if gotToday != 1 || gotWeek != 2 {
		t.Fatalf("calendar events read = today %d, week %d; want 1 and 2", gotToday, gotWeek)
	}
}

func TestCaptureCounts_ListsEverySourceAndOtherwiseSaysWhy(t *testing.T) {
	f := newCaptureCountsFixture(t)
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	event := storage.CalendarEventRow{ID: "e1", UID: "e1", Source: calendar.SourceEventKit, StartAt: f.todayStart.UnixMilli(), EndAt: f.todayStart.Add(time.Hour).UnixMilli()}
	if err := st.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{event}, f.todayStart.Add(7*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: f.store}, store: st}

	got, err := app.captureCounts(f.todayStart, f.weekStart)
	if err != nil {
		t.Fatalf("captureCounts: %v", err)
	}
	if got.Paused || got.NotReady || got.TodayStart != f.todayStart.UnixMilli() || got.WeekStart != f.weekStart.UnixMilli() {
		t.Fatalf("answer = %+v", got)
	}
	want := []CaptureCountRow{
		{Source: captureSourceScreens, Today: 4, Week: 7},
		{Source: captureSourceTextRead, Today: 2, Week: 8},
		{Source: captureSourceAudioMinutes, Today: 3, Week: 6},
		{Source: captureSourceMeetings, Today: 1, Week: 2},
		{Source: captureSourceTyping, Today: 2, Week: 4},
		{Source: captureSourceCalendarEvents, Today: 1, Week: 1},
		{Source: captureSourceCalendarPeeks, Today: 1, Week: 1},
	}
	if fmt.Sprint(got.Sources) != fmt.Sprint(want) {
		t.Fatalf("sources = %+v\nwant      %+v", got.Sources, want)
	}

	// Counts are reused for a moment, then read again.
	if _, err := f.store.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path) VALUES ('today', ?, 'new.jpg')`,
		f.todayStart.Add(10*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if again, _ := app.captureCounts(f.todayStart, f.weekStart); again.Sources[0].Today != 4 {
		t.Fatalf("screens today = %d, want the reused 4", again.Sources[0].Today)
	}
	app.captureCountsMemo.at = time.Now().Add(-captureCountsMemoTTL)
	if fresh, _ := app.captureCounts(f.todayStart, f.weekStart); fresh.Sources[0].Today != 5 {
		t.Fatalf("screens today = %d, want 5 once the reuse expired", fresh.Sources[0].Today)
	}

	// The typing count, the one that reads rows, is reused for longer.
	typingToday := func() int64 {
		t.Helper()
		app.captureCountsMemo.at = time.Time{}
		got, err := app.captureCounts(f.todayStart, f.weekStart)
		if err != nil {
			t.Fatalf("captureCounts: %v", err)
		}
		for _, row := range got.Sources {
			if row.Source == captureSourceTyping {
				return row.Today
			}
		}
		t.Fatal("no typing row")
		return 0
	}
	if err := f.store.SaveSession(ActivitySessionRow{
		ID: "later", StartTime: f.todayStart.Add(12 * time.Hour).UnixMilli(), EndTime: f.todayStart.Add(13 * time.Hour).UnixMilli(),
		TriggerKind: "idle_transition", AppNames: "[]", EventCount: 1, TriageStatus: "pending", CreatedAt: f.todayStart.Add(12 * time.Hour).UnixMilli(),
	}, []activitymon.ActivityEvent{{Timestamp: f.todayStart.Add(12 * time.Hour).UnixMilli(), Kind: activitymon.EventKeyboard, TypedText: "more"}}, nil); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if n := typingToday(); n != 2 {
		t.Fatalf("typing today = %d, want the reused 2", n)
	}
	app.captureCountsMemo.typingAt = time.Now().Add(-captureTypingMemoTTL)
	if n := typingToday(); n != 3 {
		t.Fatalf("typing today = %d, want 3 once its reuse expired", n)
	}

	// Without the activity store (still opening, or failed to open) there
	// are no numbers, rather than zeros that say nothing was captured.
	notReady, err := (&App{store: st}).GetCaptureCounts(0)
	if err != nil || !notReady.NotReady || notReady.Paused || len(notReady.Sources) != 0 {
		t.Fatalf("no activity store = %+v, %v; want not ready with no sources", notReady, err)
	}

	app.closedEyes.enabled.Store(true)
	paused, err := app.GetCaptureCounts(0)
	if err != nil {
		t.Fatalf("GetCaptureCounts paused: %v", err)
	}
	if !paused.Paused || paused.NotReady || len(paused.Sources) != 0 {
		t.Fatalf("paused answer = %+v, want paused with no sources", paused)
	}
}

func TestCaptureCountWindow(t *testing.T) {
	now := time.Date(2026, 10, 15, 14, 30, 0, 0, time.Local)
	midnight := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	cases := []struct {
		name  string
		since int64
		today time.Time
	}{
		{"zero uses local midnight", 0, midnight},
		{"a recent caller midnight is kept", midnight.Add(-time.Hour).UnixMilli(), midnight.Add(-time.Hour)},
		{"a future value falls back", now.Add(time.Hour).UnixMilli(), midnight},
		{"a stale value falls back", now.AddDate(0, 0, -3).UnixMilli(), midnight},
	}
	for _, tc := range cases {
		today, week := captureCountWindow(tc.since, now)
		if !today.Equal(tc.today) {
			t.Errorf("%s: today = %v, want %v", tc.name, today, tc.today)
		}
		if want := tc.today.AddDate(0, 0, -6); !week.Equal(want) {
			t.Errorf("%s: week = %v, want %v", tc.name, week, want)
		}
	}
}
