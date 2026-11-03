package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func createAwaitingComposeTextPairFixture(
	t *testing.T,
	store *ActivityStore,
) (TaskEpisode, ShadowRun) {
	t.Helper()
	input := taskEpisodeFixture(
		"task-compose-awaiting",
		3_000,
		3_100,
		TaskEpisodeStatusCandidate,
	)
	input.InitialHypothesis.TaskHypothesis = "Draft a launch update"
	input.InitialHypothesis.DesiredResultHypothesis =
		"A concise launch update ready to send"
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create awaiting compose episode: added=%v err=%v", added, err)
	}
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"test-model",
		3_200,
	)
	if err != nil || !added {
		t.Fatalf("create awaiting shadow run: added=%v err=%v", added, err)
	}
	app := &App{
		runner:   &shadowExecutionFakeRunner{},
		activity: &activityMonitorState{store: store},
	}
	if _, _, err := app.executeShadowCandidatePlan(
		context.Background(),
		run,
	); err != nil {
		t.Fatalf("execute awaiting shadow run: %v", err)
	}
	return episode, run
}

func taskShadowQualityEvaluationOutput(
	t *testing.T,
	store *ActivityStore,
	runID string,
) string {
	t.Helper()
	input, err := BuildShadowTaskEvaluationInput(store, runID)
	if err != nil {
		t.Fatal(err)
	}
	refs := sortedShadowTaskEvidenceIDs(input)
	if len(refs) == 0 {
		t.Fatal("quality campaign evaluation fixture has no evidence")
	}
	resultQuality := matchedTaskDimension(refs[0])
	resultQuality.Assessment = ShadowTaskQualityEquivalent
	output := ShadowTaskEvaluationOutput{
		Version:          ShadowTaskEvaluationOutputVersion,
		Summary:          "The independently captured composition results were compared.",
		Task:             matchedTaskDimension(refs[0]),
		Boundary:         matchedTaskDimension(refs[0]),
		Process:          matchedTaskDimension(refs[0]),
		ResultSimilarity: matchedTaskDimension(refs[0]),
		ResultQuality:    resultQuality,
		Timing:           matchedTaskDimension(refs[0]),
		Reasoning:        unknownTaskDimension(),
		Safety: ShadowTaskEvaluationDimension{
			Assessment:   ShadowTaskSafetySafe,
			Score:        shadowScore(1),
			Confidence:   1,
			Reason:       "The isolated mirror produced no production effect.",
			EvidenceRefs: []string{refs[0]},
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTaskShadowQualityCampaignProcessesOnlyDurableReadyPairs(
	t *testing.T,
) {
	store := newTestStore(t)
	_, readyRun, _ := createCompletedComposeTextPairFixture(t, store)
	_, waitingRun := createAwaitingComposeTextPairFixture(t, store)

	before, err := buildTaskShadowQualityCampaign(store, 200, 4_000)
	if err != nil {
		t.Fatal(err)
	}
	if before.Stage != "ready_to_process" ||
		before.Counts.ProcessablePairs != 1 ||
		before.Counts.CurrentPromptRuns != 2 ||
		before.Counts.HistoricalRuns != 0 ||
		before.Counts.AwaitingNaturalOutcomes != 1 ||
		len(before.ReadyPairRunIDs) != 1 ||
		before.ReadyPairRunIDs[0] != readyRun.ID {
		t.Fatalf("before campaign=%+v", before)
	}

	app := &App{
		runner: &shadowTaskEvaluationFakeRunner{
			response: taskShadowQualityEvaluationOutput(t, store, readyRun.ID),
		},
		activity: &activityMonitorState{store: store},
	}
	result, err := app.RunTaskShadowQualityCampaign()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 ||
		len(result.EvaluatedRunIDs) != 1 ||
		result.EvaluatedRunIDs[0] != readyRun.ID {
		t.Fatalf("campaign run=%+v", result)
	}
	if result.Campaign.Counts.Evaluated != 1 ||
		result.Campaign.Counts.CurrentPromptEvaluations != 1 ||
		result.Campaign.Counts.HistoricalEvaluations != 0 ||
		result.Campaign.Counts.AwaitingNaturalOutcomes != 1 ||
		result.Campaign.Counts.ProcessablePairs != 0 ||
		!result.Campaign.ExistingEvidenceExhausted ||
		result.Campaign.Stage != "collecting_natural_outcomes" {
		t.Fatalf("campaign after run=%+v", result.Campaign)
	}
	if evaluations, listErr := store.ListShadowRunEvaluations(
		waitingRun.ID,
	); listErr != nil || len(evaluations) != 0 {
		t.Fatalf("waiting user outcome was evaluated: %+v err=%v", evaluations, listErr)
	}
}

func TestTaskShadowQualityCampaignExcludesHistoricalPromptRuns(t *testing.T) {
	store := newTestStore(t)
	episode := createComposeShadowTaskEpisodeFixture(t, store)
	sourceStore := newTestStore(t)
	sourceEpisode := createComposeShadowTaskEpisodeFixture(t, sourceStore)
	run, _, err := sourceStore.CreateShadowTaskRunFromEpisode(
		sourceEpisode,
		"test-model",
		3_200,
	)
	if err != nil {
		t.Fatal(err)
	}
	run.PromptVersion = "shadow-task-v6"
	run.DecisionKey = strings.ReplaceAll(
		run.DecisionKey,
		shadowTaskPromptVersion,
		run.PromptVersion,
	)
	run.ID = deterministicTaskEpisodeID("shadow-task-run", run.DecisionKey)
	run.TaskEpisodeID = episode.ID
	run.SourceID = episode.ID
	if _, added, createErr := store.CreateShadowRun(run); createErr != nil || !added {
		t.Fatalf("create historical run: added=%v err=%v", added, createErr)
	}
	campaign, err := buildTaskShadowQualityCampaign(store, 200, 4_000)
	if err != nil {
		t.Fatal(err)
	}
	if campaign.Counts.HistoricalRuns != 1 ||
		campaign.Counts.CurrentPromptRuns != 0 ||
		campaign.Counts.AwaitingNaturalOutcomes != 0 ||
		campaign.Counts.ProcessablePairs != 0 ||
		campaign.Stage != "evidence_exhausted" {
		t.Fatalf("historical run entered current campaign: %+v", campaign)
	}
}

func TestTaskShadowQualityCampaignKeepsNewestBoundedRunWindow(t *testing.T) {
	store := newTestStore(t)
	episode := createComposeShadowTaskEpisodeFixture(t, store)

	const runCount = taskShadowDebugMaxRuns + 1
	for index := 0; index < runCount; index++ {
		run := newShadowRunFixture(
			fmt.Sprintf("quality-window-run-%03d", index),
			fmt.Sprintf("quality-window-decision-%03d", index),
		)
		run.Mode = "task_mirror"
		run.TaskEpisodeID = episode.ID
		run.TaskContextHash = episode.FrozenContext.SHA256
		run.CapabilityKey = TaskWorkflowComposeText
		run.PromptVersion = "shadow-task-historical"
		run.RunnerVersion = shadowTaskRunnerVersion
		run.CreatedAt = int64(10_000 + index)
		if index == runCount-1 {
			run.PromptVersion = shadowTaskPromptVersion
		}
		if _, added, err := store.CreateShadowRun(run); err != nil || !added {
			t.Fatalf("create run %d: added=%v err=%v", index, added, err)
		}
	}

	campaign, err := buildTaskShadowQualityCampaign(store, 200, 200_000)
	if err != nil {
		t.Fatal(err)
	}
	if campaign.Counts.ShadowRuns != taskShadowDebugMaxRuns ||
		campaign.Counts.HistoricalRuns != taskShadowDebugMaxRuns-1 ||
		campaign.Counts.CurrentPromptRuns != 1 {
		t.Fatalf("campaign did not retain newest run window: %+v", campaign.Counts)
	}
	latestRunID := fmt.Sprintf("quality-window-run-%03d", runCount-1)
	if len(campaign.PendingExecutionRunIDs) != 1 ||
		campaign.PendingExecutionRunIDs[0] != latestRunID {
		t.Fatalf(
			"latest current-generation run missing from campaign: %+v",
			campaign.PendingExecutionRunIDs,
		)
	}
}

func TestTaskShadowQualityCampaignExcludesAnalyzeTextWorkflow(t *testing.T) {
	store := newTestStore(t)
	_, analyzeRun, _ := createCompletedAnalyzeTextPairFixture(t, store)
	campaign, err := buildTaskShadowQualityCampaign(store, 200, 4_000)
	if err != nil {
		t.Fatal(err)
	}
	if campaign.Counts.DetectedEpisodes != 0 ||
		campaign.Counts.CurrentPromptRuns != 0 ||
		campaign.Counts.ProcessablePairs != 0 ||
		len(campaign.ReadyPairRunIDs) != 0 {
		t.Fatalf("analyze workflow contaminated compose campaign: %+v", campaign)
	}
	evaluations, err := store.ListShadowRunEvaluations(analyzeRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluations) != 0 {
		t.Fatalf("compose campaign created analyze evaluations: %+v", evaluations)
	}
}

// The campaign runs evaluations, so it starts only from the app window.
func TestTaskShadowQualityCampaignStaysOffTheBrowserBridge(t *testing.T) {
	if browserRPCAllowlist["RunTaskShadowQualityCampaign"] {
		t.Fatal("quality campaign changes state and must stay off the browser allowlist")
	}
}
