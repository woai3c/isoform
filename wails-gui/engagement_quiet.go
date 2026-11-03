package main

import (
	"context"
	"sort"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// QuietLineInterval is one stretch a quiet kind reports for a day, in RFC3339
// UTC. End is absent while open or when recovery was not observed.
type QuietLineInterval struct {
	Start string `json:"start"`
	End   string `json:"end,omitempty"`
}

// QuietLine is one quiet kind's day on Today: when its moments happened and
// how many were delivered on the quiet channel. capture_paused also carries
// the day's real pause and outage intervals. The UI writes the sentence from
// these fields.
type QuietLine struct {
	Kind      string              `json:"kind"`
	FirstAt   string              `json:"firstAt"`
	LastAt    string              `json:"lastAt"`
	Count     int                 `json:"count"`
	Intervals []QuietLineInterval `json:"intervals"`
}

// GetTodayQuietLines returns today's quiet lines: one per kind that shows on
// Today instead of interrupting and had something to say today, in the
// notification-settings order. Before the service exists the answer is an
// empty list.
func (a *App) GetTodayQuietLines() (perfValue []QuietLine, perfErr error) {
	defer logPageBinding("GetTodayQuietLines", time.Now(), &perfValue)
	svc := a.engagementService()
	if svc == nil {
		return []QuietLine{}, nil
	}
	return a.todayQuietLines(context.Background(), svc)
}

func (a *App) todayQuietLines(ctx context.Context, svc *engagementService) ([]QuietLine, error) {
	prefs := svc.Prefs()
	now := svc.now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayEnd := midnight.AddDate(0, 0, 1)
	// The upper bound follows the local calendar, including DST transitions.
	until := now.Add(time.Millisecond)
	if until.After(dayEnd) {
		until = dayEnd
	}
	rows, err := svc.store.QuietEngagementMomentsBetween(ctx, midnight, until)
	if err != nil {
		return nil, err
	}
	byKind := map[engagement.Kind][]storage.EngagementMomentRow{}
	for _, row := range rows {
		kind := engagement.Kind(row.Kind)
		if prefs.KindQuietOnToday(kind) {
			byKind[kind] = append(byKind[kind], row)
		}
	}
	// A deliberate pause produces no moment; capture_paused shows the pause
	// log's real stretches alongside its outage moments.
	var pauses []observationPause
	var outages []engagementCaptureOutageInterval
	if prefs.KindQuietOnToday(engagement.KindCapturePaused) {
		outages, err = a.captureOutageIntervals(ctx)
		if err != nil {
			return nil, err
		}
		pauses, err = a.observationPausesContext(ctx)
		if err != nil {
			return nil, err
		}
	}
	lines := []QuietLine{}
	for _, kind := range engagement.AllKinds() {
		if engagementKindSilent(kind) || !prefs.KindQuietOnToday(kind) {
			continue
		}
		moments := byKind[kind]
		intervals := []QuietLineInterval{}
		var first, last time.Time
		note := func(at time.Time) {
			if first.IsZero() || at.Before(first) {
				first = at
			}
			if at.After(last) {
				last = at
			}
		}
		for _, row := range moments {
			at := time.UnixMilli(row.CreatedAt)
			if row.CreatedAt == 0 {
				at = time.UnixMilli(row.SentAt)
			}
			note(at)
			if kind == engagement.KindCapturePaused {
				interval := QuietLineInterval{Start: at.UTC().Format(time.RFC3339)}
				for _, outage := range outages {
					if outage.Source == row.SubjectID && outage.Start == row.CreatedAt && outage.End != 0 {
						interval.End = time.UnixMilli(outage.End).UTC().Format(time.RFC3339)
						note(time.UnixMilli(outage.End))
						break
					}
				}
				intervals = append(intervals, interval)
			}
		}
		if kind == engagement.KindCapturePaused {
			for _, p := range pauses {
				if p.Start >= dayEnd.UnixMilli() || p.Start > now.UnixMilli() || (p.End != 0 && p.End <= midnight.UnixMilli()) {
					continue
				}
				interval := QuietLineInterval{Start: time.UnixMilli(p.Start).UTC().Format(time.RFC3339)}
				note(time.UnixMilli(p.Start))
				if p.End != 0 {
					interval.End = time.UnixMilli(p.End).UTC().Format(time.RFC3339)
					note(time.UnixMilli(p.End))
				}
				intervals = append(intervals, interval)
			}
			// RFC3339 UTC sorts chronologically.
			sort.Slice(intervals, func(i, j int) bool { return intervals[i].Start < intervals[j].Start })
		}
		if len(moments) == 0 && len(intervals) == 0 {
			continue
		}
		lines = append(lines, QuietLine{
			Kind:      string(kind),
			FirstAt:   first.UTC().Format(time.RFC3339),
			LastAt:    last.UTC().Format(time.RFC3339),
			Count:     len(moments),
			Intervals: intervals,
		})
	}
	return lines, nil
}
