package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yansu-agent/agent"
)

type taskOnsetFakeRunner struct {
	response string
	input    *agent.TaskInput
}

func (r *taskOnsetFakeRunner) Init(context.Context) error { return nil }
func (r *taskOnsetFakeRunner) IsReady() bool              { return true }
func (r *taskOnsetFakeRunner) ExecuteTask(
	_ context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	r.input = input
	if err := callback(&agent.StreamEvent{Type: "assistant", Content: r.response}); err != nil {
		return "", err
	}
	return "task-onset-run", nil
}
func (r *taskOnsetFakeRunner) CancelRun(string) error { return nil }
func (r *taskOnsetFakeRunner) RespondToPermission(string, string) error {
	return nil
}
func (r *taskOnsetFakeRunner) CancelPermission(string) {}
func (r *taskOnsetFakeRunner) Shutdown() error         { return nil }

func taskOnsetFixture() TaskOnsetInput {
	return TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "session-1",
		WindowStart: 1_000,
		CutoffAt:    4_000,
		Evidence: []TaskOnsetEvidence{
			{
				Ref:        "event:2",
				Kind:       "focus",
				SourceKind: "activity_event",
				SourceID:   "2",
				OccurredAt: 2_000,
				Summary:    "Focused the customer incident document.",
			},
			{
				Ref:        "event:1",
				Kind:       "app_switch",
				SourceKind: "activity_event",
				SourceID:   "1",
				OccurredAt: 1_500,
				Summary:    "Opened the incident workspace.",
			},
		},
	}
}

func TestBuildTaskOnsetPromptRejectsEvidenceAfterCutoff(t *testing.T) {
	input := taskOnsetFixture()
	input.Evidence = append(input.Evidence, TaskOnsetEvidence{
		Ref:        "event:late",
		Kind:       "click",
		SourceKind: "activity_event",
		SourceID:   "late",
		OccurredAt: input.CutoffAt + 1,
		Summary:    "Clicked send after completing the work.",
	})

	if _, _, err := buildTaskOnsetPrompt(input); err == nil ||
		!strings.Contains(err.Error(), "outside") {
		t.Fatalf("buildTaskOnsetPrompt error=%v, want cutoff rejection", err)
	}
}

func TestBuildTaskOnsetPromptSortsEvidenceAndContainsNoLaterResult(t *testing.T) {
	prompt, normalized, err := buildTaskOnsetPrompt(taskOnsetFixture())
	if err != nil {
		t.Fatalf("buildTaskOnsetPrompt: %v", err)
	}
	if normalized.Evidence[0].Ref != "event:1" || normalized.Evidence[1].Ref != "event:2" {
		t.Fatalf("evidence order=%v", normalized.Evidence)
	}
	if strings.Contains(prompt, "send after completing") {
		t.Fatal("prompt contains post-cutoff result")
	}
	if !strings.Contains(prompt, taskOnsetPromptVersion) {
		t.Fatalf("prompt missing version: %s", prompt)
	}
	if !strings.Contains(prompt, `"reasoningHypotheses"`) ||
		!strings.Contains(prompt, "Return [] when no reason is supportable") {
		t.Fatalf("prompt missing reasoning contract: %s", prompt)
	}
}

func TestNormalizeTaskOnsetReasoningPreservesSupportAndDowngradesInference(
	t *testing.T,
) {
	input := taskOnsetFixture()
	input.Evidence = append(input.Evidence, TaskOnsetEvidence{
		Ref:        "event:reason",
		Kind:       "keyboard",
		SourceKind: "activity_event",
		SourceID:   "reason",
		OccurredAt: 2_500,
		Summary: `Entered text in Chat: "I am checking this because the ` +
			`customer needs an accurate answer."`,
	})
	response := normalizeTaskOnsetResponse(input, TaskOnsetResponse{
		Episodes: []TaskOnsetPrediction{{
			LocalKey:           "incident",
			Task:               "Investigate the incident",
			DesiredResult:      "An accurate customer answer",
			InferredStartedAt:  2_500,
			TaskConfidence:     0.8,
			BoundaryConfidence: 0.7,
			ResultConfidence:   0.7,
			EvidenceRefs:       []string{"event:1", "event:reason"},
			ReasoningHypotheses: []TaskOnsetReasoningHypothesis{
				{
					Statement:   "Accuracy for the customer matters here.",
					Confidence:  0.9,
					Basis:       TaskReasoningBasisExplicitStatement,
					SourceQuote: "because the customer needs an accurate answer",
					EvidenceRefs: []string{
						"event:reason",
					},
				},
				{
					Statement:   "The user may prefer the fastest possible reply.",
					Confidence:  0.92,
					Basis:       TaskReasoningBasisExplicitStatement,
					SourceQuote: "I always optimize for speed",
					EvidenceRefs: []string{
						"event:reason",
					},
				},
				{
					Statement:  "The incident may be urgent.",
					Confidence: 0.81,
					Basis:      TaskReasoningBasisContextualInference,
					EvidenceRefs: []string{
						"event:1",
					},
				},
			},
		}},
	})
	if len(response.Episodes) != 1 ||
		len(response.Episodes[0].ReasoningHypotheses) != 3 {
		t.Fatalf("response=%+v", response)
	}
	hypotheses := response.Episodes[0].ReasoningHypotheses
	if hypotheses[0].Basis != TaskReasoningBasisExplicitStatement ||
		hypotheses[0].SourceQuote == "" || hypotheses[0].Confidence != 0.9 {
		t.Fatalf("supported explicit rationale=%+v", hypotheses[0])
	}
	for _, index := range []int{1, 2} {
		if hypotheses[index].Basis != TaskReasoningBasisContextualInference ||
			hypotheses[index].SourceQuote != "" ||
			hypotheses[index].Confidence != 0.49 {
			t.Fatalf("unsupported rationale was not capped: %+v", hypotheses[index])
		}
	}
}

