package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func createHistoricalOnsetEpisodeFixture(
	t *testing.T,
	store *ActivityStore,
	episodeID string,
	anchor taskEpisodeOnsetAnchor,
	detectedAt int64,
) TaskEpisode {
	t.Helper()
	onset := TaskOnsetEvidence{
		Ref:        anchor.Ref,
		Kind:       "keyboard",
		SourceKind: anchor.SourceKind,
		SourceID:   anchor.SourceID,
		OccurredAt: anchor.ObservedAt,
		Summary:    "The user began composing a work update.",
	}
	payload, err := json.Marshal(onset)
	if err != nil {
		t.Fatal(err)
	}
	input := taskEpisodeFixture(
		episodeID,
		anchor.ObservedAt,
		detectedAt,
		TaskEpisodeStatusActive,
	)
	input.InitialHypothesis.TaskHypothesis = "Compose a customer update"
	input.InitialHypothesis.DesiredResultHypothesis =
		"A customer update is ready"
	input.InitialEvidence = []TaskEpisodeEvidence{{
		ID:          episodeID + "-onset",
		Role:        TaskEpisodeEvidenceOnset,
		SourceKind:  anchor.SourceKind,
		SourceID:    anchor.SourceID,
		Summary:     onset.Summary,
		ObservedAt:  anchor.ObservedAt,
		PayloadJSON: string(payload),
	}}
	input.InitialHypothesis.EvidenceIDs = []string{
		input.InitialEvidence[0].ID,
	}
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create historical onset episode: added=%v err=%v", added, err)
	}
	return episode
}

