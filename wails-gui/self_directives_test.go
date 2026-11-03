package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func countSelfDirectiveRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func selfDirectiveUnrelatedCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	tables := []string{
		"shadow_runs",
		"task_episodes",
		"authority_grants",
		"background_understanding_materializations_v1",
	}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		counts[table] = countSelfDirectiveRows(t, db, table)
	}
	return counts
}

func assertSelfDirectiveCountsEqual(
	t *testing.T,
	db *sql.DB,
	want map[string]int,
) {
	t.Helper()
	for table, expected := range want {
		if got := countSelfDirectiveRows(t, db, table); got != expected {
			t.Errorf("%s rows = %d, want %d", table, got, expected)
		}
	}
}

func createSelfDirectiveFixture(
	t *testing.T,
	key string,
) (*ActivityStore, *SelfDirectiveStore, SelfDirectiveMutationResult) {
	t.Helper()
	activity := newTestStore(t)
	store := NewSelfDirectiveStore(activity.db)
	result, added, err := store.Create(CreateSelfDirectiveInput{
		IdempotencyKey: key + ":create",
		Kind:           selfModelKindBoundary,
		Statement:      "Do not modify local work without a bounded reason.",
		Scope:          "Local project work",
		DeniedCapabilities: []string{
			"tool.write",
			"tool.read",
			"tool.write",
		},
		OccurredAt: 1_000,
	})
	if err != nil || !added {
		t.Fatalf("create Self directive fixture: added=%v err=%v", added, err)
	}
	return activity, store, result
}

func reviseSelfDirectiveFixture(
	t *testing.T,
	store *SelfDirectiveStore,
	created SelfDirectiveMutationResult,
	key string,
) SelfDirectiveMutationResult {
	t.Helper()
	result, added, err := store.Revise(ReviseSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: created.SelfEntry.ID,
		IdempotencyKey:      key + ":revise",
		Statement:           "Do not change local files unless the work stays inside the active boundary.",
		Scope:               "Local project work",
		DeniedCapabilities:  []string{"tool.write"},
		OccurredAt:          2_000,
	})
	if err != nil || !added {
		t.Fatalf("revise Self directive fixture: added=%v err=%v", added, err)
	}
	return result
}

