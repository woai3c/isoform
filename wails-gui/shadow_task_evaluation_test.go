package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"yansu-agent/agent"
)

type shadowTaskEvaluationFakeRunner struct {
	response   string
	executeErr error
	input      *agent.TaskInput
}

func (r *shadowTaskEvaluationFakeRunner) Init(context.Context) error { return nil }
func (r *shadowTaskEvaluationFakeRunner) IsReady() bool              { return true }
func (r *shadowTaskEvaluationFakeRunner) ExecuteTask(
	_ context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	r.input = input
	if r.response != "" {
		if err := callback(&agent.StreamEvent{
			Type:    "assistant",
			Content: r.response,
		}); err != nil {
			return "", err
		}
	}
	if r.executeErr != nil {
		return "", r.executeErr
	}
	return "shadow-task-evaluation-run", nil
}
func (r *shadowTaskEvaluationFakeRunner) CancelRun(string) error { return nil }
func (r *shadowTaskEvaluationFakeRunner) RespondToPermission(string, string) error {
	return nil
}
func (r *shadowTaskEvaluationFakeRunner) CancelPermission(string) {}
func (r *shadowTaskEvaluationFakeRunner) Shutdown() error         { return nil }

func shadowTaskEvaluationScore(value float64) *float64 {
	return &value
}

func shadowTaskEvaluationFixture() ShadowTaskEvaluationInput {
	return ShadowTaskEvaluationInput{
		Version:           ShadowTaskEvaluationInputVersion,
		TaskEpisodeID:     "task-episode-1",
		ShadowRunID:       "shadow-run-1",
		FrozenContextHash: "frozen-context-hash",
		Shadow: ShadowTaskEvaluationWork{
			Task:          "Prepare the customer incident update",
			DesiredResult: "A factual customer update ready to send",
			Plan: []string{
				"Read the incident timeline",
				"Draft a concise status update",
			},
			Actions: []string{
				"Read the timeline",
				"Drafted directly from the incident log",
			},
			Artifacts: []ShadowTaskEvaluationArtifact{{
				ID:      "shadow-artifact-1",
				Kind:    "document",
				Name:    "Customer incident update",
				Summary: "Resolved incident, impact, and next steps.",
				Digest:  "sha256:shadow",
			}},
			Result:      "The outage is resolved; affected requests will be reviewed.",
			StartedAt:   1_000,
			CompletedAt: 2_000,
		},
		Observed: ShadowTaskEvaluationWork{
			Task:          "Write the customer incident update",
			DesiredResult: "A factual customer update ready to send",
			Actions: []string{
				"Asked the incident lead for confirmation",
				"Revised an existing customer template",
			},
			Artifacts: []ShadowTaskEvaluationArtifact{{
				ID:      "observed-artifact-1",
				Kind:    "document",
				Name:    "Customer incident update",
				Summary: "Resolved incident, impact, and follow-up review.",
				Digest:  "sha256:observed",
			}},
			Result:      "The outage is resolved; we are reviewing affected requests.",
			StartedAt:   1_100,
			CompletedAt: 2_300,
		},
		QualityCriteria: []string{
			"Accurate against the incident timeline",
			"Clear about customer impact and next steps",
		},
		PolicyBoundaries: []string{
			"Draft only; do not send externally",
		},
		Evidence: []ShadowTaskEvaluationEvidence{
			{
				ID:      "shadow:task",
				Origin:  ShadowTaskEvidenceOriginShadow,
				Kind:    ShadowTaskEvidenceTask,
				Summary: "Shadow attempted to prepare the customer incident update.",
			},
			{
				ID:      "observed:task",
				Origin:  ShadowTaskEvidenceOriginObserved,
				Kind:    ShadowTaskEvidenceTask,
				Summary: "User prepared the customer incident update.",
			},
			{
				ID:      "observed:boundary",
				Origin:  ShadowTaskEvidenceOriginObserved,
				Kind:    ShadowTaskEvidenceBoundary,
				Summary: "Task began at 1100 and completed at 2300.",
			},
			{
				ID:      "shadow:process",
				Origin:  ShadowTaskEvidenceOriginShadow,
				Kind:    ShadowTaskEvidenceAction,
				Summary: "Shadow drafted directly from the incident log.",
			},
			{
				ID:      "observed:process",
				Origin:  ShadowTaskEvidenceOriginObserved,
				Kind:    ShadowTaskEvidenceAction,
				Summary: "User confirmed details and revised a template.",
			},
			{
				ID:      "shadow:result",
				Origin:  ShadowTaskEvidenceOriginShadow,
				Kind:    ShadowTaskEvidenceResult,
				Summary: "Shadow result says the outage is resolved and requests will be reviewed.",
			},
			{
				ID:      "observed:result",
				Origin:  ShadowTaskEvidenceOriginObserved,
				Kind:    ShadowTaskEvidenceResult,
				Summary: "Observed result says the outage is resolved and requests are under review.",
			},
			{
				ID:      "observed:timing",
				Origin:  ShadowTaskEvidenceOriginObserved,
				Kind:    ShadowTaskEvidenceTiming,
				Summary: "The user completed the update before the customer deadline.",
			},
			{
				ID:      "criterion:quality",
				Origin:  ShadowTaskEvidenceOriginCriterion,
				Kind:    ShadowTaskEvidenceQualityCriterion,
				Summary: "Updates must be accurate, clear, and state next steps.",
			},
			{
				ID:      "criterion:safety",
				Origin:  ShadowTaskEvidenceOriginCriterion,
				Kind:    ShadowTaskEvidenceSafety,
				Summary: "Shadow work must remain draft-only.",
			},
		},
	}
}

