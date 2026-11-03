package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent"
	"yansu-agent/agent/authority"
)

type shadowExecutionFakeRunner struct {
	calls     int
	lastInput *agent.TaskInput
}

func (r *shadowExecutionFakeRunner) Init(context.Context) error { return nil }

func (r *shadowExecutionFakeRunner) ExecuteTask(
	ctx context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	r.calls++
	r.lastInput = input
	if input.NoTools {
		if err := callback(&agent.StreamEvent{
			Type:    "assistant",
			Content: "A complete source-backed text result.",
			RunID:   "native-shadow-agent-run",
		}); err != nil {
			return "", err
		}
		return "native-shadow-agent-run", nil
	}
	if err := os.WriteFile(
		filepath.Join(input.WorkDir, "shadow-result.md"),
		[]byte("# Shadow result\n\nA local draft."),
		0o600,
	); err != nil {
		return "", err
	}
	evaluation := authority.NewPolicy([]authority.Grant{{
		ID:         "grant-write",
		Capability: "filesystem.*",
	}}).Evaluate(ctx, authority.Context{
		RunID:      "native-shadow-agent-run",
		ShadowRun:  input.AuthorityShadowRunID,
		Mode:       input.ExecutionMode,
		Unattended: input.AutoApproveTools,
	}, authority.Action{
		Capability: "filesystem.write",
		ToolName:   "Write",
		Operation:  "write",
		Effect:     authority.EffectLocalWrite,
		Resource:   "/tmp/shadow-output.md",
	})
	payload, _ := json.Marshal(evaluation)
	if err := callback(&agent.StreamEvent{
		Type:      "tool_use",
		ToolName:  "Write",
		ToolUseID: "tool-1",
		ToolInput: map[string]any{"path": "shadow-result.md"},
		RunID:     "native-shadow-agent-run",
	}); err != nil {
		return "", err
	}
	if err := callback(&agent.StreamEvent{
		Type:      "tool_result",
		ToolName:  "Write",
		ToolUseID: "tool-1",
		Content:   "created shadow-result.md",
		RunID:     "native-shadow-agent-run",
	}); err != nil {
		return "", err
	}
	if err := callback(&agent.StreamEvent{
		Type:    "authority_decision",
		Content: string(payload),
		RunID:   "native-shadow-agent-run",
	}); err != nil {
		return "", err
	}
	if err := callback(&agent.StreamEvent{
		Type:    "assistant",
		Content: "The write was blocked; no production state changed.",
		RunID:   "native-shadow-agent-run",
	}); err != nil {
		return "", err
	}
	return "native-shadow-agent-run", nil
}

func (r *shadowExecutionFakeRunner) CancelRun(string) error                   { return nil }
func (r *shadowExecutionFakeRunner) RespondToPermission(string, string) error { return nil }
func (r *shadowExecutionFakeRunner) CancelPermission(string)                  {}
func (r *shadowExecutionFakeRunner) Shutdown() error                          { return nil }
func (r *shadowExecutionFakeRunner) IsReady() bool                            { return true }

func shadowRunWithCandidatePlan(id, decisionKey string) ShadowRun {
	run := newShadowRunFixture(id, decisionKey)
	run.Prediction.CandidateExecutionPlan = &ShadowCandidateExecutionPlan{
		Kind:           "automation",
		Name:           "draft-customer-follow-up",
		Description:    "Draft the customer follow-up.",
		Prompt:         "Draft the customer follow-up and save it to /tmp/shadow-output.md.",
		ScheduleKind:   "manual",
		ScheduleConfig: json.RawMessage(`{}`),
		Rationale:      "The customer has not replied.",
		Confidence:     0.81,
	}
	return run
}

