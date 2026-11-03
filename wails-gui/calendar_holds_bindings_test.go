package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func TestProposeHoldUsesStoredFreeTimeAndPreference(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if err := a.store.SetUIState(context.Background(), calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "local", IsDefault: true}}}
	thing := HoldThing{Title: "Plan", SourceKind: "task", SourceID: "task-1"}
	proposal, err := a.proposeHold(thing, now.Add(3*time.Hour), time.Hour, now, store, true)
	if err != nil || proposal.ID == "" || proposal.CalendarID != "local" || len(proposal.Slots) != 3 {
		t.Fatalf("proposal=%#v err=%v", proposal, err)
	}
	if len(store.saved) != 0 || len(store.removed) != 0 {
		t.Fatal("proposing wrote a calendar")
	}
	if err := a.SetOfferHolds(false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.proposeHold(thing, time.Time{}, time.Hour, now, store, true); err == nil {
		t.Fatal("offered a hold with offers switched off")
	}
	if _, err := a.proposeHold(thing, time.Time{}, time.Hour, now, store, false); err == nil {
		t.Fatal("offered a hold without permission")
	}
	long := normalizeHoldThing(HoldThing{Title: strings.Repeat("计", 45), SourceKind: "task", SourceID: "task-1"})
	if !validHoldThing(long) || len([]rune(long.Title)) != 40 {
		t.Fatalf("normalized title: %#v", long)
	}
	if validHoldThing(HoldThing{Title: "Plan", SourceKind: "unknown", SourceID: "task-1"}) {
		t.Fatal("accepted an unknown source")
	}
}

func TestHoldBindingChecksPermissionAndUndo(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHolds(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHoldActiveSource(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "local", IsDefault: true}}}
	a.holds = &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	slot := FindTimeRange{Start: now.Add(time.Hour).Format(time.RFC3339), End: now.Add(2 * time.Hour).Format(time.RFC3339)}
	proposal := HoldProposal{ID: "hold", Thing: HoldThing{Title: "Plan", SourceKind: "task", SourceID: "t"}, CalendarID: "local", Slots: []FindTimeRange{slot}}
	if _, err := a.holdTimeWithAccess(proposal, slot, "local", false); err == nil {
		t.Fatal("hold without permission")
	}
	if _, err := a.holdTimeWithAccess(proposal, FindTimeRange{}, "local", true); err == nil {
		t.Fatal("hold for an unlisted slot")
	}
	state, err := a.holdTimeWithAccess(proposal, slot, "local", true)
	if err != nil || state.State != "pending_write" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	if err := a.UndoHold("hold"); err != nil {
		t.Fatal(err)
	}
	state, err = a.GetHoldStatus("hold")
	if err != nil || state.State != "removed" || len(store.saved) != 0 {
		t.Fatalf("state=%#v saves=%d err=%v", state, len(store.saved), err)
	}
}

func TestHoldBindingOffersCheckedAlternativeWhenSlotFills(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHolds(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHoldActiveSource(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	first := FindTimeRange{Start: now.Add(time.Hour).Format(time.RFC3339), End: now.Add(2 * time.Hour).Format(time.RFC3339)}
	second := FindTimeRange{Start: now.Add(2 * time.Hour).Format(time.RFC3339), End: now.Add(3 * time.Hour).Format(time.RFC3339)}
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "local", IsDefault: true}}, events: []calendar.RawEvent{{UID: "busy", Start: first.Start, End: first.End}}}
	a.holds = &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	proposal := HoldProposal{ID: "h", Thing: HoldThing{Title: "Plan", SourceKind: "task", SourceID: "t"}, Slots: []FindTimeRange{first, second}}
	got, err := a.holdTimeWithAccess(proposal, first, "local", true)
	if err != nil || got.State != "conflict" || got.Start != second.Start || len(store.saved) != 0 {
		t.Fatalf("status=%#v saves=%d err=%v", got, len(store.saved), err)
	}
}

func TestHoldBindingAsksBeforeChangingCalendar(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHolds(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHoldActiveSource(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "replacement", IsDefault: true}}}
	a.holds = &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	slot := FindTimeRange{Start: now.Add(time.Hour).Format(time.RFC3339), End: now.Add(2 * time.Hour).Format(time.RFC3339)}
	proposal := HoldProposal{ID: "h", Thing: HoldThing{Title: "Plan", SourceKind: "task", SourceID: "t"}, Slots: []FindTimeRange{slot}}
	got, err := a.holdTimeWithAccess(proposal, slot, "unwritable", true)
	if err != nil || got.State != "calendar_unavailable" || got.CalendarID != "replacement" || len(store.saved) != 0 {
		t.Fatalf("status=%#v saves=%d err=%v", got, len(store.saved), err)
	}
}