func TestSelfDirectiveMigrationLifecycleAndImmutableLedger(t *testing.T) {
	activity := newTestStore(t)
	tx, err := activity.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateSelfUserDirectives(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := migrateSelfUserDirectives(nil); err == nil {
		t.Fatal("nil Self directive migration transaction was accepted")
	}

	unrelatedBefore := selfDirectiveUnrelatedCounts(t, activity.db)
	store := NewSelfDirectiveStore(activity.db)
	createInput := CreateSelfDirectiveInput{
		IdempotencyKey: "self-directive:lifecycle:create",
		Kind:           selfModelKindBoundary,
		Statement:      "Never write a local file outside the user's working boundary.",
		Scope:          "Local project work",
		DeniedCapabilities: []string{
			"tool.write",
			"tool.read",
			"tool.write",
		},
		OccurredAt: 1_000,
	}
	created, added, err := store.Create(createInput)
	if err != nil || !added {
		t.Fatalf("create directive: added=%v err=%v", added, err)
	}
	if created.Event.Sequence != 1 || created.SelfEntry.Revision != 1 ||
		created.SelfEntry.Status != selfModelStatusActive ||
		strings.Join(created.Event.DeniedCapabilities, ",") != "tool.read,tool.write" {
		t.Fatalf("unexpected created directive: %+v", created)
	}
	replayed, added, err := store.Create(CreateSelfDirectiveInput{
		IdempotencyKey:     createInput.IdempotencyKey,
		Kind:               createInput.Kind,
		Statement:          createInput.Statement,
		Scope:              createInput.Scope,
		DeniedCapabilities: createInput.DeniedCapabilities,
		OccurredAt:         9_000,
	})
	if err != nil || added || replayed.Event.ID != created.Event.ID ||
		replayed.Event.OccurredAt != created.Event.OccurredAt {
		t.Fatalf("create replay: result=%+v added=%v err=%v", replayed, added, err)
	}
	conflict := createInput
	conflict.Statement = "A conflicting statement."
	if _, _, err := store.Create(conflict); err == nil {
		t.Fatal("conflicting directive creation replay was accepted")
	}

	for _, mutation := range []struct {
		name  string
		query string
		arg   string
	}{
		{
			name:  "root update",
			query: "UPDATE self_user_directives_v1 SET kind = 'principle' WHERE id = ?",
			arg:   created.Directive.ID,
		},
		{
			name:  "root delete",
			query: "DELETE FROM self_user_directives_v1 WHERE id = ?",
			arg:   created.Directive.ID,
		},
		{
			name:  "event update",
			query: "UPDATE self_user_directive_events_v1 SET statement = 'tampered' WHERE id = ?",
			arg:   created.Event.ID,
		},
		{
			name:  "event delete",
			query: "DELETE FROM self_user_directive_events_v1 WHERE id = ?",
			arg:   created.Event.ID,
		},
		{
			name:  "Self update",
			query: "UPDATE self_model_entries SET statement = 'tampered' WHERE id = ?",
			arg:   created.SelfEntry.ID,
		},
		{
			name:  "Self delete",
			query: "DELETE FROM self_model_entries WHERE id = ?",
			arg:   created.SelfEntry.ID,
		},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := activity.db.Exec(mutation.query, mutation.arg); err == nil {
				t.Fatalf("immutable %s was accepted", mutation.name)
			}
		})
	}

	revised, added, err := store.Revise(ReviseSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: created.SelfEntry.ID,
		IdempotencyKey:      "self-directive:lifecycle:revise",
		Statement:           "Never write outside the active local project boundary.",
		Scope:               "Local project work",
		DeniedCapabilities:  []string{"tool.write"},
		OccurredAt:          2_000,
	})
	if err != nil || !added || revised.Event.Sequence != 2 ||
		revised.SelfEntry.Revision != 2 {
		t.Fatalf("revise directive: result=%+v added=%v err=%v", revised, added, err)
	}
	revisionReplay, added, err := store.Revise(ReviseSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: created.SelfEntry.ID,
		IdempotencyKey:      revised.Event.IdempotencyKey,
		Statement:           revised.Event.Statement,
		Scope:               revised.Event.Scope,
		DeniedCapabilities:  revised.Event.DeniedCapabilities,
		OccurredAt:          8_000,
	})
	if err != nil || added || revisionReplay.Event.ID != revised.Event.ID {
		t.Fatalf("revision replay: result=%+v added=%v err=%v", revisionReplay, added, err)
	}
	if _, _, err := store.Revise(ReviseSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: created.SelfEntry.ID,
		IdempotencyKey:      "self-directive:lifecycle:stale",
		Statement:           "Stale edit",
		OccurredAt:          3_000,
	}); err == nil {
		t.Fatal("stale directive revision was accepted")
	}
	if _, _, err := store.Revise(ReviseSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: revised.SelfEntry.ID,
		IdempotencyKey:      "self-directive:lifecycle:backdated",
		Statement:           "Backdated edit",
		OccurredAt:          1_500,
	}); err == nil {
		t.Fatal("backdated directive revision was accepted")
	}

	retracted, added, err := store.Retract(RetractSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: revised.SelfEntry.ID,
		IdempotencyKey:      "self-directive:lifecycle:retract",
		Reason:              "This boundary no longer expresses my intent.",
		OccurredAt:          3_000,
	})
	if err != nil || !added || retracted.Event.Sequence != 3 ||
		retracted.SelfEntry.Status != selfModelStatusRetracted {
		t.Fatalf("retract directive: result=%+v added=%v err=%v", retracted, added, err)
	}
	retractionReplay, added, err := store.Retract(RetractSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: revised.SelfEntry.ID,
		IdempotencyKey:      retracted.Event.IdempotencyKey,
		Reason:              retracted.Event.RetractionReason,
		OccurredAt:          8_000,
	})
	if err != nil || added || retractionReplay.Event.ID != retracted.Event.ID {
		t.Fatalf("retraction replay: result=%+v added=%v err=%v", retractionReplay, added, err)
	}
	if _, _, err := store.Revise(ReviseSelfDirectiveInput{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: retracted.SelfEntry.ID,
		IdempotencyKey:      "self-directive:lifecycle:reactivate",
		Statement:           "Reactivate old identity",
		OccurredAt:          4_000,
	}); err == nil {
		t.Fatal("retracted directive was reactivated")
	}

	detail, err := store.GetDetail(created.Directive.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Events) != 3 || len(detail.Revisions) != 3 ||
		detail.Current.ID != retracted.SelfEntry.ID {
		t.Fatalf("unexpected directive detail: %+v", detail)
	}
	if got := countSelfDirectiveRows(t, activity.db, "self_user_directives_v1"); got != 1 {
		t.Fatalf("directive roots = %d, want 1", got)
	}
	if got := countSelfDirectiveRows(t, activity.db, "self_user_directive_events_v1"); got != 3 {
		t.Fatalf("directive events = %d, want 3", got)
	}
	if got := countSelfDirectiveRows(t, activity.db, "self_model_entries"); got != 3 {
		t.Fatalf("Self revisions = %d, want 3", got)
	}
	assertSelfDirectiveCountsEqual(t, activity.db, unrelatedBefore)
}

