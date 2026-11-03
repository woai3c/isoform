package main

import (
	"errors"
	"time"

	"wails-gui/internal/calendar"
)

// holdSlots uses the existing free-time windows and returns a proposed slot
// followed by at most three alternatives. Length is rounded up to half-hours.
func holdSlots(windows []FindTimeRange, now, due time.Time, length time.Duration) ([]FindTimeRange, error) {
	if length <= 0 {
		length = time.Hour
	}
	if length > 2*time.Hour {
		length = 2 * time.Hour
	}
	length = ((length + 30*time.Minute - 1) / (30 * time.Minute)) * (30 * time.Minute)
	var result []FindTimeRange
	for _, window := range windows {
		start, errStart := time.Parse(time.RFC3339, window.Start)
		end, errEnd := time.Parse(time.RFC3339, window.End)
		if errStart != nil || errEnd != nil || !end.After(start) {
			return nil, errors.New("invalid free-time window")
		}
		if start.Before(now) {
			start = now
		}
		start = ceilFindTimeHalfHour(start)
		if !due.IsZero() && due.Before(end) {
			end = due
		}
		for at := start; !at.Add(length).After(end) && len(result) < 4; at = at.Add(length) {
			result = append(result, FindTimeRange{Start: at.Format(time.RFC3339), End: at.Add(length).Format(time.RFC3339)})
		}
		if len(result) == 4 {
			break
		}
	}
	return result, nil
}

// holdMeetingGaps trims the end of free windows so a hold cannot end within
// ten minutes of a meeting with other people.
func holdMeetingGaps(windows []FindTimeRange, events []calendar.Event) ([]FindTimeRange, error) {
	out := make([]FindTimeRange, 0, len(windows))
	for _, window := range windows {
		start, startErr := time.Parse(time.RFC3339, window.Start)
		end, endErr := time.Parse(time.RFC3339, window.End)
		if startErr != nil || endErr != nil || !end.After(start) {
			return nil, errors.New("invalid free-time window")
		}
		for _, event := range events {
			if event.Busy() && event.HasOthers() && !event.Start.Before(end) && event.Start.Before(end.Add(10*time.Minute)) {
				end = floorFindTimeHalfHour(event.Start.Add(-10 * time.Minute))
			}
		}
		if end.After(start) {
			out = append(out, FindTimeRange{Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)})
		}
	}
	return out, nil
}
