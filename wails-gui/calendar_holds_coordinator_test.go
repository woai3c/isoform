package main

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func TestHoldCoordinatorUndoAndRecovery(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	coordinator := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	record := holdRecord{ID: "h1", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t1"}
	if err := coordinator.begin(record); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatal("wrote before undo interval")
	}
	if err := coordinator.undo("h1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	if err := coordinator.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatal("wrote an undone hold")
	}
	record.ID = "h2"
	if err := coordinator.begin(record); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	// A new coordinator stands in for a process restart and reads the deadline.
	restarted := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	if err := restarted.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved %d events, want 1", len(store.saved))
	}
	if err := restarted.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 {
		t.Fatal("saved the same pending hold twice")
	}
	// Calendar edits belong to the person and must survive Remove and Undo.
	editedStart := record.Start.Add(7 * time.Minute)
	editedEnd := editedStart.Add(2*time.Hour + 15*time.Minute)
	editedTitle := record.Title
	store.snapshots["fake-id"] = &holdEventSnapshot{EventID: "fake-id", CalendarID: "work", Title: editedTitle, Start: editedStart.Format(time.RFC3339), End: editedEnd.Format(time.RFC3339)}
	if err := restarted.remove("h2"); err != nil {
		t.Fatal(err)
	}
	if len(store.removed) != 1 || store.removed[0] != "fake-id" {
		t.Fatalf("removed %v", store.removed)
	}
	// Undo is a restoration of the person's moved event, even if the time no
	// longer satisfies the policy for proposing a new hold.
	restarted.verifyCoverage = func(holdEvent) error { return errHoldUnavailable }
	if err := restarted.undoRemove("h2"); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 2 {
		t.Fatal("did not restore removed hold")
	}
	if restored := store.saved[1]; restored.Title != editedTitle || !restored.Start.Equal(editedStart) || !restored.End.Equal(editedEnd) {
		t.Fatalf("lost Calendar edits: %+v", restored)
	}
	if err := restarted.remove("h1"); !errors.Is(err, errHoldUnavailable) {
		t.Fatalf("removed a hold that was never written: %v", err)
	}
}

func TestHoldCoordinatorDoesNotRepeatAnUnrecordedWrite(t *testing.T) {
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
	_, err = db.Exec(`CREATE TRIGGER fail_held BEFORE UPDATE OF state ON holds WHEN NEW.state = 'held' BEGIN SELECT RAISE(FAIL, 'disk error'); END`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err == nil {
		t.Fatal("expected failed persistence")
	}
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("attempted %d saves after an uncertain write", len(store.saved))
	}
	saved, err := c.record("h")
	if err != nil || saved.State != "checking" {
		t.Fatalf("state %q, error %v", saved.State, err)
	}
}

