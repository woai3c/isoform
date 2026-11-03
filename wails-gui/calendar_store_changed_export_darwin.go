//go:build darwin

package main

// Kept apart from permissions_calendar_darwin.go so the //export directive
// never shares a file with Objective-C definitions in the cgo preamble (see
// audio_capture_events_export_darwin.go for the duplicate-symbol reason).

/*
#include <stdlib.h>
*/
import "C"

//export goCalendarStoreChanged
func goCalendarStoreChanged() {
	// EKEventStoreChanged fires in bursts and says nothing about what changed.
	// A non-blocking send collapses a burst into one wake-up; the sync loop
	// debounces and refetches the window. Nothing here touches Wails or the
	// store, so the posting thread is never held.
	select {
	case calendarStoreChanged <- struct{}{}:
	default:
	}
	select {
	case holdsStoreChanged <- struct{}{}:
	default:
	}
	// The store is shared with the reminders reader, and the notification
	// does not say whether events or reminders changed.
	select {
	case remindersStoreChanged <- struct{}{}:
	default:
	}
}
