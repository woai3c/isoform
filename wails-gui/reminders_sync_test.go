package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// fakeReminderSource stands in for EventKit.
type fakeReminderSource struct {
	list  []calendar.RawReminder
	err   error
	since time.Time
}

func (f *fakeReminderSource) reminders(completedSince time.Time) ([]calendar.RawReminder, error) {
	f.since = completedSince
	return f.list, f.err
}

func newRemindersTestService(t *testing.T, src *fakeReminderSource, now time.Time) (*remindersSyncService, *storage.Store) {
	t.Helper()
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &remindersSyncService{
		store:       st,
		source:      src,
		now:         func() time.Time { return now },
		loc:         time.UTC,
		granted:     func() bool { return true },
		enabled:     func() bool { return true },
		changes:     make(chan struct{}, 1),
		kicks:       make(chan string, 4),
		consentPath: filepath.Join(t.TempDir(), "reminders-consent.json"),
	}, st
}

func TestRemindersSyncUpsertsVanishesAndKeepsOnEmptyRead(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	src := &fakeReminderSource{list: []calendar.RawReminder{
		{UID: "deck", Title: "Send the deck", ListTitle: "Work", Due: "2026-10-14T16:00:00Z", Priority: 1},
		{UID: "paper", Title: "Read the paper", ListTitle: "Personal"},
		{UID: "deck", Title: "duplicate is dropped"},
		{UID: "", Title: "no identity is dropped"},
	}}
	svc, st := newRemindersTestService(t, src, now)
	var synced []remindersSyncResult
	svc.onSynced = func(res remindersSyncResult) { synced = append(synced, res) }

	res, err := svc.Sync(ctx, "test")
	if err != nil || res.Upserted != 2 || len(synced) != 1 {
		t.Fatalf("sync: %+v %v (synced %d)", res, err, len(synced))
	}
	if !src.since.Equal(now.Add(-remindersCompletedWindow)) {
		t.Fatalf("completed window starts %v", src.since)
	}
	if _, err := os.Stat(svc.consentPath); err != nil {
		t.Fatalf("consent was not recorded before the first read: %v", err)
	}
	open, _ := st.ListReminders(ctx, false)
	if len(open) != 2 || open[0].UID != "deck" || open[0].DueAt != now.Add(7*time.Hour).UnixMilli() || open[0].Title != "Send the deck" {
		t.Fatalf("open %+v", open)
	}

	// The paper is ticked off; the deck is deleted in Reminders.
	src.list = []calendar.RawReminder{{UID: "paper", Title: "Read the paper", Completed: true, CompletedAt: "2026-10-14T08:30:00Z"}}
	if res, err := svc.Sync(ctx, "test"); err != nil || res.Vanished != 1 {
		t.Fatalf("second sync: %+v %v", res, err)
	}
	all, _ := st.ListReminders(ctx, true)
	for _, row := range all {
		switch row.UID {
		case "deck":
			if row.DeletedAt == 0 {
				t.Fatalf("deck should be deleted: %+v", row)
			}
		case "paper":
			if row.CompletedAt == 0 {
				t.Fatalf("paper should be completed: %+v", row)
			}
		}
	}

	// An empty read keeps what is there.
	src.list = []calendar.RawReminder{{UID: "new", Title: "Call Ann"}}
	if _, err := svc.Sync(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	src.list = nil
	if res, err := svc.Sync(ctx, "test"); err != nil || res.Vanished != 0 {
		t.Fatalf("empty read: %+v %v", res, err)
	}
	if open, _ := st.ListReminders(ctx, false); len(open) != 1 || open[0].UID != "new" {
		t.Fatalf("one empty read must not delete: %+v", open)
	}
	// A second empty read in a row: the last reminder really is gone.
	if res, err := svc.Sync(ctx, "test"); err != nil || res.Vanished != 1 {
		t.Fatalf("second empty read: %+v %v", res, err)
	}
	if open, _ := st.ListReminders(ctx, false); len(open) != 0 {
		t.Fatalf("the deleted last reminder is still open: %+v", open)
	}
}

func TestRemindersSyncSkipsWhenNotGrantedOrSwitchedOff(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	src := &fakeReminderSource{list: []calendar.RawReminder{{UID: "a", Title: "A"}}}
	svc, st := newRemindersTestService(t, src, now)
	granted, enabled := false, true
	svc.granted = func() bool { return granted }
	svc.enabled = func() bool { return enabled }

	svc.syncIfAllowed(context.Background(), "interval")
	if rows, _ := st.ListReminders(context.Background(), true); len(rows) != 0 {
		t.Fatal("read without access")
	}
	granted, enabled = true, false
	svc.syncIfAllowed(context.Background(), "interval")
	if rows, _ := st.ListReminders(context.Background(), true); len(rows) != 0 {
		t.Fatal("read while switched off")
	}
}

// reminderPurposeFixture runs one reconciliation over the given rows.
func reconcileReminderRows(t *testing.T, store *PurposeMapStore, now time.Time, rows ...storage.ReminderRow) reminderReconcileResult {
	t.Helper()
	res, err := reconcileReminderCommitments(store, rows, now, time.UTC)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func reminderDetail(t *testing.T, store *PurposeMapStore, uid string) PurposeOpenLoopDetail {
	t.Helper()
	detail, err := store.GetDetail(reminderPurposeRootID(uid))
	if err != nil {
		t.Fatalf("detail for %s: %v", uid, err)
	}
	return detail
}

func TestReconcileReminderCommitmentsLifecycle(t *testing.T) {
	store := NewPurposeMapStore(newTestStore(t).db)
	now := time.Now()
	due := now.Add(3 * time.Hour).Truncate(time.Second)
	deck := storage.ReminderRow{UID: "deck", Title: "Send the deck", DueAt: due.UnixMilli(), List: "Work"}
	paper := storage.ReminderRow{UID: "paper", Title: "Read the paper"}
	ticked := storage.ReminderRow{UID: "old", Title: "Already done", CompletedAt: now.Add(-time.Hour).UnixMilli()}

	res := reconcileReminderRows(t, store, now, deck, paper, ticked)
	if res.Created != 2 || res.changed() != true {
		t.Fatalf("first pass %+v", res)
	}
	detail := reminderDetail(t, store, "deck")
	if detail.OpenLoop.Kind != PurposeKindCommitment || !detail.Eligible || detail.Current.LifecycleState != PurposeStateActive {
		t.Fatalf("an open reminder is an active, supported commitment: %+v", detail.Current)
	}
	if detail.Current.DueKind != PurposeDueDate || detail.Current.DueValue != due.UTC().Format(time.RFC3339) || detail.Current.Scope != `From the Reminders list "Work"` {
		t.Fatalf("deck commitment %+v", detail.Current)
	}
	if got := reminderDetail(t, store, "paper").Current; got.DueKind != PurposeDueNone || got.DueValue != "" {
		t.Fatalf("undated reminder %+v", got)
	}
	if _, err := store.GetDetail(reminderPurposeRootID("old")); err == nil {
		t.Fatal("a reminder first seen completed must not become a commitment")
	}

	// Nothing changed: nothing written.
	if res := reconcileReminderRows(t, store, now, deck, paper, ticked); res.changed() {
		t.Fatalf("idempotent pass wrote %+v", res)
	}

	// Moved to tomorrow: revised with the new due date.
	deck.DueAt = due.Add(24 * time.Hour).UnixMilli()
	if res := reconcileReminderRows(t, store, now, deck, paper); res.Revised != 1 {
		t.Fatalf("revise %+v", res)
	}
	if got := reminderDetail(t, store, "deck").Current; got.EventType != PurposeEventRevised || got.DueValue != due.Add(24*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("revised %+v", got)
	}

	// Ticked off: resolved. Deleted: dismissed.
	deck.CompletedAt = now.UnixMilli()
	paper.DeletedAt = now.UnixMilli()
	if res := reconcileReminderRows(t, store, now, deck, paper); res.Resolved != 1 || res.Dismissed != 1 {
		t.Fatalf("close %+v", res)
	}
	if got := reminderDetail(t, store, "deck").Current.LifecycleState; got != PurposeStateResolved {
		t.Fatalf("deck state %s", got)
	}
	if got := reminderDetail(t, store, "paper").Current.LifecycleState; got != PurposeStateDismissed {
		t.Fatalf("paper state %s", got)
	}
	// Repeating the closed state writes nothing.
	if res := reconcileReminderRows(t, store, now, deck, paper); res.changed() {
		t.Fatalf("closed again wrote %+v", res)
	}

	// Unticked in Reminders: reopened and active again.
	deck.CompletedAt = 0
	if res := reconcileReminderRows(t, store, now, deck); res.Reopened != 1 {
		t.Fatalf("reopen %+v", res)
	}
	if got := reminderDetail(t, store, "deck"); got.Current.LifecycleState != PurposeStateActive || !got.Eligible {
		t.Fatalf("reopened %+v", got.Current)
	}
}

func TestReconcileReminderCommitmentsLeavesOtherWritersAlone(t *testing.T) {
	store := NewPurposeMapStore(newTestStore(t).db)
	now := time.Now()
	row := storage.ReminderRow{UID: "deck", Title: "Send the deck"}
	reconcileReminderRows(t, store, now, row)
	detail := reminderDetail(t, store, "deck")

	// The user dismisses the commitment in Isoform. The reminder is still
	// open, but Isoform does not bring the commitment back.
	at := time.Now().UnixMilli()
	if _, _, err := store.AppendEvent(AppendPurposeOpenLoopEventInput{
		OpenLoopID: detail.OpenLoop.ID, ExpectedEventID: detail.Current.ID, IdempotencyKey: "user-dismiss",
		EventType: PurposeEventDismissed, Title: detail.Current.Title, DesiredOutcome: detail.Current.DesiredOutcome,
		Owner: PurposeOwnerUser, DueKind: PurposeDueNone, ReasoningStatus: PurposeReasoningUnknown, Confidence: 0.9,
		OccurredAt: at,
		Evidence:   []PurposeEvidenceInput{purposeTestEvidence("user", PurposeEvidenceExplicitUser, PurposeStanceDismiss, at)},
	}); err != nil {
		t.Fatalf("user dismiss: %v", err)
	}
	if res := reconcileReminderRows(t, store, time.Now(), row); res.changed() {
		t.Fatalf("a dismissal by the user must stand: %+v", res)
	}
}

func TestReconcileReminderCommitmentsLimit(t *testing.T) {
	store := NewPurposeMapStore(newTestStore(t).db)
	var rows []storage.ReminderRow
	for i := 0; i < reminderCommitmentLimit+3; i++ {
		rows = append(rows, storage.ReminderRow{UID: "r" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Title: "Item"})
	}
	res := reconcileReminderRows(t, store, time.Now(), rows...)
	if res.Created != reminderCommitmentLimit || res.Skipped != 3 {
		t.Fatalf("limit %+v", res)
	}
	// New reminders sorting ahead of the others do not push past the limit
	// while it is full; closing one frees a place.
	urgent := storage.ReminderRow{UID: "urgent", Title: "Urgent", DueAt: time.Now().Add(-time.Hour).UnixMilli()}
	res = reconcileReminderRows(t, store, time.Now(), append([]storage.ReminderRow{urgent}, rows...)...)
	if res.Created != 0 || res.Skipped != 4 {
		t.Fatalf("full limit %+v", res)
	}
	rows[0].CompletedAt = time.Now().UnixMilli()
	res = reconcileReminderRows(t, store, time.Now(), append([]storage.ReminderRow{urgent}, rows...)...)
	if res.Resolved != 1 || res.Created != 1 {
		t.Fatalf("after one closed %+v", res)
	}
	if _, err := store.GetDetail(reminderPurposeRootID("urgent")); err != nil {
		t.Fatalf("the freed place goes to the first open reminder: %v", err)
	}
}

func TestOverdueReminderCommitmentSlips(t *testing.T) {
	store := NewPurposeMapStore(newTestStore(t).db)
	created := time.Now()
	due := created.Add(-2 * time.Hour).Truncate(time.Second)
	row := storage.ReminderRow{UID: "deck", Title: "Send the deck", DueAt: due.UnixMilli()}
	reconcileReminderRows(t, store, created, row)

	moments := engagementPurposeSignals(reminderDetail(t, store, "deck"), created)
	if len(moments) != 1 || moments[0].Kind != engagement.KindCommitmentSlipping || moments[0].Params["title"] != "Send the deck" {
		t.Fatalf("an overdue reminder should slip: %+v", moments)
	}

	// Moved to later today: still slipping, under the new date's key. A
	// reschedule is not progress.
	row.DueAt = created.Add(time.Hour).UnixMilli()
	reconcileReminderRows(t, store, time.Now(), row)
	moments = engagementPurposeSignals(reminderDetail(t, store, "deck"), time.Now())
	if len(moments) != 1 || moments[0].Kind != engagement.KindCommitmentSlipping {
		t.Fatalf("a rescheduled reminder due soon still slips: %+v", moments)
	}

	// Ticked off: resolved, no moment.
	row.CompletedAt = time.Now().UnixMilli()
	reconcileReminderRows(t, store, time.Now(), row)
	if moments := engagementPurposeSignals(reminderDetail(t, store, "deck"), time.Now()); len(moments) != 0 {
		t.Fatalf("a completed reminder must not slip: %+v", moments)
	}
}

func TestSummarizeReminders(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	rows := []storage.ReminderRow{
		{UID: "a", List: "Work", DueAt: now.Add(-time.Hour).UnixMilli()},
		{UID: "b", List: "Work"},
		{UID: "c", List: "Home", DueAt: now.Add(time.Hour).UnixMilli()},
		{UID: "d", List: "Home", CompletedAt: 1},
		{UID: "e", List: "Home", DueAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC).UnixMilli(), DueAllDay: true},
	}
	open, overdue, lists := summarizeReminders(rows, now, time.UTC)
	if open != 4 || overdue != 1 {
		t.Fatalf("open %d overdue %d", open, overdue)
	}
	if len(lists) != 2 || lists[0].Title != "Home" || lists[0].Open != 2 || lists[1].Overdue != 1 {
		t.Fatalf("lists %+v", lists)
	}
}

func TestRemindersSwitchOffRetainsReadsAndCommitments(t *testing.T) {
	ctx := context.Background()
	purposes := NewPurposeMapStore(newTestStore(t).db)
	src := &fakeReminderSource{list: []calendar.RawReminder{{UID: "deck", Title: "Send the deck", ListTitle: "Work"}}}
	svc, st := newRemindersTestService(t, src, time.Now())
	svc.onSynced = func(res remindersSyncResult) {
		rows, _ := st.ListReminders(ctx, true)
		if _, err := reconcileReminderCommitments(purposes, rows, res.At, time.UTC); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	svc.onCleared = func(rows []storage.ReminderRow) error {
		if _, err := closeReminderCommitments(purposes, rows, time.Now(), time.UTC); err != nil {
			t.Fatalf("close: %v", err)
		}
		return nil
	}
	enabled := true
	svc.enabled = func() bool { return enabled }

	svc.syncIfAllowed(ctx, "startup")
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateActive {
		t.Fatalf("commitment after first read %+v", got.Current)
	}

	// Switched off: saved reminders and commitments stay.
	enabled = false
	svc.syncIfAllowed(ctx, "switched off")
	if rows, _ := st.ListReminders(ctx, true); len(rows) != 1 {
		t.Fatalf("reminders lost after switch-off: %+v", rows)
	}
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateActive {
		t.Fatalf("commitment after switch-off %+v", got.Current)
	}

	// Switched back on: the still-open reminder keeps its commitment.
	enabled = true
	svc.now = time.Now
	svc.syncIfAllowed(ctx, "enabled")
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateActive || !got.Eligible {
		t.Fatalf("commitment after switch-on %+v", got.Current)
	}

	// Access revoked: saved data and derived commitments are still cleared.
	svc.granted = func() bool { return false }
	svc.syncIfAllowed(ctx, "interval")
	if rows, _ := st.ListReminders(ctx, true); len(rows) != 0 {
		t.Fatal("reminders kept after access was revoked")
	}
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateDismissed {
		t.Fatalf("commitment after revocation %+v", got.Current)
	}
}

// switchOffDuringRead is a reminders source whose read runs a hook, as a
// switch-off made while EventKit answers.
type switchOffDuringRead struct {
	fakeReminderSource
	during func()
}

func (f *switchOffDuringRead) reminders(completedSince time.Time) ([]calendar.RawReminder, error) {
	if f.during != nil {
		f.during()
	}
	return f.fakeReminderSource.reminders(completedSince)
}

func TestRemindersRefreshAfterSwitchOffWritesNothing(t *testing.T) {
	ctx := context.Background()
	src := &switchOffDuringRead{fakeReminderSource: fakeReminderSource{list: []calendar.RawReminder{{UID: "deck", Title: "Send the deck"}}}}
	svc, st := newRemindersTestService(t, &src.fakeReminderSource, time.Now())
	svc.source = src
	enabled := true
	svc.enabled = func() bool { return enabled }
	synced := 0
	svc.onSynced = func(remindersSyncResult) { synced++ }

	// Refresh (SyncRemindersNow) passed its checks; the user switches off
	// while EventKit answers, and the loop refuses the new observations.
	src.during = func() {
		enabled = false
		svc.syncIfAllowed(ctx, "switched off")
	}
	if _, err := svc.Sync(ctx, "refresh"); err != errRemindersStopped {
		t.Fatalf("refresh after switch-off: %v", err)
	}
	if rows, _ := st.ListReminders(ctx, true); len(rows) != 0 || synced != 0 {
		t.Fatalf("a refresh wrote reminders back after switch-off: %+v (synced %d)", rows, synced)
	}
}

func TestRemindersRevocationClosesCommitmentsLaterWhenPurposeIsNotReady(t *testing.T) {
	ctx := context.Background()
	purposes := NewPurposeMapStore(newTestStore(t).db)
	src := &fakeReminderSource{list: []calendar.RawReminder{{UID: "deck", Title: "Send the deck", ListTitle: "Work"}}}
	svc, st := newRemindersTestService(t, src, time.Now())
	svc.onSynced = func(res remindersSyncResult) {
		rows, _ := st.ListReminders(ctx, true)
		if _, err := reconcileReminderCommitments(purposes, rows, res.At, time.UTC); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	purposeReady := false
	var closedWith []storage.ReminderRow
	svc.onCleared = func(rows []storage.ReminderRow) error {
		if !purposeReady {
			return errors.New("Purpose storage is unavailable")
		}
		closedWith = rows
		_, err := closeReminderCommitments(purposes, rows, time.Now(), time.UTC)
		return err
	}
	granted := true
	svc.granted = func() bool { return granted }
	svc.syncIfAllowed(ctx, "startup")

	// Access revoked while the Purpose Map is not open: the reminders go at
	// once, and only their ids are kept to close the commitments later.
	granted = false
	svc.syncIfAllowed(ctx, "revoked")
	if rows, _ := st.ListReminders(ctx, true); len(rows) != 0 {
		t.Fatalf("reminders kept after revocation: %+v", rows)
	}
	if pending, _ := st.GetUIState(ctx, remindersPendingCloseKey); pending != `["deck"]` {
		t.Fatalf("pending closes %q", pending)
	}
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateActive {
		t.Fatalf("commitment closed without the Purpose Map: %+v", got.Current)
	}

	// The next pass closes it, from the id alone.
	purposeReady = true
	svc.syncIfAllowed(ctx, "interval")
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateDismissed {
		t.Fatalf("commitment left open with no reminder behind it: %+v", got.Current)
	}
	if len(closedWith) != 1 || closedWith[0].Title != "" {
		t.Fatalf("closed with %+v", closedWith)
	}
	if pending, _ := st.GetUIState(ctx, remindersPendingCloseKey); pending != "" {
		t.Fatalf("pending closes kept %q", pending)
	}

	// Access granted again: the still-open reminder reopens it.
	granted = true
	svc.now = time.Now
	svc.syncIfAllowed(ctx, "enabled")
	if got := reminderDetail(t, purposes, "deck"); got.Current.LifecycleState != PurposeStateActive {
		t.Fatalf("commitment after renewed grant %+v", got.Current)
	}
}

func TestRemindersResetTheSharedStoreOnlyOnANewGrant(t *testing.T) {
	ctx := context.Background()
	src := &fakeReminderSource{}
	svc, _ := newRemindersTestService(t, src, time.Now())
	granted := true
	svc.granted = func() bool { return granted }
	resets := 0
	svc.resetStore = func() { resets++ }

	svc.noteGrantAtStart()
	svc.syncIfAllowed(ctx, "startup")
	if resets != 0 {
		t.Fatal("a launch with access already granted reset the store the calendars use")
	}
	granted = false
	svc.syncIfAllowed(ctx, "interval")
	granted = true
	svc.syncIfAllowed(ctx, "interval")
	if resets != 1 {
		t.Fatalf("a grant made while running reset the store %d times", resets)
	}
}
