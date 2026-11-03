package main

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"wails-gui/internal/calendar"
)

var errHoldUnavailable = errors.New("hold time unavailable")
var errHoldCoverageUnknown = errors.New("calendar coverage unavailable")

// checkHoldWindow re-reads EventKit immediately before a pending hold. It
// fails closed if an event cannot be interpreted or the calendar vanished.
func checkHoldWindow(store holdEventStore, event holdEvent, now time.Time) error {
	localStart := event.Start.In(time.Local)
	if strings.TrimSpace(event.Title) == "" || event.CalendarID == "" || !event.Start.After(now) || !event.End.After(event.Start) {
		return errHoldUnavailable
	}
	if utf8.RuneCountInString(event.Title) > 40 || event.End.Sub(event.Start) > 2*time.Hour || event.End.Sub(event.Start)%(30*time.Minute) != 0 || localStart.Minute()%30 != 0 || localStart.Second() != 0 || localStart.Nanosecond() != 0 {
		return errHoldUnavailable
	}
	calendars, err := store.WritableCalendars()
	if err != nil {
		return err
	}
	found := false
	for _, cal := range calendars {
		if cal.ID == event.CalendarID {
			found = true
			break
		}
	}
	if !found {
		return errHoldUnavailable
	}
	// Read ten minutes after the proposed end to catch a meeting immediately
	// following it. Read all calendars, never only the chosen one.
	raw, err := store.ReadWindow(event.Start, event.End.Add(10*time.Minute))
	if err != nil {
		return err
	}
	if _, blocked, err := holdWindowBlocker(raw, event); err != nil || blocked {
		return errHoldUnavailable
	}
	return nil
}

// holdWindowBlocker returns the busy event that makes the window unavailable,
// so a filled-up slot can name what took it.
func holdWindowBlocker(raw []calendar.RawEvent, event holdEvent) (calendar.Event, bool, error) {
	for _, item := range raw {
		occurrence, ok := calendar.NormalizeEventKit(item)
		if !ok {
			return calendar.Event{}, false, errHoldUnavailable
		}
		if !occurrence.Busy() {
			continue
		}
		if occurrence.Overlaps(event.Start, event.End) || (occurrence.HasOthers() && !occurrence.Start.Before(event.End) && occurrence.Start.Before(event.End.Add(10*time.Minute))) {
			return occurrence, true, nil
		}
	}
	return calendar.Event{}, false, nil
}