func TestTaskEpisodeOnsetIdentityQuarantinesExactHistoricalForks(t *testing.T) {
	store := newTestStore(t)
	anchor := taskEpisodeOnsetAnchor{
		Ref:        "event:keyboard:42",
		SourceKind: "activity_event",
		SourceID:   "keyboard-42",
		ObservedAt: 1_000,
	}
	first := createHistoricalOnsetEpisodeFixture(
		t,
		store,
		"historical-onset-first",
		anchor,
		1_100,
	)
	second := createHistoricalOnsetEpisodeFixture(
		t,
		store,
		"historical-onset-second",
		anchor,
		1_200,
	)

	index, err := store.taskEpisodeOnsetIdentityIndex(anchor.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	repeated := index.repeatedEpisodeIDs()
	if len(repeated) != 2 {
		t.Fatalf("repeated episodes=%v, want both immutable rows", repeated)
	}
	if index.observationKey(first.ID) != index.observationKey(second.ID) {
		t.Fatal("exact historical forks did not share one observation identity")
	}
	for _, episodeID := range []string{first.ID, second.ID} {
		if err := store.taskEpisodeRepeatedOnsetError(episodeID); !errors.Is(err, ErrTaskEpisodeRepeatedOnset) {
			t.Fatalf("episode %q repeated error=%v", episodeID, err)
		}
	}
	existing, found, err := store.FindTaskEpisodeByOnsetAnchor(
		anchor,
		anchor.ObservedAt,
	)
	if err != nil || !found || existing.ID != first.ID {
		t.Fatalf("canonical historical onset=%+v found=%v err=%v", existing, found, err)
	}
	if _, _, err := store.CreateShadowTaskRunFromEpisode(
		second,
		"test-model",
		1_300,
	); !errors.Is(err, ErrTaskEpisodeRepeatedOnset) {
		t.Fatalf("repeated onset created a run: %v", err)
	}
}

func TestTaskEpisodeOnsetIdentityUsesOnlyRevisionOnePrimaryEvidence(
	t *testing.T,
) {
	store := newTestStore(t)
	primary := taskEpisodeOnsetAnchor{
		Ref:        "event:primary",
		SourceKind: "activity_event",
		SourceID:   "primary-source",
		ObservedAt: 2_000,
	}
	alternative := taskEpisodeOnsetAnchor{
		Ref:        "event:alternative",
		SourceKind: "activity_event",
		SourceID:   "alternative-source",
		ObservedAt: 2_000,
	}
	primaryPayload, _ := json.Marshal(TaskOnsetEvidence{
		Ref: primary.Ref, Kind: "keyboard", SourceKind: primary.SourceKind,
		SourceID: primary.SourceID, OccurredAt: primary.ObservedAt,
		Summary: "Primary onset",
	})
	alternativePayload, _ := json.Marshal(TaskOnsetEvidence{
		Ref: alternative.Ref, Kind: "focus", SourceKind: alternative.SourceKind,
		SourceID: alternative.SourceID, OccurredAt: alternative.ObservedAt,
		Summary: "Alternative onset",
	})
	input := taskEpisodeFixture(
		"primary-versus-alternative",
		2_000,
		2_100,
		TaskEpisodeStatusActive,
	)
	input.InitialEvidence = []TaskEpisodeEvidence{
		{
			ID: "primary-evidence", Role: TaskEpisodeEvidenceOnset,
			SourceKind: primary.SourceKind, SourceID: primary.SourceID,
			Summary: "Primary onset", ObservedAt: 2_000,
			PayloadJSON: string(primaryPayload),
		},
		{
			ID: "alternative-evidence", Role: TaskEpisodeEvidenceOnset,
			SourceKind: alternative.SourceKind, SourceID: alternative.SourceID,
			Summary: "Alternative onset", ObservedAt: 2_000,
			PayloadJSON: string(alternativePayload),
		},
	}
	input.InitialHypothesis.EvidenceIDs = []string{"primary-evidence"}
	input.InitialHypothesis.Alternatives = []TaskEpisodeHypothesisAlternative{{
		Kind:        "task",
		Statement:   "The user may be reviewing instead of drafting.",
		Confidence:  0.25,
		EvidenceIDs: []string{"alternative-evidence"},
	}}
	if _, added, err := store.CreateTaskEpisode(input); err != nil || !added {
		t.Fatalf("create mixed-evidence episode: added=%v err=%v", added, err)
	}
	if _, found, err := store.FindTaskEpisodeByOnsetAnchor(
		alternative,
		alternative.ObservedAt,
	); err != nil || found {
		t.Fatalf("alternative-only anchor aliased primary episode: found=%v err=%v", found, err)
	}

	second := createHistoricalOnsetEpisodeFixture(
		t,
		store,
		"alternative-primary-episode",
		alternative,
		2_200,
	)
	index, err := store.taskEpisodeOnsetIdentityIndex(2_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.repeatedEpisodeIDs()) != 0 {
		t.Fatalf("alternative evidence created a false cluster: %+v", index.byKey)
	}
	if got, found, err := store.FindTaskEpisodeByOnsetAnchor(
		alternative,
		alternative.ObservedAt,
	); err != nil || !found || got.ID != second.ID {
		t.Fatalf("primary alternative anchor lookup=%+v found=%v err=%v", got, found, err)
	}
}

func TestTaskEpisodeOnsetIdentityKeepsDistinctRefsAndMalformedLegacyRowsIndependent(
	t *testing.T,
) {
	store := newTestStore(t)
	left := taskEpisodeOnsetAnchor{
		Ref: "event:left", SourceKind: "activity_event",
		SourceID: "same-source", ObservedAt: 3_000,
	}
	right := left
	right.Ref = "event:right"
	createHistoricalOnsetEpisodeFixture(
		t, store, "distinct-ref-left", left, 3_100,
	)
	createHistoricalOnsetEpisodeFixture(
		t, store, "distinct-ref-right", right, 3_200,
	)
	index, err := store.taskEpisodeOnsetIdentityIndex(3_000)
	if err != nil || len(index.repeatedEpisodeIDs()) != 0 {
		t.Fatalf("distinct refs clustered: index=%+v err=%v", index, err)
	}

	legacy := taskEpisodeFixture(
		"malformed-legacy-onset",
		3_000,
		3_300,
		TaskEpisodeStatusActive,
	)
	legacy.InitialEvidence = []TaskEpisodeEvidence{{
		ID: "malformed-legacy-evidence", Role: TaskEpisodeEvidenceOnset,
		SourceKind: "activity_event", SourceID: "legacy-source",
		Summary:    "Legacy evidence without a source-bound payload",
		ObservedAt: 3_000, PayloadJSON: `{"legacy":true}`,
	}}
	legacy.InitialHypothesis.EvidenceIDs = []string{"malformed-legacy-evidence"}
	if _, added, err := store.CreateTaskEpisode(legacy); err != nil || !added {
		t.Fatalf("create malformed legacy episode: added=%v err=%v", added, err)
	}
	index, err = store.taskEpisodeOnsetIdentityIndex(3_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, indexed := index.byEpisode[legacy.ID]; indexed {
		t.Fatal("malformed legacy primary evidence became an onset identity")
	}
}

func TestUsableSelfExcludesPersistedLearningFromRepeatedOnset(t *testing.T) {
	store := newTestStore(t)
	anchor := taskEpisodeOnsetAnchor{
		Ref: "event:self", SourceKind: "activity_event",
		SourceID: "self-source", ObservedAt: 4_000,
	}
	first := createHistoricalOnsetEpisodeFixture(
		t, store, "self-repeated-first", anchor, 4_100,
	)
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		first,
		"test-model",
		4_200,
	)
	if err != nil || !added {
		t.Fatalf("create pre-fork run: added=%v err=%v", added, err)
	}
	observation, added, err := store.AddShadowRunObservation(
		ShadowRunObservation{
			ShadowRunID: run.ID, SourceKind: "activity",
			SourceID: "self-result", ObservedAction: "Drafted an update",
			ObservedResult: "Saved the update", ObservedAt: 4_300,
			CreatedAt: 4_301,
		},
	)
	if err != nil || !added {
		t.Fatalf("add observed result: added=%v err=%v", added, err)
	}
	matched := ShadowEvaluationDimension{
		Assessment: "matched", Score: shadowScore(1), Confidence: 0.9,
	}
	evaluation, added, err := store.AddShadowRunEvaluation(
		ShadowRunEvaluation{
			ShadowRunID: run.ID, ObservationID: observation.ID,
			IdempotencyKey:    "self-repeated-evaluation",
			ComparisonVersion: shadowCorrelationPromptVersion,
			EvaluatorModel:    "test-model", Action: matched, Outcome: matched,
			Timing: matched,
			Reasoning: ShadowEvaluationDimension{
				Assessment: "unknown", Confidence: 0.2,
			},
			Safety: ShadowEvaluationDimension{
				Assessment: "safe", Score: shadowScore(1), Confidence: 0.9,
			},
			CreatedAt: 4_400,
		},
	)
	if err != nil || !added {
		t.Fatalf("add evaluation: added=%v err=%v", added, err)
	}
	const statement = "The user drafts concise progress updates."
	if _, added, err := store.AddSelfModelEntry(SelfModelEntry{
		Kind: selfModelKindPreference, Scope: TaskWorkflowComposeText,
		Statement: statement, Status: selfModelStatusProvisional,
		Confidence:      0.8,
		ReasoningStatus: "source_backed_shadow_task_evaluation",
		Evidence: []SelfModelEvidence{{
			SourceKind: "shadow_task_evaluation", SourceID: evaluation.ID,
			Statement: evaluation.Summary, Confidence: 0.8,
			OccurredAt: evaluation.CreatedAt,
		}},
		CreatedAt: 4_500,
	}); err != nil || !added {
		t.Fatalf("add historical typed Self: added=%v err=%v", added, err)
	}
	createHistoricalOnsetEpisodeFixture(
		t, store, "self-repeated-second", anchor, 4_600,
	)

	raw, err := store.ListCurrentSelfModelEntries()
	if err != nil || len(raw) != 1 {
		t.Fatalf("raw Self history changed: entries=%+v err=%v", raw, err)
	}
	usable, err := store.listUsableSelfModelEntries()
	if err != nil || len(usable) != 0 {
		t.Fatalf("repeated learning entered usable Self: entries=%+v err=%v", usable, err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	if context := app.selfModelContext("progress updates"); strings.Contains(context, statement) {
		t.Fatalf("repeated learning entered Self context: %s", context)
	}
}
