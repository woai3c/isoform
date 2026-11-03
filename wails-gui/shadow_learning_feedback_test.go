package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// firstRunTestRun is a first-run guided run, as the removed first-run journey
// stored it.
type firstRunTestRun struct {
	ID        string
	JourneyID string
}

// seedFirstRunContamination stores a first-run journey and its guided run the
// way the removed first-run journey did.
func seedFirstRunContamination(t *testing.T, store *ActivityStore) firstRunTestRun {
	t.Helper()
	run := firstRunTestRun{ID: uuid.NewString(), JourneyID: uuid.NewString()}
	if _, err := store.db.Exec(`
		INSERT INTO first_run_journeys_v1 (id, journey_key, version, created_at)
		VALUES (?, 'local-profile:first-run-v1', 'first-run-v1', 1000)`,
		run.JourneyID,
	); err != nil {
		t.Fatalf("store first-run journey: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO first_outcome_runs_v1 (
			id, journey_id, idempotency_key, execution_mode, learning_eligible,
			capture_started_at, capture_ends_at, created_at
		) VALUES (?, ?, 'first-run-contamination', 'guided_visible', 0, 1000, 2000, 1000)`,
		run.ID, run.JourneyID,
	); err != nil {
		t.Fatalf("store first-run guided run: %v", err)
	}
	return run
}

// excludeFirstRunSource excludes one source from learning the way the removed
// first-run journey did for its guided task.
func excludeFirstRunSource(
	t *testing.T,
	store *ActivityStore,
	run firstRunTestRun,
	kind, id string,
) {
	t.Helper()
	if _, err := store.db.Exec(`
		INSERT INTO first_run_exclusions_v1 (
			id, journey_id, run_id, source_kind, source_id, reason, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 2001)`,
		uuid.NewString(), run.JourneyID, run.ID, kind, id, FirstRunExclusionGuided,
	); err != nil {
		t.Fatalf("exclude first-run source (%s,%s): %v", kind, id, err)
	}
}

// addLearningCorrection stores a correction of a learned pattern the way the
// removed Desk did.
func addLearningCorrection(t *testing.T, store *ActivityStore, item AgencyLearningCorrectionRecord) {
	t.Helper()
	payloadJSON, payloadHash, err := agencyStructPayload(map[string]string{"source": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if _, err := store.db.Exec(`
		INSERT INTO agency_learning_corrections_v1 (
			id, idempotency_key, journey_id, capability_key, learning_revision_id,
			original_statement, correction, payload_json, payload_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.IdempotencyKey, item.JourneyID,
		strings.TrimSpace(item.CapabilityKey),
		item.LearningRevisionID, item.OriginalStatement, item.Correction,
		payloadJSON, payloadHash, item.CreatedAt,
	); err != nil {
		t.Fatalf("store learning correction: %v", err)
	}
}

func TestFirstRunContaminationBlocksSessionGroupAndEpisodeExecution(t *testing.T) {
	store := newTestStore(t)
	firstRun := seedFirstRunContamination(t, store)
	excludeFirstRunSource(t, store, firstRun, "activity_session", "guided-session")
	productionEpisode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"production-open-during-guided-run",
		1_000,
		1_100,
		TaskEpisodeStatusCandidate,
	))
	if err != nil || !added {
		t.Fatalf("create production episode: added=%v err=%v", added, err)
	}
	triager := &ActivityTriager{app: &App{
		activity: &activityMonitorState{store: store},
	}}
	result := triager.runTaskShadowProductionFlow(nil, sessionGroup{
		Sessions: []ActivitySessionRow{{
			ID:        "guided-session",
			StartTime: 1_000,
			EndTime:   10_000_000,
		}},
	}, nil)
	if len(result.OnsetAttemptIDs) != 0 ||
		len(result.TaskEpisodeIDs) != 0 ||
		len(result.ExpiredTaskEpisodeIDs) != 0 ||
		len(result.ShadowRunIDs) != 0 ||
		len(result.Errors) != 0 {
		t.Fatalf("guided session entered task shadow pipeline: %+v", result)
	}
	productionEpisode, err = store.GetTaskEpisode(productionEpisode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if productionEpisode.CurrentStatus != TaskEpisodeStatusCandidate {
		t.Fatalf("guided activity expired production work: %+v", productionEpisode)
	}

	excluded, err := store.isFirstRunSessionGroupLearningExcluded(sessionGroup{
		Sessions: []ActivitySessionRow{{ID: "guided-session"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !excluded {
		t.Fatal("guided activity session was eligible for task shadow execution")
	}

	episode, added, err := store.CreateTaskEpisode(
		taskShadowDebugEpisodeInput("guided-episode", 3_000, false),
	)
	if err != nil || !added {
		t.Fatalf("CreateTaskEpisode: added=%v err=%v", added, err)
	}
	excludeFirstRunSource(t, store, firstRun, "task_episode", episode.ID)
	excluded, err = store.isTaskEpisodeFirstRunLearningExcluded(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !excluded {
		t.Fatal("guided task episode was eligible for shadow execution")
	}

	resultEvidence, added, err := store.AppendTaskEpisodeEvidence(
		TaskEpisodeEvidence{
			TaskEpisodeID: episode.ID,
			Role:          TaskEpisodeEvidenceResult,
			SourceKind:    "activity_event",
			SourceID:      "guided-result-event",
			ObservedAt:    3_100,
			CreatedAt:     3_100,
			Summary:       "A guided tutorial result.",
		},
	)
	if err != nil || !added {
		t.Fatalf("append guided result: added=%v err=%v", added, err)
	}
	t.Setenv("HOME", t.TempDir())
	sources, err := GatherTaskContextSources(TaskContextSourceRequest{
		Store:    store,
		CutoffAt: 4_000,
		Query:    "guided tutorial result",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range sources.Candidates {
		if candidate.SourceKind == taskContextSourceTaskEpisodeOutcomeKind &&
			candidate.SourceID == resultEvidence.ID {
			t.Fatalf("guided outcome entered task context: %+v", candidate)
		}
	}
	foundGuidedExclusion := false
	for _, item := range sources.Exclusions {
		if item.SourceKind == taskContextSourceTaskEpisodeOutcomeKind &&
			item.SourceID == resultEvidence.ID &&
			item.Reason == TaskContextSourceReasonGuidedFirstRun {
			foundGuidedExclusion = true
		}
	}
	if !foundGuidedExclusion {
		t.Fatalf("guided outcome exclusion missing: %+v", sources.Exclusions)
	}
}

func TestFirstRunContaminationExcludesEvaluationFromCapabilityLearning(t *testing.T) {
	graph := seedTaskShadowDebugGraph(t)
	firstRun := seedFirstRunContamination(t, graph.Store)
	excludeFirstRunSource(t, graph.Store, firstRun, "shadow_run_evaluation", graph.Evaluation.ID)

	excluded, err := graph.Store.isShadowTaskLearningEvaluationFirstRunExcluded(
		graph.Evaluation.ID,
		graph.Run.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !excluded {
		t.Fatal("guided shadow run was eligible for evaluation")
	}
	records, err := graph.Store.ListShadowTaskLearningEvaluationRecords(graph.Run.CapabilityKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("guided evaluation entered capability aggregation: %+v", records)
	}
}

func TestFirstRunContaminationBlocksLearningMaterialization(t *testing.T) {
	graph := seedTaskShadowDebugGraph(t)
	firstRun := seedFirstRunContamination(t, graph.Store)
	excludeFirstRunSource(t, graph.Store, firstRun, "shadow_run_evaluation", graph.Evaluation.ID)

	targets := []string{
		ShadowTaskLearningTargetMemory,
		ShadowTaskLearningTargetKnowledge,
		ShadowTaskLearningTargetTypedSelf,
	}
	for index, target := range targets {
		candidate := ShadowTaskLearningCandidate{
			ProjectionKey:   "guided:first-run:" + target,
			RevisionID:      "guided-first-run-revision:" + target,
			RevisionMode:    ShadowTaskLearningRevisionAppendOnly,
			PolicyVersion:   ShadowTaskLearningPolicyVersion,
			TargetKind:      target,
			LearningKind:    ShadowTaskLearningKindProcessPattern,
			CapabilityKey:   graph.Run.CapabilityKey,
			Statement:       "This must never reach Memory, Knowledge, or Self.",
			EvidenceHash:    "guided-first-run-evidence:" + target,
			AuthorityEffect: ShadowTaskLearningAuthorityNone,
			Qualification: ShadowTaskLearningQualification{
				Decision: ShadowTaskLearningDecisionQualified,
			},
			Evidence: []ShadowTaskLearningProvenance{{
				EvaluationID:  graph.Evaluation.ID,
				TaskEpisodeID: graph.Episode.ID,
				ShadowRunID:   graph.Run.ID,
			}},
		}
		if _, _, err := PersistShadowTaskLearningProjection(
			graph.Store,
			ShadowTaskLearningProjection{
				PolicyVersion:   ShadowTaskLearningPolicyVersion,
				AuthorityEffect: ShadowTaskLearningAuthorityNone,
				EvaluationCount: 1,
				Candidates:      []ShadowTaskLearningCandidate{candidate},
			},
			2_000+int64(index),
		); err != nil {
			t.Fatal(err)
		}
		graph.App.materializeShadowTaskLearningCandidate(
			candidate,
			3_000+int64(index),
		)
	}
	items, err := graph.Store.ListShadowTaskLearningMaterializations(10)
	if err != nil {
		t.Fatal(err)
	}
	denied := make(map[string]ShadowTaskLearningMaterialization)
	for _, item := range items {
		if item.Status == "denied" && item.Error == firstRunContaminationReason {
			denied[item.TargetKind] = item
		}
	}
	for _, target := range targets {
		if item, ok := denied[target]; !ok || item.TargetID != "" {
			t.Fatalf("guided %s materialization=%+v", target, item)
		}
	}
}
