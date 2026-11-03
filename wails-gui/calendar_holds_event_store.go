package main

import (
	"errors"
	"time"

	"wails-gui/internal/calendar"
)

var errHoldWriteFailed = errors.New("calendar rejected the write")
var errHoldWriteUncertain = errors.New("calendar write outcome is uncertain")
var errHoldCalendarUnwritable = errors.New("calendar refused changes")
var errHoldEdited = errors.New("HOLD_CALENDAR_EDITED")

// holdEventStore separates calendar access from hold decisions. Tests supply a
// fake store; they must never save or remove a real calendar event.
type holdEventStore interface {
	WritableCalendars() ([]holdCalendar, error)
	ReadWindow(start, end time.Time) ([]calendar.RawEvent, error)
	Lookup(identifier, externalIdentifier string) (*holdEventSnapshot, error)
	Save(event holdEvent) (holdEventIdentity, error)
	Remove(identifier string) error
}

type holdCalendar struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	SourceID        string `json:"sourceId"`
	SourceTitle     string `json:"sourceTitle"`
	SourceType      string `json:"sourceType"`
	SupportsBusy    bool   `json:"supportsBusy"`
	IsDefault       bool   `json:"isDefault"`
	MeetingsAccount bool   `json:"meetingsAccount"`
}

type holdEvent struct {
	CalendarID string
	Title      string
	Start      time.Time
	End        time.Time
}

type holdEventIdentity struct {
	EventIdentifier    string `json:"eventIdentifier"`
	ExternalIdentifier string `json:"externalIdentifier"`
}

// holdEventSnapshot is the current copy of an event previously written by Isoform.
type holdEventSnapshot struct {
	EventID    string `json:"eventId"`
	CalendarID string `json:"calendarId"`
	Title      string `json:"title"`
	Start      string `json:"start"`
	End        string `json:"end"`
	// Restoring these edits would violate the title-only writer contract.
	HasUnsupportedEdits bool `json:"hasUnsupportedEdits"`
}

// The EventKit bridge excludes subscribed, read-only and birthday calendars.
func holdWritableCalendars(calendars []holdCalendar) []holdCalendar {
	out := make([]holdCalendar, 0, len(calendars))
	for _, cal := range calendars {
		if cal.SupportsBusy {
			out = append(out, cal)
		}
	}
	return out
}