func TestNormalizeTaskOnsetReasoningRequiresProfileForRepeatedPattern(
	t *testing.T,
) {
	input := taskOnsetFixture()
	input.WorkProfile = &WorkPatternProfileContext{
		SnapshotID:     "profile",
		SnapshotSHA256: strings.Repeat("a", 64),
		PolicyVersion:  WorkPatternProfilePolicyVersion,
		SourceCutoffAt: input.WindowStart - 1,
		Status:         WorkPatternProfileStatusQualified,
		InfluenceMode:  WorkPatternInfluenceProfile,
		SelectedProjections: []WorkPatternProjection{{
			ScopeKind: WorkPatternScopeGlobal,
			ScopeKey:  "all",
			Metrics: []WorkPatternMetric{{
				Name: "median_dwell_seconds",
			}},
		}},
	}
	profileRef := "metric:global:all:median_dwell_seconds"
	response := normalizeTaskOnsetResponse(input, TaskOnsetResponse{
		Episodes: []TaskOnsetPrediction{{
			LocalKey:           "incident",
			Task:               "Investigate the incident",
			DesiredResult:      "Identify the likely cause",
			InferredStartedAt:  1_500,
			TaskConfidence:     0.8,
			BoundaryConfidence: 0.7,
			ResultConfidence:   0.7,
			EvidenceRefs:       []string{"event:1"},
			ReasoningHypotheses: []TaskOnsetReasoningHypothesis{{
				Statement:    "The user may inspect briefly before switching.",
				Confidence:   0.75,
				Basis:        TaskReasoningBasisRepeatedPattern,
				EvidenceRefs: []string{"event:1"},
				ProfileRefs:  []string{profileRef, "metric:invented"},
			}},
		}},
	})
	hypothesis := response.Episodes[0].ReasoningHypotheses[0]
	if hypothesis.Basis != TaskReasoningBasisRepeatedPattern ||
		len(hypothesis.ProfileRefs) != 1 ||
		hypothesis.ProfileRefs[0] != profileRef {
		t.Fatalf("repeated-pattern reasoning=%+v", hypothesis)
	}
	if len(response.Episodes[0].ProfileInfluenceRefs) != 1 ||
		response.Episodes[0].ProfileInfluenceRefs[0] != profileRef {
		t.Fatalf("reasoning profile provenance was not bound: %+v", response.Episodes[0])
	}

	input.WorkProfile = nil
	withoutProfile := normalizeTaskOnsetResponse(input, response)
	hypothesis = withoutProfile.Episodes[0].ReasoningHypotheses[0]
	if hypothesis.Basis != TaskReasoningBasisContextualInference ||
		hypothesis.Confidence != 0.49 || len(hypothesis.ProfileRefs) != 0 {
		t.Fatalf("profile-free repeated pattern=%+v", hypothesis)
	}
}

