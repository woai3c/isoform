package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"yansu-agent/agent"
)

type taskShadowRolloutFakeRunner struct {
	phases []string
}

func (r *taskShadowRolloutFakeRunner) Init(context.Context) error { return nil }
func (r *taskShadowRolloutFakeRunner) IsReady() bool              { return true }
func (r *taskShadowRolloutFakeRunner) CancelRun(string) error     { return nil }
func (r *taskShadowRolloutFakeRunner) RespondToPermission(string, string) error {
	return nil
}
func (r *taskShadowRolloutFakeRunner) CancelPermission(string) {}
func (r *taskShadowRolloutFakeRunner) Shutdown() error         { return nil }

func decodeTaskShadowRolloutPrompt(
	prompt, marker string,
	target any,
) error {
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return fmt.Errorf("prompt marker %q not found", marker)
	}
	decoder := json.NewDecoder(
		strings.NewReader(prompt[index+len(marker):]),
	)
	return decoder.Decode(target)
}

func (r *taskShadowRolloutFakeRunner) ExecuteTask(
	_ context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	var response any
	switch {
	case strings.Contains(input.Prompt, "You are Yansu's task-onset detector."):
		r.phases = append(r.phases, "onset")
		var observed TaskOnsetInput
		if err := decodeTaskShadowRolloutPrompt(
			input.Prompt,
			"\nObservation:\n",
			&observed,
		); err != nil {
			return "", err
		}
		if len(observed.Evidence) == 0 {
			return "", fmt.Errorf("onset prompt has no evidence")
		}
		response = TaskOnsetResponse{Episodes: []TaskOnsetPrediction{{
			LocalKey:           "survey",
			Task:               "Fill and submit a browser survey",
			DesiredResult:      "The completed survey is submitted",
			InferredStartedAt:  observed.Evidence[0].OccurredAt,
			TaskConfidence:     0.91,
			BoundaryConfidence: 0.84,
			ResultConfidence:   0.88,
			EvidenceRefs:       []string{observed.Evidence[0].Ref},
		}}}
	case strings.Contains(input.Prompt, "You are Yansu's task-lifecycle observer."):
		r.phases = append(r.phases, "lifecycle")
		var observed TaskLifecycleInput
		if err := decodeTaskShadowRolloutPrompt(
			input.Prompt,
			"\nObservation:\n",
			&observed,
		); err != nil {
			return "", err
		}
		if len(observed.Episodes) != 1 {
			return "", fmt.Errorf(
				"lifecycle prompt has %d episodes",
				len(observed.Episodes),
			)
		}
		var terminal TaskLifecycleEvidence
		for _, item := range observed.SuffixEvidence {
			if item.Kind == "submission" {
				terminal = item
				break
			}
		}
		if terminal.Ref == "" {
			return "", fmt.Errorf("lifecycle prompt has no submission evidence")
		}
		response = TaskLifecycleResponse{
			Observations: []TaskLifecycleObservation{{
				LocalKey:           "survey-complete",
				Kind:               TaskLifecycleKindCompleted,
				EpisodeIDs:         []string{observed.Episodes[0].EpisodeID},
				OccurredAt:         terminal.OccurredAt,
				Confidence:         0.94,
				BoundaryConfidence: 0.87,
				ResultSummary:      "The browser survey was submitted.",
				ResultConfidence:   0.91,
				EvidenceRefs:       []string{terminal.Ref},
				Reason:             "The observed submit control was activated.",
			}},
		}
	default:
		r.phases = append(r.phases, "unexpected")
		return "", fmt.Errorf("unexpected production runner prompt")
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return "", err
	}
	if err := callback(&agent.StreamEvent{
		Type:    "assistant",
		Content: string(payload),
	}); err != nil {
		return "", err
	}
	return "task-shadow-rollout-run", nil
}

func TestTaskShadowOnsetWindowsUseLeadingAndTrailingProductionSlices(t *testing.T) {
	const start = int64(1_000)
	end := start + (10 * time.Minute).Milliseconds()
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:        "session-1",
		StartTime: start,
		EndTime:   end,
	}}}
	events := []activitymon.ActivityEvent{
		{
			Timestamp:   start + time.Second.Milliseconds(),
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Mail",
			WindowTitle: "Customer update",
		},
		{
			Timestamp:   start + (5 * time.Minute).Milliseconds(),
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Terminal",
			WindowTitle: "Unrelated middle activity",
		},
		{
			Timestamp:   end - time.Second.Milliseconds(),
			Kind:        activitymon.EventClick,
			AppName:     "Mail",
			WindowTitle: "Customer update",
			ClickLabel:  "Send",
		},
	}

	windows := taskShadowOnsetWindows(group, events)
	if len(windows) != 2 {
		t.Fatalf("windows=%d, want leading and trailing slices: %+v", len(windows), windows)
	}
	horizon := taskOnsetObservationHorizon.Milliseconds()
	if windows[0].WindowStart != start || windows[0].CutoffAt != start+horizon {
		t.Fatalf("leading window=%+v", windows[0])
	}
	if windows[1].WindowStart != end-horizon || windows[1].CutoffAt != end {
		t.Fatalf("trailing window=%+v", windows[1])
	}
	if len(windows[0].Evidence) != 1 || len(windows[1].Evidence) != 1 {
		t.Fatalf("window evidence leaked across boundaries: %+v", windows)
	}
	for _, window := range windows {
		for _, evidence := range window.Evidence {
			if evidence.OccurredAt == events[1].Timestamp {
				t.Fatal("middle activity leaked into an onset window")
			}
		}
	}
}

