package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func shadowTaskLearningTestScore(value float64) *float64 {
	return &value
}

func shadowTaskLearningTestUnknown(reason string) ShadowTaskEvaluationDimension {
	return ShadowTaskEvaluationDimension{
		Assessment: ShadowTaskAssessmentUnknown,
		Score:      nil,
		Confidence: 0.2,
		Reason:     reason,
	}
}

func shadowTaskLearningTestRecord(index int) ShadowTaskLearningEvaluationRecord {
	suffix := fmt.Sprintf("%d", index)
	id := func(prefix string) string { return prefix + ":" + suffix }
	contextHash := fmt.Sprintf("%064x", index+1)
	input := ShadowTaskEvaluationInput{
		Version:           ShadowTaskEvaluationInputVersion,
		TaskEpisodeID:     id("task-episode"),
		ShadowRunID:       id("shadow-run"),
		FrozenContextHash: contextHash,
		Shadow: ShadowTaskEvaluationWork{
			Task:          "Prepare the customer incident update",
			DesiredResult: "A factual customer update ready to send",
			Plan:          []string{"Read the timeline", "Draft the update"},
			Actions:       []string{"Read the incident log", "Drafted locally"},
			Result:        "The incident is resolved and affected requests will be reviewed.",
			StartedAt:     int64(1_000 + index),
			CompletedAt:   int64(2_000 + index),
		},
		Observed: ShadowTaskEvaluationWork{
			Task:          "Prepare the customer incident update",
			DesiredResult: "A factual customer update ready to send",
			Actions: []string{
				"Confirmed incident details before drafting the update",
			},
			Result:      "The incident is resolved and affected requests are under review.",
			StartedAt:   int64(1_100 + index),
			CompletedAt: int64(2_200 + index),
		},
		QualityCriteria: []string{
			"Accurate against the incident timeline",
			"Clear about impact and next steps",
		},
		PolicyBoundaries: []string{"Draft only; do not send externally"},
		Evidence: []ShadowTaskEvaluationEvidence{
			{
				ID:         id("shadow:task"),
				Origin:     ShadowTaskEvidenceOriginShadow,
				Kind:       ShadowTaskEvidenceTask,
				OccurredAt: int64(1_000 + index),
				Summary:    "Shadow attempted the incident update.",
			},
			{
				ID:         id("observed:task"),
				Origin:     ShadowTaskEvidenceOriginObserved,
				Kind:       ShadowTaskEvidenceTask,
				OccurredAt: int64(1_100 + index),
				Summary:    "The user prepared the incident update.",
			},
			{
				ID:         id("observed:boundary"),
				Origin:     ShadowTaskEvidenceOriginObserved,
				Kind:       ShadowTaskEvidenceBoundary,
				OccurredAt: int64(1_100 + index),
				Summary:    "The observed task boundary covers drafting the update.",
			},
			{
				ID:         id("shadow:process"),
				Origin:     ShadowTaskEvidenceOriginShadow,
				Kind:       ShadowTaskEvidenceAction,
				OccurredAt: int64(1_400 + index),
				Summary:    "Shadow drafted directly from the log.",
			},
			{
				ID:         id("observed:process"),
				Origin:     ShadowTaskEvidenceOriginObserved,
				Kind:       ShadowTaskEvidenceAction,
				OccurredAt: int64(1_500 + index),
				Summary:    "Confirm incident details before drafting the update.",
			},
			{
				ID:         id("shadow:result"),
				Origin:     ShadowTaskEvidenceOriginShadow,
				Kind:       ShadowTaskEvidenceResult,
				OccurredAt: int64(2_000 + index),
				Summary:    "Shadow produced a local incident update.",
			},
			{
				ID:         id("observed:result"),
				Origin:     ShadowTaskEvidenceOriginObserved,
				Kind:       ShadowTaskEvidenceResult,
				OccurredAt: int64(2_200 + index),
				Summary:    "Incident updates state resolution, impact, and next steps.",
			},
			{
				ID:         id("observed:timing"),
				Origin:     ShadowTaskEvidenceOriginObserved,
				Kind:       ShadowTaskEvidenceTiming,
				OccurredAt: int64(2_200 + index),
				Summary:    "The update was completed before the customer deadline.",
			},
			{
				ID:         id("observed:safety"),
				Origin:     ShadowTaskEvidenceOriginObserved,
				Kind:       ShadowTaskEvidenceSafety,
				OccurredAt: int64(2_200 + index),
				Summary:    "No external send occurred during the task.",
			},
			{
				ID:      id("criterion:quality"),
				Origin:  ShadowTaskEvidenceOriginCriterion,
				Kind:    ShadowTaskEvidenceQualityCriterion,
				Summary: "Incident updates must be accurate, clear, and actionable.",
			},
			{
				ID:      id("criterion:safety"),
				Origin:  ShadowTaskEvidenceOriginCriterion,
				Kind:    ShadowTaskEvidenceSafety,
				Summary: "Shadow work must stay draft-only.",
			},
		},
	}
	output := &ShadowTaskEvaluationOutput{
		Version: ShadowTaskEvaluationOutputVersion,
		Summary: "The completed task and result align.",
		Task: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskAssessmentMatched,
			Score:      shadowTaskLearningTestScore(0.95),
			Confidence: 0.95,
			Reason:     "Both sides performed the same task.",
			EvidenceRefs: []string{
				id("shadow:task"),
				id("observed:task"),
			},
		},
		Boundary: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskAssessmentMatched,
			Score:      shadowTaskLearningTestScore(0.9),
			Confidence: 0.9,
			Reason:     "The boundary covers the observed task.",
			EvidenceRefs: []string{
				id("observed:boundary"),
			},
		},
		Process: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskAssessmentPartial,
			Score:      shadowTaskLearningTestScore(0.82),
			Confidence: 0.92,
			Reason:     "Both used the incident record, with an extra confirmation step.",
			EvidenceRefs: []string{
				id("shadow:process"),
				id("observed:process"),
			},
		},
		ResultSimilarity: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskAssessmentMatched,
			Score:      shadowTaskLearningTestScore(0.92),
			Confidence: 0.93,
			Reason:     "Both results communicate resolution and review.",
			EvidenceRefs: []string{
				id("shadow:result"),
				id("observed:result"),
			},
		},
		ResultQuality: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskQualityEquivalent,
			Score:      shadowTaskLearningTestScore(0.9),
			Confidence: 0.9,
			Reason:     "The observed result satisfies the supplied criteria.",
			EvidenceRefs: []string{
				id("shadow:result"),
				id("observed:result"),
				id("criterion:quality"),
			},
		},
		Timing: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskAssessmentMatched,
			Score:      shadowTaskLearningTestScore(0.9),
			Confidence: 0.88,
			Reason:     "The work completed within the useful window.",
			EvidenceRefs: []string{
				id("observed:boundary"),
				id("observed:timing"),
			},
		},
		Reasoning: shadowTaskLearningTestUnknown(
			"No explicit rationale was observed.",
		),
		Safety: ShadowTaskEvaluationDimension{
			Assessment: ShadowTaskSafetySafe,
			Score:      shadowTaskLearningTestScore(1),
			Confidence: 0.95,
			Reason:     "The isolated work remained draft-only.",
			EvidenceRefs: []string{
				id("observed:safety"),
				id("criterion:safety"),
			},
		},
	}
	return ShadowTaskLearningEvaluationRecord{
		EvaluationID:        id("evaluation"),
		PersistedAt:         int64(3_000 + index),
		TaskEpisodeStatus:   TaskEpisodeStatusCompleted,
		ShadowPromptVersion: shadowTaskPromptVersion,
		ComparisonVersion:   ShadowTaskComparatorVersion,
		EvaluatorVersion:    ShadowTaskEvaluatorVersion,
		CapabilityKey:       "communications.incident_update",
		TaskEpisodeID:       input.TaskEpisodeID,
		ShadowRunID:         input.ShadowRunID,
		FrozenContextHash:   input.FrozenContextHash,
		Input:               input,
		Evaluation:          output,
	}
}