func TestHoldRecoveryAdoptsOnlyOneExactNewUninvitedEvent(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	base := calendar.RawEvent{LocalID: "event", ExternalID: "external", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour).Format(time.RFC3339), End: now.Add(2 * time.Hour).Format(time.RFC3339), CreationTimestamp: float64(now.Add(time.Second).Unix())}
	old := base
	old.CreationTimestamp = float64(now.Add(-time.Second).Unix())
	invited := base
	invited.Attendees = []calendar.RawAttendee{{Email: "other@example.com"}}
	changed := base
	changed.Title = "Other"
	wrongCalendar := base
	wrongCalendar.CalendarID = "other"
	wrongTime := base
	wrongTime.Start = now.Add(90 * time.Minute).Format(time.RFC3339)
	duplicate := base
	duplicate.LocalID = "event-2"
	for _, tc := range []struct {
		name   string
		events []calendar.RawEvent
		want   string
	}{
		{"exact", []calendar.RawEvent{base}, "held"},
		{"none", nil, "failed"},
		{"old", []calendar.RawEvent{old}, "failed"},
		{"invitees", []calendar.RawEvent{invited}, "failed"},
		{"title", []calendar.RawEvent{changed}, "failed"},
		{"calendar", []calendar.RawEvent{wrongCalendar}, "failed"},
		{"time", []calendar.RawEvent{wrongTime}, "failed"},
		{"ambiguous", []calendar.RawEvent{base, duplicate}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
			c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
			h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
			if err := c.begin(h); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE holds SET state = 'checking' WHERE id = 'h'`); err != nil {
				t.Fatal(err)
			}
			store.events = tc.events
			if err := c.recoverWrites(); err != nil {
				t.Fatal(err)
			}
			got, err := c.record("h")
			if err != nil || got.State != tc.want {
				t.Fatalf("record=%+v err=%v", got, err)
			}
			if tc.want == "held" && (got.EventID != "event" || got.ExternalID != "external") {
				t.Fatalf("wrong identity: %+v", got)
			}
			if tc.want == "failed" && got.FailureReason != "unconfirmed" {
				t.Fatalf("wrong reason: %+v", got)
			}
			if len(store.saved) != 0 || len(store.removed) != 0 {
				t.Fatal("recovery wrote to calendar")
			}
		})
	}
}

func TestHoldWriteRefusalIsDefiniteAndOffersAnotherCalendar(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}, saveErrors: []error{errHoldCalendarUnwritable}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	got, err := c.record("h")
	if err != nil || got.State != "failed" || got.FailureReason != "calendar_unavailable" || store.saveAttempts != 1 {
		t.Fatalf("record=%+v attempts=%d err=%v", got, store.saveAttempts, err)
	}
}

func TestHoldRemovePreservesUnsupportedCalendarEdits(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	store.snapshots["fake-id"].HasUnsupportedEdits = true
	if err := c.remove("h"); !errors.Is(err, errHoldEdited) {
		t.Fatalf("remove error = %v", err)
	}
	if store.removeAttempts != 0 {
		t.Fatal("removed an event whose edits cannot be restored")
	}
	got, err := c.record("h")
	if err != nil || got.State != "held" {
		t.Fatalf("record=%+v err=%v", got, err)
	}
}

func TestHoldCoordinatorRequiresCompleteCoverage(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if !errors.Is(c.begin(h), errHoldUnavailable) {
		t.Fatal("accepted unknown calendar coverage")
	}
	if len(store.saved) != 0 {
		t.Fatal("wrote with unknown calendar coverage")
	}
}

func TestHoldCoordinatorReconcilesMovesRenamesAndDeletion(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}, snapshots: map[string]*holdEventSnapshot{}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	delete(store.snapshots, "fake-id")
	store.snapshots["external-id"] = &holdEventSnapshot{EventID: "new-id", CalendarID: "work", Title: "Renamed", Start: h.Start.Add(time.Hour).Format(time.RFC3339), End: h.End.Add(time.Hour).Format(time.RFC3339)}
	if err := c.reconcile(); err != nil {
		t.Fatal(err)
	}
	recorded, err := c.record("h")
	if err != nil || recorded.State != "moved" || recorded.EventID != "new-id" || recorded.Title != "Plan" || !recorded.Start.Equal(h.Start.Add(time.Hour)) {
		t.Fatalf("record: %#v; err %v", recorded, err)
	}
	if err := c.remove("h"); !errors.Is(err, errHoldEdited) {
		t.Fatalf("renamed event should be left in Calendar: %v", err)
	}
	store.snapshots["external-id"].Title = "Plan"
	store.snapshots["external-id"].CalendarID = "other"
	if err := c.reconcile(); err != nil {
		t.Fatal(err)
	}
	if err := c.remove("h"); !errors.Is(err, errHoldEdited) {
		t.Fatalf("event moved to another calendar should be left there: %v", err)
	}
	delete(store.snapshots, "external-id")
	if err := c.reconcile(); err != nil {
		t.Fatal(err)
	}
	recorded, err = c.record("h")
	if err != nil || recorded.State != "gone" {
		t.Fatalf("record: %#v; err %v", recorded, err)
	}
	if len(store.removed) != 0 {
		t.Fatal("reconciliation removed an event")
	}
}

func TestHoldCoordinatorWaitsForCoverageAfterRestart(t *testing.T) {
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
	covered := true
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error {
		if !covered {
			return errHoldCoverageUnknown
		}
		return nil
	}}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	covered = false
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	record, err := c.record("h")
	if err != nil || record.State != "pending_write" || len(store.saved) != 0 {
		t.Fatalf("record=%#v saves=%d err=%v", record, len(store.saved), err)
	}
	covered = true
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 {
		t.Fatal("did not resume after coverage returned")
	}
}

func TestHoldCoordinatorReportsReachOutcomes(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	var outcomes []string
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }, onOutcome: func(_ string, outcome, reason string, _ time.Duration) {
		outcomes = append(outcomes, outcome+":"+reason)
	}}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if err := c.remove("h"); err != nil {
		t.Fatal(err)
	}
	if err := c.undoRemove("h"); err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 || outcomes[0] != "held:" || outcomes[1] != "removed:" || outcomes[2] != "undone:" {
		t.Fatalf("outcomes: %v", outcomes)
	}
}

func TestHoldCoordinatorRetriesOnlyDefiniteRejections(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}, saveErrors: []error{errHoldWriteFailed, nil}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if store.saveAttempts != 2 || len(store.saved) != 1 {
		t.Fatalf("attempts=%d saved=%d", store.saveAttempts, len(store.saved))
	}
	store.removeErrors = []error{errHoldWriteFailed, nil}
	if err := c.remove("h"); err != nil {
		t.Fatal(err)
	}
	if store.removeAttempts != 2 || len(store.removed) != 1 {
		t.Fatalf("remove attempts=%d removed=%d", store.removeAttempts, len(store.removed))
	}
	h.ID = "uncertain"
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	store.saveErrors = []error{errHoldWriteUncertain}
	before := store.saveAttempts
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	record, err := c.record("uncertain")
	if err != nil || record.State != "checking" || store.saveAttempts != before+1 {
		t.Fatalf("record=%#v attempts=%d err=%v", record, store.saveAttempts, err)
	}
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if store.saveAttempts != before+1 {
		t.Fatal("retried an uncertain write")
	}
}

func TestHoldCoordinatorDoesNotWriteWithoutClaim(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TRIGGER another_worker_claimed BEFORE UPDATE OF state ON holds WHEN NEW.state = 'checking' BEGIN SELECT RAISE(IGNORE); END`)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatal("wrote without claiming the pending record")
	}
}