func TestSelfDirectiveRejectsGrantLikeOrUnknownPolicies(t *testing.T) {
	activity := newTestStore(t)
	store := NewSelfDirectiveStore(activity.db)
	tests := []CreateSelfDirectiveInput{
		{
			IdempotencyKey:     "self-directive:invalid:principle-policy",
			Kind:               selfModelKindPrinciple,
			Statement:          "Prefer concise explanations.",
			DeniedCapabilities: []string{"tool.read"},
			OccurredAt:         1_000,
		},
		{
			IdempotencyKey:     "self-directive:invalid:wildcard",
			Kind:               selfModelKindBoundary,
			Statement:          "Deny everything.",
			DeniedCapabilities: []string{"tool.*"},
			OccurredAt:         1_000,
		},
		{
			IdempotencyKey:     "self-directive:invalid:external",
			Kind:               selfModelKindBoundary,
			Statement:          "Do not send messages.",
			DeniedCapabilities: []string{"slack.message.send"},
			OccurredAt:         1_000,
		},
	}
	for _, input := range tests {
		if _, _, err := store.Create(input); err == nil {
			t.Errorf("invalid Self directive was accepted: %+v", input)
		}
	}
	if got := countSelfDirectiveRows(t, activity.db, "self_user_directives_v1"); got != 0 {
		t.Fatalf("invalid inputs created %d roots", got)
	}
	if got := countSelfDirectiveRows(t, activity.db, "self_model_entries"); got != 0 {
		t.Fatalf("invalid inputs created %d Self entries", got)
	}
}

func TestSelfDirectiveCompleteDetailReadIsNotPresentationLimited(t *testing.T) {
	activity := newTestStore(t)
	store := NewSelfDirectiveStore(activity.db)
	for index, statement := range []string{
		"Preserve a reversible path.",
		"State uncertainty before acting.",
	} {
		if _, added, err := store.Create(CreateSelfDirectiveInput{
			IdempotencyKey: fmt.Sprintf("complete-read:%d", index),
			Kind:           selfModelKindPrinciple,
			Statement:      statement,
			OccurredAt:     int64(index + 1),
		}); err != nil || !added {
			t.Fatalf("create directive %d: added=%v err=%v", index, added, err)
		}
	}
	limited, err := store.List(1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("limited list = %d, err=%v", len(limited), err)
	}
}

func dropSelfDirectiveTrigger(t *testing.T, db *sql.DB, trigger string) {
	t.Helper()
	if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
		t.Fatalf("drop trigger %s: %v", trigger, err)
	}
}

