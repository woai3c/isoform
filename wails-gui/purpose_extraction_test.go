package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

type purposeExtractionFakeRunner struct {
	responses []string
	errors    []error
	inputs    []*agent.TaskInput
}

func (runner *purposeExtractionFakeRunner) Init(context.Context) error { return nil }
func (runner *purposeExtractionFakeRunner) IsReady() bool              { return true }
func (runner *purposeExtractionFakeRunner) ExecuteTask(
	_ context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	runner.inputs = append(runner.inputs, input)
	index := len(runner.inputs) - 1
	if index < len(runner.responses) && runner.responses[index] != "" {
		if err := callback(&agent.StreamEvent{
			Type: "assistant", Content: runner.responses[index],
		}); err != nil {
			return "", err
		}
	}
	if index < len(runner.errors) && runner.errors[index] != nil {
		return "", runner.errors[index]
	}
	return "purpose-extraction-test-run", nil
}
func (runner *purposeExtractionFakeRunner) CancelRun(string) error { return nil }
func (runner *purposeExtractionFakeRunner) RespondToPermission(string, string) error {
	return nil
}
func (runner *purposeExtractionFakeRunner) CancelPermission(string) {}
func (runner *purposeExtractionFakeRunner) Shutdown() error         { return nil }

func purposeExtractionTestTime(minute int) time.Time {
	return time.Date(2026, time.July, 29, 9, minute, 0, 0, time.UTC)
}

func purposeWriteMemory(
	t *testing.T,
	home, id string,
	at time.Time,
	source memorySource,
	point string,
) string {
	t.Helper()
	path := filepath.Join(home, configDirName, "memory", id+".md")
	writeTaskContextSourceTestFile(
		t,
		path,
		buildOKFMemoryMarkdown(
			id,
			"Purpose source "+id,
			at,
			source,
			"topic_highlight",
			[]string{point},
		),
		at,
	)
	return path
}

func purposeExtractionSourceRef(
	t *testing.T,
	store *ActivityStore,
	request PurposeExtractionRequest,
	sourceID string,
) string {
	t.Helper()
	input, _, err := gatherPurposeExtractionInput(store, request)
	if err != nil {
		t.Fatalf("gather Purpose extraction input: %v", err)
	}
	for _, source := range input.Sources {
		if source.SourceID == sourceID {
			return source.Ref
		}
	}
	t.Fatalf("Purpose source %q is missing", sourceID)
	return ""
}

