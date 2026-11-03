package main

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"
)

// Backlog item 70: the "at the computer" day line summed capture sessions
// only. Sessions are cut after 30 s without input and flushed every five
// minutes, so reading, listening and waiting on an answer — stretches with
// nothing to capture — counted nowhere. Present time keeps the session spans
// and adds each gap between two consecutive sessions that the evidence says
// the person sat through: ten minutes or less, with the screen on and
// unlocked and observing unpaused the whole way.
const activityPresentGapMaxMs = int64(10 * time.Minute / time.Millisecond)

// activityPresentGap is one between-sessions stretch that still counts as
// time at the computer, with the sessions on each side.
type activityPresentGap struct {
	startMs       int64
	endMs         int64
	beforeSession string
	afterSession  string
}

// activityPresentTimeMs sums the sessions' spans inside [startMs, endMs) and
// adds every gap between consecutive sessions that still counts as present.
// Spans are clipped to the window exactly like the day line clips them; the
// sessions may arrive in any order. Day, Week, the calendar heatmap, daily
// averages, the phone brief and the thread time split all use this rule.
func (a *App) activityPresentTimeMs(ctx context.Context, store *ActivityStore, sessions []ActivitySessionRow, startMs, endMs int64) (int64, []activityPresentGap, error) {
	var breaks activityPresenceBreak
	if len(sessions) >= 2 {
		var err error
		breaks, err = a.activityPresenceBreaks(ctx, store, startMs, endMs)
		if err != nil {
			return 0, nil, err
		}
	}
	total, gaps := activityPresentTimeWithBreaks(sessions, startMs, endMs, breaks)
	locked, err := activityLockScreenMs(ctx, store, sessions, startMs, endMs)
	if err != nil {
		return 0, nil, err
	}
	return max(int64(0), total-locked), gaps, nil
}

