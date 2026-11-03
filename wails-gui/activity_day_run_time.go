package main

import (
	"context"
	"errors"
	"slices"
)

// A run wholly inside the day owns its recorded active duration. Continuous
// crossings clip exactly. A sparse crossing needs its original event intervals;
// never spread the aggregate uniformly across an idle overnight span.
func (s *ActivityStore) activityRunDayActiveMs(ctx context.Context, run ActivityTimelineRunRow, start, end int64) (int64, error) {
	if run.EndMs < start || (run.EndMs == start && run.StartMs < run.EndMs) || run.StartMs >= end {
		return 0, nil
	}
	if run.StartMs >= start && run.EndMs <= end {
		return run.ActiveMs, nil
	}
	if run.ActiveMs == run.EndMs-run.StartMs {
		return max(int64(0), min(end, run.EndMs)-max(start, run.StartMs)), nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	allowed, err := s.timelineRunEvidenceAllowed(run)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, errors.New("ACTIVITY_DAY_TIME_UNAVAILABLE")
	}

	events, err := scanTimelineRunEventsFromRowID(s.reader(), run.FirstEventRowID, run.LastEventRowID)
	if err != nil {
		return 0, err
	}
	allowed, err = s.timelineRunEvidenceAllowed(run)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, errors.New("ACTIVITY_DAY_TIME_UNAVAILABLE")
	}
	sortTimelineRunEvents(events)
	day := &timelineRunDayAllocation{start: start, end: end, active: map[string]int64{}}
	for _, source := range deriveTimelineRuns(events, day) {
		actual, declared := slices.Clone(source.SessionIDs), slices.Clone(run.SessionIDs)
		slices.Sort(actual)
		slices.Sort(declared)
		if !slices.Equal(actual, declared) {
			continue
		}
		if source.FirstEventRowID == run.FirstEventRowID && source.LastEventRowID == run.LastEventRowID && source.AppKey == run.AppKey && source.ActiveMs == run.ActiveMs && source.StartMs == run.StartMs && source.EndMs == run.EndMs {
			return day.active[source.ID], ctx.Err()
		}
	}
	// Missing/stale source intervals cannot license guessed day ownership.
	return 0, errors.New("ACTIVITY_DAY_TIME_UNAVAILABLE")
}

// activityRunActiveIntervals reconstructs only the timestamped contributions
// to a run's active counter. Sparse counters cannot be spread over their span.
func (s *ActivityStore) activityRunActiveIntervals(ctx context.Context, run ActivityTimelineRunRow) ([]activityThreadInterval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allowed, err := s.timelineRunEvidenceAllowed(run)
	if err != nil || !allowed {
		return nil, errors.New("ACTIVITY_DAY_TIME_UNAVAILABLE")
	}
	if run.ActiveMs == run.EndMs-run.StartMs && run.EndMs > run.StartMs {
		return []activityThreadInterval{{start: run.StartMs, end: run.EndMs}}, nil
	}
	events, err := scanTimelineRunEventsFromRowID(s.reader(), run.FirstEventRowID, run.LastEventRowID)
	if err != nil {
		return nil, err
	}
	allowed, err = s.timelineRunEvidenceAllowed(run)
	if err != nil || !allowed {
		return nil, errors.New("ACTIVITY_DAY_TIME_UNAVAILABLE")
	}
	sortTimelineRunEvents(events)
	allocation := &timelineRunDayAllocation{start: run.StartMs, end: run.EndMs + 1000, active: map[string]int64{}, spans: map[string][]activityThreadInterval{}}
	for _, source := range deriveTimelineRuns(events, allocation) {
		actual, declared := slices.Clone(source.SessionIDs), slices.Clone(run.SessionIDs)
		slices.Sort(actual)
		slices.Sort(declared)
		if slices.Equal(actual, declared) && source.FirstEventRowID == run.FirstEventRowID && source.LastEventRowID == run.LastEventRowID && source.AppKey == run.AppKey && source.ActiveMs == run.ActiveMs && source.StartMs == run.StartMs && source.EndMs == run.EndMs {
			return allocation.spans[source.ID], ctx.Err()
		}
	}
	return nil, errors.New("ACTIVITY_DAY_TIME_UNAVAILABLE")
}