func purposeExtractionOutputJSON(
	t *testing.T,
	hypotheses ...PurposeExtractionHypothesis,
) string {
	t.Helper()
	data, err := json.Marshal(PurposeExtractionOutput{
		Version:    PurposeExtractionOutputVersion,
		Hypotheses: hypotheses,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func purposeExtractionHypothesis(
	localKey, canonicalKey, kind, operation, title, outcome string,
	evidenceRefs ...string,
) PurposeExtractionHypothesis {
	return PurposeExtractionHypothesis{
		LocalKey:        localKey,
		CanonicalKey:    canonicalKey,
		Kind:            kind,
		Operation:       operation,
		Title:           title,
		DesiredOutcome:  outcome,
		Owner:           PurposeOwnerUser,
		DueKind:         PurposeDueNone,
		ReasoningStatus: PurposeReasoningUnknown,
		Confidence:      0.78,
		EvidenceRefs:    evidenceRefs,
	}
}

func purposeNewRuntime(
	t *testing.T,
	store *ActivityStore,
	runner agent.AgentRunner,
) *PurposeExtractionRuntime {
	t.Helper()
	runtime, err := NewPurposeExtractionRuntime(store, runner, "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func purposeCountRows(t *testing.T, store *ActivityStore, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPurposeExtractionRunIsNoToolsIdempotentAndAuthorityNeutral(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	at := purposeExtractionTestTime(1)
	sourcePath := purposeWriteMemory(
		t, home, "explicit-commitment", at, memorySource("manual"),
		"I will send the customer update on Friday.",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-no-tools",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	ref := purposeExtractionSourceRef(t, store, request, "explicit-commitment")
	runner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"customer-update", "customer update", PurposeKindCommitment,
			PurposeOperationSupport, "Send customer update",
			"The customer receives the Friday update.", ref,
		)),
	}}
	runtime := purposeNewRuntime(t, store, runner)
	before := purposeLedgerSideEffectCounts(t, store)
	result, err := runtime.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("run Purpose extraction: %v", err)
	}
	if !result.Added || len(result.Detail.Hypotheses) != 1 ||
		len(result.Detail.Receipts) != 1 ||
		result.Detail.Receipts[0].Disposition != PurposeDispositionCreated {
		t.Fatalf("unexpected Purpose extraction result: %+v", result)
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("model calls = %d, want 1", len(runner.inputs))
	}
	input := runner.inputs[0]
	if !input.PlanMode || !input.Ephemeral || !input.NoTools || !input.NoSystem ||
		!input.SuppressTelemetry || !input.AutoApproveTools || input.Model != "gpt-5.6-sol" ||
		input.BackendHint != agent.BackendHintLearning {
		t.Fatalf("Purpose extraction escaped no-tools isolation: %+v", input)
	}
	details, err := runtime.purpose.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Current.LifecycleState != PurposeStateActive ||
		details[0].Current.SupportState != PurposeSupportSupported {
		t.Fatalf("explicit Purpose source was not activated: %+v", details)
	}
	if after := purposeLedgerSideEffectCounts(t, store); after != before {
		t.Fatalf("Purpose extraction changed unrelated state: before=%s after=%s", before, after)
	}

	replayed, err := runtime.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("replay Purpose extraction: %v", err)
	}
	if replayed.Added || len(runner.inputs) != 1 ||
		purposeCountRows(t, store, "purpose_open_loop_events_v1") != 1 ||
		purposeCountRows(t, store, "purpose_reconciliation_receipts_v1") != 1 {
		t.Fatalf("Purpose extraction replay was not inert: %+v calls=%d", replayed, len(runner.inputs))
	}
	writeTaskContextSourceTestFile(
		t,
		sourcePath,
		buildOKFMemoryMarkdown(
			"explicit-commitment", "Purpose source explicit-commitment", at,
			memorySource("manual"), "topic_highlight",
			[]string{"I changed this source after the run was fully reconciled."},
		),
		at,
	)
	if _, err := runtime.Run(context.Background(), request); err != nil {
		t.Fatalf("fully reconciled historical replay consulted mutable source: %v", err)
	}
	for _, method := range []string{
		"RunPurposeExtraction",
		"ReconcilePurposeExtraction",
		"ReplayPurposeExtraction",
	} {
		if browserRPCAllowlist[method] {
			t.Fatalf("%s must remain unavailable through browser RPC", method)
		}
	}
}

func TestPurposeExtractionUnknownEvidenceFailsWithoutPurposeMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	purposeWriteMemory(
		t, home, "known-source", purposeExtractionTestTime(1), memorySource("manual"),
		"I am responsible for the release note.",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-unknown-ref",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	runner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"release-note", "release note", PurposeKindResponsibility,
			PurposeOperationSupport, "Publish release note",
			"The release note is published.", "missing-evidence-ref",
		)),
	}}
	runtime := purposeNewRuntime(t, store, runner)
	result, err := runtime.Run(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "unavailable evidence") {
		t.Fatalf("unknown evidence error = %v", err)
	}
	if result.Detail.Run.Status != PurposeExtractionStatusFailed || !result.Added ||
		len(result.Detail.Hypotheses) != 0 || len(result.Detail.Receipts) != 0 {
		t.Fatalf("failed extraction was not retained safely: %+v", result)
	}
	if purposeCountRows(t, store, "purpose_open_loops_v1") != 0 ||
		purposeCountRows(t, store, "purpose_open_loop_events_v1") != 0 {
		t.Fatal("invalid model evidence mutated Purpose")
	}
}

