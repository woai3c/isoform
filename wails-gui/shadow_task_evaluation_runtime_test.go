package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestTryEvaluateShadowTaskRunWaitsForObservedOutcome(t *testing.T) {
	store := newTestStore(t)
	episode := seedLifecycleTaskEpisode(t, store, 1_000)
	run, _, err := store.CreateShadowTaskRunFromEpisode(episode, "test-model", 1_300)
	if err != nil {
		t.Fatalf("CreateShadowTaskRunFromEpisode: %v", err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	_, _, err = app.tryEvaluateShadowTaskRun(context.Background(), run.ID)
	if !errors.Is(err, ErrShadowTaskAwaitingObservedOutcome) {
		t.Fatalf("error = %v, want awaiting outcome", err)
	}
}

func TestTryEvaluateShadowTaskRunRejectsInsufficientFrozenContext(
	t *testing.T,
) {
	store := newTestStore(t)
	input := taskEpisodeFixture(
		"evaluation-insufficient-context",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.TaskHypothesis =
		"Convert email content into a one-page document"
	input.InitialHypothesis.DesiredResultHypothesis =
		"A one-page version of the email content"
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create episode: added=%v err=%v", added, err)
	}
	run, _, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"test-model",
		1_200,
	)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	_, _, err = app.tryEvaluateShadowTaskRun(context.Background(), run.ID)
	if !errors.Is(err, ErrTaskWorkflowInsufficientFrozenContext) {
		t.Fatalf("evaluation error=%v", err)
	}
	if evaluations, listErr := store.ListShadowRunEvaluations(run.ID); listErr != nil || len(evaluations) != 0 {
		t.Fatalf("evaluations=%+v err=%v", evaluations, listErr)
	}
}

func TestTryEvaluateShadowTaskRunRecordsExactModelAttempt(t *testing.T) {
	store := newTestStore(t)
	episode, run := createExecutedShadowTaskFixture(t, store)
	input, err := BuildShadowTaskEvaluationInput(store, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	refs := sortedShadowTaskEvidenceIDs(input)
	if len(refs) == 0 {
		t.Fatal("evaluation fixture has no evidence")
	}
	output := ShadowTaskEvaluationOutput{
		Version:          ShadowTaskEvaluationOutputVersion,
		Summary:          "The task and result were similar.",
		Task:             matchedTaskDimension(refs[0]),
		Boundary:         matchedTaskDimension(refs[0]),
		Process:          matchedTaskDimension(refs[0]),
		ResultSimilarity: matchedTaskDimension(refs[0]),
		ResultQuality:    unknownTaskDimension(),
		Timing:           matchedTaskDimension(refs[0]),
		Reasoning:        unknownTaskDimension(),
		Safety: ShadowTaskEvaluationDimension{
			Assessment:   ShadowTaskSafetySafe,
			Score:        shadowScore(1),
			Confidence:   1,
			Reason:       "The mirrored runtime blocked side effects.",
			EvidenceRefs: []string{},
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	runner := &shadowTaskEvaluationFakeRunner{response: string(raw)}
	app := &App{
		runner:   runner,
		activity: &activityMonitorState{store: store},
	}

	result, added, err := app.tryEvaluateShadowTaskRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !added || result.Evaluation.ID == "" {
		t.Fatalf("result=%+v added=%v", result, added)
	}
	attempts, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
		Phase:         TaskAgentAttemptPhaseEvaluation,
		TaskEpisodeID: episode.ID,
		ShadowRunID:   run.ID,
		Limit:         10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts=%d, want 1: %+v", len(attempts), attempts)
	}
	attempt := attempts[0]
	if attempt.ShadowEvaluationID != result.Evaluation.ID ||
		attempt.PromptText != runner.input.Prompt ||
		attempt.RawOutput != string(raw) ||
		attempt.Status != TaskAgentAttemptStatusSucceeded {
		t.Fatalf("attempt=%+v", attempt)
	}
}
