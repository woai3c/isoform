package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func createShadowTaskEpisodeFixture(t *testing.T, store *ActivityStore) TaskEpisode {
	t.Helper()
	input := taskEpisodeFixture(
		"task-for-shadow",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.BoundaryConfidence = 0.71
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create task episode: added=%v err=%v", added, err)
	}
	return episode
}

func createComposeShadowTaskEpisodeFixture(
	t *testing.T,
	store *ActivityStore,
) TaskEpisode {
	t.Helper()
	input := taskEpisodeFixture(
		"task-compose-for-shadow",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.TaskHypothesis = "Draft a customer proposal"
	input.InitialHypothesis.DesiredResultHypothesis =
		"A concise proposal ready to send"
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create compose task episode: added=%v err=%v", added, err)
	}
	return episode
}

func createAnalyzeShadowTaskEpisodeFixture(
	t *testing.T,
	store *ActivityStore,
) TaskEpisode {
	t.Helper()
	input := taskEpisodeFixture(
		"task-analyze-for-shadow",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.TaskHypothesis = "Review the supplied proposal"
	input.InitialHypothesis.DesiredResultHypothesis =
		"Findings that identify the material gaps and risks"
	input.FrozenContext.Manifest = []TaskContextManifestEntry{{
		SourceKind: taskContextSourceActivitySnapshotKind,
		SourceID:   "activity-session:proposal",
		ObservedAt: 999,
		Included:   true,
	}}
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create analyze task episode: added=%v err=%v", added, err)
	}
	return episode
}

func createReasoningShadowTaskEpisodeFixture(
	t *testing.T,
	store *ActivityStore,
) TaskEpisode {
	t.Helper()
	input := taskEpisodeFixture(
		"task-reasoning-for-shadow",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.TaskHypothesis = "Draft a customer proposal"
	input.InitialHypothesis.DesiredResultHypothesis =
		"A concise proposal ready to send"
	input.InitialEvidence[0].ID = "task-reasoning-onset-evidence"
	input.InitialHypothesis.ReasoningHypotheses = []TaskEpisodeReasoningHypothesis{{
		Statement:   "The user may value a concrete customer outcome.",
		Confidence:  0.49,
		Basis:       TaskReasoningBasisContextualInference,
		EvidenceIDs: []string{input.InitialEvidence[0].ID},
	}}
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create reasoning task episode: added=%v err=%v", added, err)
	}
	return episode
}

func TestCreateShadowTaskRunLinksFrozenTaskContext(t *testing.T) {
	store := newTestStore(t)
	episode := createShadowTaskEpisodeFixture(t, store)
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"gpt-5.6-sol",
		1_200,
	)
	if err != nil || !added {
		t.Fatalf("create shadow task: added=%v err=%v", added, err)
	}
	if run.Mode != "task_mirror" ||
		run.TaskEpisodeID != episode.ID ||
		run.TaskContextHash != episode.FrozenContext.SHA256 ||
		run.Context.PromptContext != episode.FrozenContext.CanonicalJSON {
		t.Fatalf("task linkage changed: %+v", run)
	}
	if run.Prediction.CandidateExecutionPlan == nil ||
		!strings.Contains(run.Prediction.CandidateExecutionPlan.Prompt, episode.ID) ||
		!strings.Contains(run.Prediction.CandidateExecutionPlan.Prompt, episode.FrozenContext.SHA256) {
		t.Fatalf("worker prompt lacks frozen provenance: %+v", run.Prediction.CandidateExecutionPlan)
	}

	replayed, replayAdded, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"gpt-5.6-terra",
		1_300,
	)
	if err != nil || replayAdded || replayed.ID != run.ID ||
		replayed.Model != "gpt-5.6-sol" ||
		replayed.ObservationHorizonEnd != run.ObservationHorizonEnd {
		t.Fatalf("idempotent replay: run=%+v added=%v err=%v", replayed, replayAdded, err)
	}
	runs, err := store.ListShadowTaskRuns(episode.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("task runs=%+v err=%v", runs, err)
	}
}