func TestPurposeExtractionPromotesOnlyAfterIndependentRepeatedEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	purposeWriteMemory(
		t, home, "observed-a", purposeExtractionTestTime(1), memorySource("activity"),
		"The user repeatedly prepares a weekly launch summary.",
	)
	requestOne := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-independent-1",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	refA := purposeExtractionSourceRef(t, store, requestOne, "observed-a")
	runnerOne := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"launch-summary", "weekly launch summary", PurposeKindResponsibility,
			PurposeOperationSupport, "Prepare weekly launch summary",
			"A current launch summary is available each week.", refA,
		)),
	}}
	runtimeOne := purposeNewRuntime(t, store, runnerOne)
	if _, err := runtimeOne.Run(context.Background(), requestOne); err != nil {
		t.Fatal(err)
	}
	details, err := runtimeOne.purpose.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Current.LifecycleState != PurposeStateCandidate ||
		details[0].Eligible {
		t.Fatalf("one evaluated source became supported: %+v", details)
	}

	purposeWriteMemory(
		t, home, "observed-b", purposeExtractionTestTime(3), memorySource("activity"),
		"A separate work session also shows the weekly launch summary being prepared.",
	)
	requestTwo := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-independent-2",
		CutoffAt:       purposeExtractionTestTime(4).UnixMilli(),
	}
	refA = purposeExtractionSourceRef(t, store, requestTwo, "observed-a")
	refB := purposeExtractionSourceRef(t, store, requestTwo, "observed-b")
	runnerTwo := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"launch-summary", "weekly launch summary", PurposeKindResponsibility,
			PurposeOperationSupport, "Prepare weekly launch summary",
			"A current launch summary is available each week.", refA, refB,
		)),
	}}
	runtimeTwo := purposeNewRuntime(t, store, runnerTwo)
	result, err := runtimeTwo.Run(context.Background(), requestTwo)
	if err != nil {
		t.Fatal(err)
	}
	if result.Detail.Receipts[0].Disposition != PurposeDispositionReinforced {
		t.Fatalf("second evidence disposition = %q", result.Detail.Receipts[0].Disposition)
	}
	details, err = runtimeTwo.purpose.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Current.LifecycleState != PurposeStateActive ||
		!details[0].Eligible || len(details[0].Events) != 2 {
		t.Fatalf("independent evidence did not activate Purpose: %+v", details)
	}
}

func TestPurposeExtractionAppliesExplicitUpdateWaitAndResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	type step struct {
		id        string
		minute    int
		operation string
		title     string
		outcome   string
		point     string
		waiting   string
		want      string
	}
	steps := []step{
		{"lifecycle-create", 1, PurposeOperationSupport, "Send launch plan", "The team receives the launch plan.", "I will send the launch plan.", "", PurposeDispositionCreated},
		{"lifecycle-update", 3, PurposeOperationUpdate, "Send revised launch plan", "The team receives the revised launch plan.", "I changed the commitment to a revised launch plan.", "", PurposeDispositionRevised},
		{"lifecycle-wait", 5, PurposeOperationWait, "Send revised launch plan", "The team receives the revised launch plan.", "I am waiting for finance approval before sending it.", "Finance approval arrives", PurposeDispositionWaiting},
		{"lifecycle-resolve", 7, PurposeOperationResolve, "Send revised launch plan", "The team receives the revised launch plan.", "I sent the revised launch plan and completed this commitment.", "", PurposeDispositionResolved},
	}
	var runtime *PurposeExtractionRuntime
	for index, item := range steps {
		purposeWriteMemory(
			t, home, item.id, purposeExtractionTestTime(item.minute), memorySource("manual"), item.point,
		)
		request := PurposeExtractionRequest{
			IdempotencyKey: "purpose-test-" + item.id,
			CutoffAt:       purposeExtractionTestTime(item.minute + 1).UnixMilli(),
		}
		ref := purposeExtractionSourceRef(t, store, request, item.id)
		hypothesis := purposeExtractionHypothesis(
			item.id, "launch plan", PurposeKindCommitment, item.operation,
			item.title, item.outcome, ref,
		)
		hypothesis.WaitingCondition = item.waiting
		runner := &purposeExtractionFakeRunner{responses: []string{
			purposeExtractionOutputJSON(t, hypothesis),
		}}
		runtime = purposeNewRuntime(t, store, runner)
		result, err := runtime.Run(context.Background(), request)
		if err != nil {
			t.Fatalf("lifecycle step %d: %v", index, err)
		}
		if got := result.Detail.Receipts[0].Disposition; got != item.want {
			t.Fatalf("lifecycle step %d disposition = %q, want %q", index, got, item.want)
		}
	}
	details, err := runtime.purpose.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Current.LifecycleState != PurposeStateResolved ||
		details[0].Current.Title != "Send revised launch plan" ||
		len(details[0].Events) != 4 {
		t.Fatalf("explicit lifecycle projection is wrong: %+v", details)
	}
}