func rewriteSelfDirectiveEvent(
	t *testing.T,
	db *sql.DB,
	event SelfDirectiveEventRecord,
) {
	t.Helper()
	payloadJSON, payloadHash, err := selfDirectiveEventPayloadJSON(event)
	if err != nil {
		t.Fatal(err)
	}
	capabilitiesJSON, err := json.Marshal(event.DeniedCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE self_user_directive_events_v1
		SET sequence = ?, expected_self_entry_id = ?, self_entry_id = ?,
			self_source_hash = ?, denied_capabilities_json = ?,
			payload_json = ?, payload_hash = ?
		WHERE id = ?`,
		event.Sequence,
		event.ExpectedSelfEntryID,
		event.SelfEntryID,
		event.SelfSourceHash,
		string(capabilitiesJSON),
		payloadJSON,
		payloadHash,
		event.ID,
	); err != nil {
		t.Fatal(err)
	}
}

func TestSelfDirectiveReadsFailClosedOnTampering(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(t *testing.T, activity *ActivityStore, store *SelfDirectiveStore, created, revised SelfDirectiveMutationResult)
	}{
		{
			name: "root payload",
			tamper: func(t *testing.T, activity *ActivityStore, _ *SelfDirectiveStore, created, _ SelfDirectiveMutationResult) {
				dropSelfDirectiveTrigger(t, activity.db, "self_user_directives_v1_no_update")
				if _, err := activity.db.Exec(`
					UPDATE self_user_directives_v1 SET payload_json = '{}'
					WHERE id = ?`, created.Directive.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "event payload",
			tamper: func(t *testing.T, activity *ActivityStore, _ *SelfDirectiveStore, _, revised SelfDirectiveMutationResult) {
				dropSelfDirectiveTrigger(t, activity.db, "self_user_directive_events_v1_no_update")
				if _, err := activity.db.Exec(`
					UPDATE self_user_directive_events_v1 SET payload_hash = 'tampered'
					WHERE id = ?`, revised.Event.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "event sequence",
			tamper: func(t *testing.T, activity *ActivityStore, _ *SelfDirectiveStore, _, revised SelfDirectiveMutationResult) {
				dropSelfDirectiveTrigger(t, activity.db, "self_user_directive_events_v1_no_update")
				revised.Event.Sequence = 3
				rewriteSelfDirectiveEvent(t, activity.db, revised.Event)
			},
		},
		{
			name: "event predecessor relation",
			tamper: func(t *testing.T, activity *ActivityStore, _ *SelfDirectiveStore, _, revised SelfDirectiveMutationResult) {
				dropSelfDirectiveTrigger(t, activity.db, "self_user_directive_events_v1_no_update")
				revised.Event.ExpectedSelfEntryID = "wrong-predecessor"
				rewriteSelfDirectiveEvent(t, activity.db, revised.Event)
			},
		},
		{
			name: "Self source hash",
			tamper: func(t *testing.T, activity *ActivityStore, _ *SelfDirectiveStore, _, revised SelfDirectiveMutationResult) {
				dropSelfDirectiveTrigger(t, activity.db, "self_model_entries_no_update")
				if _, err := activity.db.Exec(`
					UPDATE self_model_entries SET source_hash = 'tampered'
					WHERE id = ?`, revised.SelfEntry.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Self revision relation",
			tamper: func(t *testing.T, activity *ActivityStore, _ *SelfDirectiveStore, _, revised SelfDirectiveMutationResult) {
				dropSelfDirectiveTrigger(t, activity.db, "self_model_entries_no_update")
				if _, err := activity.db.Exec(`
					UPDATE self_model_entries SET revision = 5
					WHERE id = ?`, revised.SelfEntry.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity, store, created := createSelfDirectiveFixture(t, "tamper:"+test.name)
			revised := reviseSelfDirectiveFixture(t, store, created, "tamper:"+test.name)
			test.tamper(t, activity, store, created, revised)
			if _, err := store.GetDetail(created.Directive.ID); err == nil {
				t.Fatal("tampered Self directive detail was accepted")
			}
		})
	}
}

func TestTypedSelfReadsVerifySourceHash(t *testing.T) {
	activity := newTestStore(t)
	entry, added, err := activity.AddSelfModelEntry(SelfModelEntry{
		Kind:            selfModelKindPreference,
		Statement:       "Prefer focused test output.",
		Status:          selfModelStatusActive,
		Confidence:      0.9,
		ReasoningStatus: "source_backed",
		Evidence: []SelfModelEvidence{{
			SourceKind: "test",
			SourceID:   "typed-self-integrity",
			Confidence: 0.9,
			OccurredAt: 1_000,
		}},
		CreatedAt: 1_000,
	})
	if err != nil || !added {
		t.Fatalf("add Self entry: added=%v err=%v", added, err)
	}
	dropSelfDirectiveTrigger(t, activity.db, "self_model_entries_no_update")
	if _, err := activity.db.Exec(`
		UPDATE self_model_entries SET statement = 'tampered statement'
		WHERE id = ?`, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := activity.GetSelfModelEntryByID(entry.ID); err == nil {
		t.Fatal("tampered typed Self entry was returned by ID")
	}
	if _, err := activity.ListCurrentSelfModelEntries(); err == nil {
		t.Fatal("tampered typed Self entry was returned by list")
	}
}