func shadowTaskEvaluationOutputJSON(reasoning string) string {
	return `{
		"version": "` + ShadowTaskEvaluationOutputVersion + `",
		"summary": "The tasks and results align, while the processes differ.",
		"task": {
			"assessment": "matched",
			"score": 1.2,
			"confidence": 1.1,
			"reason": "Both sides prepared the same customer update.",
			"evidenceRefs": ["shadow:task", "observed:task"]
		},
		"boundary": {
			"assessment": "partial",
			"score": 0.8,
			"confidence": 0.8,
			"reason": "The inferred scope matches, with a small timing difference.",
			"evidenceRefs": ["observed:boundary"]
		},
		"process": {
			"assessment": "different",
			"score": 0.2,
			"confidence": 0.95,
			"reason": "Shadow drafted directly while the user confirmed and revised.",
			"evidenceRefs": ["shadow:process", "observed:process"]
		},
		"result_similarity": {
			"assessment": "matched",
			"score": 0.96,
			"confidence": 0.94,
			"reason": "Both results communicate resolution and follow-up review.",
			"evidenceRefs": ["shadow:result", "observed:result"]
		},
		"result_quality": {
			"assessment": "equivalent",
			"score": 0.86,
			"confidence": 0.8,
			"reason": "Both satisfy the supplied accuracy and clarity criterion.",
			"evidenceRefs": ["shadow:result", "observed:result", "criterion:quality"]
		},
		"timing": {
			"assessment": "partial",
			"score": 0.85,
			"confidence": 0.8,
			"reason": "Both completed within the useful window.",
			"evidenceRefs": ["observed:boundary", "observed:timing"]
		},
		"reasoning": ` + reasoning + `,
		"safety": {
			"assessment": "safe",
			"score": 1.4,
			"confidence": 0.9,
			"reason": "The shadow output stayed draft-only.",
			"evidenceRefs": ["criterion:safety"]
		}
	}`
}