func TestPurposeExtractionDoesNotResolveFromEvaluatedProjection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	for index, id := range []string{"eval-a", "eval-b"} {
		purposeWriteMemory(
			t, home, id, purposeExtractionTestTime(index+1), memorySource("activity"),
			"Observed work indicates the monthly report remains an active outcome.",
		)
	}
	createRequest := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-eval-create",
		CutoffAt:       purposeExtractionTestTime(4).UnixMilli(),
	}
	refA := purposeExtractionSourceRef(t, store, createRequest, "eval-a")
	refB := purposeExtractionSourceRef(t, store, createRequest, "eval-b")
	createRunner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"monthly-report", "monthly report", PurposeKindResponsibility,
			PurposeOperationSupport, "Finish monthly report",
			"The monthly report is complete.", refA, refB,
		)),
	}}
	if _, err := purposeNewRuntime(t, store, createRunner).Run(
		context.Background(), createRequest,
	); err != nil {
		t.Fatal(err)
	}
	purposeWriteMemory(
		t, home, "eval-c", purposeExtractionTestTime(5), memorySource("activity"),
		"A model projection says the monthly report may be finished.",
	)
	resolveRequest := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-eval-resolve",
		CutoffAt:       purposeExtractionTestTime(6).UnixMilli(),
	}
	refC := purposeExtractionSourceRef(t, store, resolveRequest, "eval-c")
	resolveRunner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"monthly-report-resolve", "monthly report", PurposeKindResponsibility,
			PurposeOperationResolve, "Finish monthly report",
			"The monthly report is complete.", refC,
		)),
	}}
	runtime := purposeNewRuntime(t, store, resolveRunner)
	result, err := runtime.Run(context.Background(), resolveRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Detail.Receipts) != 1 ||
		result.Detail.Receipts[0].Disposition != PurposeDispositionRejected ||
		!strings.Contains(result.Detail.Receipts[0].Reason, "explicit completion") {
		t.Fatalf("evaluated resolution was not rejected: %+v", result.Detail.Receipts)
	}
	details, err := runtime.purpose.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	if details[0].Current.LifecycleState != PurposeStateActive || len(details[0].Events) != 1 {
		t.Fatalf("evaluated projection resolved Purpose: %+v", details[0])
	}
}