func TestNormalizeTaskOnsetResponseSupportsOverlapAndDropsInvalidRefs(t *testing.T) {
	input, err := normalizeTaskOnsetInput(taskOnsetFixture())
	if err != nil {
		t.Fatalf("normalize input: %v", err)
	}
	response := normalizeTaskOnsetResponse(input, TaskOnsetResponse{
		Episodes: []TaskOnsetPrediction{
			{
				LocalKey:           "incident",
				Task:               "Investigate the incident",
				DesiredResult:      "Identify the likely cause",
				InferredStartedAt:  1_500,
				TaskConfidence:     1.2,
				BoundaryConfidence: 0.8,
				EvidenceRefs:       []string{"event:1", "invented"},
			},
			{
				LocalKey:           "reply",
				Task:               "Prepare a customer update",
				DesiredResult:      "A factual update is ready",
				InferredStartedAt:  2_000,
				TaskConfidence:     0.7,
				BoundaryConfidence: 0.6,
				EvidenceRefs:       []string{"event:2"},
			},
		},
	})

	if len(response.Episodes) != 2 {
		t.Fatalf("episodes=%d, want overlapping pair: %+v", len(response.Episodes), response)
	}
	if got := response.Episodes[0].TaskConfidence; got != 1 {
		t.Fatalf("clamped confidence=%v, want 1", got)
	}
	if got := response.Episodes[0].EvidenceRefs; len(got) != 1 || got[0] != "event:1" {
		t.Fatalf("evidence refs=%v, want only event:1", got)
	}
}

func TestRunTaskOnsetDetectionUsesNoToolsAndValidatesModelOutput(t *testing.T) {
	runner := &taskOnsetFakeRunner{response: `{
		"episodes": [{
			"localKey": "incident",
			"task": "Investigate the incident",
			"desiredResult": "Identify the likely cause",
			"inferredStartedAt": 1500,
			"taskConfidence": 0.85,
			"boundaryConfidence": 0.7,
			"evidenceRefs": ["event:1", "missing"],
			"alternatives": []
		}]
	}`}
	response, raw, prompt, err := runTaskOnsetDetection(
		context.Background(),
		runner,
		"gpt-5.6-terra",
		taskOnsetFixture(),
	)
	if err != nil {
		t.Fatalf("runTaskOnsetDetection: %v raw=%q prompt=%q", err, raw, prompt)
	}
	if runner.input == nil ||
		!runner.input.NoTools ||
		runner.input.AutoApproveTools ||
		!runner.input.PlanMode ||
		!runner.input.Ephemeral ||
		!runner.input.NoSystem ||
		!runner.input.SuppressTelemetry {
		t.Fatalf("runner input=%+v", runner.input)
	}
	if len(response.Episodes) != 1 ||
		len(response.Episodes[0].EvidenceRefs) != 1 ||
		response.Episodes[0].EvidenceRefs[0] != "event:1" {
		t.Fatalf("response=%+v", response)
	}
}