func TestCreateShadowTaskRunFreezesTextCompositionContract(t *testing.T) {
	store := newTestStore(t)
	input := taskEpisodeFixture(
		"task-compose-text",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.TaskHypothesis = "Draft a customer proposal"
	input.InitialHypothesis.DesiredResultHypothesis =
		"A concise proposal ready to send"
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create task episode: added=%v err=%v", added, err)
	}

	run, added, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"gpt-5.6-sol",
		1_200,
	)
	if err != nil || !added {
		t.Fatalf("create shadow task: added=%v err=%v", added, err)
	}
	if run.CapabilityKey != TaskWorkflowComposeText {
		t.Fatalf("capability=%q, want %q", run.CapabilityKey, TaskWorkflowComposeText)
	}
	prompt := run.Prediction.CandidateExecutionPlan.Prompt
	if !strings.Contains(prompt, "complete ready-to-use text") ||
		!strings.Contains(prompt, "Do not invoke any tool") ||
		!strings.Contains(prompt, "Do not send, submit, publish") ||
		!strings.Contains(prompt, "Prompt version: "+shadowTaskPromptVersion) ||
		strings.Contains(prompt, "even when also writing") {
		t.Fatalf("text composition contract missing from prompt: %s", prompt)
	}
	if run.PromptVersion != "shadow-task-v7" ||
		run.RunnerVersion != "task-mirror-runtime-v2" ||
		run.ObservationHorizonEnd != 1_200+shadowTaskHorizon.Milliseconds() {
		t.Fatalf("current shadow generation=%+v", run)
	}
}

func TestCreateShadowTaskRunFreezesTextAnalysisContract(t *testing.T) {
	store := newTestStore(t)
	episode := createAnalyzeShadowTaskEpisodeFixture(t, store)
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"gpt-5.6-sol",
		1_200,
	)
	if err != nil || !added {
		t.Fatalf("create shadow task: added=%v err=%v", added, err)
	}
	if run.CapabilityKey != TaskWorkflowAnalyzeText {
		t.Fatalf("capability=%q, want %q", run.CapabilityKey, TaskWorkflowAnalyzeText)
	}
	prompt := run.Prediction.CandidateExecutionPlan.Prompt
	for _, required := range []string{
		"Text-analysis parity contract",
		"complete source-backed analysis",
		"Separate evidence-backed findings from unknowns",
		"Do not retrieve, infer, or cite newer",
		"Do not invoke any tool",
		"Prompt version: " + shadowTaskPromptVersion,
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("analysis contract lacks %q: %s", required, prompt)
		}
	}
}

func TestCreateShadowTaskRunFreezesUnverifiedReasoningAcrossReconciliation(
	t *testing.T,
) {
	firstStore := newTestStore(t)
	firstEpisode := createReasoningShadowTaskEpisodeFixture(t, firstStore)
	first, added, err := firstStore.CreateShadowTaskRunFromEpisode(
		firstEpisode,
		"gpt-5.6-sol",
		1_200,
	)
	if err != nil || !added {
		t.Fatalf("create immediate shadow: added=%v err=%v", added, err)
	}
	if len(first.Prediction.ReasoningHypotheses) != 1 {
		t.Fatalf("reasoning hypotheses=%+v", first.Prediction.ReasoningHypotheses)
	}
	hypothesis := first.Prediction.ReasoningHypotheses[0]
	if hypothesis.Basis != TaskReasoningBasisContextualInference ||
		hypothesis.Confidence != 0.49 ||
		len(hypothesis.EvidenceRefs) != 1 ||
		hypothesis.EvidenceRefs[0] != "observed:task-reasoning-onset-evidence" {
		t.Fatalf("frozen shadow reasoning=%+v", hypothesis)
	}
	prompt := first.Prediction.CandidateExecutionPlan.Prompt
	if !strings.Contains(prompt, "competing, unverified explanations") ||
		!strings.Contains(prompt, hypothesis.Statement) ||
		!strings.Contains(prompt, "treat [] as unknown reasoning") {
		t.Fatalf("worker prompt lost reasoning contract: %s", prompt)
	}

	reconciledStore := newTestStore(t)
	reconciledEpisode := createReasoningShadowTaskEpisodeFixture(
		t,
		reconciledStore,
	)
	reconciled, added, err := reconciledStore.CreateShadowTaskRunFromEpisode(
		reconciledEpisode,
		"gpt-5.6-sol",
		5_000,
	)
	if err != nil || !added {
		t.Fatalf("create reconciled shadow: added=%v err=%v", added, err)
	}
	if reconciled.ID != first.ID ||
		reconciled.PredictionHash != first.PredictionHash ||
		reconciled.DecisionKey != first.DecisionKey {
		t.Fatalf("reconciliation changed prediction identity: first=%+v reconciled=%+v", first, reconciled)
	}
}