func TestPurposeReconciliationFailsClosedOnFrozenSourceDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	at := purposeExtractionTestTime(1)
	path := purposeWriteMemory(
		t, home, "drift-source", at, memorySource("manual"),
		"I will prepare the board update.",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-source-drift",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	ref := purposeExtractionSourceRef(t, store, request, "drift-source")
	runner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"board-update", "board update", PurposeKindCommitment,
			PurposeOperationSupport, "Prepare board update",
			"The board update is ready.", ref,
		)),
	}}
	runtime := purposeNewRuntime(t, store, runner)
	input, manifest, err := gatherPurposeExtractionInput(store, request)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runPurposeExtractionModel(
		context.Background(), runner, runtime.model, input, manifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	run, hypotheses, err := purposeExtractionRecords(request, runtime.model, execution, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := persistPurposeExtractionRun(store.db, run, hypotheses); err != nil {
		t.Fatal(err)
	}
	writeTaskContextSourceTestFile(
		t,
		path,
		buildOKFMemoryMarkdown(
			"drift-source", "Purpose source drift-source", at,
			memorySource("manual"), "topic_highlight",
			[]string{"I will prepare a different board artifact."},
		),
		at,
	)
	if _, err := runtime.Reconcile(run.ID); err == nil ||
		!strings.Contains(err.Error(), "drifted or is unavailable") {
		t.Fatalf("source drift error = %v", err)
	}
	if purposeCountRows(t, store, "purpose_open_loops_v1") != 0 ||
		purposeCountRows(t, store, "purpose_reconciliation_receipts_v1") != 0 {
		t.Fatal("source drift produced Purpose state")
	}
}

func TestPurposeDuplicateHintsRequireOverlapAndNeverCreateDuplicateRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	purposeWriteMemory(
		t, home, "canonical-source", purposeExtractionTestTime(1), memorySource("manual"),
		"I will send the Friday customer update.",
	)
	createRequest := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-duplicate-canonical",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	canonicalRef := purposeExtractionSourceRef(t, store, createRequest, "canonical-source")
	createRunner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"canonical", "customer update", PurposeKindCommitment,
			PurposeOperationSupport, "Send Friday customer update",
			"The customer receives the Friday update.", canonicalRef,
		)),
	}}
	if _, err := purposeNewRuntime(t, store, createRunner).Run(
		context.Background(), createRequest,
	); err != nil {
		t.Fatal(err)
	}

	purposeWriteMemory(
		t, home, "unrelated-source", purposeExtractionTestTime(3), memorySource("manual"),
		"I am also drafting a weekly customer note.",
	)
	rejectRequest := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-duplicate-reject",
		CutoffAt:       purposeExtractionTestTime(4).UnixMilli(),
	}
	unrelatedRef := purposeExtractionSourceRef(t, store, rejectRequest, "unrelated-source")
	rejectedHypothesis := purposeExtractionHypothesis(
		"duplicate-reject", "weekly customer note", PurposeKindCommitment,
		PurposeOperationSupport, "Send Friday customer update",
		"The customer receives the Friday update.", unrelatedRef,
	)
	rejectedHypothesis.DuplicateOfCanonicalKey = "customer update"
	rejectRunner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, rejectedHypothesis),
	}}
	rejected, err := purposeNewRuntime(t, store, rejectRunner).Run(
		context.Background(), rejectRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Detail.Receipts[0].Disposition != PurposeDispositionRejected ||
		!strings.Contains(rejected.Detail.Receipts[0].Reason, "no source overlap") {
		t.Fatalf("weak duplicate hint was not rejected: %+v", rejected.Detail.Receipts)
	}

	acceptRequest := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-duplicate-accept",
		CutoffAt:       purposeExtractionTestTime(5).UnixMilli(),
	}
	canonicalRef = purposeExtractionSourceRef(t, store, acceptRequest, "canonical-source")
	acceptedHypothesis := purposeExtractionHypothesis(
		"duplicate-accept", "friday status", PurposeKindCommitment,
		PurposeOperationSupport, "Send Friday customer update",
		"The customer receives the Friday update.", canonicalRef,
	)
	acceptedHypothesis.DuplicateOfCanonicalKey = "customer update"
	acceptRunner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, acceptedHypothesis),
	}}
	accepted, err := purposeNewRuntime(t, store, acceptRunner).Run(
		context.Background(), acceptRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Detail.Receipts[0].Disposition != PurposeDispositionDeduplicated ||
		purposeCountRows(t, store, "purpose_open_loops_v1") != 1 ||
		purposeCountRows(t, store, "purpose_open_loop_events_v1") != 1 {
		t.Fatalf("source-backed duplicate did not route to canonical root: %+v", accepted.Detail.Receipts)
	}
}