func TestShadowExecutionAttemptCapturesBlockedPlanAndIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	run, _, err := store.CreateShadowRun(shadowRunWithCandidatePlan(
		"shadow-execution-run",
		"shadow-execution-decision",
	))
	if err != nil {
		t.Fatal(err)
	}
	runner := &shadowExecutionFakeRunner{}
	app := &App{
		runner:   runner,
		activity: &activityMonitorState{store: store},
	}

	attempt, added, err := app.executeShadowCandidatePlan(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if !added || attempt.Status != "completed" || len(attempt.Decisions) != 1 {
		t.Fatalf("attempt=%+v added=%v", attempt, added)
	}
	decision := attempt.Decisions[0]
	if decision.Decision != authority.DecisionDeny ||
		decision.PolicyVersion != authority.ShadowPolicyVersion ||
		decision.Context.ShadowRun != run.ID ||
		decision.Context.Mode != authority.ExecutionModeShadow ||
		decision.GrantID != "" {
		t.Fatalf("decision=%+v, want ungranted shadow denial", decision)
	}
	if runner.lastInput == nil ||
		runner.lastInput.ExecutionMode != authority.ExecutionModeShadow ||
		runner.lastInput.AuthorityShadowRunID != run.ID ||
		!runner.lastInput.AutoApproveTools ||
		!runner.lastInput.Ephemeral {
		t.Fatalf("task input=%+v, want isolated shadow task", runner.lastInput)
	}
	if attempt.Prompt != run.Prediction.CandidateExecutionPlan.Prompt ||
		attempt.PlanHash == "" ||
		!strings.Contains(attempt.AssistantOutput, "no production state changed") {
		t.Fatalf("attempt did not retain frozen plan and output: %+v", attempt)
	}
	if len(attempt.Actions) != 2 ||
		len(attempt.Artifacts) != 1 ||
		attempt.Artifacts[0].Path != "shadow-result.md" ||
		!strings.Contains(attempt.Artifacts[0].Content, "local draft") {
		t.Fatalf("attempt did not retain action/artifact receipt: %+v", attempt)
	}
	if runner.lastInput.WorkDir == os.TempDir() ||
		!strings.Contains(filepath.Base(runner.lastInput.WorkDir), "yansu-shadow-") {
		t.Fatalf("shadow workdir was not isolated: %q", runner.lastInput.WorkDir)
	}
	if _, err := os.Stat(runner.lastInput.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("shadow workspace still exists after persistence: %v", err)
	}

	replayed, replayAdded, err := app.executeShadowCandidatePlan(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if replayAdded || replayed.ID != attempt.ID || runner.calls != 1 {
		t.Fatalf("replay=%+v added=%v calls=%d, want idempotent reuse", replayed, replayAdded, runner.calls)
	}

	items, err := store.ListShadowExecutionAttempts(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != attempt.ID {
		t.Fatalf("stored attempts=%+v", items)
	}
	if _, err := store.db.Exec(
		`UPDATE shadow_execution_attempts SET status = 'error' WHERE id = ?`,
		attempt.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("attempt update error=%v, want immutable rejection", err)
	}
}

func TestShadowExecutionExternalRunnerIsRecordedAsUnsupported(t *testing.T) {
	store := newTestStore(t)
	run, _, err := store.CreateShadowRun(shadowRunWithCandidatePlan(
		"shadow-external-run",
		"shadow-external-decision",
	))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		runner:   agent.NewExternalAgentRunner(agent.ExternalAgentConfig{Format: "codex"}),
		activity: &activityMonitorState{store: store},
	}
	attempt, added, err := app.executeShadowCandidatePlan(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if !added || attempt.Status != "unsupported_runner" ||
		!strings.Contains(attempt.Error, "native policy-controlled runner") ||
		len(attempt.Decisions) != 0 {
		t.Fatalf("attempt=%+v added=%v", attempt, added)
	}
}

func TestShadowExecutionRejectsTaskMirrorOutsideProductionRollout(t *testing.T) {
	store := newTestStore(t)
	runInput := shadowRunWithCandidatePlan(
		"shadow-outside-rollout-run",
		"shadow-outside-rollout-decision",
	)
	runInput.Mode = "task_mirror"
	runInput.TaskEpisodeID = "outside-rollout-episode"
	runInput.TaskContextHash = strings.Repeat("a", 64)
	runInput.CapabilityKey = "watch.a.youtube.video"
	runner := &shadowExecutionFakeRunner{}
	app := &App{
		runner:   runner,
		activity: &activityMonitorState{store: store},
	}

	_, added, err := app.executeShadowCandidatePlan(
		context.Background(),
		runInput,
	)
	if !errors.Is(err, ErrTaskWorkflowOutsideProductionRollout) {
		t.Fatalf("execution error=%v, want rollout rejection", err)
	}
	if added || runner.calls != 0 {
		t.Fatalf("added=%v runner calls=%d, want no execution", added, runner.calls)
	}
	attempts, listErr := store.ListShadowExecutionAttempts(runInput.ID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(attempts) != 0 {
		t.Fatalf("unsupported task mirror persisted attempts: %+v", attempts)
	}
}
