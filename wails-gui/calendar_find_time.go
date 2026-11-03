package main

import (
	"context"
	"errors"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
)

// FindTimeRange is a free interval in the computer's local time zone.
type FindTimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// findTimeWindows keeps answers inside work hours and outside quiet hours.
// It uses the same cached calendar events as Today, never EventKit itself.
func findTimeWindows(events []calendar.Event, now time.Time, quiet engagement.QuietWindow) ([]FindTimeRange, error) {
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	result := []FindTimeRange{}
	for offset := 0; offset < 7 && len(result) < 3; offset++ {
		date := day.AddDate(0, 0, offset)
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		start := time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, loc)
		end := time.Date(date.Year(), date.Month(), date.Day(), 18, 0, 0, 0, loc)
		if now.After(start) {
			start = ceilFindTimeHalfHour(now)
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
			if !block.End.After(block.Start) {
				continue
			}
			var run time.Time
			flush := func(until time.Time) {
				if !run.IsZero() && until.Sub(run) >= 30*time.Minute && len(result) < 3 {
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
	return result, nil
}

func floorFindTimeHalfHour(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute()/30*30, 0, 0, at.Location())
}

func ceilFindTimeHalfHour(at time.Time) time.Time {
	start := floorFindTimeHalfHour(at)
	if start.Before(at) {
		return start.Add(30 * time.Minute)
	}
	return start
}

// findTime reads only currently permitted calendar records. Withheld records
// make the answer unavailable: omitting a hidden conflict would imply free time.
func (a *App) findTime(ctx context.Context, now time.Time) ([]FindTimeRange, error) {
	return a.findTimeFromSources(ctx, now, true)
}

func (a *App) findTimeFromSources(ctx context.Context, now time.Time, requireComplete bool) ([]FindTimeRange, error) {
	events, err := a.findTimeEvents(ctx, now, now, now.AddDate(0, 0, 8), requireComplete)
	if err != nil {
		return nil, err
	}
	quiet := engagement.DefaultPrefs().Quiet
	if a.engagement != nil {
		quiet = a.engagement.Prefs().Quiet
	}
	return findTimeWindows(events, now, quiet)
}

// findTimeEvents checks the current calendar snapshot and every record in the
// requested window. The clock for freshness is always now, not the query date.
func (a *App) findTimeEvents(ctx context.Context, now, start, end time.Time, requireComplete bool) ([]calendar.Event, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("calendar unavailable")
	}
	policy, err := a.mcpReadPrivacy(ctx)
	if err != nil || !policy.calendar {
		return nil, errors.New("calendar unavailable")
	}
	connected, err := a.todayCalendarConnected(ctx)
	if err != nil || !connected {
		return nil, errors.New("calendar unavailable")
	}
	lastSync, err := a.store.GetUIState(ctx, calendarLastSyncKey)
	if err != nil {
		return nil, errors.New("calendar unavailable")
	}
	syncedAt, err := time.Parse(time.RFC3339, lastSync)
	if err != nil || syncedAt.After(now.Add(time.Minute)) || now.Sub(syncedAt) > 2*calendarSyncInterval || start.Before(syncedAt.Add(-calendarWindowBack)) || end.After(syncedAt.Add(calendarWindowAhead)) {
		return nil, errors.New("calendar unavailable")
	}
	sourceRows, err := a.store.ListCalendarSources(ctx)
	if err != nil {
		return nil, err
	}
	sources := make([]calendar.Source, 0, len(sourceRows))
	for _, row := range sourceRows {
		sources = append(sources, calendarSourceFromRow(row))
	}
	// The browser peek records events but not the dates it was able to see.
	// An uncovered browser calendar therefore cannot prove this window free.
	if requireComplete && len(calendar.WebCalendarsToPeek(sources, now, calendar.PeekRecentUse)) != 0 {
		return nil, errors.New("calendar unavailable")
	}
	subjects, err := mobileBriefMailSubjects(a)
	if err != nil {
		return nil, err
	}
	rows, err := a.store.ListCalendarEvents(ctx, start.UnixMilli(), end.UnixMilli(), false)
	if err != nil {
		return nil, err
	}
	events := make([]calendar.Event, 0, len(rows))
	for _, row := range rows {
		if !policy.calendarAllowed(row) || mobileBriefValueEchoesMail(row, subjects) {
			return nil, errors.New("calendar unavailable")
		}
		if row.StartAt <= 0 || row.EndAt <= row.StartAt {
			return nil, errors.New("calendar unavailable")
		}
		events = append(events, calendarEventFromRow(row))
	}
	return events, nil
}

// findTimeRangeWindows returns at most one complete free interval per weekday.
func findTimeRangeWindows(events []calendar.Event, now, start, end time.Time, length time.Duration, quiet engagement.QuietWindow, profile holdWorkHours) ([]FindTimeRange, error) {
	result := []FindTimeRange{}
	for day := start; day.Before(end) && len(result) < 3; day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		from, to := holdTimeOnDay(day, profile.Start), holdTimeOnDay(day, profile.End)
		if now.After(from) {
			from = ceilFindTimeHalfHour(now)
		}
		if !to.After(from) {
			continue
		}
		blocks, err := calendar.FreeBlocks(events, from, to, length)
		if err != nil {
			return nil, err
		}
		found := false
		for _, block := range blocks {
			from, to = ceilFindTimeHalfHour(block.Start.In(day.Location())), floorFindTimeHalfHour(block.End.In(day.Location()))
			var run time.Time
			for at := from; !at.After(to); at = at.Add(time.Minute) {
				if at.Equal(to) || quiet.Contains(at) {
					if !run.IsZero() && at.Sub(run) >= length {
						result = append(result, FindTimeRange{Start: run.Format(time.RFC3339), End: at.Format(time.RFC3339)})
						found = true
						break
					}
					run = time.Time{}
				} else if run.IsZero() {
					run = at
				}
			}
			if found {
				break
			}
		}
	}
	return result, nil
}

