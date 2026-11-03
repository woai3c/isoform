package calendar

import (
	"errors"
	"sort"
	"time"
)

// FreeBlocks returns the gaps in [start, end) after subtracting busy events.
// A malformed busy event makes the window uncertain instead of free.
func FreeBlocks(events []Event, start, end time.Time, minimum time.Duration) ([]Block, error) {
	if !end.After(start) || minimum <= 0 {
		return nil, errors.New("invalid free-time window")
	}
	busy := make([]Event, 0, len(events))
	for _, event := range events {
		if !event.Busy() || event.Status == "cancelled" || event.Availability == "transparent" {
			continue
		}
		if event.Start.IsZero() || !event.End.After(event.Start) {
			return nil, errors.New("calendar event has an invalid time")
		}
		if event.Overlaps(start, end) {
			busy = append(busy, event)
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].Start.Before(busy[j].Start) })
	result := []Block{}
	cursor := start
	for _, event := range busy {
		if event.Start.After(cursor) {
			until := event.Start
			if until.After(end) {
				until = end
			}
			if until.Sub(cursor) >= minimum {
				result = append(result, Block{Start: cursor, End: until})
			}
		}
		if event.End.After(cursor) {
			cursor = event.End
		}
		if !cursor.Before(end) {
			return result, nil
		}
	}
	if end.Sub(cursor) >= minimum {
		result = append(result, Block{Start: cursor, End: end})
	}
	return result, nil
}
