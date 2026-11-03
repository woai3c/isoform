package main

import (
	"strings"
	"testing"
)

func persistWorkPatternLearningEvaluation(
	t *testing.T,
	store *ActivityStore,
	run ShadowRun,
	createdAt int64,
) ShadowRunEvaluation {
	t.Helper()
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
		Summary:          "The shadow result matched the observed user result.",
		Task:             matchedTaskDimension(refs[0]),
		Boundary:         matchedTaskDimension(refs[0]),
		Process:          matchedTaskDimension(refs[0]),
		ResultSimilarity: matchedTaskDimension(refs[0]),
		ResultQuality: ShadowTaskEvaluationDimension{
			Assessment:   ShadowTaskQualityEquivalent,
			Score:        shadowScore(0.9),
			Confidence:   0.9,
			Reason:       "The observed and isolated results are equivalent.",
			EvidenceRefs: []string{refs[0]},
		},
		Timing:    matchedTaskDimension(refs[0]),
		Reasoning: unknownTaskDimension(),
		Safety: ShadowTaskEvaluationDimension{
			Assessment:   ShadowTaskSafetySafe,
			Score:        shadowScore(1),
			Confidence:   1,
			Reason:       "All work stayed isolated.",
			EvidenceRefs: []string{},
		},
	}
	prompt, _, err := buildShadowTaskEvaluationPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, added, err := PersistShadowTaskEvaluation(
		store,
		input,
		ShadowTaskEvaluationResult{
			EvaluatorVersion:  ShadowTaskEvaluatorVersion,
			ComparatorVersion: ShadowTaskComparatorVersion,
			InputVersion:      ShadowTaskEvaluationInputVersion,
			ComparatorPrompt:  prompt,
			ComparatorHash:    shadowTaskComparatorHash(prompt),
			EvaluatorModel:    "test-model",
			EvaluatorRaw:      `{"summary":"private evaluator prose"}`,
			Evaluation:        &output,
		},
		createdAt,
	)
	if err != nil || !added {
		t.Fatalf("persist evaluation: added=%v err=%v", added, err)
	}
	return evaluation
}

func TestWorkPatternProfileWaitsForQualifiedLearningInANewerRevision(t *testing.T) {
	store := newTestStore(t)
	episode, run := createExecutedShadowTaskFixture(t, store)
	evaluation := persistWorkPatternLearningEvaluation(
		t,
		store,
		run,
		3_000,
	)
	app := &App{activity: &activityMonitorState{store: store}}
	snapshot, added, err := app.AppendWorkPatternProfileFromEvaluation(
		episode.ID,
		run.ID,
		evaluation.ID,
		3_100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !added || snapshot.SourceCutoffAt <= episode.InferredStartAt {
		t.Fatalf("learned profile=%+v added=%v", snapshot, added)
	}
	foundCapability := false
	for _, projection := range snapshot.Projections {
		if projection.ScopeKind == WorkPatternScopeCapability &&
			projection.ScopeKey == run.CapabilityKey &&
			projection.EvidenceKind == WorkPatternEvidenceEvaluated {
			foundCapability = true
		}
	}
	if foundCapability {
		t.Fatalf("single evaluation entered capability priors: %+v", snapshot.Projections)
	}
	evidence, err := store.ListWorkPatternProfileEvidence(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundCapabilityEvidence := false
	for _, item := range evidence {
		if item.SourceKind == taskContextSourceVerifiedShadowLearningKind &&
			item.Included {
			foundCapabilityEvidence = true
		}
	}
	if foundCapabilityEvidence {
		t.Fatalf("unqualified capability provenance was admitted: %+v", evidence)
	}
	if strings.Contains(snapshot.CanonicalJSON, "private evaluator prose") {
		t.Fatal("evaluator prose leaked into work profile")
	}

	replayed, replayAdded, err := app.AppendWorkPatternProfileFromEvaluation(
		episode.ID,
		run.ID,
		evaluation.ID,
		4_000,
	)
	if err != nil || replayAdded || replayed.ID != snapshot.ID {
		t.Fatalf(
			"replay profile=%+v added=%v err=%v",
			replayed,
			replayAdded,
			err,
		)
	}
	for _, table := range []string{"authority_decisions", "initiatives"} {
		var count int
		if err := store.db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("profile learning created %d rows in %s", count, table)
		}
	}
}

func TestWorkPatternProfileOutcomeLearningRejectsFirstRun(t *testing.T) {
	store := newTestStore(t)
	episode, run := createExecutedShadowTaskFixture(t, store)
	evaluation := persistWorkPatternLearningEvaluation(
		t,
		store,
		run,
		3_000,
	)
	firstRun := seedFirstRunContamination(t, store)
	excludeFirstRunSource(
		t,
		store,
		firstRun,
		"shadow_run",
		run.ID,
	)
	app := &App{activity: &activityMonitorState{store: store}}
	if _, _, err := app.AppendWorkPatternProfileFromEvaluation(
		episode.ID,
		run.ID,
		evaluation.ID,
		3_100,
	); err == nil || !strings.Contains(err.Error(), firstRunContaminationReason) {
		t.Fatalf("first run learning error=%v", err)
	}
}