// activityLockScreenMs is the session time spent in front of the lock screen
// or screen saver (activity review v1.0.0 #5: loginwindow never counts as
// time at the computer). Each such event lasts until the next event of its
// session, or the session's end.
func activityLockScreenMs(ctx context.Context, store *ActivityStore, sessions []ActivitySessionRow, startMs, endMs int64) (int64, error) {
	if len(sessions) == 0 {
		return 0, nil
	}
	spans := make(map[string]activityThreadInterval, len(sessions))
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		start, end := max(session.StartTime, startMs), min(session.EndTime, endMs)
		if end > start {
			spans[session.ID] = activityThreadInterval{start: start, end: end}
			ids = append(ids, session.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	rows, err := store.reader().QueryContext(ctx, `SELECT e.session_id, e.timestamp,
		COALESCE((SELECT MIN(n.timestamp) FROM activity_events n WHERE n.session_id = e.session_id AND n.timestamp > e.timestamp), s.end_time)
		FROM activity_events e JOIN activity_sessions s ON e.session_id = s.id
		WHERE e.session_id IN (SELECT value FROM json_each(?)) AND e.timestamp < ? AND json_valid(e.data)
		AND (json_extract(e.data, '$.appBundleId') IN ('com.apple.screensaver.engine', 'com.apple.loginwindow')
			OR lower(json_extract(e.data, '$.appName')) IN ('loginwindow', 'screensaverengine', 'screen saver engine'))`, composeJSON(ids), endMs)
	if err != nil {
		return 0, err
	}
	locked := map[string][]activityThreadInterval{}
	for rows.Next() {
		var id string
		var start, end int64
		if err := rows.Scan(&id, &start, &end); err != nil {
			rows.Close()
			return 0, err
		}
		session := spans[id]
		start, end = max(start, session.start), min(end, session.end)
		if end > start {
			locked[id] = append(locked[id], activityThreadInterval{start: start, end: end})
		}
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	var total int64
	for _, intervals := range locked {
		for _, interval := range mergeActivityThreadIntervals(intervals, 0) {
			total += interval.end - interval.start
		}
	}
	return total, nil
}

// Reuse the same arithmetic for a month without fetching screen-state events
// again for every day. Each invocation still clips to one local day.
func activityPresentTimeWithBreaks(sessions []ActivitySessionRow, startMs, endMs int64, breaks activityPresenceBreak) (int64, []activityPresentGap) {
	type span struct {
		id    string
		start int64
		end   int64
	}
	spans := make([]span, 0, len(sessions))
	for _, session := range sessions {
		start, end := max(session.StartTime, startMs), min(session.EndTime, endMs)
		if end > start {
			spans = append(spans, span{id: session.ID, start: start, end: end})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})
	var total int64
	for _, s := range spans {
		total += s.end - s.start
	}
	if len(spans) < 2 {
		return total, nil
	}
	var gaps []activityPresentGap
	// Merged coverage, not just consecutive pairs: a session nested inside
	// another (chat ingest beside capture) must not hide the gap after the
	// outer one ends. Session spans still sum as they always have.
	covered, coveredID := spans[0].end, spans[0].id
	for i := 1; i < len(spans); i++ {
		gapStart, gapEnd := covered, spans[i].start
		if gapEnd-gapStart > 0 && gapEnd-gapStart <= activityPresentGapMaxMs && !breaks.broken(gapStart, gapEnd) {
			total += gapEnd - gapStart
			gaps = append(gaps, activityPresentGap{startMs: gapStart, endMs: gapEnd, beforeSession: coveredID, afterSession: spans[i].id})
		}
		if spans[i].end > covered {
			covered, coveredID = spans[i].end, spans[i].id
		}
	}
	return total, gaps
}

// activityPresenceBreak holds the evidence that the person was away through
// what would otherwise look like a reading gap: the recorded moments the
// screen went off (lock, sleep, screensaver or the login window) and the
// Closed Eyes pauses, when nothing is observed at all.
type activityPresenceBreak struct {
	eventTimes []int64 // sorted
	pauses     []observationPause
	pausesErr  error
}

// activityPresenceBreaks collects that evidence for one window in a single
// pass, so a day's gaps are checked without a query per gap. Protected
// sessions stay unread; a lock recorded only inside one is a bounded miss,
// since the gap around it is at most activityPresentGapMaxMs.
func (a *App) activityPresenceBreaks(ctx context.Context, store *ActivityStore, startMs, endMs int64) (activityPresenceBreak, error) {
	out := activityPresenceBreak{}
	if store == nil || store.db == nil {
		return out, errors.New("activity store unavailable")
	}
	rows, err := store.reader().QueryContext(ctx, `SELECT e.timestamp FROM activity_events e
		JOIN activity_sessions s ON e.session_id = s.id
		WHERE e.timestamp >= ? AND e.timestamp < ?`+sqlExcludeProtected+`
		AND json_valid(e.data)
		AND ((e.kind = 'session_state' AND json_extract(e.data, '$.stateName') IN ('locked', 'sleep'))
			OR json_extract(e.data, '$.appBundleId') IN ('com.apple.screensaver.engine', 'com.apple.loginwindow')
			OR lower(json_extract(e.data, '$.appName')) IN ('loginwindow', 'screensaverengine', 'screen saver engine'))
		ORDER BY e.timestamp`, startMs, endMs)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			rows.Close()
			return out, err
		}
		out.eventTimes = append(out.eventTimes, at)
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	// The pause log fails closed (observation_pauses.go): when it cannot be
	// read, every gap is treated as paused rather than guessed present.
	out.pauses, out.pausesErr = a.observationPausesContext(ctx)
	if out.pausesErr != nil {
		log.Printf("[activity] pause log unreadable, no between-session time counts: %v", out.pausesErr)
	}
	return out, nil
}

// broken reports whether the stretch [startMs, endMs) holds evidence the
// person was not present. The left edge is inclusive on purpose: a lock or
// screensaver switch recorded as the last event of the session before the gap
// lands exactly on it, and the stretch after it was screen-off time, not
// reading time. The right edge is exclusive for the same reason: a lock that
// became the first event of the next session happened at the gap's end, so
// the stretch before it was still screen-on. An unlock or a plain idle mark
// is not a break, so a gap the person sat through is never excluded.
func (b activityPresenceBreak) broken(startMs, endMs int64) bool {
	i := sort.Search(len(b.eventTimes), func(i int) bool { return b.eventTimes[i] >= startMs })
	if i < len(b.eventTimes) && b.eventTimes[i] < endMs {
		return true
	}
	if b.pausesErr != nil {
		return true
	}
	for _, p := range b.pauses {
		if p.Start < endMs && (p.End == 0 || p.End > startMs) {
			return true
		}
	}
	return false
}
