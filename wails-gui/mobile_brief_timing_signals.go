package main

import (
	"context"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/platform/activitymon"
)

const mobileBriefTimingMeaningfulActivity = 5 * time.Minute

// timingInput reduces existing observations to clock times. It neither opens
// capture content nor records a new history. Phone relay activity is not proof
// of foreground use, so FirstPhone remains unknown until clients provide it.
func (s *mobileBriefService) timingInput(now time.Time) mobileBriefTimingInput {
	input := mobileBriefTimingInput{}
	if s == nil || s.app == nil {
		return input
	}
	input.Quiet = engagement.DefaultPrefs().Quiet
	if s.app.engagement != nil {
		input.Quiet = s.app.engagement.Prefs().Quiet
	}
	s.mu.Lock()
	sleeping, sleepAt := s.sleeping, s.sleepAt
	s.mu.Unlock()
	input.Sleeping = sleeping
	if s.app.store == nil || now.IsZero() {
		return input
	}
	// The observer's queue can drop sleep/wake events. Its native cache is
	// authoritative for newer transitions; the context snapshot may be stale.
	if s.sleepState != nil {
		latest := s.sleepState()
		if !latest.At.IsZero() && !latest.At.Before(sleepAt) && !latest.At.After(now) {
			switch latest.Kind {
			case activitymon.SystemStateSleep:
				input.Sleeping = true
			case activitymon.SystemStateWake:
				input.Sleeping = false
			}
		}
	}
	ctx := context.Background()
	privacy, err := mobileBriefTimingPrivacy(ctx, s.app, now)
	if err != nil {
		return input
	}
	loc := s.location
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to := today.AddDate(0, 0, -mobileBriefTimingHistory), today.AddDate(0, 0, 1)
	days := make(map[string]*mobileBriefTimingDay, mobileBriefTimingHistory+1)
	input.Days = make([]mobileBriefTimingDay, mobileBriefTimingHistory)
	for index := range input.Days {
		date := from.AddDate(0, 0, index).Format("2006-01-02")
		input.Days[index].Date = date
		days[date] = &input.Days[index]
	}
	input.Today.Date = today.Format("2006-01-02")
	days[input.Today.Date] = &input.Today
	for date, day := range days {
		marks, ok := loadContextDayMarks(ctx, s.app.store, date)
		if !ok || marks.FirstInput <= 0 || marks.LastInput < marks.FirstInput ||
			!privacy.timeAllowed(marks.FirstInput, marks.LastInput) {
			continue
		}
		first, last := time.UnixMilli(marks.FirstInput).In(loc), time.UnixMilli(marks.LastInput).In(loc)
		if first.Format("2006-01-02") != date || last.Format("2006-01-02") != date || last.After(now) {
			continue
		}
		// Only input-specific marks have sufficient provenance. Legacy
		// wake-inclusive marks never train timing, regardless of their span.
		// Require multiple input observations across a meaningful interval.
		if date != input.Today.Date && last.Sub(first) < mobileBriefTimingMeaningfulActivity {
			continue
		}
		day.FirstActivity, day.LastActivity = first, last
	}
	opens, err := s.app.store.ListMobileBriefOpenTimes(ctx, from.UnixMilli(), now.Add(time.Millisecond).UnixMilli())
	if err == nil {
		for _, stamp := range opens {
			at := time.UnixMilli(stamp).In(loc)
			day := days[at.Format("2006-01-02")]
			if day != nil && privacy.timeAllowed(stamp, stamp) && (day.FirstOpen.IsZero() || at.Before(day.FirstOpen)) {
				day.FirstOpen = at
			}
		}
	}
	collectMobileBriefCalendarTiming(ctx, s.app, privacy, from, to, calendarAccessGranted(), days)
	return input
}

// Native calendar metadata needs both the current OS grant and app read gates.
// Web-derived and session-linked records stay out instead of inspecting their
// window/event content to establish provenance.
func collectMobileBriefCalendarTiming(ctx context.Context, app *App, privacy *mcpReadPrivacy, from, to time.Time, nativeGranted bool, days map[string]*mobileBriefTimingDay) {
	if !nativeGranted || app == nil || app.store == nil || privacy == nil || !privacy.calendar ||
		!privacy.appAllowed("eventkit", "com.apple.iCal", "Calendar") {
		return
	}
	rows, err := app.store.ListNativeCalendarTiming(ctx, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return
	}
	for _, row := range rows {
		if !privacy.appAllowed(row.Provider) || !privacy.timeAllowed(row.StartAt, row.EndAt) {
			continue
		}
		start, end := time.UnixMilli(row.StartAt).In(from.Location()), time.UnixMilli(row.EndAt).In(from.Location())
		first := start
		if first.Before(from) {
			first = from
		}
		first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, from.Location())
		for date := first; date.Before(to) && date.Before(end); date = date.AddDate(0, 0, 1) {
			dateKey := date.Format("2006-01-02")
			day := days[dateKey]
			if day == nil {
				continue
			}
			// Each overlapping local date stays busy through the real end.
			// Pair its original start with that end for provenance. Crossing
			// midnight is not a new morning meeting, and a midnight end
			// excludes the date that follows it.
			if start.Format("2006-01-02") == dateKey && (day.FirstMeeting.IsZero() || start.Before(day.FirstMeeting)) {
				day.FirstMeeting = start
			}
			if day.LastMeeting.IsZero() || end.After(day.LastMeeting) {
				day.LastMeeting = end
				day.LastMeetingStart = start
			}
		}
	}
}
