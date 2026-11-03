//go:build !darwin

package main

import (
	"time"

	"wails-gui/internal/calendar"
)

// systemRemindersSupported reports whether this platform can read the OS
// reminders. Only macOS can today.
func systemRemindersSupported() bool {
	return false
}

// remindersAuthorization reports the Reminders permission. Platforms without
// an app-level reminders permission report not_applicable.
func remindersAuthorization() string {
	return "not_applicable"
}

// remindersAccessGranted is the Granted flag of the "reminders" permission item.
func remindersAccessGranted() bool {
	return false
}

// requestRemindersAuthorization has nothing to request on this platform.
func requestRemindersAuthorization() (bool, error) {
	return false, errRemindersUnsupported
}

// enumerateSystemReminders is unavailable outside macOS.
func enumerateSystemReminders(completedSince time.Time) ([]calendar.RawReminder, error) {
	return nil, errRemindersUnsupported
}
