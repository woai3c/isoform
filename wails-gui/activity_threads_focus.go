package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

// A bar in Stitched by work is a stretch of focused work (activity review
// v1.0.0 P0 #2). A moment's span can hold hours of idle time, overnight
// included, so stretches are drawn from the moments' run intervals instead:
// idle longer than the gap the day line still counts as present
// (activityPresentGapMaxMs) ends a stretch, and nothing outside the day being
// read is drawn. A thread carried in from yesterday therefore starts at its
// first stretch today, never at midnight.
const activityThreadFocusGapMs = activityPresentGapMaxMs

// activityLockScreenApp is the lock screen or screen saver: never work, never an app.
func activityLockScreenApp(appName, appKey string) bool {
	key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(appKey), "app:"))
	switch strings.ToLower(strings.TrimSpace(appName)) {
	case "loginwindow", "screensaverengine", "screen saver engine":
		return true
	}
	switch key {
	case "loginwindow", "com.apple.loginwindow", "com.apple.screensaver.engine", "name:loginwindow":
		return true
	}
	return false
}

// activityThreadFocusIntervals returns when a moment's work happened inside
// [start, end). Continuous runs are their span; a sparse run whose missing
// time could hide an away gap is replayed from its events. Legacy moments
// without readable runs keep their span, unless that span crosses into the
// day from before it, which could only be guessed.
func (a *App) activityThreadFocusIntervals(ctx context.Context, row ActivityRow, evidence *activityThreadDayEvidence, start, end int64) ([]activityThreadInterval, error) {
	var out []activityThreadInterval
	add := func(interval activityThreadInterval) {
		interval.start, interval.end = max(interval.start, start), min(interval.end, end)
		if interval.end > interval.start {
			out = append(out, interval)
		}
	}
	for _, id := range composeParseStrings(row.RunIDs) {
		run, ok := evidence.runs[id]
		if !ok || !evidence.allowed[id] || activityLockScreenApp(run.AppName, run.AppKey) || run.EndMs < start || run.StartMs >= end {
			continue
		}
		spans := []activityThreadInterval{{start: run.StartMs, end: max(run.EndMs, run.StartMs+min(run.ActiveMs, 1000))}}
		if run.EndMs-run.StartMs-run.ActiveMs > activityThreadFocusGapMs-timelineGroupActiveGapCapMs {
			replayed, err := a.activity.store.activityRunActiveIntervals(ctx, run)
			switch {
			case err == nil:
				spans = replayed
			case ctx.Err() != nil:
				return nil, ctx.Err()
			default:
				// Unproven source intervals: draw the recorded active time
				// where it ended rather than across the idle span.
				spans = []activityThreadInterval{{start: max(run.StartMs, run.EndMs-run.ActiveMs), end: run.EndMs}}
			}
		}
		for _, span := range spans {
			add(span)
		}
	}
	if len(out) == 0 && row.StartMs >= start {
		add(activityThreadInterval{start: row.StartMs, end: max(row.EndMs, row.StartMs+1000)})
	}
	return mergeActivityThreadIntervals(out, 0), nil
}

// mergeActivityThreadIntervals joins intervals separated by at most tolerance.
func mergeActivityThreadIntervals(intervals []activityThreadInterval, tolerance int64) []activityThreadInterval {
	if len(intervals) == 0 {
		return nil
	}
	sorted := append([]activityThreadInterval(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].start != sorted[j].start {
			return sorted[i].start < sorted[j].start
		}
		return sorted[i].end < sorted[j].end
	})
	out := []activityThreadInterval{sorted[0]}
	for _, interval := range sorted[1:] {
		last := &out[len(out)-1]
		if interval.start-last.end <= tolerance {
			last.end = max(last.end, interval.end)
			continue
		}
		out = append(out, interval)
	}
	return out
}

// activityThreadFocusStretches keeps the stretch rule (a run of a thread's
// moments with no other thread's moment between them) and draws each run as
// its focused intervals, split where the person was away. A stretch's active
// time is its moments' day-owned time, shared by how much of each moment's
// focused time falls in it, so a thread's stretches still sum to its time.
func (a *App) activityThreadFocusStretches(ctx context.Context, views []ActivityThreadView, live map[string]ActivityRow, spent map[string]int64, evidence *activityThreadDayEvidence, start, end int64) (map[string][]ActivityThreadStretch, error) {
	var ordered []activityThreadTimedMoment
	for _, view := range views {
		for _, id := range view.ActivityIDs {
			if row, ok := live[id]; ok {
				ordered = append(ordered, activityThreadTimedMoment{row: row, threadID: view.ID})
			}
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].row.StartMs < ordered[j].row.StartMs })

	type group struct {
		threadID string
		rows     []ActivityRow
	}
	var groups []group
	lastGroup := map[string]int{}
	lastIndex := map[string]int{}
	for i, moment := range ordered {
		previous, ok := lastIndex[moment.threadID]
		if !ok || i != previous+1 {
			groups = append(groups, group{threadID: moment.threadID})
			lastGroup[moment.threadID] = len(groups) - 1
		}
		g := &groups[lastGroup[moment.threadID]]
		g.rows = append(g.rows, moment.row)
		lastIndex[moment.threadID] = i
	}

	focus := make(map[string][]activityThreadInterval, len(ordered))
	for _, moment := range ordered {
		intervals, err := a.activityThreadFocusIntervals(ctx, moment.row, evidence, start, end)
		if err != nil {
			return nil, err
		}
		focus[moment.row.ID] = intervals
	}

	out := map[string][]ActivityThreadStretch{}
	for _, g := range groups {
		var all []activityThreadInterval
		for _, row := range g.rows {
			all = append(all, focus[row.ID]...)
		}
		merged := mergeActivityThreadIntervals(all, activityThreadFocusGapMs)
		stretches := make([]ActivityThreadStretch, len(merged))
		for i, interval := range merged {
			stretches[i] = ActivityThreadStretch{Start: interval.start, End: interval.end, ActivityIDs: []string{}, idleBefore: i > 0}
		}
		for _, row := range g.rows {
			var total int64
			overlaps := make([]int64, len(merged))
			for i, interval := range merged {
				for _, own := range focus[row.ID] {
					overlaps[i] += max(int64(0), min(interval.end, own.end)-max(interval.start, own.start))
				}
				total += overlaps[i]
			}
			if total == 0 {
				continue
			}
			var given int64
			lastShare := -1
			for i, overlap := range overlaps {
				if overlap == 0 {
					continue
				}
				share := spent[row.ID] * overlap / total
				stretches[i].ActiveMs += share
				stretches[i].ActivityIDs = append(stretches[i].ActivityIDs, row.ID)
				given += share
				lastShare = i
			}
			stretches[lastShare].ActiveMs += spent[row.ID] - given
		}
		out[g.threadID] = append(out[g.threadID], stretches...)
	}
	for id := range out {
		sort.SliceStable(out[id], func(i, j int) bool { return out[id][i].Start < out[id][j].Start })
	}
	return out, nil
}