func TestNormalizeShadowTaskEvaluationUnknownReasoningWithoutSupport(t *testing.T) {
	input, err := normalizeShadowTaskEvaluationInput(shadowTaskEvaluationFixture())
	if err != nil {
		t.Fatalf("normalize input: %v", err)
	}
	raw := shadowTaskEvaluationOutputJSON(`{
		"assessment": "matched",
		"score": 0.9,
		"confidence": 0.99,
		"reason": "The motivations appear aligned.",
		"evidenceRefs": ["shadow:task", "observed:task"]
	}`)
	output, err := decodeShadowTaskEvaluationOutput(raw)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	output, _, err = normalizeShadowTaskEvaluationOutput(input, output)
	if err != nil {
		t.Fatalf("normalize output: %v", err)
	}

	if output.Reasoning.Assessment != ShadowTaskAssessmentUnknown {
		t.Fatalf("reasoning assessment=%q, want unknown", output.Reasoning.Assessment)
	}
	if output.Reasoning.Score != nil {
		t.Fatalf("reasoning score=%v, want nil", *output.Reasoning.Score)
	}
	if output.Reasoning.Confidence > shadowTaskUnsupportedReasoningConfidenceCap {
		t.Fatalf("reasoning confidence=%v, want capped", output.Reasoning.Confidence)
	}
	if output.Process.Assessment != ShadowTaskAssessmentDifferent {
		t.Fatalf("process assessment changed to %q", output.Process.Assessment)
	}
}

func TestShadowTaskEvaluationComparesPredictionOnlyToSupportedReasoning(
	t *testing.T,
) {
	input := shadowTaskEvaluationFixture()
	input.PredictedReasoning = []ShadowReasoningHypothesis{{
		Statement:    "The user may prioritize customer certainty.",
		Confidence:   0.49,
		Basis:        TaskReasoningBasisContextualInference,
		EvidenceRefs: []string{"observed:task"},
	}}
	input.Evidence = append(input.Evidence, ShadowTaskEvaluationEvidence{
		ID:      "observed:reason",
		Origin:  ShadowTaskEvidenceOriginObserved,
		Kind:    ShadowTaskEvidenceExplicitReasoning,
		Summary: "because the customer needs certainty",
	})
	normalized, err := normalizeShadowTaskEvaluationInput(input)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _, err := buildShadowTaskEvaluationPrompt(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, input.PredictedReasoning[0].Statement) ||
		!strings.Contains(prompt, "cannot validate itself") {
		t.Fatalf("reasoning comparison contract missing: %s", prompt)
	}

	raw := shadowTaskEvaluationOutputJSON(`{
		"assessment": "matched",
		"score": 0.88,
		"confidence": 0.9,
		"reason": "The source-backed rationale supports the hypothesis.",
		"evidenceRefs": ["observed:reason"]
	}`)
	output, err := decodeShadowTaskEvaluationOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	output, _, err = normalizeShadowTaskEvaluationOutput(normalized, output)
	if err != nil {
		t.Fatal(err)
	}
	if output.Reasoning.Assessment != ShadowTaskAssessmentMatched ||
		output.Reasoning.Score == nil || *output.Reasoning.Score != 0.88 {
		t.Fatalf("supported reasoning=%+v", output.Reasoning)
	}

	invalid := input
	invalid.PredictedReasoning = append(
		[]ShadowReasoningHypothesis(nil),
		input.PredictedReasoning...,
	)
	invalid.PredictedReasoning[0].EvidenceRefs = []string{"invented:evidence"}
	if _, err := normalizeShadowTaskEvaluationInput(invalid); err == nil ||
		!strings.Contains(err.Error(), "unknown evidence") {
		t.Fatalf("unknown predicted evidence error=%v", err)
	}
}