func TestTaskShadowOnsetWindowsCollapseShortSessionToOneSlice(t *testing.T) {
	const start = int64(1_000)
	end := start + time.Minute.Milliseconds()
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:        "session-short",
		StartTime: start,
		EndTime:   end,
	}}}
	windows := taskShadowOnsetWindows(group, []activitymon.ActivityEvent{{
		Timestamp:   start + 1,
		Kind:        activitymon.EventAppSwitch,
		AppName:     "Notes",
		WindowTitle: "Draft",
	}})
	if len(windows) != 1 {
		t.Fatalf("windows=%d, want one: %+v", len(windows), windows)
	}
	if windows[0].WindowStart != start || windows[0].CutoffAt != end {
		t.Fatalf("window=%+v", windows[0])
	}
}

func TestTaskShadowPredictionDuplicateRequiresSameTaskAndNearbyBoundary(t *testing.T) {
	seen := []TaskOnsetPrediction{{
		Task:              "Prepare   customer update",
		InferredStartedAt: 10_000,
	}}
	if !taskShadowPredictionDuplicate(seen, TaskOnsetPrediction{
		Task:              "prepare customer UPDATE",
		InferredStartedAt: 10_000 + taskShadowOnsetDedupeWindow.Milliseconds(),
	}) {
		t.Fatal("same normalized task at the dedupe boundary was not suppressed")
	}
	if taskShadowPredictionDuplicate(seen, TaskOnsetPrediction{
		Task:              "Prepare customer update",
		InferredStartedAt: 10_001 + taskShadowOnsetDedupeWindow.Milliseconds(),
	}) {
		t.Fatal("same task outside the dedupe boundary was suppressed")
	}
	if taskShadowPredictionDuplicate(seen, TaskOnsetPrediction{
		Task:              "Investigate the incident",
		InferredStartedAt: 10_000,
	}) {
		t.Fatal("overlapping but distinct task was suppressed")
	}
}

func TestTaskShadowEpisodeReconciliationRequiresRecentOpenProductionWorkflow(
	t *testing.T,
) {
	const horizonEnd = int64(2_000_000)
	episode := TaskEpisode{
		ID:              "episode-convert-email",
		InferredStartAt: horizonEnd - time.Minute.Milliseconds(),
		CurrentStatus:   TaskEpisodeStatusActive,
		LatestHypothesis: TaskEpisodeHypothesisRevision{
			TaskHypothesis:          "Convert email content into a one-page document using ChatGPT",
			DesiredResultHypothesis: "A one-page version of the email content",
		},
		FrozenContext: TaskEpisodeFrozenContext{
			Manifest: []TaskContextManifestEntry{{
				SourceKind: taskContextSourceActivitySnapshotKind,
				SourceID:   "session:42",
				ObservedAt: horizonEnd - 2*time.Minute.Milliseconds(),
				Included:   true,
			}},
		},
	}
	if !taskShadowEpisodeEligibleForReconciliation(episode, horizonEnd) {
		t.Fatal("recent open text transformation was not eligible for reconciliation")
	}
	analyze := episode
	analyze.ID = "episode-review-proposal"
	analyze.LatestHypothesis.TaskHypothesis = "Review the supplied proposal"
	analyze.LatestHypothesis.DesiredResultHypothesis =
		"Findings that identify the material gaps and risks"
	if !taskShadowEpisodeEligibleForReconciliation(analyze, horizonEnd) {
		t.Fatal("recent source-backed analysis was not eligible for reconciliation")
	}
	analyze.FrozenContext.Manifest = nil
	if taskShadowEpisodeEligibleForReconciliation(analyze, horizonEnd) {
		t.Fatal("analysis without frozen source context entered reconciliation")
	}
	missingSource := episode
	missingSource.FrozenContext.Manifest = nil
	if taskShadowEpisodeEligibleForReconciliation(missingSource, horizonEnd) {
		t.Fatal("source-dependent episode without frozen source context entered reconciliation")
	}
	if !taskShadowEpisodeReconciliationCandidate(missingSource, horizonEnd) {
		t.Fatal("insufficient episode disappeared instead of becoming a fallback candidate")
	}

	stale := episode
	stale.InferredStartAt = horizonEnd - taskShadowReconcileLookback.Milliseconds() - 1
	if taskShadowEpisodeEligibleForReconciliation(stale, horizonEnd) {
		t.Fatal("stale episode entered reconciliation")
	}

	completed := episode
	completed.CurrentStatus = TaskEpisodeStatusCompleted
	if taskShadowEpisodeEligibleForReconciliation(completed, horizonEnd) {
		t.Fatal("terminal episode entered reconciliation")
	}

	unsupported := episode
	unsupported.LatestHypothesis.TaskHypothesis =
		"Review images in a social post carousel"
	unsupported.LatestHypothesis.DesiredResultHypothesis =
		"Select the strongest image"
	if taskShadowEpisodeEligibleForReconciliation(unsupported, horizonEnd) {
		t.Fatal("unsupported workflow entered reconciliation")
	}
}

