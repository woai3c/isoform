//go:build !darwin

package main

import (
	"time"

	"wails-gui/internal/calendar"
)

// systemCalendarsSupported reports whether this platform can read the OS
// calendars. Only macOS can today.
func systemCalendarsSupported() bool {
	return false
}

// calendarAuthorization reports the Calendars permission. Platforms without
// an app-level calendar permission report not_applicable.
func calendarAuthorization() string {
	return "not_applicable"
}

// calendarAccessGranted is the Granted flag of the "calendars" permission item.
func calendarAccessGranted() bool {
	return false
}

// requestCalendarAuthorization has nothing to request on this platform.
func requestCalendarAuthorization() (bool, error) {
	return false, errCalendarUnsupported
}

// enumerateSystemCalendars is unavailable outside macOS.
func enumerateSystemCalendars() ([]calendar.RawCalendar, error) {
	return nil, errCalendarUnsupported
}

// enumerateSystemCalendarEvents is unavailable outside macOS.
func enumerateSystemCalendarEvents(start, end time.Time) ([]calendar.RawEvent, error) {
	return nil, errCalendarUnsupported
}

// watchSystemCalendarChanges has no store to watch here.
func watchSystemCalendarChanges() {}

// resetSystemCalendarStore has no store to reset here.
func resetSystemCalendarStore() {}

type eventKitHoldStore struct{}

func (eventKitHoldStore) WritableCalendars() ([]holdCalendar, error) {
	return nil, errCalendarUnsupported
}
func (eventKitHoldStore) ReadWindow(start, end time.Time) ([]calendar.RawEvent, error) {
	return nil, errCalendarUnsupported
}
func (eventKitHoldStore) Save(event holdEvent) (holdEventIdentity, error) {
	return holdEventIdentity{}, errCalendarUnsupported
}
func (eventKitHoldStore) Remove(identifier string) error { return errCalendarUnsupported }

func (eventKitHoldStore) Lookup(identifier, externalIdentifier string) (*holdEventSnapshot, error) {
	return nil, errCalendarUnsupported
}