func TestShadowTaskLearningExcludesPriorPromptGeneration(t *testing.T) {
	current := shadowTaskLearningTestRecord(1)
	historical := shadowTaskLearningTestRecord(2)
	historical.ShadowPromptVersion = "shadow-task-v4"

	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{historical, current},
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.EvaluationCount != 1 ||
		len(projection.ExcludedEvaluations) != 1 ||
		projection.ExcludedEvaluations[0].EvaluationID != historical.EvaluationID ||
		projection.ExcludedEvaluations[0].Reason != "incompatible_shadow_prompt_version" {
		t.Fatalf("version-scoped projection=%+v", projection)
	}
}

func TestShadowTaskLearningExcludesPriorComparisonGenerations(t *testing.T) {
	current := shadowTaskLearningTestRecord(1)
	oldInput := shadowTaskLearningTestRecord(2)
	oldInput.Input.Version = "shadow-task-evaluation-input-v2"
	oldOutput := shadowTaskLearningTestRecord(3)
	oldOutput.Evaluation.Version = "shadow-task-evaluation-output-v2"
	oldComparator := shadowTaskLearningTestRecord(4)
	oldComparator.ComparisonVersion = "shadow-task-comparator-v2"
	oldEvaluator := shadowTaskLearningTestRecord(5)
	oldEvaluator.EvaluatorVersion = "shadow-task-evaluator-v2"

	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{
			oldEvaluator,
			current,
			oldComparator,
			oldOutput,
			oldInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.EvaluationCount != 1 ||
		len(projection.ExcludedEvaluations) != 4 {
		t.Fatalf("version-scoped projection=%+v", projection)
	}
	reasons := make(map[string]bool)
	for _, exclusion := range projection.ExcludedEvaluations {
		reasons[exclusion.Reason] = true
	}
	for _, want := range []string{
		"incompatible_evaluation_input_version",
		"incompatible_evaluation_output_version",
		"incompatible_comparator_version",
		"incompatible_evaluator_version",
	} {
		if !reasons[want] {
			t.Errorf("missing exclusion %q: %+v", want, projection.ExcludedEvaluations)
		}
	}
}

func shadowTaskLearningWithReasoning(
	record ShadowTaskLearningEvaluationRecord,
	kind, statement string,
) ShadowTaskLearningEvaluationRecord {
	evidenceID := "observed:reasoning:" + record.EvaluationID
	record.Input.Evidence = append(
		append([]ShadowTaskEvaluationEvidence(nil), record.Input.Evidence...),
		ShadowTaskEvaluationEvidence{
			ID:         evidenceID,
			Origin:     ShadowTaskEvidenceOriginObserved,
			Kind:       kind,
			OccurredAt: record.PersistedAt - 100,
			Summary:    statement,
		},
	)
	output := *record.Evaluation
	output.Reasoning = ShadowTaskEvaluationDimension{
		Assessment:   ShadowTaskAssessmentMatched,
		Score:        shadowTaskLearningTestScore(0.9),
		Confidence:   0.95,
		Reason:       "The rationale is explicitly source-backed.",
		EvidenceRefs: []string{evidenceID},
	}
	record.Evaluation = &output
	return record
}

func shadowTaskLearningFindCandidate(
	t *testing.T,
	projection ShadowTaskLearningProjection,
	targetKind, learningKind string,
) ShadowTaskLearningCandidate {
	t.Helper()
	for _, candidate := range projection.Candidates {
		if candidate.TargetKind == targetKind &&
			candidate.LearningKind == learningKind {
			return candidate
		}
	}
	t.Fatalf(
		"candidate target=%q learning=%q not found in %+v",
		targetKind,
		learningKind,
		projection.Candidates,
	)
	return ShadowTaskLearningCandidate{}
}

func shadowTaskLearningHasCandidate(
	projection ShadowTaskLearningProjection,
	targetKind, learningKind string,
) bool {
	for _, candidate := range projection.Candidates {
		if candidate.TargetKind == targetKind &&
			candidate.LearningKind == learningKind {
			return true
		}
	}
	return false
}

func TestShadowTaskLearningRequiresPersistedCompletedSourceBackedEvaluation(t *testing.T) {
	notPersisted := shadowTaskLearningTestRecord(1)
	notPersisted.PersistedAt = 0
	incomplete := shadowTaskLearningTestRecord(2)
	incomplete.TaskEpisodeStatus = TaskEpisodeStatusActive
	missingPayload := shadowTaskLearningTestRecord(3)
	missingPayload.Evaluation = nil
	noObserved := shadowTaskLearningTestRecord(4)
	for i := range noObserved.Input.Evidence {
		if noObserved.Input.Evidence[i].Origin == ShadowTaskEvidenceOriginObserved {
			noObserved.Input.Evidence[i].Origin = ShadowTaskEvidenceOriginShadow
		}
	}

	projection, err := BuildShadowTaskLearningProjection([]ShadowTaskLearningEvaluationRecord{
		notPersisted,
		incomplete,
		missingPayload,
		noObserved,
	})
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	if projection.EvaluationCount != 0 || len(projection.Candidates) != 0 {
		t.Fatalf("unsafe records projected: %+v", projection)
	}
	if len(projection.ExcludedEvaluations) != 4 {
		t.Fatalf("exclusions=%+v, want 4", projection.ExcludedEvaluations)
	}
	reasons := make([]string, 0, len(projection.ExcludedEvaluations))
	for _, exclusion := range projection.ExcludedEvaluations {
		reasons = append(reasons, exclusion.Reason)
	}
	for _, want := range []string{
		"evaluation_not_persisted",
		"task_episode_not_completed",
		"persisted_evaluation_missing_payload",
		"no_source_backed_observed_evidence",
	} {
		if !slices.Contains(reasons, want) {
			t.Fatalf("exclusion reasons=%v, missing %q", reasons, want)
		}
	}
}

func TestShadowTaskLearningReadinessNeverCreatesAuthority(t *testing.T) {
	records := make([]ShadowTaskLearningEvaluationRecord, 0, 5)
	for i := 1; i <= 5; i++ {
		records = append(records, shadowTaskLearningTestRecord(i))
	}
	projection, err := BuildShadowTaskLearningProjection(records)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	capability := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetCapabilityProfile,
		ShadowTaskLearningKindCapabilityEvidence,
	)
	if capability.Qualification.Decision != ShadowTaskLearningDecisionQualified {
		t.Fatalf("capability qualification=%+v", capability.Qualification)
	}
	if projection.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
		capability.AuthorityEffect != ShadowTaskLearningAuthorityNone {
		t.Fatalf("authority effect projection=%q candidate=%q",
			projection.AuthorityEffect,
			capability.AuthorityEffect,
		)
	}
	allowedTargets := []string{
		ShadowTaskLearningTargetCapabilityProfile,
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningTargetKnowledge,
		ShadowTaskLearningTargetTypedSelf,
	}
	for _, candidate := range projection.Candidates {
		if !slices.Contains(allowedTargets, candidate.TargetKind) {
			t.Fatalf("candidate created unsupported target %q", candidate.TargetKind)
		}
		if candidate.AuthorityEffect != ShadowTaskLearningAuthorityNone {
			t.Fatalf("candidate can affect authority: %+v", candidate)
		}
	}
	payload, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "authority_grant") ||
		strings.Contains(string(payload), `"grants"`) {
		t.Fatalf("projection contains authority material: %s", payload)
	}
}

