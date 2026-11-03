package main

import (
	"context"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
)

type holdActivity struct{ Start, End time.Time }
type holdWorkHours struct {
	Start, End time.Duration
	Worked     [7]bool
}

// inferHoldWorkHours uses only non-protected session times. With no recent
// activity, the handoff's 9 AM to 6 PM default applies.
func inferHoldWorkHours(activity []holdActivity, now time.Time) holdWorkHours {
	profile := holdWorkHours{Start: 9 * time.Hour, End: 18 * time.Hour}
	first, last := 24*time.Hour, time.Duration(0)
	seen := false
	for _, item := range activity {
		start, end := item.Start.In(now.Location()), item.End.In(now.Location())
		if start.After(now) || end.Before(now.AddDate(0, 0, -28)) || !end.After(start) {
			continue
		}
		profile.Worked[start.Weekday()] = true
		if end.Before(now.AddDate(0, 0, -14)) {
			continue
		}
		startTime := time.Duration(start.Hour())*time.Hour + time.Duration(start.Minute())*time.Minute
		endTime := time.Duration(end.Hour())*time.Hour + time.Duration(end.Minute())*time.Minute
		if start.YearDay() != end.YearDay() || start.Year() != end.Year() {
			continue
		}
		if startTime < first {
			first = startTime
		}
		if endTime > last {
			last = endTime
		}
		seen = true
	}
	if seen && last-first >= 30*time.Minute {
		profile.Start, profile.End = first, last
	}
	return profile
}

func (a *App) holdActivityProfile(now time.Time) holdWorkHours {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return inferHoldWorkHours(nil, now)
	}
	rows, err := a.activity.store.GetFilteredSessions(2000, 0, now.AddDate(0, 0, -28).UnixMilli(), now.UnixMilli(), "")
	if err != nil {
		return inferHoldWorkHours(nil, now)
	}
	exclusions, err := a.activity.store.ListFirstRunExclusions("")
	if err != nil {
		return inferHoldWorkHours(nil, now)
	}
	return inferHoldWorkHours(holdActivityFromSessions(rows, exclusions), now)
}

func holdActivityFromSessions(rows []ActivitySessionRow, exclusions []FirstRunExclusionRecord) []holdActivity {
	excluded := make(map[string]bool)
	for _, entry := range exclusions {
		if entry.SourceKind == "activity_session" {
			excluded[entry.SourceID] = true
		}
	}
	activity := make([]holdActivity, 0, len(rows))
	for _, row := range rows {
		if !excluded[row.ID] {
			activity = append(activity, holdActivity{Start: time.UnixMilli(row.StartTime), End: time.UnixMilli(row.EndTime)})
		}
	}
	return activity
}

// holdTimeWindows shares the cached event and quiet-hour rules with Find time,
// while using observed work hours and the handoff's weekend exception.
func holdTimeWindows(events []calendar.Event, now, due time.Time, length time.Duration, quiet engagement.QuietWindow, profile holdWorkHours) ([]FindTimeRange, error) {
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var result []FindTimeRange
	futureWorkDays := 0
	for offset := 0; offset < 8 && len(result) < 4; offset++ {
		date := day.AddDate(0, 0, offset)
		weekend := date.Weekday() == time.Saturday || date.Weekday() == time.Sunday
		if weekend && !profile.Worked[date.Weekday()] {
			// A deadline before the next Monday leaves no later weekday.
			daysToMonday := (8 - int(date.Weekday())) % 7
			nextMonday := holdTimeOnDay(date.AddDate(0, 0, daysToMonday), profile.Start)
			if due.IsZero() || due.After(nextMonday) {
				continue
			}
			previous, err := holdSlots(result, now, due, length)
			if err != nil {
				return nil, err
			}
			if len(previous) > 0 {
				continue
			}
		}
		if offset > 0 {
			futureWorkDays++
			if futureWorkDays > 2 {
				break
			}
		}
		start, end := holdTimeOnDay(date, profile.Start), holdTimeOnDay(date, profile.End)
		if now.After(start) {
			start = ceilFindTimeHalfHour(now)
		}
		if !due.IsZero() && due.Before(end) {
			end = due
		}
		if !end.After(start) {
			continue
		}
		blocks, err := calendar.FreeBlocks(events, start, end, 30*time.Minute)
		if err != nil {
			return nil, err
		}
		for _, block := range blocks {
			block.Start = ceilFindTimeHalfHour(block.Start.In(loc))
			block.End = floorFindTimeHalfHour(block.End.In(loc))
			var run time.Time
			flush := func(until time.Time) {
				if !run.IsZero() && until.Sub(run) >= 30*time.Minute && len(result) < 4 {
					result = append(result, FindTimeRange{Start: run.Format(time.RFC3339), End: until.Format(time.RFC3339)})
				}
				run = time.Time{}
			}
			for at := block.Start; at.Before(block.End); at = at.Add(time.Minute) {
				if quiet.Contains(at) {
					flush(at)
				} else if run.IsZero() {
					run = at
				}
			}
			flush(block.End)
		}
	}
	return holdMeetingGaps(result, events)
}

func holdTimeOnDay(date time.Time, sinceMidnight time.Duration) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), int(sinceMidnight/time.Hour), int((sinceMidnight%time.Hour)/time.Minute), 0, 0, date.Location())
}

func (a *App) holdWindows(now, due time.Time, length time.Duration, granted bool) ([]FindTimeRange, error) {
	// The ordinary reader enforces permission, source privacy, snapshot age,
	// and browser coverage before we use the same cached events.
	if _, err := a.findTimeWithAccess(context.Background(), now, granted); err != nil {
		return nil, err
	}
	events, err := a.listCalendarEvents(context.Background(), now, now.AddDate(0, 0, 8))
	if err != nil {
		return nil, err
	}
	quiet := engagement.DefaultPrefs().Quiet
	if a.engagement != nil {
		quiet = a.engagement.Prefs().Quiet
	}
	return holdTimeWindows(events, now, due, length, quiet, a.holdActivityProfile(now))
}
