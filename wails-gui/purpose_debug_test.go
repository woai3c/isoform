package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func purposeDebugLedgerCounts(t *testing.T, store *ActivityStore) string {
	t.Helper()
	var counts string
	if err := store.db.QueryRow(`
		SELECT printf('%d|%d|%d|%d|%d|%d|%d|%d',
			(SELECT COUNT(*) FROM purpose_open_loops_v1),
			(SELECT COUNT(*) FROM purpose_open_loop_events_v1),
			(SELECT COUNT(*) FROM purpose_open_loop_evidence_v1),
			(SELECT COUNT(*) FROM purpose_open_loop_relations_v1),
			(SELECT COUNT(*) FROM purpose_open_loop_relation_events_v1),
			(SELECT COUNT(*) FROM purpose_extraction_runs_v1),
			(SELECT COUNT(*) FROM purpose_extraction_hypotheses_v1),
			(SELECT COUNT(*) FROM purpose_reconciliation_receipts_v1)
		)`).Scan(&counts); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestPurposeDebugReadsCompleteValidatedRoot(t *testing.T) {
	activity := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: activity}}
	understanding, err := NewPurposeUnderstandingService(app)
	if err != nil {
		t.Fatal(err)
	}
	understanding.mu.Lock()
	understanding.started = true
	understanding.current = &PurposeUnderstandingCycleStatus{
		State:        PurposeUnderstandingStateChecking,
		TriggerKinds: []string{PurposeUnderstandingTriggerMemory},
		Model:        "gpt-5.6-sol", Fingerprint: strings.Repeat("b", 64),
		IncludedCount: 4, CurrentPurposeCount: 1,
		ChangedRootIDs: []string{}, ChangedRelationIDs: []string{},
		RecoveredRunIDs: []string{}, StartedAt: time.Now().Add(-time.Second).UnixMilli(),
	}
	understanding.running = true
	understanding.mu.Unlock()
	app.purposeUnderstanding = understanding
	base := time.Now().Add(-time.Minute).UnixMilli()
	purposeStore := NewPurposeMapStore(activity.db)
	created := userPurposeForTest(
		t, purposeStore, "purpose-debug:create", PurposeKindGoal,
		"Prepare the launch decision",
		"The launch decision is supported by a verified readiness brief.",
		PurposeDueWindow, "This week", base,
	)
	detail, err := purposeStore.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := purposeTaskContextReference(detail)
	if err != nil {
		t.Fatal(err)
	}
	before := purposeDebugLedgerCounts(t, activity)
	snapshot, err := app.GetPurposeDebugSnapshot(10)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != purposeDebugVersion || snapshot.IntegrityStatus != "validated" ||
		!snapshot.Understanding.Started || !snapshot.Understanding.Running ||
		snapshot.Understanding.Current == nil ||
		snapshot.Understanding.Current.Fingerprint != strings.Repeat("b", 64) ||
		snapshot.Counts.OpenLoops != 1 || snapshot.Counts.EligibleOpenLoops != 1 ||
		len(snapshot.OpenLoops) != 1 {
		t.Fatalf("unexpected Purpose debug snapshot: %+v", snapshot)
	}
	root := snapshot.OpenLoops[0]
	if root.Purpose.ID != detail.OpenLoop.ID || !root.Eligibility.Eligible ||
		root.Eligibility.Decision != "included" ||
		root.Eligibility.ProjectionReference != ref ||
		!root.Eligibility.HasExplicitSupport || root.RootPayloadHash == "" {
		t.Fatalf("unexpected root summary: %+v", root)
	}
	rootDetail, err := app.GetPurposeDebugOpenLoop(detail.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rootDetail.Product.Purpose.ID != detail.OpenLoop.ID ||
		len(rootDetail.Product.Events) != 1 || len(rootDetail.Product.Evidence) != 1 ||
		rootDetail.RootPayloadJSON == "" {
		t.Fatalf("unexpected root detail: %+v", rootDetail)
	}
	if _, err := app.GetPurposeDebugSnapshot(10); err != nil {
		t.Fatal(err)
	}
	if after := purposeDebugLedgerCounts(t, activity); after != before {
		t.Fatalf("Purpose debug reads mutated state: before=%s after=%s", before, after)
	}
}

func TestPurposeDebugExtractionDetailOmitsRawSourceAndPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	activity := newTestStore(t)
	at := purposeExtractionTestTime(20)
	purposeWriteMemory(
		t, home, "purpose-debug-private-source", at, memorySource("manual"),
		"private-source-body-unique must never be returned by diagnostics",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-debug:extraction",
		CutoffAt:       purposeExtractionTestTime(21).UnixMilli(),
	}
	ref := purposeExtractionSourceRef(
		t, activity, request, "purpose-debug-private-source",
	)
	runner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"debug-purpose", "debug purpose", PurposeKindGoal,
			PurposeOperationSupport, "Inspect Purpose diagnostics",
			"Purpose provenance is inspectable.", ref,
		)),
	}}
	runtime := purposeNewRuntime(t, activity, runner)
	result, err := runtime.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: activity}}
	detail, err := app.GetPurposeDebugExtractionRun(result.Detail.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Sources) != 1 || len(detail.Manifest) == 0 ||
		len(detail.Hypotheses) != 1 || len(detail.Receipts) != 1 ||
		detail.Sources[0].IndependenceKey == "" || detail.Summary.PromptHash == "" ||
		detail.CurrentPurpose == nil ||
		detail.ManifestSummary.Version != PurposeExtractionManifestVersion ||
		detail.ManifestSummary.CurrentPurposeCount != 0 ||
		detail.ManifestSummary.CurrentPurposeHash == "" {
		t.Fatalf("unexpected Purpose extraction detail: %+v", detail)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-source-body-unique") ||
		strings.Contains(string(encoded), "You extract current operational purpose") {
		t.Fatalf("Purpose diagnostics exposed raw source content or prompt: %s", encoded)
	}
}

func TestPurposeDebugMethodsAreBrowserAllowlistedWithoutRawMutation(t *testing.T) {
	for _, method := range []string{
		"GetPurposeDebugSnapshot",
		"GetPurposeDebugOpenLoop",
		"GetPurposeDebugExtractionRun",
	} {
		if !browserRPCAllowlist[method] {
			t.Fatalf("%s is not browser RPC allowlisted", method)
		}
	}
	for _, method := range []string{
		"CreatePurposeOpenLoop",
		"AppendPurposeOpenLoopEvent",
		"CreatePurposeRelation",
		"RetractPurposeRelation",
		"RunPurposeExtraction",
		"ReplayPurposeExtraction",
		"RepairPurposeLedger",
	} {
		if browserRPCAllowlist[method] {
			t.Fatalf("raw Purpose debug mutation %s must not be allowlisted", method)
		}
	}
}