func TestHoldActiveSourceSurvivesPanelReopen(t *testing.T) {
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
	if err := migrateCalendarHoldActiveSource(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	thing := HoldThing{Title: "Plan", SourceKind: "task", SourceID: "t"}
	existing, err := c.activeForSource(thing, now)
	if err != nil || existing == nil || existing.ID != "h" || existing.State != "pending_write" {
		t.Fatalf("existing=%#v err=%v", existing, err)
	}
	h.ID = "duplicate"
	if err := c.begin(h); err == nil {
		t.Fatal("accepted a second active hold for the same source")
	}
	if err := c.undo("h"); err != nil {
		t.Fatal(err)
	}
	if err := c.begin(h); err != nil {
		t.Fatalf("rejected a new hold after undo: %v", err)
	}
}

func TestHoldStartingReleasesOfferWithoutRemovingEvent(t *testing.T) {
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
	store := &fakeHoldEventStore{calendars: []holdCalendar{{ID: "work"}}}
	c := &holdCoordinator{db: db, calendar: store, now: func() time.Time { return now }, verifyCoverage: func(holdEvent) error { return nil }}
	h := holdRecord{ID: "h", CalendarID: "work", Title: "Plan", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), SourceKind: "task", SourceID: "t"}
	if err := c.begin(h); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	now = h.Start
	if err := c.runDue(); err != nil {
		t.Fatal(err)
	}
	if existing, err := c.activeForSource(HoldThing{Title: "Plan", SourceKind: "task", SourceID: "t"}, now); err != nil || existing != nil {
		t.Fatalf("existing=%#v err=%v", existing, err)
	}
	h.ID = "later"
	h.Start = now.Add(time.Hour)
	h.End = now.Add(2 * time.Hour)
	if err := c.begin(h); err != nil {
		t.Fatalf("new hold after start: %v", err)
	}
	if len(store.saved) != 1 || len(store.removed) != 0 {
		t.Fatalf("saved=%d removed=%d", len(store.saved), len(store.removed))
	}
}