func TestShadowTaskLearningUsesRuntimeSafetyWithoutUserModelContamination(
	t *testing.T,
) {
	records := make([]ShadowTaskLearningEvaluationRecord, 0, 5)
	for index := 1; index <= 5; index++ {
		record := shadowTaskLearningTestRecord(index)
		safetyID := fmt.Sprintf("shadow:runtime-safety:%d", index)
		for evidenceIndex := range record.Input.Evidence {
			if record.Input.Evidence[evidenceIndex].Kind ==
				ShadowTaskEvidenceSafety {
				record.Input.Evidence[evidenceIndex].Kind =
					ShadowTaskEvidenceAction
			}
		}
		record.Input.Evidence = append(
			record.Input.Evidence,
			ShadowTaskEvaluationEvidence{
				ID:      safetyID,
				Origin:  ShadowTaskEvidenceOriginShadow,
				Kind:    ShadowTaskEvidenceSafety,
				Summary: "allow read-only isolated execution",
			},
		)
		record.Evaluation.Safety.EvidenceRefs = []string{safetyID}
		records = append(records, record)
	}

	projection, err := BuildShadowTaskLearningProjection(records)
	if err != nil {
		t.Fatal(err)
	}
	capability := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetCapabilityProfile,
		ShadowTaskLearningKindCapabilityEvidence,
	)
	if capability.Qualification.Decision !=
		ShadowTaskLearningDecisionQualified {
		t.Fatalf("runtime safety did not qualify capability: %+v", capability.Qualification)
	}
	for _, candidate := range projection.Candidates {
		for _, evidence := range candidate.Evidence {
			if evidence.Origin == ShadowTaskEvidenceOriginShadow {
				t.Fatalf("runtime safety contaminated user learning: %+v", evidence)
			}
		}
	}
}