// activityThreadSetStretches sets a view's stretches with their resume points
// and the gaps between them, labeled by what filled each gap.
func activityThreadSetStretches(view *ActivityThreadView, stretches []ActivityThreadStretch, live map[string]ActivityRow, evidence *activityThreadDayEvidence, sources activityThreadGapOccupancy, titles map[string]string, broken func(start, end int64) bool) {
	view.Stretches = stretches
	if view.Stretches == nil {
		view.Stretches = []ActivityThreadStretch{}
	}
	view.Gaps = []ActivityThreadGap{}
	for j := range view.Stretches {
		stretch := &view.Stretches[j]
		lastID := stretch.ActivityIDs[len(stretch.ActivityIDs)-1]
		stretch.ResumePoint = activityThreadResumePointFromEvidence(live[lastID], evidence)
		if j == 0 {
			continue
		}
		previous := view.Stretches[j-1]
		start, end := previous.End, stretch.Start
		if end < start {
			start = end
		}
		label, threadID := sources.label(start, end, view.ID, titles)
		view.Gaps = append(view.Gaps, ActivityThreadGap{
			Start: start, End: end, Minutes: int((end - start) / int64(time.Minute/time.Millisecond)),
			Label: label, ThreadID: threadID,
			Idle: stretch.idleBefore && label == "away" && threadID == "" && !broken(start, end),
		})
	}
}

// carryActivityThreadStretches redraws threads that began on an earlier day
// for the day being read: their stretches are the focused work inside this
// day, and their gaps are labeled by what this day's moments did then.
func (a *App) carryActivityThreadStretches(ctx context.Context, day, origin int64, carried []ActivityThreadView, spent map[string]int64, current []ActivityThreadView) error {
	store := a.activity.store
	end := activityThreadDayEnd(day)
	members := map[string]bool{}
	for _, view := range carried {
		for _, id := range view.ActivityIDs {
			members[id] = true
		}
	}
	originRows, err := store.dayActivityRows(ctx, origin)
	if err != nil {
		return err
	}
	live := map[string]ActivityRow{}
	var liveRows []ActivityRow
	for _, row := range a.filterComposerModelCandidates(store, originRows) {
		if members[row.ID] {
			live[row.ID] = row
			liveRows = append(liveRows, row)
		}
	}
	evidence, err := a.activityThreadDayEvidence(ctx, liveRows)
	if err != nil {
		return err
	}
	stretches, err := a.activityThreadFocusStretches(ctx, carried, live, spent, evidence, day, end)
	if err != nil {
		return err
	}
	dayRows, err := store.dayActivityRows(ctx, day)
	if err != nil {
		return err
	}
	dayRows = a.filterComposerModelCandidates(store, dayRows)
	_, dayMembers, err := store.loadActivityThreads(ctx, day)
	if err != nil {
		return err
	}
	memberByActivity := make(map[string]activityThreadMember, len(dayMembers))
	for _, member := range dayMembers {
		memberByActivity[member.ActivityID] = member
	}
	viewIndex := make(map[string]int, len(current))
	titles := make(map[string]string, len(current)+len(carried))
	for i, view := range current {
		viewIndex[view.ID] = i
		titles[view.ID] = view.Title
	}
	sources := activityThreadGapSources(dayRows, memberByActivity, viewIndex)
	broken, err := a.activityThreadAwayEvidence(ctx, day, end)
	if err != nil {
		return err
	}
	for i := range carried {
		activityThreadSetStretches(&carried[i], stretches[carried[i].ID], live, evidence, sources, titles, broken)
	}
	return nil
}

// activityThreadAwayEvidence reports whether the computer was locked, asleep
// or not observing during a gap: the same evidence the day line uses. It is
// read once, and only when some gap could be idle; an unreadable read counts
// as away, so an unknown gap stays an interruption.
func (a *App) activityThreadAwayEvidence(ctx context.Context, start, end int64) (func(start, end int64) bool, error) {
	var breaks *activityPresenceBreak
	return func(from, to int64) bool {
		if breaks == nil {
			read, err := a.activityPresenceBreaks(ctx, a.activity.store, start, end)
			if err != nil {
				read = activityPresenceBreak{pausesErr: err}
			}
			breaks = &read
		}
		return breaks.broken(from, to)
	}, nil
}