type FindTimeDetails struct {
	Ranges           []FindTimeRange `json:"ranges"`
	CheckedCalendars []string        `json:"checkedCalendars"`
	BrowserOnly      []string        `json:"browserOnly"`
}

func (a *App) findTimeDetails(ctx context.Context, now time.Time, granted bool, calendars []calendar.RawCalendar) (FindTimeDetails, error) {
	result := FindTimeDetails{Ranges: []FindTimeRange{}, CheckedCalendars: []string{}, BrowserOnly: []string{}}
	if !granted {
		return result, errors.New("calendar unavailable")
	}
	ranges, err := a.findTimeFromSources(ctx, now, false)
	if err != nil {
		return result, err
	}
	return a.findTimeDetailsCoverage(ctx, now, calendars, ranges)
}

func (a *App) findTimeDetailsCoverage(ctx context.Context, now time.Time, calendars []calendar.RawCalendar, ranges []FindTimeRange) (FindTimeDetails, error) {
	result := FindTimeDetails{Ranges: ranges, CheckedCalendars: []string{}, BrowserOnly: []string{}}
	seen := map[string]bool{}
	for _, cal := range calendars {
		if cal.Title != "" && !seen[cal.Title] {
			result.CheckedCalendars = append(result.CheckedCalendars, cal.Title)
			seen[cal.Title] = true
		}
	}
	rows, err := a.store.ListCalendarSources(ctx)
	if err != nil {
		return result, err
	}
	sources := make([]calendar.Source, 0, len(rows))
	for _, row := range rows {
		sources = append(sources, calendarSourceFromRow(row))
	}
	for _, provider := range calendar.WebCalendarsToPeek(sources, now, calendar.PeekRecentUse) {
		switch provider {
		case "google":
			result.BrowserOnly = append(result.BrowserOnly, "Google")
		case "outlook":
			result.BrowserOnly = append(result.BrowserOnly, "Outlook")
		}
	}
	return result, nil
}

// GetFindTimeDetailsForRange reads a bounded, inclusive range of local dates.
// It never asks for calendar access or treats a hidden event as free time.
func (a *App) GetFindTimeDetailsForRange(first, last string, minutes int) (FindTimeDetails, error) {
	if !calendarAccessGranted() || minutes < 1 || minutes > 24*60 {
		return FindTimeDetails{}, errors.New("calendar unavailable")
	}
	now := time.Now()
	start, err := time.ParseInLocation("2006-01-02", first, now.Location())
	if err != nil {
		return FindTimeDetails{}, err
	}
	lastDay, err := time.ParseInLocation("2006-01-02", last, now.Location())
	if err != nil || lastDay.Before(start) || lastDay.After(start.AddDate(0, 0, 30)) {
		return FindTimeDetails{}, errors.New("invalid date range")
	}
	end := lastDay.AddDate(0, 0, 1)
	events, err := a.findTimeEvents(context.Background(), now, start, end, false)
	if err != nil {
		return FindTimeDetails{}, err
	}
	quiet := engagement.DefaultPrefs().Quiet
	if a.engagement != nil {
		quiet = a.engagement.Prefs().Quiet
	}
	ranges, err := findTimeRangeWindows(events, now, start, end, time.Duration(minutes)*time.Minute, quiet, a.holdActivityProfile(now))
	if err != nil {
		return FindTimeDetails{}, err
	}
	calendars, err := enumerateSystemCalendars()
	if err != nil {
		return FindTimeDetails{}, err
	}
	return a.findTimeDetailsCoverage(context.Background(), now, calendars, ranges)
}

// GetFindTimeDetails names both the calendars checked and uncovered web sources.
func (a *App) GetFindTimeDetails() (FindTimeDetails, error) {
	if !calendarAccessGranted() {
		return FindTimeDetails{}, errors.New("calendar unavailable")
	}
	calendars, err := enumerateSystemCalendars()
	if err != nil {
		return FindTimeDetails{}, err
	}
	return a.findTimeDetails(context.Background(), time.Now(), true, calendars)
}

func (a *App) findTimeWithAccess(ctx context.Context, now time.Time, granted bool) ([]FindTimeRange, error) {
	if !granted {
		return nil, errors.New("calendar unavailable")
	}
	return a.findTime(ctx, now)
}

// GetFindTime reads cached events only and never requests calendar access.
func (a *App) GetFindTime() ([]FindTimeRange, error) {
	return a.findTimeWithAccess(context.Background(), time.Now(), calendarAccessGranted())
}
