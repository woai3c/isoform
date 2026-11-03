//go:build !darwin

package main

// newCalendarPeekDriver returns nil outside macOS: there is no browser
// automation to drive, so the peek scheduler never starts.
func newCalendarPeekDriver() calendarPeekDriver { return nil }