func TestTaskOnsetPromptKeepsIdenticalTraceAndChangesOnlyProfilePrior(
	t *testing.T,
) {
	base := taskOnsetFixture()
	base.WindowStart = 10_000
	base.CutoffAt = 20_000
	for index := range base.Evidence {
		base.Evidence[index].OccurredAt += 9_000
	}
	profileFor := func(
		id string,
		switches, dwell float64,
	) WorkPatternProfileContext {
		profile := WorkPatternProfileContext{
			SnapshotID:     id,
			SnapshotSHA256: strings.Repeat(id[:1], 64),
			PolicyVersion:  WorkPatternProfilePolicyVersion,
			SourceCutoffAt: 9_000,
			Status:         WorkPatternProfileStatusQualified,
			Confidence:     0.8,
			InfluenceMode:  WorkPatternInfluenceProfile,
			SelectedProjections: []WorkPatternProjection{{
				ScopeKind:    WorkPatternScopeGlobal,
				ScopeKey:     "all",
				Label:        "All retained work",
				EvidenceKind: WorkPatternEvidenceObserved,
				Status:       WorkPatternProfileStatusQualified,
				Metrics: []WorkPatternMetric{
					{
						Name:  "switches_per_active_hour",
						Value: switches,
						Unit:  "count",
					},
					{
						Name:  "median_dwell_seconds",
						Value: dwell,
						Unit:  "seconds",
					},
				},
			}},
		}
		payload, err := json.Marshal(workPatternProfileContextPayload{
			SnapshotID:          profile.SnapshotID,
			SnapshotSHA256:      profile.SnapshotSHA256,
			PolicyVersion:       profile.PolicyVersion,
			SourceCutoffAt:      profile.SourceCutoffAt,
			Status:              profile.Status,
			Confidence:          profile.Confidence,
			InfluenceMode:       profile.InfluenceMode,
			SelectedProjections: profile.SelectedProjections,
			SemanticSignals:     []WorkPatternSemanticSignal{},
			CapabilitySignals:   []WorkPatternCapabilitySignal{},
			Contradictions:      []WorkPatternProfileCorrection{},
			SelectionManifest: []WorkPatternProfileSelectionEntry{{
				Ref:           "projection:global:all",
				Kind:          "projection",
				EvidenceClass: WorkPatternEvidenceObserved,
				Included:      true,
				Reason:        "selected_for_current_context",
			}},
			SelectionReason: "qualified_profile_available",
		})
		if err != nil {
			t.Fatal(err)
		}
		profile.CanonicalJSON = string(payload)
		return profile
	}
	fast := profileFor("fast-profile", 45, 4)
	focused := profileFor("focused-profile", 1, 900)

	fastInput := base
	fastInput.WorkProfile = &fast
	fastPrompt, fastNormalized, err := buildTaskOnsetPrompt(fastInput)
	if err != nil {
		t.Fatal(err)
	}
	focusedInput := base
	focusedInput.WorkProfile = &focused
	focusedPrompt, focusedNormalized, err := buildTaskOnsetPrompt(focusedInput)
	if err != nil {
		t.Fatal(err)
	}
	neutralPrompt, neutralNormalized, err := buildTaskOnsetPrompt(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(fastNormalized.Evidence) != len(neutralNormalized.Evidence) ||
		len(focusedNormalized.Evidence) != len(neutralNormalized.Evidence) {
		t.Fatal("profile prior changed the observation trace")
	}
	for _, want := range []string{
		`"switches_per_active_hour"`,
		`"median_dwell_seconds"`,
		`metric:global:all:switches_per_active_hour`,
	} {
		if !strings.Contains(fastPrompt, want) ||
			!strings.Contains(focusedPrompt, want) {
			t.Fatalf("profile prompts are missing %q", want)
		}
	}
	if fastPrompt == focusedPrompt ||
		strings.Contains(neutralPrompt, `"workProfile"`) ||
		strings.Contains(
			neutralPrompt,
			"metric:global:all:switches_per_active_hour",
		) {
		t.Fatal("neutral and profile prompt variants are not isolated")
	}
}

func TestTaskOnsetProfileConflictCapsConfidenceAndRejectsUnknownRefs(
	t *testing.T,
) {
	input := taskOnsetFixture()
	input.WorkProfile = &WorkPatternProfileContext{
		SnapshotID:     "profile",
		SnapshotSHA256: strings.Repeat("a", 64),
		PolicyVersion:  WorkPatternProfilePolicyVersion,
		SourceCutoffAt: input.WindowStart - 1,
		Status:         WorkPatternProfileStatusQualified,
		InfluenceMode:  WorkPatternInfluenceProfile,
		SelectedProjections: []WorkPatternProjection{{
			ScopeKind: WorkPatternScopeGlobal,
			ScopeKey:  "all",
			Metrics: []WorkPatternMetric{{
				Name: "median_dwell_seconds",
			}},
		}},
	}
	validRef := "metric:global:all:median_dwell_seconds"
	response := normalizeTaskOnsetResponse(input, TaskOnsetResponse{
		Episodes: []TaskOnsetPrediction{{
			LocalKey:             "incident",
			Task:                 "Investigate incident",
			DesiredResult:        "Cause identified",
			InferredStartedAt:    1_500,
			TaskConfidence:       0.95,
			BoundaryConfidence:   0.9,
			ResultConfidence:     0.85,
			EvidenceRefs:         []string{"event:1"},
			ProfileInfluenceRefs: []string{validRef, "metric:invented"},
			ProfileConflict:      true,
		}},
	})
	if len(response.Episodes) != 1 {
		t.Fatalf("response=%+v", response)
	}
	got := response.Episodes[0]
	if len(got.ProfileInfluenceRefs) != 1 ||
		got.ProfileInfluenceRefs[0] != validRef ||
		!got.ProfileConflict {
		t.Fatalf("profile provenance=%+v", got)
	}
	if got.TaskConfidence != 0.6 ||
		got.BoundaryConfidence != 0.6 ||
		got.ResultConfidence != 0.6 {
		t.Fatalf("conflict confidence was not capped: %+v", got)
	}

	input.WorkProfile = nil
	neutral := normalizeTaskOnsetResponse(input, TaskOnsetResponse{
		Episodes: []TaskOnsetPrediction{got},
	})
	if len(neutral.Episodes) != 1 ||
		len(neutral.Episodes[0].ProfileInfluenceRefs) != 0 ||
		neutral.Episodes[0].ProfileConflict {
		t.Fatalf("profile provenance leaked into neutral onset: %+v", neutral)
	}
}
