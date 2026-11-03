package main

import (
	"context"
	"encoding/json"
	"time"
)

const engagementCaptureIntervalsKey = "engagement_capture_intervals"

// Capture health samples supply observed endpoints, never an inferred recovery.
// End stays zero if recovery was not observed (including across a restart).
type engagementCaptureOutageInterval struct {
	Source string `json:"source"`
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
}

func (a *App) captureOutageIntervals(ctx context.Context) ([]engagementCaptureOutageInterval, error) {
	raw, err := a.store.GetUIState(ctx, engagementCaptureIntervalsKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var intervals []engagementCaptureOutageInterval
	err = json.Unmarshal([]byte(raw), &intervals)
	return intervals, err
}

// The capture watcher is the only writer. Keep the same short retention and
// bound as the pause log, but never coalesce intervals into invented outages.
func (a *App) recordCaptureOutageIntervals(ctx context.Context, due, ended []engagementCaptureOutage, now time.Time) error {
	if a.store == nil || (len(due) == 0 && len(ended) == 0) {
		return nil
	}
	intervals, err := a.captureOutageIntervals(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, outage := range due {
		found := false
		for _, interval := range intervals {
			if interval.Source == outage.Kind && interval.Start == outage.Start.UnixMilli() {
				found = true
				break
			}
		}
		if !found {
			intervals = append(intervals, engagementCaptureOutageInterval{Source: outage.Kind, Start: outage.Start.UnixMilli()})
			changed = true
		}
	}
	for i := range intervals {
		for _, outage := range ended {
			if intervals[i].Source == outage.Kind && intervals[i].Start == outage.Start.UnixMilli() && intervals[i].End == 0 {
				intervals[i].End = now.UnixMilli()
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	cutoff := now.Add(-observationPausesKept).UnixMilli()
	kept := intervals[:0]
	for _, interval := range intervals {
		if interval.Start >= cutoff || interval.End >= cutoff {
			kept = append(kept, interval)
		}
	}
	if len(kept) > observationPausesMax {
		kept = kept[len(kept)-observationPausesMax:]
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return a.store.SetUIState(ctx, engagementCaptureIntervalsKey, string(raw))
}