func TestShadowTaskLearningRuntimeSafetyConcernVetoesQualification(
	t *testing.T,
) {
	record := shadowTaskLearningTestRecord(1)
	for evidenceIndex := range record.Input.Evidence {
		if record.Input.Evidence[evidenceIndex].Kind == ShadowTaskEvidenceSafety {
			record.Input.Evidence[evidenceIndex].Kind = ShadowTaskEvidenceAction
		}
	}
	record.Input.Evidence = append(
		record.Input.Evidence,
		ShadowTaskEvaluationEvidence{
			ID:      "shadow:runtime-safety:concern",
			Origin:  ShadowTaskEvidenceOriginShadow,
			Kind:    ShadowTaskEvidenceSafety,
			Summary: "deny local write outside the isolated workspace",
		},
	)
	record.Evaluation.Safety.Assessment = ShadowTaskSafetyConcern
	record.Evaluation.Safety.Score = shadowTaskLearningTestScore(0.5)
	record.Evaluation.Safety.EvidenceRefs = []string{
		"shadow:runtime-safety:concern",
	}

	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{record},
	)
	if err != nil {
		t.Fatal(err)
	}
	capability := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetCapabilityProfile,
		ShadowTaskLearningKindCapabilityEvidence,
	)
	if capability.Qualification.Decision != ShadowTaskLearningDecisionRejected ||
		!slices.Contains(
			capability.Qualification.Reasons,
			"source_backed_safety_concern",
		) {
		t.Fatalf("runtime safety concern did not veto qualification: %+v", capability.Qualification)
	}
}

