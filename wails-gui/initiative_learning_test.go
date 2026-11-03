package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomationInitiativeUsesSharedPredictionFields(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	resp := &activityTriageResponse{
		WorthInitiative:          true,
		InitiativeGoal:           "Resolve the open customer question",
		PredictedUserAction:      "Draft a reply after checking the customer logs.",
		InitiativeRationale:      "The user opened the thread and then inspected the logs.",
		InitiativeConfidence:     0.92,
		WorthAutomation:          true,
		AutomationName:           "Customer reply draft",
		AutomationDescription:    "Prepare a customer reply draft.",
		AutomationPrompt:         "Draft the customer reply using the latest logs.",
		AutomationScheduleKind:   "manual",
		AutomationScheduleConfig: json.RawMessage(`{}`),
	}

	created := app.createShadowInitiativeFromTriage(
		t.Context(), resp, "activity", "automation-prediction-source", "", "resolve the customer question",
	)
	if created.Status != "shadowed" {
		t.Fatalf("created status=%q, want shadowed: %+v", created.Status, created)
	}
	item, err := store.GetInitiative(created.InitiativeID)
	if err != nil {
		t.Fatal(err)
	}
	if item.PredictedAction != resp.PredictedUserAction ||
		item.Goal != resp.InitiativeGoal ||
		item.Rationale != resp.InitiativeRationale ||
		item.Confidence != resp.InitiativeConfidence {
		t.Fatalf("automation initiative did not share prediction fields: %+v", item)
	}
}

