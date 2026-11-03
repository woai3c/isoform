package main

import (
	"context"
	"errors"
	"testing"
)

func TestRepeatedOnsetBlocksExistingRunExecutionPairAndContinuation(
	t *testing.T,
) {
	store := newTestStore(t)
	anchor := taskEpisodeOnsetAnchor{
		Ref: "event:existing-run", SourceKind: "activity_event",
		SourceID: "existing-run-source", ObservedAt: 5_000,
	}
	first := createHistoricalOnsetEpisodeFixture(
		t, store, "existing-run-first", anchor, 5_100,
	)
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		first,
		"test-model",
		5_200,
	)
	if err != nil || !added {
		t.Fatalf("create pre-fork run: added=%v err=%v", added, err)
	}
	createHistoricalOnsetEpisodeFixture(
		t, store, "existing-run-second", anchor, 5_300,
	)

	app := &App{activity: &activityMonitorState{store: store}}
	if _, _, err := app.executeShadowCandidatePlan(
		context.Background(),
		run,
	); !errors.Is(err, ErrTaskEpisodeRepeatedOnset) {
		t.Fatalf("existing run execution error=%v", err)
	}
	var attemptCount int
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM shadow_execution_attempts
		WHERE shadow_run_id = ?`, run.ID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 0 {
		t.Fatalf("repeated onset produced %d execution attempts", attemptCount)
	}

	readiness, err := BuildShadowTaskPairReadiness(store, run.ID)
	if err != nil || readiness.Ready ||
		readiness.State != ShadowTaskPairStateRepeatedOnset {
		t.Fatalf("pair readiness=%+v err=%v", readiness, err)
	}
	continuation, err := BuildTaskShadowContinuationSnapshot(store, 5_400, 100)
	if err != nil || len(continuation.Items) != 1 ||
		continuation.Items[0].State != taskShadowContinuationBlocked ||
		continuation.Items[0].Action != taskShadowContinuationActionNone {
		t.Fatalf("continuation=%+v err=%v", continuation, err)
	}
}

func TestRepeatedOnsetIsOmittedFromLifecycleObservation(t *testing.T) {
	store := newTestStore(t)
	repeatedAnchor := taskEpisodeOnsetAnchor{
		Ref: "event:lifecycle-repeated", SourceKind: "activity_event",
		SourceID: "lifecycle-repeated-source", ObservedAt: 6_000,
	}
	first := createHistoricalOnsetEpisodeFixture(
		t, store, "lifecycle-repeated-first", repeatedAnchor, 6_100,
	)
	second := createHistoricalOnsetEpisodeFixture(
		t, store, "lifecycle-repeated-second", repeatedAnchor, 6_200,
	)
	independent := createHistoricalOnsetEpisodeFixture(
		t,
		store,
		"lifecycle-independent",
		taskEpisodeOnsetAnchor{
			Ref: "event:lifecycle-independent", SourceKind: "activity_event",
			SourceID: "lifecycle-independent-source", ObservedAt: 6_300,
		},
		6_400,
	)
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"lifecycle-containment-window",
		6_500,
		7_000,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Episodes) != 1 ||
		input.Episodes[0].EpisodeID != independent.ID {
		t.Fatalf("lifecycle episodes=%+v", input.Episodes)
	}
	for _, item := range input.Episodes {
		if item.EpisodeID == first.ID || item.EpisodeID == second.ID {
			t.Fatalf("repeated onset entered lifecycle input: %+v", input.Episodes)
		}
	}
}

func TestRepeatedOnsetIsExcludedFromReleaseIndex(t *testing.T) {
	episodes := []taskShadowDebugEpisodeIndexRow{
		{Summary: TaskShadowDebugEpisodeSummary{
			ID: "repeated-newest", Status: TaskEpisodeStatusCompleted,
			TaskHypothesis: "Compose a customer update",
		}},
		{Summary: TaskShadowDebugEpisodeSummary{
			ID: "independent-older", Status: TaskEpisodeStatusCompleted,
			TaskHypothesis: "Analyze a proposal",
		}},
	}
	runs := map[string][]taskShadowDebugRunIndexRow{
		"repeated-newest": {{
			ID: "repeated-run", CapabilityKey: TaskWorkflowComposeText,
			PromptVersion: shadowTaskPromptVersion,
		}},
		"independent-older": {{
			ID: "independent-run", CapabilityKey: TaskWorkflowAnalyzeText,
			PromptVersion: shadowTaskPromptVersion,
		}},
	}
	selection := taskShadowReleaseGateIndexSelectionExcluding(
		episodes,
		runs,
		map[string]int{"repeated-newest": 1, "independent-older": 1},
		map[string]struct{}{"repeated-newest": {}},
	)
	if selection.FirstEligibleEpisodeID != "independent-older" ||
		selection.FirstCurrentEpisodeID != "independent-older" ||
		len(selection.PossibleCompleteEpisodeIDs) != 1 ||
		selection.PossibleCompleteEpisodeIDs[0] != "independent-older" {
		t.Fatalf("release selection=%+v", selection)
	}
}

func TestRepeatedOnsetLearningRecordIsExplicitlyExcluded(t *testing.T) {
	record := shadowTaskLearningTestRecord(1)
	record.ExclusionReason = taskEpisodeRepeatedOnsetReason
	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{record},
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.EvaluationCount != 0 || len(projection.Candidates) != 0 ||
		len(projection.ExcludedEvaluations) != 1 ||
		projection.ExcludedEvaluations[0].Reason !=
			taskEpisodeRepeatedOnsetReason {
		t.Fatalf("repeated learning projection=%+v", projection)
	}
}