func TestShadowTaskLearningExcludesHiddenAndAgentGeneratedClaims(t *testing.T) {
	const hidden = "HIDDEN CLAIM: the user always sends the first draft"
	record := shadowTaskLearningTestRecord(1)
	record.Input.Shadow.Result = hidden
	record.Input.Evidence = append(record.Input.Evidence, ShadowTaskEvaluationEvidence{
		ID:      "shadow:hidden",
		Origin:  ShadowTaskEvidenceOriginShadow,
		Kind:    ShadowTaskEvidenceExplicitReasoning,
		Summary: hidden,
	})
	output := *record.Evaluation
	output.Summary = hidden
	output.Process.Reason = hidden
	output.Process.EvidenceRefs = append(output.Process.EvidenceRefs, "shadow:hidden")
	record.Evaluation = &output

	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{record},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	payload, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), hidden) {
		t.Fatalf("hidden or agent-generated content leaked: %s", payload)
	}
	for _, candidate := range projection.Candidates {
		for _, evidence := range candidate.Evidence {
			if evidence.Origin == ShadowTaskEvidenceOriginShadow {
				t.Fatalf("shadow evidence projected: %+v", evidence)
			}
		}
	}
}

func TestShadowTaskLearningPreservesUnknownAsNonNumeric(t *testing.T) {
	record := shadowTaskLearningTestRecord(1)
	output := *record.Evaluation
	output.Timing = shadowTaskLearningTestUnknown("Timing could not be observed.")
	output.Reasoning = shadowTaskLearningTestUnknown("Reasoning was not explicit.")
	output.Process.EvidenceRefs = []string{"shadow:process:1"}
	record.Evaluation = &output

	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{record},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	capability := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetCapabilityProfile,
		ShadowTaskLearningKindCapabilityEvidence,
	)
	unknownCount := 0
	for _, dimension := range capability.Dimensions {
		if dimension.Assessment != ShadowTaskAssessmentUnknown {
			continue
		}
		unknownCount++
		if dimension.Score != nil {
			t.Fatalf("unknown dimension became numeric: %+v", dimension)
		}
	}
	if unknownCount != 3 {
		t.Fatalf("unknown dimensions=%d, want 3: %+v", unknownCount, capability.Dimensions)
	}
}

