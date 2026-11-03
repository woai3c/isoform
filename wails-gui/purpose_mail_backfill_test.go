package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func purposeMailBackfillTestDB(t *testing.T, path string, mail bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if mail {
		if _, err := db.Exec(`CREATE TABLE mail_messages (subject TEXT NOT NULL, at INTEGER NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		return db
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migratePurposeMap(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func purposeMailBackfillTestCreate(t *testing.T, store *PurposeMapStore, key, title, evidenceClass string) PurposeOpenLoopDetail {
	t.Helper()
	at := time.Now().Add(-time.Hour).UnixMilli()
	input := purposeTestCreateInput(key, PurposeKindGoal, key,
		[]PurposeEvidenceInput{purposeTestEvidence(key, evidenceClass, PurposeStanceSupport, at)}, at)
	input.Title = title
	created, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestPurposeMailBackfillDryRunDoesNotWriteOrCreateSchema(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	before := purposeMailBackfillTestCreate(t, store, "dry", "Private board review", PurposeEvidenceObservedUser)
	asOf := time.Now()
	if _, err := mail.Exec(`INSERT INTO mail_messages VALUES (?, ?)`, before.Current.Title, asOf.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	counts, err := backfillPurposeMailTitles(activity, mail, true, asOf)
	if err != nil || counts != (purposeMailBackfillCounts{Scanned: 1, Matched: 1}) {
		t.Fatalf("dry run: %+v, %v", counts, err)
	}
	var exists int
	if err := activity.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'purpose_mail_title_repairs_v1'`).Scan(&exists); err != nil || exists != 0 {
		t.Fatalf("dry run changed schema: %d, %v", exists, err)
	}
	after, err := store.GetDetail(before.OpenLoop.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("dry run changed Purpose: %v", err)
	}
}

func TestPurposeMailBackfillPreservesLedgerLifecycleAndHistoricalReads(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	for _, state := range []string{PurposeStateCandidate, PurposeStateActive, PurposeStateWaiting, PurposeStateResolved, PurposeStateDismissed, PurposeStateContradicted} {
		class := PurposeEvidenceExplicitUser
		if state == PurposeStateCandidate {
			class = PurposeEvidenceObservedUser
		}
		detail := purposeMailBackfillTestCreate(t, store, state, "Private review "+state, class)
		eventType, stance := "", ""
		switch state {
		case PurposeStateWaiting:
			eventType, stance = PurposeEventWaiting, PurposeStanceWait
		case PurposeStateResolved:
			eventType, stance = PurposeEventResolved, PurposeStanceResolve
		case PurposeStateDismissed:
			eventType, stance = PurposeEventDismissed, PurposeStanceDismiss
		case PurposeStateContradicted:
			eventType, stance = PurposeEventContradicted, PurposeStanceContradict
		}
		if eventType != "" {
			input := purposeTestAppendInput(detail, state+"-transition", eventType, stance, PurposeEvidenceExplicitUser, state+"-transition", time.Now().Add(-time.Minute).UnixMilli())
			if state == PurposeStateWaiting {
				input.WaitingCondition = "Waiting for a reply"
			}
			if _, _, err := store.AppendEvent(input); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := mail.Exec(`INSERT INTO mail_messages VALUES (?, ?)`, detail.Current.Title, time.Now().Add(-time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	before, err := loadPurposeLedger(activity)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UnixMilli()
	// Ensure the before-repair historical cutoff is distinct at millisecond precision.
	time.Sleep(2 * time.Millisecond)
	asOf := time.Now()
	counts, err := backfillPurposeMailTitles(activity, mail, false, asOf)
	if err != nil || counts != (purposeMailBackfillCounts{Scanned: 6, Matched: 6, Retitled: 6}) {
		t.Fatalf("apply: %+v, %v", counts, err)
	}
	raw, err := loadPurposeLedger(activity)
	if err != nil || !reflect.DeepEqual(before, raw) {
		t.Fatalf("repair changed immutable ledger or provenance: %v", err)
	}
	historical, err := store.ListDetailsAt(cutoff)
	if err != nil || !reflect.DeepEqual(before, historical) {
		t.Fatalf("repair changed historical projection: %v", err)
	}
	all, err := store.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	afterAt, err := store.ListDetailsAt(asOf.UnixMilli())
	if err != nil || !reflect.DeepEqual(all, afterAt) {
		t.Fatalf("public readers disagree: %v", err)
	}
	for i, original := range before {
		repaired, err := store.GetDetail(original.OpenLoop.ID)
		if err != nil || !reflect.DeepEqual(repaired, all[i]) {
			t.Fatalf("lookup differs from list: %v", err)
		}
		if repaired.Current.Title != "Goal to review" || mobileGoalView(repaired, asOf).Title != "Goal to review" {
			t.Fatal("repaired title missing from Purpose or goal view")
		}
		if repaired.Eligible != original.Eligible || repaired.Current.LifecycleState != original.Current.LifecycleState ||
			repaired.Current.SupportState != original.Current.SupportState || !reflect.DeepEqual(repaired.Events, original.Events) ||
			!reflect.DeepEqual(repaired.Evidence, original.Evidence) || repaired.Current.PayloadHash != original.Current.PayloadHash {
			t.Fatal("repair changed lifecycle, support, eligibility or immutable history")
		}
		var oldTitle, authorityEffect string
		if err := activity.QueryRow(`SELECT original_title, authority_effect FROM purpose_mail_title_repairs_v1 WHERE event_id = ?`, original.Current.ID).Scan(&oldTitle, &authorityEffect); err != nil || oldTitle != original.Current.Title || authorityEffect != "none" {
			t.Fatalf("missing local original-title metadata: %v", err)
		}
		oldRef, err := purposeTaskContextReference(original)
		if err != nil {
			t.Fatal(err)
		}
		newRef, err := purposeTaskContextReference(repaired)
		if err != nil || oldRef == newRef {
			t.Fatal("repaired display content did not get a distinct projection reference")
		}
		if content := purposeTaskContextContent(repaired, "hash"); strings.Contains(content, original.Current.Title) || !strings.Contains(content, "Title: Goal to review") {
			t.Fatal("task context used the original title")
		}
	}
	input, err := purposeExtractionCurrentPurposeAt(store, asOf.UnixMilli())
	if err != nil || len(input) != 6 {
		t.Fatalf("extraction current Purpose: %v", err)
	}
	for _, item := range input {
		if item.Title != "Goal to review" {
			t.Fatal("extraction current Purpose retained mail subject")
		}
	}
	counts, err = backfillPurposeMailTitles(activity, mail, false, time.Now())
	if err != nil || counts != (purposeMailBackfillCounts{Scanned: 6, Matched: 6, AlreadyRepaired: 6}) {
		t.Fatalf("repeat was not idempotent: %+v, %v", counts, err)
	}
	if _, err := activity.Exec(`UPDATE purpose_mail_title_repairs_v1 SET replacement_title = 'Changed'`); err == nil {
		t.Fatal("repair metadata was mutable")
	}
	if _, err := activity.Exec(`DELETE FROM purpose_mail_title_repairs_v1`); err == nil {
		t.Fatal("repair metadata could be deleted")
	}
}

func TestPurposeMailBackfillMatchesOnlyExactRecentSubjects(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	asOf := time.Now()
	for _, title := range []string{"Hi", "PRIVATE   REVIEW", "Discuss private review next week", "Old topic", "Future topic", "Unrelated goal"} {
		purposeMailBackfillTestCreate(t, store, title, title, PurposeEvidenceObservedUser)
	}
	for _, item := range []struct {
		subject string
		at      time.Time
	}{
		{"Hi", asOf.Add(-time.Hour)},
		{"Re: Private review", asOf.Add(-time.Hour)},
		{"Old topic", asOf.Add(-8 * 24 * time.Hour)},
		{"Future topic", asOf.Add(time.Hour)},
	} {
		if _, err := mail.Exec(`INSERT INTO mail_messages VALUES (?, ?)`, item.subject, item.at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := backfillPurposeMailTitles(activity, mail, false, time.Now())
	if err != nil || counts != (purposeMailBackfillCounts{Scanned: 6, Matched: 2, Retitled: 2}) {
		t.Fatalf("exact recent subject match: %+v, %v", counts, err)
	}
	details, err := store.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, detail := range details {
		if detail.Current.Title != "Goal to review" {
			continue
		}
		// A later intentional title revision wins; the repair targets one event.
		input := purposeTestAppendInput(detail, detail.OpenLoop.ID+"-rename", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "user-rename", time.Now().UnixMilli())
		input.Title = "My own goal title"
		if _, _, err := store.AppendEvent(input); err != nil {
			t.Fatal(err)
		}
		updated, err := store.GetDetail(detail.OpenLoop.ID)
		if err != nil || updated.Current.Title != input.Title {
			t.Fatalf("repair overrode a later user title: %v", err)
		}
	}
}

func TestPurposeMailBackfillCommandUsesExplicitFilesAndCountsOnlyLogs(t *testing.T) {
	dir := t.TempDir()
	activityPath, mailPath := filepath.Join(dir, "activity.db"), filepath.Join(dir, "mail.db")
	activity := purposeMailBackfillTestDB(t, activityPath, false)
	mail := purposeMailBackfillTestDB(t, mailPath, true)
	detail := purposeMailBackfillTestCreate(t, NewPurposeMapStore(activity), "command", "Private merger terms", PurposeEvidenceObservedUser)
	if _, err := mail.Exec(`INSERT INTO mail_messages VALUES (?, ?)`, detail.Current.Title, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	activityBytes, err := os.ReadFile(activityPath)
	if err != nil {
		t.Fatal(err)
	}
	mailBytes, err := os.ReadFile(mailPath)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{purposeMailBackfillCommand, "--activity-db", activityPath, "--mail-db", mailPath}
	code, handled := runPurposeMailBackfillCommand(append(append([]string{}, args...), "--dry-run"), &output)
	if !handled || code != 0 || output.String() != "[purpose-mail-backfill] dry_run=true scanned=1 matched=1 retitled=0 already_repaired=0\n" {
		t.Fatalf("dry command: %d, %v, %s", code, handled, output.String())
	}
	activityAfter, err := os.ReadFile(activityPath)
	if err != nil || !bytes.Equal(activityBytes, activityAfter) {
		t.Fatalf("dry command modified activity file: %v", err)
	}
	mailAfter, err := os.ReadFile(mailPath)
	if err != nil || !bytes.Equal(mailBytes, mailAfter) {
		t.Fatalf("dry command modified mail file: %v", err)
	}
	output.Reset()
	code, handled = runPurposeMailBackfillCommand(args, &output)
	if !handled || code != 0 || output.String() != "[purpose-mail-backfill] dry_run=false scanned=1 matched=1 retitled=1 already_repaired=0\n" {
		t.Fatalf("apply command: %d, %v, %s", code, handled, output.String())
	}
	if strings.Contains(output.String(), detail.Current.Title) || strings.Contains(output.String(), activityPath) {
		t.Fatal("command logs contained private titles or paths")
	}
	output.Reset()
	missing := filepath.Join(dir, "missing.db")
	if code, handled := runPurposeMailBackfillCommand([]string{purposeMailBackfillCommand, "--activity-db", missing, "--mail-db", mailPath}, &output); !handled || code != 1 {
		t.Fatalf("nonexistent file not rejected: %d, %v", code, handled)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("command created a missing database")
	}
	if code, handled := runPurposeMailBackfillCommand([]string{purposeMailBackfillCommand}, &output); !handled || code != 2 {
		t.Fatal("command accepted implicit home paths")
	}
	if _, handled := runPurposeMailBackfillCommand([]string{"--other-command"}, &output); handled {
		t.Fatal("backfill claimed another command")
	}
}

func purposeMailBackfillTestRepair(t *testing.T, store *PurposeMapStore, mail *sql.DB, detail PurposeOpenLoopDetail) (PurposeOpenLoopDetail, time.Time) {
	t.Helper()
	asOf := time.Now()
	if _, err := mail.Exec(`INSERT INTO mail_messages VALUES (?, ?)`, detail.Current.Title, asOf.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if counts, err := backfillPurposeMailTitles(store.db, mail, false, asOf); err != nil || counts.Retitled != 1 {
		t.Fatalf("repair fixture: %+v, %v", counts, err)
	}
	repaired, err := store.GetDetail(detail.OpenLoop.ID)
	if err != nil || repaired.Current.Title != purposeMailNeutralTitle(detail.OpenLoop.Kind) {
		t.Fatalf("read repaired fixture: %v", err)
	}
	return repaired, asOf
}

func TestPurposeMailBackfillRepairFollowsUnchangedTitleLineage(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	original := purposeMailBackfillTestCreate(t, store, "lineage", "Private acquisition review", PurposeEvidenceExplicitUser)
	repaired, repairTime := purposeMailBackfillTestRepair(t, store, mail, original)
	time.Sleep(2 * time.Millisecond)
	input := purposeTestAppendInput(repaired, "lineage-due-date", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceObservedUser, "due-date", time.Now().UnixMilli())
	input.Title = original.Current.Title
	input.DueKind, input.DueValue = PurposeDueDate, "2027-01-01"
	if _, _, err := store.AppendEvent(input); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetDetail(original.OpenLoop.ID)
	if err != nil || updated.Current.Title != repaired.Current.Title || updated.Current.ID == original.Current.ID {
		t.Fatalf("unchanged-title update lost repair: %+v, %v", updated.Current, err)
	}
	if raw := purposeCurrentLedgerEvent(updated); raw.Title != original.Current.Title || raw.DueValue != input.DueValue {
		t.Fatal("due-date update changed the authoritative title")
	}
	prior, err := store.ListDetailsAt(repairTime.UnixMilli())
	if err != nil || len(prior) != 1 || prior[0].Current.ID != original.Current.ID || prior[0].Current.Title != repaired.Current.Title {
		t.Fatalf("historical repair cutoff changed: %+v, %v", prior, err)
	}
	counts, err := backfillPurposeMailTitles(activity, mail, false, time.Now())
	if err != nil || counts.AlreadyRepaired != 1 || counts.Retitled != 0 {
		t.Fatalf("unchanged-title descendant duplicated repair metadata: %+v, %v", counts, err)
	}
	for index, title := range []string{"My own revised goal", original.Current.Title} {
		input := purposeTestAppendInput(updated, title, PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "rename", time.Now().UnixMilli())
		input.Title = title
		if _, _, err := store.AppendEvent(input); err != nil {
			t.Fatal(err)
		}
		updated, err = store.GetDetail(original.OpenLoop.ID)
		if err != nil || updated.Current.Title != title {
			t.Fatalf("explicit title change %d did not end the old repair lineage: %+v, %v", index, updated.Current, err)
		}
	}
}

func TestPurposeMailBackfillUserLifecycleKeepsRawAuthoredTitle(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	original := purposeMailBackfillTestCreate(t, store, "user-lifecycle", "Private planning request", PurposeEvidenceExplicitUser)
	detail, _ := purposeMailBackfillTestRepair(t, store, mail, original)
	for _, action := range []string{userPurposeWaiting, userPurposeResolve, userPurposeReopen} {
		input, err := userPurposeAppendInput(detail, userPurposeTransition{
			Key: action, Origin: "desktop", OpenLoopID: detail.OpenLoop.ID,
			ExpectedEventID: detail.Current.ID, Action: action, WaitingOn: "A response",
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if input.Title != original.Current.Title {
			t.Fatal("display repair became lifecycle authored text")
		}
		expectedEvidence := userPurposeEvidence(input.IdempotencyKey, "desktop", input.Evidence[0].Stance, original.Current.Title, input.OccurredAt)
		if !reflect.DeepEqual(input.Evidence[0], expectedEvidence) {
			t.Fatal("display repair became user evidence")
		}
		if _, _, err := store.AppendEvent(input); err != nil {
			t.Fatal(err)
		}
		detail, err = store.GetDetail(original.OpenLoop.ID)
		if err != nil || detail.Current.Title != "Goal to review" || purposeCurrentLedgerEvent(detail).Title != original.Current.Title {
			t.Fatalf("lifecycle lost raw/display title separation: %v", err)
		}
	}
}

func TestPurposeMailBackfillReminderUpdatesRetainRepairWithoutAuthorship(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	row := storage.ReminderRow{UID: "repaired-reminder", Title: "Private renewal request"}
	if result, err := reconcileReminderCommitments(store, []storage.ReminderRow{row}, time.Now(), time.UTC); err != nil || result.Created != 1 {
		t.Fatalf("create reminder: %+v, %v", result, err)
	}
	original, err := store.GetDetail(reminderPurposeRootID(row.UID))
	if err != nil {
		t.Fatal(err)
	}
	purposeMailBackfillTestRepair(t, store, mail, original)
	check := func() {
		t.Helper()
		detail, err := store.GetDetail(original.OpenLoop.ID)
		if err != nil || detail.Current.Title != "Commitment to review" {
			t.Fatalf("reminder change lost title repair: %+v, %v", detail.Current, err)
		}
		for _, event := range detail.Events {
			if event.Title != original.Current.Title {
				t.Fatal("reminder lifecycle persisted the projected title as authored text")
			}
		}
	}
	row.DueAt = time.Now().Add(time.Hour).UnixMilli()
	if result, err := reconcileReminderCommitments(store, []storage.ReminderRow{row}, time.Now(), time.UTC); err != nil || result.Revised != 1 {
		t.Fatalf("change due date: %+v, %v", result, err)
	}
	check()
	row.CompletedAt = time.Now().UnixMilli()
	if result, err := reconcileReminderCommitments(store, []storage.ReminderRow{row}, time.Now(), time.UTC); err != nil || result.Resolved != 1 {
		t.Fatalf("complete reminder: %+v, %v", result, err)
	}
	check()
	row.CompletedAt = 0
	if result, err := reconcileReminderCommitments(store, []storage.ReminderRow{row}, time.Now(), time.UTC); err != nil || result.Reopened != 1 {
		t.Fatalf("reopen reminder: %+v, %v", result, err)
	}
	check()
	if count, err := closeReminderCommitments(store, []storage.ReminderRow{row}, time.Now(), time.UTC); err != nil || count != 1 {
		t.Fatalf("close reminder source: %d, %v", count, err)
	}
	check()
	row.Title = "My new reminder title"
	if result, err := reconcileReminderCommitments(store, []storage.ReminderRow{row}, time.Now(), time.UTC); err != nil || result.Reopened != 1 {
		t.Fatalf("reopen renamed reminder: %+v, %v", result, err)
	}
	updated, err := store.GetDetail(original.OpenLoop.ID)
	if err != nil || updated.Current.Title != row.Title {
		t.Fatalf("intentional reminder rename was overridden: %+v, %v", updated.Current, err)
	}
}

func TestPurposeMailBackfillContextReaderKeepsRepairAndCancellation(t *testing.T) {
	activity := purposeMailBackfillTestDB(t, ":memory:", false)
	mail := purposeMailBackfillTestDB(t, ":memory:", true)
	store := NewPurposeMapStore(activity)
	original := purposeMailBackfillTestCreate(t, store, "context-reader", "Private planning review", PurposeEvidenceExplicitUser)
	repaired, _ := purposeMailBackfillTestRepair(t, store, mail, original)
	details, err := store.ListDetailsAllContext(context.Background())
	if err != nil || len(details) != 1 || !reflect.DeepEqual(details[0], repaired) {
		t.Fatalf("context reader lost repaired projection: %+v, %v", details, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ListDetailsAllContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("context reader ignored cancellation: %v", err)
	}
	queryer := purposeContextQueryer{ctx: ctx, db: activity}
	if _, err := projectPurposeMailTitleRepairs(queryer, []PurposeOpenLoopDetail{original}, time.Now().UnixMilli()); !errors.Is(err, context.Canceled) {
		t.Fatalf("repair metadata read ignored cancellation: %v", err)
	}
}
