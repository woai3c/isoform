package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/platform/activitymon"
)

// What Isoform captured, in numbers: the "What Isoform captured" card in
// Settings > Observation and the quiet line on Today. Each count is read from
// the rows Isoform keeps, through an index that bounds the work to the window
// (see captureCountQueries); none scans a whole table.
//
// Activity rows reach activity.db when a session is saved (an idle pause, a
// meeting ending or the periodic flush), so the newest few minutes show up
// once their session is saved.

// Capture count sources, in the order the card lists them. The frontend maps
// each id to its label; clipboard items and browser visits are not kept on
// this branch, so they have no source.
const (
	captureSourceScreens        = "screens"
	captureSourceTextRead       = "textRead"
	captureSourceAudioMinutes   = "audioMinutes"
	captureSourceMeetings       = "meetings"
	captureSourceTyping         = "typing"
	captureSourceCalendarEvents = "calendarEvents"
	captureSourceCalendarPeeks  = "calendarPeeks"
)

// captureCountWeekDays is the length of the longer window, today included.
const captureCountWeekDays = 7

// captureCountsMemoTTL is how long one answer is reused. The Today line and
// the Settings card refresh on the same events, and activity.db has a single
// connection that capture writes wait on.
const captureCountsMemoTTL = 2 * time.Second

// captureTypingMemoTTL is how long the typing count is reused. It is the one
// count that reads rows (the data of each keyboard event of the week), and
// every session save asks for the counts again.
const captureTypingMemoTTL = 5 * time.Minute

// captureCountsMemo holds the last counts. Its lock is held while counting,
// so callers that arrive together share one run.
type captureCountsMemo struct {
	mu         sync.Mutex
	at         time.Time
	todayStart int64
	weekStart  int64
	sources    []CaptureCountRow
	// typing is the typing count, kept for captureTypingMemoTTL for the
	// windows it was counted for.
	typing           captureTally
	typingAt         time.Time
	typingTodayStart int64
	typingWeekStart  int64
}

// CaptureCountRow is one source: how many it captured today and in the last
// seven days, today included.
type CaptureCountRow struct {
	Source string `json:"source"`
	Today  int64  `json:"today"`
	Week   int64  `json:"week"`
}

// CaptureCounts is the GetCaptureCounts answer. While observing is paused it
// carries Paused and no sources, so no surface shows numbers then. NotReady
// (also with no sources) means activity.db is not open yet: at launch it can
// take a while (a new index is built on the first launch after an update),
// and zeros then would wrongly say nothing was captured. It is also set when
// the store could not be opened at all.
type CaptureCounts struct {
	Paused     bool              `json:"paused"`
	NotReady   bool              `json:"notReady"`
	TodayStart int64             `json:"todayStart"`
	WeekStart  int64             `json:"weekStart"`
	Sources    []CaptureCountRow `json:"sources"`
}

// captureTally is one source's pair of counts.
type captureTally struct {
	today int64
	week  int64
}

// GetCaptureCounts counts what Isoform captured today and in the last seven
// days. since is the start of today in Unix ms on the caller's clock (local
// midnight); 0, or a value that is not within the last day, means Go's own
// local midnight. Wails binding, read-only.
func (a *App) GetCaptureCounts(since int64) (perfValue CaptureCounts, perfErr error) {
	defer logPageBinding("GetCaptureCounts", time.Now(), &perfValue)
	if a == nil {
		return CaptureCounts{}, nil
	}
	todayStart, weekStart := captureCountWindow(since, time.Now())
	return a.captureCounts(todayStart, weekStart)
}

func (a *App) captureCounts(todayStart, weekStart time.Time) (CaptureCounts, error) {
	out := CaptureCounts{TodayStart: todayStart.UnixMilli(), WeekStart: weekStart.UnixMilli()}
	if a.IsClosedEyesModeEnabled() {
		out.Paused = true
		return out, nil
	}
	// initActivityMonitor sets a.activity once NewActivityStore returns.
	am := a.activity
	if am == nil || am.store == nil {
		out.NotReady = true
		return out, nil
	}

	memo := &a.captureCountsMemo
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.sources != nil && memo.todayStart == out.TodayStart && memo.weekStart == out.WeekStart &&
		time.Since(memo.at) < captureCountsMemoTTL {
		out.Sources = append([]CaptureCountRow(nil), memo.sources...)
		return out, nil
	}

	typingKept := !memo.typingAt.IsZero() && memo.typingTodayStart == out.TodayStart &&
		memo.typingWeekStart == out.WeekStart && time.Since(memo.typingAt) < captureTypingMemoTTL
	var skip []string
	if typingKept {
		skip = append(skip, captureSourceTyping)
	}
	tallies, err := am.store.captureTallies(out.TodayStart, out.WeekStart, skip...)
	if err != nil {
		return CaptureCounts{}, err
	}
	if typingKept {
		tallies[captureSourceTyping] = memo.typing
	} else {
		memo.typing = tallies[captureSourceTyping]
		memo.typingAt = time.Now()
		memo.typingTodayStart, memo.typingWeekStart = out.TodayStart, out.WeekStart
	}
	if a.store != nil {
		today, week, err := a.store.CountCalendarEventsRead(context.Background(), calendar.SourceEventKit, out.TodayStart, out.WeekStart)
		if err != nil {
			return CaptureCounts{}, fmt.Errorf("count calendar events read: %w", err)
		}
		tallies[captureSourceCalendarEvents] = captureTally{today: today, week: week}
	}

	for _, source := range []string{
		captureSourceScreens,
		captureSourceTextRead,
		captureSourceAudioMinutes,
		captureSourceMeetings,
		captureSourceTyping,
		captureSourceCalendarEvents,
		captureSourceCalendarPeeks,
	} {
		tally := tallies[source]
		out.Sources = append(out.Sources, CaptureCountRow{Source: source, Today: tally.today, Week: tally.week})
	}
	memo.at = time.Now()
	memo.todayStart, memo.weekStart = out.TodayStart, out.WeekStart
	memo.sources = append([]CaptureCountRow(nil), out.Sources...)
	return out, nil
}