func TestShadowTaskLearningReasoningRequiresAllowedSourceEvidence(t *testing.T) {
	unsupported := shadowTaskLearningTestRecord(1)
	output := *unsupported.Evaluation
	output.Reasoning = ShadowTaskEvaluationDimension{
		Assessment:   ShadowTaskAssessmentMatched,
		Score:        shadowTaskLearningTestScore(0.95),
		Confidence:   0.99,
		Reason:       "The evaluator inferred a rationale.",
		EvidenceRefs: []string{"observed:process:1"},
	}
	unsupported.Evaluation = &output

	projection, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{unsupported},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	if shadowTaskLearningHasCandidate(
		projection,
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningKindReasoning,
	) || shadowTaskLearningHasCandidate(
		projection,
		ShadowTaskLearningTargetTypedSelf,
		ShadowTaskLearningKindReasoning,
	) {
		t.Fatalf("unsupported reasoning projected: %+v", projection.Candidates)
	}

	explicit := shadowTaskLearningWithReasoning(
		shadowTaskLearningTestRecord(2),
		ShadowTaskEvidenceExplicitReasoning,
		"Confirm incident ownership before describing next steps.",
	)
	projection, err = BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{explicit},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection explicit: %v", err)
	}
	memory := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningKindReasoning,
	)
	self := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetTypedSelf,
		ShadowTaskLearningKindReasoning,
	)
	if memory.Qualification.Decision != ShadowTaskLearningDecisionQualified {
		t.Fatalf("explicit reasoning memory=%+v", memory.Qualification)
	}
	if self.Qualification.Decision != ShadowTaskLearningDecisionCollecting {
		t.Fatalf("single rationale should not establish Self: %+v", self.Qualification)
	}
	for _, evidence := range memory.Evidence {
		if evidence.Kind != ShadowTaskEvidenceExplicitReasoning {
			t.Fatalf("reasoning candidate contains unrelated evidence: %+v", evidence)
		}
	}
}

func TestShadowTaskLearningCorrectionCreatesAppendOnlySelfRevision(t *testing.T) {
	const correction = "Always confirm the incident owner before naming next steps."
	firstRecord := shadowTaskLearningWithReasoning(
		shadowTaskLearningTestRecord(1),
		ShadowTaskEvidenceExplicitCorrection,
		correction,
	)
	first, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{firstRecord},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection first: %v", err)
	}
	firstMemory := shadowTaskLearningFindCandidate(
		t,
		first,
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningKindCorrection,
	)
	firstSelf := shadowTaskLearningFindCandidate(
		t,
		first,
		ShadowTaskLearningTargetTypedSelf,
		ShadowTaskLearningKindCorrection,
	)
	if firstMemory.Qualification.Decision != ShadowTaskLearningDecisionQualified ||
		firstSelf.Qualification.Decision != ShadowTaskLearningDecisionQualified {
		t.Fatalf("correction not qualified memory=%+v self=%+v",
			firstMemory.Qualification,
			firstSelf.Qualification,
		)
	}
	if firstSelf.RevisionMode != ShadowTaskLearningRevisionAppendOnly ||
		firstSelf.SuggestedSelfStatus != selfModelStatusProvisional {
		t.Fatalf("first Self correction=%+v", firstSelf)
	}

	secondRecord := shadowTaskLearningWithReasoning(
		shadowTaskLearningTestRecord(2),
		ShadowTaskEvidenceExplicitCorrection,
		correction,
	)
	second, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{firstRecord, secondRecord},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection second: %v", err)
	}
	secondSelf := shadowTaskLearningFindCandidate(
		t,
		second,
		ShadowTaskLearningTargetTypedSelf,
		ShadowTaskLearningKindCorrection,
	)
	if firstSelf.ProjectionKey != secondSelf.ProjectionKey {
		t.Fatalf("semantic correction key changed %q -> %q",
			firstSelf.ProjectionKey,
			secondSelf.ProjectionKey,
		)
	}
	if firstSelf.RevisionID == secondSelf.RevisionID {
		t.Fatalf("new evidence did not create an append-only revision: %+v", secondSelf)
	}
	if secondSelf.SuggestedSelfStatus != selfModelStatusActive {
		t.Fatalf("repeated correction status=%q, want active",
			secondSelf.SuggestedSelfStatus,
		)
	}
}

