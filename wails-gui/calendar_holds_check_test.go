package main

import (
	"errors"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

type fakeHoldEventStore struct {
	calendars      []holdCalendar
	events         []calendar.RawEvent
	err            error
	saved          []holdEvent
	removed        []string
	snapshots      map[string]*holdEventSnapshot
	onSave         func()
	saveErrors     []error
	saveAttempts   int
	removeErrors   []error
	removeAttempts int
}

func (f *fakeHoldEventStore) WritableCalendars() ([]holdCalendar, error) { return f.calendars, f.err }
func (f *fakeHoldEventStore) ReadWindow(_, _ time.Time) ([]calendar.RawEvent, error) {
	return f.events, f.err
}
func (f *fakeHoldEventStore) Lookup(id, externalID string) (*holdEventSnapshot, error) {
	if snapshot := f.snapshots[id]; snapshot != nil {
		return snapshot, f.err
	}
	return f.snapshots[externalID], f.err
}
func (f *fakeHoldEventStore) Save(e holdEvent) (holdEventIdentity, error) {
	f.saveAttempts++
	if len(f.saveErrors) > 0 {
		err := f.saveErrors[0]
		f.saveErrors = f.saveErrors[1:]
		if err != nil {
			return holdEventIdentity{}, err
		}
	}
	if f.err != nil {
		return holdEventIdentity{}, f.err
	}
	f.saved = append(f.saved, e)
	if f.snapshots == nil {
		f.snapshots = make(map[string]*holdEventSnapshot)
	}
	f.snapshots["fake-id"] = &holdEventSnapshot{EventID: "fake-id", CalendarID: e.CalendarID, Title: e.Title, Start: e.Start.Format(time.RFC3339), End: e.End.Format(time.RFC3339)}
	if f.onSave != nil {
		f.onSave()
	}
	return holdEventIdentity{EventIdentifier: "fake-id", ExternalIdentifier: "external-id"}, f.err
}
func (f *fakeHoldEventStore) Remove(id string) error {
	f.removeAttempts++
	if len(f.removeErrors) > 0 {
		err := f.removeErrors[0]
		f.removeErrors = f.removeErrors[1:]
		if err != nil {
			return err
		}
	}
	if f.err != nil {
		return f.err
	}
	f.removed = append(f.removed, id)
	return nil
}

func TestHoldWindowCheckUsesAllCalendarsAndFailsClosed(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	event := holdEvent{CalendarID: "chosen", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "chosen"}}}
	if err := checkHoldWindow(store, event, now); err != nil {
		t.Fatal(err)
	}
	conflict := calendar.RawEvent{UID: "other-event", CalendarID: "other", Start: event.Start.Format(time.RFC3339), End: event.End.Format(time.RFC3339)}
	store.events = []calendar.RawEvent{conflict}
	if !errors.Is(checkHoldWindow(store, event, now), errHoldUnavailable) {
		t.Fatal("overlap on another calendar was accepted")
	}
	conflict.Availability = "free"
	store.events = []calendar.RawEvent{conflict}
	if err := checkHoldWindow(store, event, now); err != nil {
		t.Fatalf("free event: %v", err)
	}
	conflict.Availability = "busy"
	conflict.Start = event.End.Add(5 * time.Minute).Format(time.RFC3339)
	conflict.End = event.End.Add(time.Hour).Format(time.RFC3339)
	conflict.Attendees = []calendar.RawAttendee{{Email: "other@example.test"}}
	store.events = []calendar.RawEvent{conflict}
	if !errors.Is(checkHoldWindow(store, event, now), errHoldUnavailable) {
		t.Fatal("meeting immediately after hold was accepted")
	}
	store.events = []calendar.RawEvent{{UID: "bad", Start: "invalid"}}
	if !errors.Is(checkHoldWindow(store, event, now), errHoldUnavailable) {
		t.Fatal("unreadable event was accepted")
	}
	store.events = nil
	store.calendars = nil
	if !errors.Is(checkHoldWindow(store, event, now), errHoldUnavailable) {
		t.Fatal("missing writable calendar was accepted")
	}
	store.calendars = []holdCalendar{{ID: "chosen"}}
	event.Start = event.Start.Add(time.Minute)
	if !errors.Is(checkHoldWindow(store, event, now), errHoldUnavailable) {
		t.Fatal("accepted a start outside a half hour")
	}
	if len(store.saved) != 0 || len(store.removed) != 0 {
		t.Fatal("checking must not write")
	}
}

func TestHoldCalendarsAllowWritableAccountsSupportingBusy(t *testing.T) {
	calendars := []holdCalendar{{ID: "local", SourceType: "local", SupportsBusy: true}, {ID: "unsupported", SourceType: "local"}, {ID: "exchange", SourceType: "exchange", SupportsBusy: true}, {ID: "unknown"}}
	got := holdWritableCalendars(calendars)
	if len(got) != 2 || got[0].ID != "local" || got[1].ID != "exchange" {
		t.Fatalf("writable calendars: %#v", got)
	}
}