func TestShadowTaskRunCannotStartAfterObservedCompletion(t *testing.T) {
	store := newTestStore(t)
	episode := createShadowTaskEpisodeFixture(t, store)
	if _, _, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: episode.ID,
		ToStatus:      TaskEpisodeStatusActive,
		Reason:        "continued work observed",
		OccurredAt:    1_500,
		CreatedAt:     1_500,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: episode.ID,
		ToStatus:      TaskEpisodeStatusCompleted,
		Reason:        "result observed",
		OccurredAt:    2_000,
		CreatedAt:     2_000,
	}); err != nil {
		t.Fatal(err)
	}
	episode, err := store.GetTaskEpisode(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateShadowTaskRunFromEpisode(episode, "model", 2_100); err == nil {
		t.Fatal("completed task unexpectedly started a shadow run")
	}
}

func TestExecuteTaskEpisodeShadowUsesPolicyControlledRuntime(t *testing.T) {
	store := newTestStore(t)
	episode := createComposeShadowTaskEpisodeFixture(t, store)
	runner := &shadowExecutionFakeRunner{}
	app := &App{
		runner:   runner,
		activity: &activityMonitorState{store: store},
	}
	run, attempt, added, err := app.executeTaskEpisodeShadow(context.Background(), episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !added || run.Mode != "task_mirror" || attempt.ShadowRunID != run.ID {
		t.Fatalf("run=%+v attempt=%+v added=%v", run, attempt, added)
	}
	if runner.lastInput == nil ||
		!runner.lastInput.NoTools ||
		runner.lastInput.AutoApproveTools ||
		!runner.lastInput.Ephemeral ||
		!runner.lastInput.DenyExternalWrites ||
		runner.lastInput.ProgrammaticToolCalling ||
		!runner.lastInput.SuppressTelemetry ||
		runner.lastInput.ExecutionMode != "shadow" {
		t.Fatalf("runtime input=%+v", runner.lastInput)
	}
	if len(attempt.Actions) != 0 || len(attempt.Decisions) != 0 {
		t.Fatalf("direct-text attempt carried effects: %+v", attempt)
	}
}

func TestExecuteAnalyzeTaskEpisodeShadowUsesNoToolsRuntime(t *testing.T) {
	store := newTestStore(t)
	episode := createAnalyzeShadowTaskEpisodeFixture(t, store)
	runner := &shadowExecutionFakeRunner{}
	app := &App{
		runner:   runner,
		activity: &activityMonitorState{store: store},
	}
	run, _, added, err := app.executeTaskEpisodeShadow(
		context.Background(),
		episode.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !added || run.CapabilityKey != TaskWorkflowAnalyzeText {
		t.Fatalf("run=%+v added=%v", run, added)
	}
	if runner.lastInput == nil ||
		!runner.lastInput.NoTools ||
		runner.lastInput.AutoApproveTools ||
		!runner.lastInput.Ephemeral ||
		!runner.lastInput.DenyExternalWrites ||
		runner.lastInput.ProgrammaticToolCalling ||
		!runner.lastInput.SuppressTelemetry ||
		runner.lastInput.ExecutionMode != "shadow" {
		t.Fatalf("runtime input=%+v", runner.lastInput)
	}
}

func TestExecuteTaskEpisodeShadowRejectsOutsideProductionRollout(t *testing.T) {
	store := newTestStore(t)
	episode := createShadowTaskEpisodeFixture(t, store)
	runner := &shadowExecutionFakeRunner{}
	app := &App{
		runner:   runner,
		activity: &activityMonitorState{store: store},
	}

	_, _, added, err := app.executeTaskEpisodeShadow(
		context.Background(),
		episode.ID,
	)
	if !errors.Is(err, ErrTaskWorkflowOutsideProductionRollout) {
		t.Fatalf("execution error=%v, want rollout rejection", err)
	}
	if added || runner.calls != 0 {
		t.Fatalf("added=%v runner calls=%d, want no execution", added, runner.calls)
	}
	runs, listErr := store.ListShadowTaskRuns(episode.ID, 10)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("unsupported episode created task-mirror runs: %+v", runs)
	}
}