func TestShadowTaskLearningRequiresRepeatedEvaluatedProcessAndResultEvidence(t *testing.T) {
	records := []ShadowTaskLearningEvaluationRecord{
		shadowTaskLearningTestRecord(1),
		shadowTaskLearningTestRecord(2),
		shadowTaskLearningTestRecord(3),
	}
	projection, err := BuildShadowTaskLearningProjection(records)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	memoryProcess := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningKindProcessPattern,
	)
	knowledgeProcess := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetKnowledge,
		ShadowTaskLearningKindProcessPattern,
	)
	knowledgeResult := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetKnowledge,
		ShadowTaskLearningKindResultPattern,
	)
	for _, candidate := range []ShadowTaskLearningCandidate{
		memoryProcess,
		knowledgeProcess,
		knowledgeResult,
	} {
		if candidate.Qualification.Decision != ShadowTaskLearningDecisionQualified {
			t.Fatalf("candidate did not qualify after repeated outcomes: %+v", candidate)
		}
		if candidate.Qualification.QualifiedEvaluationCount != 3 ||
			candidate.Qualification.ObservedContextCount != 3 {
			t.Fatalf("candidate repetition counts=%+v", candidate.Qualification)
		}
	}
}

func TestShadowTaskLearningPreservesResultAndProcessProvenance(t *testing.T) {
	records := []ShadowTaskLearningEvaluationRecord{
		shadowTaskLearningTestRecord(1),
		shadowTaskLearningTestRecord(2),
		shadowTaskLearningTestRecord(3),
	}
	projection, err := BuildShadowTaskLearningProjection(records)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection: %v", err)
	}
	process := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningKindProcessPattern,
	)
	result := shadowTaskLearningFindCandidate(
		t,
		projection,
		ShadowTaskLearningTargetKnowledge,
		ShadowTaskLearningKindResultPattern,
	)
	assertProvenance := func(
		name string,
		candidate ShadowTaskLearningCandidate,
		wantKind string,
	) {
		t.Helper()
		evaluations := make(map[string]struct{})
		foundKind := false
		allowedRefs := make(map[string]map[string]struct{})
		for _, evidence := range candidate.Evidence {
			if evidence.EvaluationID == "" ||
				evidence.TaskEpisodeID == "" ||
				evidence.ShadowRunID == "" ||
				evidence.ContextHash == "" ||
				evidence.PersistedAt <= 0 {
				t.Fatalf("%s incomplete provenance: %+v", name, evidence)
			}
			if evidence.Origin == ShadowTaskEvidenceOriginShadow {
				t.Fatalf("%s leaked shadow evidence: %+v", name, evidence)
			}
			evaluations[evidence.EvaluationID] = struct{}{}
			if evidence.Kind == wantKind {
				foundKind = true
			}
			if allowedRefs[evidence.EvaluationID] == nil {
				allowedRefs[evidence.EvaluationID] = make(map[string]struct{})
			}
			allowedRefs[evidence.EvaluationID][evidence.EvidenceID] = struct{}{}
		}
		if len(evaluations) != 3 || !foundKind {
			t.Fatalf("%s provenance evaluations=%d foundKind=%v: %+v",
				name,
				len(evaluations),
				foundKind,
				candidate.Evidence,
			)
		}
		for _, dimension := range candidate.Dimensions {
			for _, ref := range dimension.EvidenceRefs {
				if _, ok := allowedRefs[dimension.EvaluationID][ref]; !ok {
					t.Fatalf("%s dimension ref %q has no preserved source", name, ref)
				}
			}
		}
	}
	assertProvenance("process", process, ShadowTaskEvidenceAction)
	assertProvenance("result", result, ShadowTaskEvidenceResult)
}

func TestShadowTaskLearningProjectionIsDeterministicAndIdempotent(t *testing.T) {
	first := shadowTaskLearningTestRecord(1)
	second := shadowTaskLearningTestRecord(2)
	third := shadowTaskLearningTestRecord(3)
	left, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{third, first, second, first},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection left: %v", err)
	}
	right, err := BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{first, second, third},
	)
	if err != nil {
		t.Fatalf("BuildShadowTaskLearningProjection right: %v", err)
	}
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("projection is not deterministic\nleft:  %s\nright: %s",
			leftJSON,
			rightJSON,
		)
	}
	if left.EvaluationCount != 3 {
		t.Fatalf("duplicate evaluation was not idempotent: %+v", left)
	}

	conflict := first
	conflictingOutput := *conflict.Evaluation
	conflictingOutput.Summary = "Conflicting immutable payload."
	conflict.Evaluation = &conflictingOutput
	_, err = BuildShadowTaskLearningProjection(
		[]ShadowTaskLearningEvaluationRecord{first, conflict},
	)
	if err == nil || !strings.Contains(err.Error(), "conflicting immutable payloads") {
		t.Fatalf("conflicting idempotency error=%v", err)
	}
}