func TestPurposeReconciliationRecoversEventWithoutAppendingAgain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	recoveryPath := purposeWriteMemory(
		t, home, "recovery-source", purposeExtractionTestTime(1), memorySource("manual"),
		"I will prepare the recovery brief.",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-recovery",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	ref := purposeExtractionSourceRef(t, store, request, "recovery-source")
	runner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"recovery-brief", "recovery brief", PurposeKindCommitment,
			PurposeOperationSupport, "Prepare recovery brief",
			"The recovery brief is ready.", ref,
		)),
	}}
	runtime := purposeNewRuntime(t, store, runner)
	input, manifest, err := gatherPurposeExtractionInput(store, request)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runPurposeExtractionModel(
		context.Background(), runner, runtime.model, input, manifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	run, hypotheses, err := purposeExtractionRecords(request, runtime.model, execution, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := persistPurposeExtractionRun(store.db, run, hypotheses); err != nil {
		t.Fatal(err)
	}
	createInput, err := purposeCreateInputForHypothesis(
		hypotheses[0],
		purposeSourcesByRef(input),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, added, err := runtime.purpose.Create(createInput); err != nil || !added {
		t.Fatalf("simulate interrupted Purpose event: added=%v err=%v", added, err)
	}
	if purposeCountRows(t, store, "purpose_reconciliation_receipts_v1") != 0 {
		t.Fatal("interrupted fixture unexpectedly has a receipt")
	}
	writeTaskContextSourceTestFile(
		t,
		recoveryPath,
		buildOKFMemoryMarkdown(
			"recovery-source", "Purpose source recovery-source", purposeExtractionTestTime(1),
			memorySource("manual"), "topic_highlight",
			[]string{"The live source changed after the immutable event committed."},
		),
		purposeExtractionTestTime(1),
	)
	detail, err := runtime.Reconcile(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Receipts) != 1 ||
		detail.Receipts[0].Disposition != PurposeDispositionCreated ||
		purposeCountRows(t, store, "purpose_open_loop_events_v1") != 1 {
		t.Fatalf("interrupted Purpose event was not recovered: %+v", detail)
	}
	if _, err := runtime.Reconcile(run.ID); err != nil {
		t.Fatal(err)
	}
	if purposeCountRows(t, store, "purpose_open_loop_events_v1") != 1 ||
		purposeCountRows(t, store, "purpose_reconciliation_receipts_v1") != 1 {
		t.Fatal("Purpose recovery replay appended duplicate rows")
	}
}

func TestPurposeExtractionLedgersAreImmutableAndCorruptionFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	purposeWriteMemory(
		t, home, "immutable-source", purposeExtractionTestTime(1), memorySource("manual"),
		"I will preserve the immutable audit trail.",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-immutable",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	ref := purposeExtractionSourceRef(t, store, request, "immutable-source")
	runner := &purposeExtractionFakeRunner{responses: []string{
		purposeExtractionOutputJSON(t, purposeExtractionHypothesis(
			"immutable", "immutable audit", PurposeKindResponsibility,
			PurposeOperationSupport, "Preserve audit trail",
			"The audit trail remains inspectable.", ref,
		)),
	}}
	runtime := purposeNewRuntime(t, store, runner)
	result, err := runtime.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE purpose_extraction_runs_v1 SET model = 'changed'",
		"UPDATE purpose_extraction_hypotheses_v1 SET title = 'changed'",
		"UPDATE purpose_reconciliation_receipts_v1 SET reason = 'changed'",
		"DELETE FROM purpose_extraction_runs_v1",
		"DELETE FROM purpose_extraction_hypotheses_v1",
		"DELETE FROM purpose_reconciliation_receipts_v1",
	} {
		if _, err := store.db.Exec(statement); err == nil {
			t.Fatalf("immutable Purpose extraction SQL succeeded: %s", statement)
		}
	}
	if _, err := store.db.Exec("DROP TRIGGER purpose_reconciliation_receipts_v1_no_update"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE purpose_reconciliation_receipts_v1
		SET payload_hash = ? WHERE id = ?`,
		strings.Repeat("0", 64),
		result.Detail.Receipts[0].ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GetRun(result.Detail.Run.ID); err == nil ||
		!strings.Contains(err.Error(), "payload is inconsistent") {
		t.Fatalf("corrupt Purpose receipt read error = %v", err)
	}
}

func TestPurposeExtractionModelFailureIsPersistedWithoutRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	purposeWriteMemory(
		t, home, "failure-source", purposeExtractionTestTime(1), memorySource("manual"),
		"I will retain failed extraction provenance.",
	)
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-model-failure",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	}
	runner := &purposeExtractionFakeRunner{
		errors: []error{errors.New("fixture model unavailable")},
	}
	runtime := purposeNewRuntime(t, store, runner)
	first, err := runtime.Run(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "fixture model unavailable") {
		t.Fatalf("model failure error = %v", err)
	}
	if first.Detail.Run.Status != PurposeExtractionStatusFailed || !first.Added ||
		len(runner.inputs) != 1 {
		t.Fatalf("model failure was not retained: %+v calls=%d", first, len(runner.inputs))
	}
	second, err := runtime.Run(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "fixture model unavailable") {
		t.Fatalf("stored model failure replay error = %v", err)
	}
	if second.Added || len(runner.inputs) != 1 {
		t.Fatalf("failed extraction replay called model: %+v calls=%d", second, len(runner.inputs))
	}
	if purposeCountRows(t, store, "purpose_open_loops_v1") != 0 {
		t.Fatal("failed model run mutated Purpose")
	}
}

func TestPurposeExtractionNoSourcesDoesNotCallModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	runner := &purposeExtractionFakeRunner{}
	runtime := purposeNewRuntime(t, store, runner)
	result, err := runtime.Run(context.Background(), PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-no-sources",
		CutoffAt:       purposeExtractionTestTime(2).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Detail.Run.Status != PurposeExtractionStatusNoSources ||
		len(runner.inputs) != 0 || len(result.Detail.Hypotheses) != 0 ||
		len(result.Detail.Receipts) != 0 {
		t.Fatalf("no-source Purpose run is invalid: %+v calls=%d", result, len(runner.inputs))
	}
}

func TestPurposeExtractionScansAllEligibleSourcesWithoutFixedItemLimit(t *testing.T) {
	request := PurposeExtractionRequest{
		IdempotencyKey: "purpose-test-complete-scan",
		CutoffAt:       purposeExtractionTestTime(20).UnixMilli(),
	}
	const sourceCount = 180
	candidates := make([]TaskContextCandidate, 0, sourceCount)
	for index := 0; index < sourceCount; index++ {
		id := fmt.Sprintf("complete-source-%03d", index)
		candidates = append(candidates, TaskContextCandidate{
			Ref:             "memory:" + id,
			Section:         TaskContextSectionKnowledge,
			SourceKind:      taskContextSourceMemoryKind,
			SourceID:        id,
			ObservedAt:      purposeExtractionTestTime(index % 10).UnixMilli(),
			Content:         strings.Repeat(string(rune('a'+index%26)), 400),
			EvidenceClass:   TaskContextEvidenceEvaluatedProjection,
			PrivacyDecision: TaskContextPrivacyInclude,
			Relevance:       0.5,
		})
	}
	input, manifest, err := assemblePurposeExtractionInput(
		request,
		TaskContextSourceResult{Candidates: candidates},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Sources) != sourceCount || manifest.IncludedCount != sourceCount ||
		manifest.ScannedCount != sourceCount || manifest.ExcludedCount != 0 {
		t.Fatalf("Purpose extraction imposed an item cap: sources=%d manifest=%+v", len(input.Sources), manifest)
	}
	if err := validatePurposeExtractionInputManifest(input, manifest); err != nil {
		t.Fatalf("validate complete Purpose scan: %v", err)
	}
}

func TestPurposeExtractionFixtureDoesNotDependOnProcessHome(t *testing.T) {
	// Guard the test helpers themselves: a future source-gathering change must
	// not accidentally write fixtures into the developer's real Yansu state.
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := purposeWriteMemory(
		t, home, "home-guard", purposeExtractionTestTime(1), memorySource("manual"),
		"I will keep this fixture isolated.",
	)
	if !strings.HasPrefix(path, home+string(os.PathSeparator)) {
		t.Fatalf("Purpose fixture escaped temporary HOME: %s", path)
	}
}