func TestTaskShadowReconciliationExecutesAnalyzeTextWithNoTools(t *testing.T) {
	store := newTestStore(t)
	episode := createAnalyzeShadowTaskEpisodeFixture(t, store)
	runner := &shadowExecutionFakeRunner{}
	triager := &ActivityTriager{
		app: &App{
			runner:   runner,
			activity: &activityMonitorState{store: store},
		},
	}
	runIDs, diagnostics, errorsOut := triager.reconcileRecentEligibleTaskEpisodes(
		context.Background(),
		2_000,
	)
	triager.wg.Wait()
	if len(errorsOut) != 0 || len(runIDs) != 1 || len(diagnostics) != 1 {
		t.Fatalf(
			"runs=%v diagnostics=%v errors=%v",
			runIDs,
			diagnostics,
			errorsOut,
		)
	}
	if !strings.Contains(diagnostics[0], TaskWorkflowAnalyzeText) ||
		!strings.Contains(diagnostics[0], episode.ID) {
		t.Fatalf("analysis reconciliation diagnostic=%v", diagnostics)
	}
	if runner.lastInput == nil ||
		!runner.lastInput.NoTools ||
		runner.lastInput.AutoApproveTools ||
		!runner.lastInput.Ephemeral ||
		runner.lastInput.ProgrammaticToolCalling ||
		!runner.lastInput.DenyExternalWrites ||
		!runner.lastInput.SuppressTelemetry {
		t.Fatalf("analysis reconciliation runtime=%+v", runner.lastInput)
	}
	attempts, err := store.ListShadowExecutionAttempts(runIDs[0])
	if err != nil || len(attempts) != 1 || attempts[0].Status != "completed" {
		t.Fatalf("analysis attempts=%+v err=%v", attempts, err)
	}
}

func TestTaskShadowProductionFlowObservesUnsupportedWorkflowWithoutExecutingIt(
	t *testing.T,
) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	runner := &taskShadowRolloutFakeRunner{}
	triager := &ActivityTriager{
		app: &App{
			runner: runner,
			activity: &activityMonitorState{
				store: store,
			},
		},
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:        "survey-session",
		StartTime: 1_000,
		EndTime:   3_000,
	}}}
	events := []activitymon.ActivityEvent{
		{
			Timestamp:   1_100,
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Safari",
			WindowTitle: "Customer survey",
		},
		{
			Timestamp:   2_500,
			Kind:        activitymon.EventClick,
			AppName:     "Safari",
			WindowTitle: "Customer survey",
			ClickLabel:  "Submit",
		},
	}

	result := triager.runTaskShadowProductionFlow(
		context.Background(),
		group,
		events,
	)
	if len(result.Errors) != 0 {
		t.Fatalf("production flow errors=%+v", result.Errors)
	}
	if len(result.TaskEpisodeIDs) != 1 ||
		len(result.ShadowRunIDs) != 0 ||
		len(result.LifecycleAttemptIDs) != 1 {
		t.Fatalf("production flow result=%+v", result)
	}
	if len(result.Diagnostics) != 1 ||
		!strings.Contains(result.Diagnostics[0], "outside") ||
		!strings.Contains(result.Diagnostics[0], TaskWorkflowComposeText) {
		t.Fatalf("production diagnostics=%+v", result.Diagnostics)
	}
	if len(result.WorkflowDecisions) != 1 ||
		result.WorkflowDecisions[0].Source !=
			TaskShadowWorkflowDecisionCapturedOnset ||
		result.WorkflowDecisions[0].State !=
			TaskShadowWorkflowStateOutsideRegistry ||
		result.WorkflowDecisions[0].TaskEpisodeID != result.TaskEpisodeIDs[0] ||
		result.WorkflowDecisions[0].ProductionOnsetAttemptID == "" ||
		!containsString(
			result.ProductionOnsetAttemptIDs,
			result.WorkflowDecisions[0].ProductionOnsetAttemptID,
		) ||
		result.WorkflowDecisions[0].ExecutionDispatched {
		t.Fatalf("unsupported workflow decision=%+v", result.WorkflowDecisions)
	}
	if strings.Contains(strings.Join(runner.phases, ","), "unexpected") {
		t.Fatalf("unsupported workflow reached shadow execution: %+v", runner.phases)
	}
	episode, err := store.GetTaskEpisode(result.TaskEpisodeIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if episode.CurrentStatus != TaskEpisodeStatusCompleted {
		t.Fatalf("episode status=%q, want completed", episode.CurrentStatus)
	}
	runs, err := store.ListShadowTaskRuns(episode.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("unsupported episode created runs: %+v", runs)
	}
}