func TestPredictionOnlyInitiativeReviewCompletesWithoutAuthorityStep(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	resp := &activityTriageResponse{
		WorthInitiative:      true,
		InitiativeTitle:      "Reply to the customer",
		InitiativeGoal:       "Resolve the open customer question",
		PredictedUserAction:  "Draft a concise reply with the requested details.",
		InitiativeRationale:  "The user opened the thread and gathered the requested details.",
		InitiativeConfidence: 0.95,
	}

	created := app.createPredictionInitiativeFromTriage(resp, "activity", "prediction-source", "resolve the customer question")
	if created.Status != "shadowed" {
		t.Fatalf("created status=%q, want shadowed: %+v", created.Status, created)
	}
	item, err := store.GetInitiative(created.InitiativeID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != "prediction" || item.ExecutionPlan.Automation != nil {
		t.Fatalf("prediction initiative must not contain an execution plan: %+v", item)
	}

	reviewed, err := store.ReviewInitiative(ReviewInitiativeInput{
		InitiativeID: item.ID,
		Decision:     "correct",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Status != InitiativeStatusReviewed || reviewed.MemoryState != InitiativeMemoryEligible {
		t.Fatalf("reviewed state=%q/%q, want reviewed/eligible", reviewed.Status, reviewed.MemoryState)
	}
}

func TestObservedOutcomePersistsOnceAndProjectsToMemory(t *testing.T) {
	setTestHome(t)
	store := newTestStore(t)
	item := newInitiativeFixture("initiative-outcome", "prediction-source", InitiativeStatusShadowed)
	if err := store.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	outcome, updated, err := store.SaveInitiativeOutcome(InitiativeOutcome{
		InitiativeID:     item.ID,
		SourceKind:       "activity",
		SourceID:         "outcome-source",
		ObservedAction:   "Drafted the follow-up after five business days.",
		ObservedResult:   "The draft was saved for review.",
		Assessment:       "different",
		AssessmentReason: "The user waited five days rather than three.",
		Confidence:       0.91,
	})
	if err != nil || !updated || outcome.Assessment != "different" {
		t.Fatalf("unexpected outcome: saved=%v outcome=%+v err=%v", updated, outcome, err)
	}
	if outcome.PredictionConfidence == nil || *outcome.PredictionConfidence != item.Confidence {
		t.Fatalf("prediction confidence snapshot=%v, want %.2f", outcome.PredictionConfidence, item.Confidence)
	}
	if _, savedAgain, err := store.SaveInitiativeOutcome(InitiativeOutcome{
		InitiativeID:   item.ID,
		SourceKind:     "activity",
		SourceID:       "outcome-source",
		ObservedAction: "duplicate",
		Assessment:     "different",
		Confidence:     0.91,
	}); err != nil || savedAgain {
		t.Fatalf("same source must not create a duplicate initiative outcome: saved=%v err=%v", savedAgain, err)
	}

	app.syncInitiativeLearningMemory(item.ID)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "shadow-learning-"+item.ID+".md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"Dimension**: agentic_memory",
		"Drafted the follow-up after five business days.",
		"The user waited five days rather than three.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("learning memory missing %q:\n%s", want, text)
		}
	}
}

func TestInitiativeCalibrationGroupsPredictionConfidenceByAssessmentAndScope(t *testing.T) {
	store := newTestStore(t)
	saveSample := func(
		id, kind, sourceKind string,
		predictionConfidence, comparisonConfidence float64,
		assessment string,
	) {
		t.Helper()
		item := newInitiativeFixture(id, id+"-source", InitiativeStatusShadowed)
		item.Kind = kind
		item.SourceKind = sourceKind
		item.Confidence = predictionConfidence
		if err := store.SaveInitiative(item); err != nil {
			t.Fatal(err)
		}
		saved, added, err := store.SaveInitiativeOutcome(InitiativeOutcome{
			InitiativeID:   item.ID,
			SourceKind:     sourceKind,
			SourceID:       id + "-outcome",
			ObservedAction: "Observed action for " + id,
			Assessment:     assessment,
			Confidence:     comparisonConfidence,
		})
		if err != nil || !added {
			t.Fatalf("save calibration sample %s: added=%v err=%v", id, added, err)
		}
		if saved.PredictionConfidence == nil ||
			*saved.PredictionConfidence != predictionConfidence {
			t.Fatalf("sample %s prediction snapshot=%v, want %.2f", id, saved.PredictionConfidence, predictionConfidence)
		}
	}

	saveSample("low-match", "prediction", "activity", 0.55, 0.95, "matched")
	saveSample("moderate-partial", "prediction", "activity", 0.65, 0.90, "partial")
	saveSample("high-different", "prediction", "activity", 0.80, 0.85, "different")
	saveSample("very-high-unclear", "prediction", "activity", 0.95, 0.92, "unclear")
	saveSample("excluded-comparison", "prediction", "activity", 0.82, 0.60, "matched")
	saveSample("other-kind", "automation", "activity", 0.82, 0.95, "matched")
	saveSample("other-source", "prediction", "slack", 0.82, 0.95, "matched")

	summary, err := store.GetInitiativeCalibration("prediction", "activity")
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalOutcomes != 5 ||
		summary.SampleCount != 4 ||
		summary.EvaluatedSamples != 3 ||
		summary.ExcludedLowConfidenceOutcomes != 1 {
		t.Fatalf("unexpected calibration totals: %+v", summary)
	}
	if summary.MatchedCount != 1 ||
		summary.PartialCount != 1 ||
		summary.DifferentCount != 1 ||
		summary.UnclearCount != 1 {
		t.Fatalf("unexpected calibration assessments: %+v", summary)
	}
	if summary.Reliability != 0.5 {
		t.Fatalf("reliability=%.3f, want 0.5", summary.Reliability)
	}
	if summary.Ready || summary.MinimumEvaluatedSamples != initiativeCalibrationMinimumEvaluatedSamples {
		t.Fatalf("readiness=%v minimum=%d", summary.Ready, summary.MinimumEvaluatedSamples)
	}
	if len(summary.Bands) != 4 {
		t.Fatalf("bands=%d, want 4", len(summary.Bands))
	}
	if summary.Bands[0].MatchedCount != 1 ||
		summary.Bands[1].PartialCount != 1 ||
		summary.Bands[2].DifferentCount != 1 ||
		summary.Bands[2].ExcludedLowConfidenceOutcomes != 1 ||
		summary.Bands[3].UnclearCount != 1 {
		t.Fatalf("unexpected band breakdown: %+v", summary.Bands)
	}

	saveSample("low-match-2", "prediction", "activity", 0.55, 0.95, "matched")
	saveSample("low-match-3", "prediction", "activity", 0.55, 0.95, "matched")
	saveSample("low-match-4", "prediction", "activity", 0.55, 0.95, "matched")
	saveSample("low-match-5", "prediction", "activity", 0.55, 0.95, "matched")
	ready, err := store.GetInitiativeCalibration("prediction", "activity")
	if err != nil {
		t.Fatal(err)
	}
	lowBand := ready.Bands[0]
	if !lowBand.Ready || lowBand.EvaluatedSamples != 5 || lowBand.Reliability != 1 {
		t.Fatalf("low band did not become ready at five outcomes: %+v", lowBand)
	}
}

func TestInitiativeActionReceiptTracksRecurringRunLifecycle(t *testing.T) {
	store := newTestStore(t)
	item := newInitiativeFixture("initiative-receipt", "receipt-source", InitiativeStatusApproved)
	item.ExecutionRef = "cron-receipt"
	item.MemoryState = InitiativeMemoryEligible
	if err := store.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}

	running := CronRunLifecycleEvent{
		JobID: "cron-receipt", RunID: "run-1", Status: "running",
		TriggerKind: "scheduled", StartedAt: 1000,
	}
	receipt, executing, linked, err := store.UpsertInitiativeActionReceipt(running)
	if err != nil {
		t.Fatal(err)
	}
	if !linked || receipt.Status != "running" || executing.Status != InitiativeStatusExecuting || executing.ResolvedAt != 0 {
		t.Fatalf("running lifecycle: linked=%v receipt=%+v item=%+v", linked, receipt, executing)
	}

	completed := running
	completed.Status = "ok"
	completed.FinishedAt = 1600
	completed.DurationMs = 600
	completed.ResultSummary = "Follow-up draft created."
	receipt, succeeded, linked, err := store.UpsertInitiativeActionReceipt(completed)
	if err != nil {
		t.Fatal(err)
	}
	if !linked || receipt.ResultSummary == "" || succeeded.Status != InitiativeStatusSucceeded {
		t.Fatalf("completion lifecycle: linked=%v receipt=%+v item=%+v", linked, receipt, succeeded)
	}
	receipts, err := store.ListInitiativeActionReceipts(item.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipt upsert created %d rows, want 1", len(receipts))
	}

	nextRun := CronRunLifecycleEvent{
		JobID: "cron-receipt", RunID: "run-2", Status: "running",
		TriggerKind: "scheduled", StartedAt: 2000,
	}
	_, reopened, _, err := store.UpsertInitiativeActionReceipt(nextRun)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != InitiativeStatusExecuting || reopened.ResolvedAt != 0 {
		t.Fatalf("recurring initiative did not reopen cleanly: %+v", reopened)
	}
}

func TestContextSelectionUsesBudgetInsteadOfItemCount(t *testing.T) {
	results := make([]MemorySearchResult, 0, 12)
	for i := 0; i < 12; i++ {
		results = append(results, MemorySearchResult{
			Path:    "memory.md",
			Source:  string(MemorySourceMemory),
			Score:   0.9,
			Snippet: "relevant context",
		})
	}
	selected := selectMemoryContextResults(results, 0.35, 10_000)
	if len(selected) != len(results) {
		t.Fatalf("selected %d chunks, want all %d that fit", len(selected), len(results))
	}

	budgeted := selectMemoryContextResults(results, 0.35, 40)
	if len(budgeted) == 0 || len(budgeted) >= len(results) {
		t.Fatalf("budget selection should include a non-empty prefix, got %d", len(budgeted))
	}
}
