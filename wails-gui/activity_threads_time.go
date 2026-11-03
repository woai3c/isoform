package main

import (
	"context"
	"encoding/json"
)

// activityMomentAppTimes carries the same source allocation into per-app rows.
type activityMomentAppTimes map[string]map[string]ActivityAppUsage

// activityMomentTimeMs splits a day's "at the computer" time across moments.
// It counts exactly what the day line counts (getDailySummary; both go
// through activityPresentTimeMs): each session's length inside the day is
// shared among the runs it contains, by how much of the session each run
// covers, and a run's share goes to the moment that holds it. A
// between-sessions gap the day line counts goes to the moment before it when
// both sides are in the same thread, and is split in half between the two
// sides' moments otherwise. Time in runs that belong to no moment is reported
// under the empty id, as is idle presence outside a crossing run's source
// intervals. Crossing moments use the recorded active counter and its exact
// source intervals rather than redistributing idle presence into them. Other
// moments and the returned presence total retain the existing day-line rule.
// Optional appTimes is filled from the same run shares, without another read.
func (a *App) activityMomentTimeMs(ctx context.Context, startMs, endMs int64, appTimes ...activityMomentAppTimes) (map[string]int64, int64, error) {
	store := a.activity.store
	sessions, err := store.modelSessionsOverlapping(startMs, endMs)
	if err != nil {
		return nil, 0, err
	}
	sessions = a.filterActivityModelSessionsContext(ctx, store, sessions)
	presentMs, gaps, err := a.activityPresentTimeMs(ctx, store, sessions, startMs, endMs)
	if err != nil {
		return nil, 0, err
	}
	type runShare struct {
		start, end      int64
		activity        string
		appKey, appName string
	}
	bySession := map[string][]runShare{}
	type crossingRun struct {
		share  runShare
		source ActivityTimelineRunRow
	}
	var crossings []crossingRun
	crossTime := map[string]int64{}
	crossApps := activityMomentAppTimes{}
	if len(sessions) > 0 {
		rows, err := store.reader().QueryContext(ctx, `SELECT g.start_ms, g.end_ms, g.session_ids, COALESCE(l.activity_id, ''), g.app_key, g.app_name, g.evidence_excluded,
            EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(g.session_ids) THEN g.session_ids ELSE '[]' END) j JOIN activity_sessions s ON s.id=j.value WHERE s.triage_status='protected'), g.id, g.active_ms, g.first_event_row_id, g.last_event_row_id, COALESCE(a.start_ms,0), COALESCE(a.end_ms,0)
			FROM activity_timeline_groups g LEFT JOIN activity_run_links l ON l.run_id = g.id LEFT JOIN activities a ON a.id=l.activity_id
			WHERE g.end_ms >= ? AND g.start_ms < ? AND json_valid(g.session_ids)`, startMs-activityThreadDayMs, endMs)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var run runShare
			var sessionIDs string
			var excluded, protected bool
			var source ActivityTimelineRunRow
			var momentStart, momentEnd int64
			if err := rows.Scan(&run.start, &run.end, &sessionIDs, &run.activity, &run.appKey, &run.appName, &excluded, &protected, &source.ID, &source.ActiveMs, &source.FirstEventRowID, &source.LastEventRowID, &momentStart, &momentEnd); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if excluded || protected || activityEvidenceAppExcluded(run.appKey, run.appName) {
				// Keep its temporal weight so withheld source time cannot be
				// reassigned to a different visible moment in this session.
				run.activity, run.appKey, run.appName = "", "", ""
			}
			var ids []string
			_ = json.Unmarshal([]byte(sessionIDs), &ids)
			if run.activity != "" && (momentStart < startMs || momentEnd > endMs) {
				source.StartMs, source.EndMs, source.AppKey, source.AppName, source.SessionIDs = run.start, run.end, run.appKey, run.appName, ids
				crossings = append(crossings, crossingRun{run, source})
				crossTime[run.activity] = 0
			}
			for _, id := range ids {
				bySession[id] = append(bySession[id], run)
			}
		}
		if err := firstErr(rows.Err(), rows.Close()); err != nil {
			return nil, 0, err
		}
	}
	// Release the reader lease before sparse event replay (single-reader pools).
	for _, item := range crossings {
		active, err := store.activityRunDayActiveMs(ctx, item.source, startMs, endMs)
		if err != nil {
			return nil, 0, err
		}
		crossTime[item.share.activity] += active
		if crossApps[item.share.activity] == nil {
			crossApps[item.share.activity] = map[string]ActivityAppUsage{}
		}
		app := crossApps[item.share.activity][item.share.appKey]
		app.AppKey, app.AppName = item.share.appKey, item.share.appName
		app.ActiveMs += active
		crossApps[item.share.activity][item.share.appKey] = app
	}
	out := map[string]int64{}
	add := func(run runShare, ms int64) {
		out[run.activity] += ms
		if len(appTimes) == 0 || ms == 0 {
			return
		}
		if appTimes[0][run.activity] == nil {
			appTimes[0][run.activity] = map[string]ActivityAppUsage{}
		}
		usage := appTimes[0][run.activity][run.appKey]
		usage.AppKey, usage.AppName = run.appKey, run.appName
		usage.ActiveMs += ms
		appTimes[0][run.activity][run.appKey] = usage
	}
	for _, session := range sessions {
		sessionStart, sessionEnd := max(session.StartTime, startMs), min(session.EndTime, endMs)
		length := sessionEnd - sessionStart
		if length <= 0 {
			continue
		}
		runs := bySession[session.ID]
		if len(runs) == 0 {
			out[""] += length
			continue
		}
		weights := make([]int64, len(runs))
		var sum int64
		for i, run := range runs {
			weights[i] = max(int64(0), min(sessionEnd, run.end)-max(sessionStart, run.start))
			sum += weights[i]
		}
		if sum == 0 {
			// A run whose span sits outside the clipped session still owns its
			// session: share equally.
			for i := range weights {
				weights[i] = 1
			}
			sum = int64(len(weights))
		}
		var given int64
		largest := 0
		for i, run := range runs {
			share := length * weights[i] / sum
			add(run, share)
			given += share
			if weights[i] > weights[largest] {
				largest = i
			}
		}
		add(runs[largest], length-given)
	}
	if len(gaps) > 0 {
		// A gap's time belongs to the moments touching its edges: the run
		// ending last in the session before it, the run starting first in the
		// session after it.
		edgeActivity := func(runs []runShare, last bool) runShare {
			if len(runs) == 0 {
				return runShare{}
			}
			pick := 0
			for i, run := range runs {
				switch true {
				case last && run.end >= runs[pick].end:
					pick = i
				case !last && run.start < runs[pick].start:
					pick = i
				}
			}
			return runs[pick]
		}
		_, members, err := store.loadActivityThreads(ctx, startOfDayLocal(startMs))
		if err != nil {
			return nil, 0, err
		}
		threadOf := make(map[string]string, len(members))
		for _, m := range members {
			threadOf[m.ActivityID] = m.ThreadID
		}
		for _, gap := range gaps {
			before := edgeActivity(bySession[gap.beforeSession], true)
			after := edgeActivity(bySession[gap.afterSession], false)
			length := gap.endMs - gap.startMs
			switch true {
			case threadOf[before.activity] == threadOf[after.activity]:
				// One thread on both sides (or none): the person stayed on
				// the same work, so the gap belongs to it.
				add(before, length)
			default:
				half := length / 2
				add(before, half)
				add(after, length-half)
			}
		}
	}
	for id, active := range crossTime {
		// Session presence outside the source's bounded active intervals is
		// unowned; it must not inflate the source activity on either day.
		out[""] += max(int64(0), out[id]-active)
		out[id] = active
		if len(appTimes) > 0 {
			appTimes[0][id] = crossApps[id]
		}
	}
	return out, presentMs, nil
}