// captureCountWindow returns the start of today and of the seven-day window.
// A since in the future or more than a day back (a stale page or a wrong
// caller clock) falls back to local midnight, so the windows never stretch.
func captureCountWindow(since int64, now time.Time) (time.Time, time.Time) {
	local := now.In(time.Local)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
	today := midnight
	if since > 0 {
		candidate := time.UnixMilli(since).In(time.Local)
		if !candidate.After(now) && now.Sub(candidate) <= 24*time.Hour {
			today = candidate
		}
	}
	return today, today.AddDate(0, 0, -(captureCountWeekDays - 1))
}

// captureCountQueries are the activity.db counts. Each takes (todayStart,
// weekStart) and returns (week, today), and each reads an index bounded by
// the window rather than the rows themselves where it can:
//
//   - screens: idx_activity_snapshots_timestamp, then session identity for privacy.
//   - audio minutes and web calendar checks: idx_activity_events_kind_time,
//     bounded by kind/time, with session identity checked for privacy. Speech
//     payloads (including voice embeddings) are never read.
//   - typing: idx_activity_events_kind_time, then the keyboard rows only, to
//     see whether typed text was kept (typedText is omitted when empty). As
//     the one count that reads rows, it is reused for captureTypingMemoTTL.
//   - meetings: idx_activity_sessions_start_time; a meeting counts once its
//     transcript is saved.
//   - text read: idx_activity_sessions_start_time picks the sessions of the
//     window, then idx_ocr_frames_session counts their frames, covering;
//     CROSS JOIN keeps the sessions as the outer loop. Frame rows are never
//     read: most carry kilobytes of text and boxes, so even their frame_time
//     costs a page per frame. A frame counts on the day its session started,
//     and the rare text-less rows on-demand OCR writes when it reads a
//     screen the worker skipped count too.
//
// Audio minutes are the distinct clock minutes in which speech was
// transcribed: speech arrives in batches of about ten seconds and no
// duration is kept, so the copy says "minutes with speech".
var captureCountQueries = []struct {
	source string
	query  string
}{
	{captureSourceScreens, `
		SELECT COUNT(*), COALESCE(SUM(timestamp >= ?1), 0)
		FROM activity_snapshots snap
		CROSS JOIN activity_sessions s ON s.id = snap.session_id
		WHERE timestamp >= ?2` + sqlExcludeProtected},
	{captureSourceTextRead, `
		SELECT COUNT(*), COALESCE(SUM(s.start_time >= ?1), 0)
		FROM activity_sessions s
		CROSS JOIN activity_ocr_frames f ON f.session_id = s.id
		WHERE s.start_time >= ?2` + sqlExcludeProtected},
	{captureSourceAudioMinutes, `
		SELECT COUNT(DISTINCT timestamp / 60000),
			COUNT(DISTINCT CASE WHEN timestamp >= ?1 THEN timestamp / 60000 END)
		FROM activity_events e
		CROSS JOIN activity_sessions s ON s.id = e.session_id
		WHERE kind = '` + string(activitymon.EventSpeech) + `' AND timestamp >= ?2` + sqlExcludeProtected},
	{captureSourceMeetings, `
		SELECT COUNT(*), COALESCE(SUM(start_time >= ?1), 0)
		FROM activity_sessions s
		WHERE start_time >= ?2 AND is_meeting = 1 AND transcript_path <> ''` + sqlExcludeProtected},
	{captureSourceTyping, `
		SELECT COUNT(*), COALESCE(SUM(timestamp >= ?1), 0)
		FROM activity_events e
		CROSS JOIN activity_sessions s ON s.id = e.session_id
		WHERE kind = '` + string(activitymon.EventKeyboard) + `' AND timestamp >= ?2
			AND instr(data, '"typedText":') > 0` + sqlExcludeProtected},
	{captureSourceCalendarPeeks, `
		SELECT COUNT(*), COALESCE(SUM(timestamp >= ?1), 0)
		FROM activity_events e
		CROSS JOIN activity_sessions s ON s.id = e.session_id
		WHERE kind = '` + string(activitymon.EventCalendarPeek) + `' AND timestamp >= ?2` + sqlExcludeProtected},
}

// captureTallies runs captureCountQueries for one pair of windows, leaving
// out the sources in skip.
func (s *ActivityStore) captureTallies(todayStart, weekStart int64, skip ...string) (map[string]captureTally, error) {
	out := make(map[string]captureTally, len(captureCountQueries))
	for _, q := range captureCountQueries {
		if slices.Contains(skip, q.source) {
			continue
		}
		var tally captureTally
		if err := s.db.QueryRow(q.query, todayStart, weekStart).Scan(&tally.week, &tally.today); err != nil {
			return nil, fmt.Errorf("count %s: %w", q.source, err)
		}
		out[q.source] = tally
	}
	return out, nil
}
