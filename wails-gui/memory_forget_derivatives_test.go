package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func seedMemoryForgetSelf(t *testing.T, store *ActivityStore, id, statement string) SelfModelEntry {
	t.Helper()
	entry, _, err := store.AddSelfModelEntry(SelfModelEntry{
		Kind: selfModelKindPreference, Scope: "project:yansu", Statement: statement,
		Status: selfModelStatusActive, Confidence: 0.9, ReasoningStatus: "explicit_user",
		Evidence: []SelfModelEvidence{{SourceKind: taskContextSourceMemoryKind, SourceID: id,
			SourceRefs: []string{"memory:" + id}, Confidence: 1, OccurredAt: time.Now().Add(-time.Minute).UnixMilli()}},
		CreatedAt: time.Now().Add(-time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func memoryForgetTriggerDefinitions(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		result[name] = ddl
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMemoryForgetDeletesDerivedLearning(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	const id = "forget-commitment"
	purposeWriteMemory(t, os.Getenv("HOME"), id, time.Now().Add(-time.Hour), memorySource("manual"), "I will send the customer update on Friday.")
	forgotten := seedMemoryForgetSelf(t, rig.store, id, "Draft concise project updates.")
	independent := seedMemoryForgetSelf(t, rig.store, id+"-other", "Keep independent notes.")
	addSelfDreamTestEntry(t, rig.store, "Draft concise project updates", time.Now().Add(-time.Minute).UnixMilli())
	dream, err := rig.app.runSelfDreamShadow(context.Background(), "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	var compilation SelfDreamCompilationRecord
	for _, operation := range dream.Operations {
		if operation.Operation != SelfDreamOperationMerge {
			continue
		}
		reviewed, err := rig.app.ReviewSelfDreamOperation(ReviewSelfDreamOperationInput{
			OperationID: operation.ID, IdempotencyKey: "forget-merge", Decision: SelfDreamDecisionAccept,
		})
		if err != nil {
			t.Fatal(err)
		}
		compilation = reviewed.Compilations[0]
		break
	}
	if compilation.ID == "" {
		t.Fatal("test requires a compiled Self Dream output")
	}
	request := PurposeExtractionRequest{IdempotencyKey: "forget-purpose", CutoffAt: time.Now().UnixMilli()}
	ref := purposeExtractionSourceRef(t, rig.store, request, id)
	runtime := purposeNewRuntime(t, rig.store, &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis("update", "customer update", PurposeKindCommitment,
			PurposeOperationSupport, "Send customer update", "Send the Friday update.", ref)),
	}})
	purpose, err := runtime.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(purpose.Detail.Receipts) != 1 {
		t.Fatalf("expected one Purpose receipt: %+v", purpose)
	}
	// An independent Purpose root must survive even when a relation to the
	// erased root needs removal.
	independentEvidence := purposeTestEvidence("unrelated", PurposeEvidenceExplicitUser, PurposeStanceSupport, time.Now().UnixMilli())
	// A source from another provenance namespace may legitimately share the
	// memory's human-readable ID without citing that memory.
	independentEvidence.SourceID = id
	independentPurpose, _, err := runtime.purpose.Create(purposeTestCreateInput("keep-purpose", PurposeKindGoal, "independent",
		[]PurposeEvidenceInput{independentEvidence}, time.Now().UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []struct{ id, ref string }{{"forget-profile", "memory:" + id}, {"keep-profile", "memory:" + id + "-other"}} {
		_, err := rig.store.db.Exec(`INSERT INTO work_pattern_profile_snapshots_v1
			(id, policy_version, source_window_start, source_cutoff_at, source_hash, status, sample_count, session_count, confidence, canonical_json, sha256, created_at)
			VALUES (?, 'forget-test', 1, 2, ?, 'qualified', 1, 1, 1, ?, ?, 3)`, snapshot.id, snapshot.id,
			`{"semanticSignals":[{"ref":"`+snapshot.ref+`"}]}`, snapshot.id)
		if err != nil {
			t.Fatal(err)
		}
	}
	beforeTriggers := memoryForgetTriggerDefinitions(t, rig.store.db)
	tx, err := rig.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	preview, err := collectMemoryForgetDerivatives(tx, id)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range preview {
		seen[item.ID] = true
	}
	for _, expected := range []string{forgotten.ID, dream.Run.ID, compilation.ID, compilation.OutputEntryIDs[0],
		purpose.Detail.Run.ID, purpose.Detail.Receipts[0].OpenLoopID, "forget-profile"} {
		if !seen[expected] {
			t.Errorf("preview omitted derivative %s", expected)
		}
	}
	for _, unrelated := range []string{independent.ID, independentPurpose.OpenLoop.ID, "keep-profile"} {
		if seen[unrelated] {
			t.Errorf("preview included independent record %s", unrelated)
		}
	}
	if err := deleteMemoryForgetDerivatives(tx, id); err != nil {
		t.Fatal(err)
	}
	remaining, err := collectMemoryForgetDerivatives(tx, id)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("derivatives remain: %+v err=%v", remaining, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := memoryForgetTriggerDefinitions(t, rig.store.db); !reflect.DeepEqual(beforeTriggers, after) {
		t.Fatal("forget changed immutable ledger trigger definitions")
	}
	for _, check := range []struct{ table, id string }{
		{"self_model_entries", independent.ID}, {"purpose_open_loops_v1", independentPurpose.OpenLoop.ID}, {"work_pattern_profile_snapshots_v1", "keep-profile"},
	} {
		var count int
		if err := rig.store.db.QueryRow(`SELECT COUNT(*) FROM `+check.table+` WHERE id = ?`, check.id).Scan(&count); err != nil || count != 1 {
			t.Errorf("independent %s/%s missing: count=%d err=%v", check.table, check.id, count, err)
		}
		if _, err := rig.store.db.Exec(`DELETE FROM `+check.table+` WHERE id = ?`, check.id); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("immutable guard was not restored for %s: %v", check.table, err)
		}
	}
	var foreignKeyViolations int
	if err := rig.store.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyViolations); err != nil || foreignKeyViolations != 0 {
		t.Fatalf("foreign-key violations=%d err=%v", foreignKeyViolations, err)
	}
}

func TestMemoryForgetDerivativesRollbackRestoresRowsAndGuards(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	entry := seedMemoryForgetSelf(t, rig.store, "forget-rollback", "Keep this until deletion succeeds.")
	if _, err := rig.store.db.Exec(`CREATE TRIGGER reject_test_erasure BEFORE DELETE ON self_model_entries BEGIN SELECT RAISE(ABORT, 'test erasure failure'); END`); err != nil {
		t.Fatal(err)
	}
	before := memoryForgetTriggerDefinitions(t, rig.store.db)
	tx, err := rig.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = deleteMemoryForgetDerivatives(tx, "forget-rollback")
	if err == nil || !strings.Contains(err.Error(), "test erasure failure") {
		t.Fatalf("delete failure was not surfaced: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.store.GetSelfModelEntryByID(entry.ID); err != nil {
		t.Fatalf("failed delete lost source entry: %v", err)
	}
	if after := memoryForgetTriggerDefinitions(t, rig.store.db); !reflect.DeepEqual(before, after) {
		t.Fatal("failed delete did not restore original triggers")
	}
}

func TestMemoryForgetRejectsLateDerivedPersistence(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	const id = "forgotten-during-inference"
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, id, []string{"# Forgotten\n\nPrivate source."}, time.Now()); err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(PurposeExtractionInput{Sources: []PurposeExtractionSource{{SourceKind: taskContextSourceMemoryKind, SourceID: id}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = persistPurposeExtractionRun(rig.store.db, PurposeExtractionRunRecord{InputJSON: string(input)}, nil)
	if !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("late Purpose persistence error=%v", err)
	}
	_, _, err = rig.store.AddSelfDreamRun(SelfDreamRunRecord{Manifest: []TaskContextManifestEntry{{SourceKind: taskContextSourceMemoryKind, SourceID: id, Included: true}}})
	if !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("late Self Dream persistence error=%v", err)
	}
	for _, table := range []string{"self_dream_runs_v1", "purpose_extraction_runs_v1"} {
		if count := purposeCountRows(t, rig.store, table); count != 0 {
			t.Errorf("late %s persisted %d rows", table, count)
		}
	}
}

func TestMemoryForgetPreservesIndependentDirectiveRevision(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	directives := NewSelfDirectiveStore(rig.store.db)
	directive, _, err := directives.Create(CreateSelfDirectiveInput{
		IdempotencyKey: "forget-independent-directive", Kind: selfModelKindBoundary,
		Statement: "Do not modify local work without a bounded reason.", Scope: "Local project work",
		DeniedCapabilities: []string{"tool.write"}, OccurredAt: time.Now().Add(-time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	derived := directive.SelfEntry
	derived.Status = selfModelStatusRetracted
	derived.ReasoningStatus = "self_dream_superseded"
	derived.CreatedAt = time.Now().UnixMilli()
	derived.Evidence = append(derived.Evidence, SelfModelEvidence{SourceKind: "self_dream_supersession", SourceID: "decision",
		SourceRefs: []string{"memory:forget-mixed"}, Confidence: 1, OccurredAt: derived.CreatedAt})
	// Seed historical compiler output directly. Current background ingestion
	// protects user directives from revisions outside their lifecycle ledger.
	var added bool
	{
		seedTx, beginErr := rig.store.db.Begin()
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		derived, added, err = addSelfModelEntryTx(seedTx, derived)
		if err != nil {
			seedTx.Rollback()
			t.Fatal(err)
		}
		if err := seedTx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil || !added {
		t.Fatalf("append derived revision: added=%v err=%v", added, err)
	}
	tx, err := rig.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	preview, err := collectMemoryForgetDerivatives(tx, "forget-mixed")
	if err != nil || len(preview) != 1 || preview[0].ID != derived.ID {
		t.Fatalf("mixed-provenance preview=%+v err=%v", preview, err)
	}
	if err := deleteMemoryForgetDerivatives(tx, "forget-mixed"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	detail, err := directives.GetDetail(directive.Directive.ID)
	if err != nil || detail.Current.ID != directive.SelfEntry.ID || len(detail.Events) != 1 {
		t.Fatalf("independent directive changed: detail=%+v err=%v", detail, err)
	}
}

func TestMemoryForgetPublicFlowRechecksDerivativesAndClearsBothStores(t *testing.T) {
	app, _ := newForgetTestApp(t)
	activity := newTestStore(t)
	app.activity = &activityMonitorState{store: activity}
	entry, err := app.SaveMemoryEntry("forget-public-cascade", "# Preference\n\nPrefer private concise project updates.")
	if err != nil {
		t.Fatal(err)
	}
	first := seedMemoryForgetSelf(t, activity, entry.ID, "Keep updates concise.")
	if err := app.memoryStore.Sync("forget-public-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.memoryStore.db.Exec(`UPDATE memory_chunks SET embedding = '[1,0]' WHERE path = ?`, entry.ID+".md"); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := seedMemoryForgetSelf(t, activity, entry.ID, "New learning appeared after preview.")
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err == nil {
		t.Fatal("confirmation accepted a changed derivative scope")
	}
	if _, err := app.GetMemoryEntry(entry.ID); err != nil {
		t.Fatalf("stale derivative preview removed memory: %v", err)
	}
	preview, err = app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 3 || preview.EmbeddingCount != 1 {
		t.Fatalf("public preview omitted derivatives or embedding: %+v", preview)
	}
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, err := activity.GetSelfModelEntryByID(id); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("public forget retained derived Self %s: %v", id, err)
		}
	}
	if _, err := app.GetMemoryEntry(entry.ID); err == nil {
		t.Fatal("public forget retained the memory file")
	}
	for _, table := range []string{"memory_files", "memory_chunks", "memory_chunks_fts"} {
		var count int
		if err := app.memoryStore.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("public forget retained index rows %s: count=%d err=%v", table, count, err)
		}
	}
}