func TestShadowTaskEvaluationKeepsResultSimilaritySeparateFromProcess(t *testing.T) {
	input, err := normalizeShadowTaskEvaluationInput(shadowTaskEvaluationFixture())
	if err != nil {
		t.Fatalf("normalize input: %v", err)
	}
	raw := shadowTaskEvaluationOutputJSON(`{
		"assessment": "unknown",
		"score": null,
		"confidence": 0.2,
		"reason": "No explicit rationale was observed.",
		"evidenceRefs": []
	}`)
	output, err := decodeShadowTaskEvaluationOutput(raw)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	output, _, err = normalizeShadowTaskEvaluationOutput(input, output)
	if err != nil {
		t.Fatalf("normalize output: %v", err)
	}

	if output.ResultSimilarity.Assessment != ShadowTaskAssessmentMatched ||
		output.ResultSimilarity.Score == nil ||
		*output.ResultSimilarity.Score != 0.96 {
		t.Fatalf("result similarity=%+v", output.ResultSimilarity)
	}
	if output.Process.Assessment != ShadowTaskAssessmentDifferent ||
		output.Process.Score == nil ||
		*output.Process.Score != 0.2 {
		t.Fatalf("process=%+v", output.Process)
	}
}

func TestNormalizeShadowTaskEvaluationFiltersAndInvalidatesInventedEvidence(t *testing.T) {
	input, err := normalizeShadowTaskEvaluationInput(shadowTaskEvaluationFixture())
	if err != nil {
		t.Fatalf("normalize input: %v", err)
	}
	raw := strings.Replace(
		shadowTaskEvaluationOutputJSON(`{
			"assessment": "unknown",
			"score": null,
			"confidence": 0.2,
			"reason": "No explicit rationale was observed.",
			"evidenceRefs": []
		}`),
		`"evidenceRefs": ["shadow:task", "observed:task"]`,
		`"evidenceRefs": ["shadow:task", "invented:mixed"]`,
		1,
	)
	raw = strings.Replace(
		raw,
		`"evidenceRefs": ["observed:boundary"]`,
		`"evidenceRefs": ["invented:only"]`,
		1,
	)
	output, err := decodeShadowTaskEvaluationOutput(raw)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	output, rejected, err := normalizeShadowTaskEvaluationOutput(input, output)
	if err != nil {
		t.Fatalf("normalize output: %v", err)
	}

	if got := output.Task.EvidenceRefs; len(got) != 1 || got[0] != "shadow:task" {
		t.Fatalf("task evidence refs=%v", got)
	}
	if output.Boundary.Assessment != ShadowTaskAssessmentUnknown ||
		output.Boundary.Score != nil {
		t.Fatalf("boundary=%+v, want invalidated unknown", output.Boundary)
	}
	if !containsString(rejected, "invented:mixed") ||
		!containsString(rejected, "invented:only") {
		t.Fatalf("rejected refs=%v", rejected)
	}
}

func TestRunShadowTaskEvaluationPreservesPromptProvenanceAndAllDimensions(t *testing.T) {
	raw := shadowTaskEvaluationOutputJSON(`{
		"assessment": "unknown",
		"score": null,
		"confidence": 0.2,
		"reason": "No explicit rationale was observed.",
		"evidenceRefs": []
	}`)
	runner := &shadowTaskEvaluationFakeRunner{response: raw}
	result, err := runShadowTaskEvaluation(
		context.Background(),
		runner,
		"gpt-5.6-sol",
		shadowTaskEvaluationFixture(),
	)
	if err != nil {
		t.Fatalf("runShadowTaskEvaluation: %v", err)
	}
	if result.Evaluation == nil {
		t.Fatal("evaluation is nil")
	}
	if runner.input == nil ||
		!runner.input.NoTools ||
		runner.input.AutoApproveTools ||
		!runner.input.PlanMode ||
		!runner.input.Ephemeral ||
		!runner.input.NoSystem ||
		!runner.input.SuppressTelemetry ||
		runner.input.BackendHint != agent.BackendHintLearning {
		t.Fatalf("runner input=%+v", runner.input)
	}
	if runner.input.Prompt != result.ComparatorPrompt {
		t.Fatal("result did not preserve exact runner prompt")
	}
	if result.ComparatorHash != shadowTaskComparatorHash(result.ComparatorPrompt) {
		t.Fatalf("comparator hash=%q", result.ComparatorHash)
	}
	if result.EvaluatorModel != "gpt-5.6-sol" ||
		result.EvaluatorRaw != raw ||
		result.EvaluatorVersion != ShadowTaskEvaluatorVersion ||
		result.ComparatorVersion != ShadowTaskComparatorVersion {
		t.Fatalf("provenance=%+v", result)
	}
	if result.Evaluation.Task.Assessment == "" ||
		result.Evaluation.Boundary.Assessment == "" ||
		result.Evaluation.Process.Assessment == "" ||
		result.Evaluation.ResultSimilarity.Assessment == "" ||
		result.Evaluation.ResultQuality.Assessment == "" ||
		result.Evaluation.Timing.Assessment == "" ||
		result.Evaluation.Reasoning.Assessment == "" ||
		result.Evaluation.Safety.Assessment == "" {
		t.Fatalf("missing dimension: %+v", result.Evaluation)
	}
	if result.Evaluation.Task.Score == nil || *result.Evaluation.Task.Score != 1 {
		t.Fatalf("task score was not clamped: %+v", result.Evaluation.Task)
	}
	if result.Evaluation.Safety.Score == nil || *result.Evaluation.Safety.Score != 1 {
		t.Fatalf("safety score was not clamped: %+v", result.Evaluation.Safety)
	}
}

func TestRunShadowTaskEvaluationReturnsProvenanceOnMalformedAndModelErrors(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		runner := &shadowTaskEvaluationFakeRunner{response: `{"version":`}
		result, err := runShadowTaskEvaluation(
			context.Background(),
			runner,
			"gpt-5.6-sol",
			shadowTaskEvaluationFixture(),
		)
		if err == nil || !strings.Contains(err.Error(), "parse shadow task evaluation") {
			t.Fatalf("error=%v", err)
		}
		if result.EvaluatorRaw != `{"version":` ||
			result.ComparatorPrompt == "" ||
			result.ComparatorHash == "" ||
			result.Evaluation != nil {
			t.Fatalf("malformed provenance=%+v", result)
		}
	})

	t.Run("missing required field", func(t *testing.T) {
		raw := strings.Replace(
			shadowTaskEvaluationOutputJSON(`{
				"assessment": "unknown",
				"score": null,
				"confidence": 0.2,
				"reason": "No explicit rationale was observed.",
				"evidenceRefs": []
			}`),
			`"evidenceRefs": ["shadow:task", "observed:task"]`,
			`"omittedEvidenceRefs": true`,
			1,
		)
		runner := &shadowTaskEvaluationFakeRunner{response: raw}
		result, err := runShadowTaskEvaluation(
			context.Background(),
			runner,
			"gpt-5.6-sol",
			shadowTaskEvaluationFixture(),
		)
		if err == nil ||
			!strings.Contains(err.Error(), `missing required field "evidenceRefs"`) {
			t.Fatalf("error=%v", err)
		}
		if result.EvaluatorRaw != raw || result.Evaluation != nil {
			t.Fatalf("strict-shape provenance=%+v", result)
		}
	})

	t.Run("model", func(t *testing.T) {
		runner := &shadowTaskEvaluationFakeRunner{
			response:   `{"partial":"output"}`,
			executeErr: errors.New("model unavailable"),
		}
		result, err := runShadowTaskEvaluation(
			context.Background(),
			runner,
			"gpt-5.6-sol",
			shadowTaskEvaluationFixture(),
		)
		if err == nil || !strings.Contains(err.Error(), "model unavailable") {
			t.Fatalf("error=%v", err)
		}
		if result.EvaluatorRaw != `{"partial":"output"}` ||
			result.ComparatorPrompt == "" ||
			result.ComparatorHash == "" ||
			result.Evaluation != nil {
			t.Fatalf("model-error provenance=%+v", result)
		}
	})
}
