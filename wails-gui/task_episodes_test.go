package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskEpisodeEmptyReasoningPreservesHistoricalPayloadShape(t *testing.T) {
	input := taskEpisodeFixture(
		"episode-historical-shape",
		1_000,
		2_000,
		TaskEpisodeStatusCandidate,
	)
	payload, err := json.Marshal(input.InitialHypothesis)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "reasoningHypotheses") {
		t.Fatalf("empty reasoning changed historical payload shape: %s", payload)
	}
}

func taskEpisodeFixture(
	id string,
	inferredStartAt, detectedAt int64,
	status string,
) CreateTaskEpisodeInput {
	return CreateTaskEpisodeInput{
		ID:              id,
		InferredStartAt: inferredStartAt,
		DetectedAt:      detectedAt,
		InitialStatus:   status,
		FrozenContext: TaskEpisodeFrozenContext{
			CutoffAt:      inferredStartAt,
			CanonicalJSON: `{"project":"Yansu","apps":["Editor","Browser"]}`,
		},
		InitialEvidence: []TaskEpisodeEvidence{{
			Role:       TaskEpisodeEvidenceOnset,
			SourceKind: "activity_session",
			SourceID:   id + "-source",
			Summary:    "The user opened the project and began editing.",
			ObservedAt: inferredStartAt,
		}},
		InitialHypothesis: TaskEpisodeHypothesisRevision{
			TaskHypothesis:          "Implement the task episode persistence layer",
			DesiredResultHypothesis: "A tested append-only persistence layer",
			TaskConfidence:          0.82,
			DesiredResultConfidence: 0.76,
			Alternatives: []TaskEpisodeHypothesisAlternative{{
				Kind:       "task",
				Statement:  "Review the persistence design without editing",
				Confidence: 0.24,
			}},
		},
	}
}

func TestTaskEpisodeCreateReadListAllowsOverlap(t *testing.T) {
	store := newTestStore(t)
	firstInput := taskEpisodeFixture(
		"episode-overlap-first",
		1_000,
		2_000,
		TaskEpisodeStatusCandidate,
	)
	secondInput := taskEpisodeFixture(
		"episode-overlap-second",
		1_500,
		2_100,
		TaskEpisodeStatusActive,
	)

	first, added, err := store.CreateTaskEpisode(firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if !added || first.CurrentStatus != TaskEpisodeStatusCandidate {
		t.Fatalf("first=%+v added=%v", first, added)
	}
	second, added, err := store.CreateTaskEpisode(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if !added || second.CurrentStatus != TaskEpisodeStatusActive {
		t.Fatalf("second=%+v added=%v", second, added)
	}

	items, err := store.ListTaskEpisodes("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("episodes=%+v, want two overlapping episodes", items)
	}
	if items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("episode order=%v, want deterministic inferred-start order", []string{
			items[0].ID,
			items[1].ID,
		})
	}
	active, err := store.ListTaskEpisodes(TaskEpisodeStatusActive, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != second.ID {
		t.Fatalf("active episodes=%+v", active)
	}
	got, err := store.GetTaskEpisode(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InferredStartAt != 1_000 ||
		got.DetectedAt != 2_000 ||
		got.FrozenContext.CutoffAt != 1_000 ||
		got.LatestHypothesis.Revision != 1 {
		t.Fatalf("stored episode=%+v", got)
	}
	if normalizeTaskEpisodeListLimit(0) != taskEpisodeDefaultListLimit ||
		normalizeTaskEpisodeListLimit(taskEpisodeMaxListLimit+1) != taskEpisodeMaxListLimit {
		t.Fatal("task episode list limits are not bounded")
	}
}

func TestTaskEpisodeSupportsEveryLifecycleStatus(t *testing.T) {
	store := newTestStore(t)
	statuses := []string{
		TaskEpisodeStatusCandidate,
		TaskEpisodeStatusActive,
		TaskEpisodeStatusPaused,
		TaskEpisodeStatusCompleted,
		TaskEpisodeStatusAbandoned,
		TaskEpisodeStatusMerged,
		TaskEpisodeStatusSplit,
		TaskEpisodeStatusExpired,
	}
	for i, status := range statuses {
		start := int64(1_000 + i*100)
		episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
			"episode-status-"+status,
			start,
			start+50,
			status,
		))
		if err != nil {
			t.Fatalf("create %s episode: %v", status, err)
		}
		if !added || episode.CurrentStatus != status {
			t.Fatalf("%s episode=%+v added=%v", status, episode, added)
		}
		filtered, err := store.ListTaskEpisodes(status, 10)
		if err != nil {
			t.Fatalf("list %s episodes: %v", status, err)
		}
		if len(filtered) != 1 || filtered[0].ID != episode.ID {
			t.Fatalf("%s episodes=%+v", status, filtered)
		}
	}
}

func TestTaskEpisodeLateDetectionCanonicalContextIsImmutableAndIdempotent(t *testing.T) {
	store := newTestStore(t)
	input := taskEpisodeFixture(
		"episode-late-detection",
		1_000,
		5_000,
		TaskEpisodeStatusCandidate,
	)
	input.FrozenContext.CanonicalJSON = `{
		"project": "Yansu",
		"apps": ["Editor", "Browser"],
		"state": {"branch": "vision/shadow-agent", "dirty": false}
	}`

	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("late-detected episode was not created")
	}
	wantCanonical := `{"apps":["Editor","Browser"],"project":"Yansu","state":{"branch":"vision/shadow-agent","dirty":false}}`
	if episode.FrozenContext.CanonicalJSON != wantCanonical {
		t.Fatalf("canonical context=%q, want %q", episode.FrozenContext.CanonicalJSON, wantCanonical)
	}
	sum := sha256.Sum256([]byte(wantCanonical))
	wantHash := hex.EncodeToString(sum[:])
	if episode.FrozenContext.SHA256 != wantHash {
		t.Fatalf("context hash=%q, want %q", episode.FrozenContext.SHA256, wantHash)
	}
	if episode.FrozenContext.CutoffAt != input.InferredStartAt ||
		episode.FrozenContext.CutoffAt >= input.DetectedAt {
		t.Fatalf("late detection did not retain backdated cutoff: %+v", episode.FrozenContext)
	}

	retry := input
	retry.FrozenContext.CanonicalJSON = `{"state":{"dirty":false,"branch":"vision/shadow-agent"},"apps":["Editor","Browser"],"project":"Yansu"}`
	replayed, replayAdded, err := store.CreateTaskEpisode(retry)
	if err != nil {
		t.Fatal(err)
	}
	if replayAdded || replayed.ID != episode.ID || replayed.FrozenContext.SHA256 != wantHash {
		t.Fatalf("replayed=%+v added=%v", replayed, replayAdded)
	}

	conflict := input
	conflict.FrozenContext.CanonicalJSON = `{"project":"A different project"}`
	if _, _, err := store.CreateTaskEpisode(conflict); err == nil ||
		!strings.Contains(err.Error(), "different immutable creation payload") {
		t.Fatalf("conflicting retry error=%v", err)
	}
	badCutoff := taskEpisodeFixture(
		"episode-bad-cutoff",
		1_000,
		5_000,
		TaskEpisodeStatusCandidate,
	)
	badCutoff.FrozenContext.CutoffAt = 4_000
	if _, _, err := store.CreateTaskEpisode(badCutoff); err == nil ||
		!strings.Contains(err.Error(), "cutoff") {
		t.Fatalf("bad cutoff error=%v", err)
	}
	badHash := taskEpisodeFixture(
		"episode-bad-hash",
		1_000,
		5_000,
		TaskEpisodeStatusCandidate,
	)
	badHash.FrozenContext.SHA256 = strings.Repeat("0", 64)
	if _, _, err := store.CreateTaskEpisode(badHash); err == nil ||
		!strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("bad hash error=%v", err)
	}

	if _, err := store.db.Exec(`
		UPDATE task_episode_context_snapshots
		SET canonical_json = '{}'
		WHERE task_episode_id = ?`,
		episode.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("frozen context update error=%v", err)
	}
	if _, err := store.db.Exec(`
		DELETE FROM task_episode_context_snapshots
		WHERE task_episode_id = ?`,
		episode.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("frozen context delete error=%v", err)
	}
}

func TestTaskEpisodeEvidenceAndHypothesisRevisionsAreAppendOnly(t *testing.T) {
	store := newTestStore(t)
	episode, _, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"episode-revisions",
		1_000,
		2_000,
		TaskEpisodeStatusActive,
	))
	if err != nil {
		t.Fatal(err)
	}
	resultInput := TaskEpisodeEvidence{
		ID:            "episode-result-evidence",
		TaskEpisodeID: episode.ID,
		Role:          TaskEpisodeEvidenceResult,
		SourceKind:    "file",
		SourceID:      "task_episodes.go",
		Summary:       "The persistence implementation compiled.",
		PayloadJSON:   `{"exitCode":0,"command":"go test . -run ^$"}`,
		ObservedAt:    3_000,
		CreatedAt:     3_100,
	}
	result, added, err := store.AppendTaskEpisodeEvidence(resultInput)
	if err != nil {
		t.Fatal(err)
	}
	if !added || result.Role != TaskEpisodeEvidenceResult || result.ContentHash == "" {
		t.Fatalf("result=%+v added=%v", result, added)
	}
	replayedResult, replayAdded, err := store.AppendTaskEpisodeEvidence(resultInput)
	if err != nil {
		t.Fatal(err)
	}
	if replayAdded || replayedResult.ID != result.ID {
		t.Fatalf("replayed result=%+v added=%v", replayedResult, replayAdded)
	}
	conflictingResult := resultInput
	conflictingResult.Summary = "The persistence implementation failed."
	if _, _, err := store.AppendTaskEpisodeEvidence(conflictingResult); err == nil ||
		!strings.Contains(err.Error(), "different immutable payload") {
		t.Fatalf("conflicting result retry error=%v", err)
	}

	revisionInput := TaskEpisodeHypothesisRevision{
		ID:                      "episode-hypothesis-revision-2",
		TaskEpisodeID:           episode.ID,
		TaskHypothesis:          "Implement and verify immutable Task Episode persistence",
		DesiredResultHypothesis: "All focused persistence tests pass",
		TaskConfidence:          0.94,
		DesiredResultConfidence: 0.91,
		EvidenceIDs: append(
			[]string{result.ID},
			episode.LatestHypothesis.EvidenceIDs...,
		),
		Alternatives: []TaskEpisodeHypothesisAlternative{{
			Kind:        "desired_result",
			Statement:   "Only package compilation is required",
			Confidence:  0.13,
			EvidenceIDs: []string{result.ID},
		}},
		ReasoningHypotheses: []TaskEpisodeReasoningHypothesis{{
			Statement:   "Passing tests may be the user's immediate criterion.",
			Confidence:  0.49,
			Basis:       TaskReasoningBasisContextualInference,
			EvidenceIDs: []string{result.ID},
		}},
		CreatedAt: 3_200,
	}
	revision, added, err := store.AppendTaskEpisodeHypothesis(revisionInput)
	if err != nil {
		t.Fatal(err)
	}
	if !added || revision.Revision != 2 {
		t.Fatalf("revision=%+v added=%v", revision, added)
	}
	replayedRevision, replayAdded, err := store.AppendTaskEpisodeHypothesis(revisionInput)
	if err != nil {
		t.Fatal(err)
	}
	if replayAdded || replayedRevision.ID != revision.ID || replayedRevision.Revision != 2 {
		t.Fatalf("replayed revision=%+v added=%v", replayedRevision, replayAdded)
	}
	conflictingRevision := revisionInput
	conflictingRevision.DesiredResultHypothesis = "A conflicting result"
	if _, _, err := store.AppendTaskEpisodeHypothesis(conflictingRevision); err == nil ||
		!strings.Contains(err.Error(), "different immutable payload") {
		t.Fatalf("conflicting hypothesis retry error=%v", err)
	}
	skippedRevision := revisionInput
	skippedRevision.ID = "episode-hypothesis-revision-4"
	skippedRevision.Revision = 4
	skippedRevision.TaskHypothesis = "Skip a hypothesis revision"
	if _, _, err := store.AppendTaskEpisodeHypothesis(skippedRevision); err == nil ||
		!strings.Contains(err.Error(), "must be 3") {
		t.Fatalf("skipped revision error=%v", err)
	}

	hypotheses, err := store.ListTaskEpisodeHypotheses(episode.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hypotheses) != 2 ||
		hypotheses[0].Revision != 1 ||
		hypotheses[1].Revision != 2 {
		t.Fatalf("hypotheses=%+v", hypotheses)
	}
	results, err := store.ListTaskEpisodeEvidence(
		episode.ID,
		TaskEpisodeEvidenceResult,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != result.ID {
		t.Fatalf("result evidence=%+v", results)
	}
	current, err := store.GetTaskEpisode(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.LatestHypothesis.ID != revision.ID ||
		current.LatestHypothesis.Revision != 2 ||
		len(current.LatestHypothesis.ReasoningHypotheses) != 1 {
		t.Fatalf("current hypothesis=%+v", current.LatestHypothesis)
	}
	if _, err := store.db.Exec(`
		UPDATE task_episode_hypotheses
		SET task_confidence = 0
		WHERE id = ?`,
		revision.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("hypothesis update error=%v", err)
	}
}

func TestTaskEpisodeTransitionsAndRelationshipLinks(t *testing.T) {
	store := newTestStore(t)
	mainEpisode, _, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"episode-lifecycle",
		1_000,
		2_000,
		TaskEpisodeStatusCandidate,
	))
	if err != nil {
		t.Fatal(err)
	}
	activeInput := TaskEpisodeTransition{
		ID:            "episode-transition-active",
		TaskEpisodeID: mainEpisode.ID,
		FromStatus:    TaskEpisodeStatusCandidate,
		ToStatus:      TaskEpisodeStatusActive,
		Reason:        "Sustained work confirmed the task.",
		OccurredAt:    2_100,
		CreatedAt:     2_110,
	}
	active, added, err := store.AppendTaskEpisodeTransition(activeInput)
	if err != nil {
		t.Fatal(err)
	}
	if !added || active.Sequence != 2 {
		t.Fatalf("active transition=%+v added=%v", active, added)
	}
	replayed, replayAdded, err := store.AppendTaskEpisodeTransition(activeInput)
	if err != nil {
		t.Fatal(err)
	}
	if replayAdded || replayed.ID != active.ID {
		t.Fatalf("replayed transition=%+v added=%v", replayed, replayAdded)
	}
	conflict := activeInput
	conflict.Reason = "Conflicting retry payload."
	if _, _, err := store.AppendTaskEpisodeTransition(conflict); err == nil ||
		!strings.Contains(err.Error(), "different immutable payload") {
		t.Fatalf("conflicting transition retry error=%v", err)
	}
	if _, _, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: mainEpisode.ID,
		FromStatus:    TaskEpisodeStatusCandidate,
		ToStatus:      TaskEpisodeStatusAbandoned,
		OccurredAt:    2_200,
	}); err == nil || !strings.Contains(err.Error(), "expected current status") {
		t.Fatalf("stale transition error=%v", err)
	}

	for _, transition := range []TaskEpisodeTransition{
		{
			TaskEpisodeID: mainEpisode.ID,
			FromStatus:    TaskEpisodeStatusActive,
			ToStatus:      TaskEpisodeStatusPaused,
			Reason:        "The user switched tasks.",
			OccurredAt:    2_300,
		},
		{
			TaskEpisodeID: mainEpisode.ID,
			FromStatus:    TaskEpisodeStatusPaused,
			ToStatus:      TaskEpisodeStatusActive,
			Reason:        "The user resumed the task.",
			OccurredAt:    2_500,
		},
		{
			TaskEpisodeID: mainEpisode.ID,
			FromStatus:    TaskEpisodeStatusActive,
			ToStatus:      TaskEpisodeStatusCompleted,
			Reason:        "The result was observed.",
			OccurredAt:    3_000,
		},
	} {
		if _, added, err := store.AppendTaskEpisodeTransition(transition); err != nil || !added {
			t.Fatalf("append transition=%+v added=%v err=%v", transition, added, err)
		}
	}
	if _, _, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: mainEpisode.ID,
		FromStatus:    TaskEpisodeStatusCompleted,
		ToStatus:      TaskEpisodeStatusActive,
		OccurredAt:    3_100,
	}); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("terminal transition error=%v", err)
	}
	transitions, err := store.ListTaskEpisodeTransitions(mainEpisode.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantStatuses := []string{
		TaskEpisodeStatusCandidate,
		TaskEpisodeStatusActive,
		TaskEpisodeStatusPaused,
		TaskEpisodeStatusActive,
		TaskEpisodeStatusCompleted,
	}
	if len(transitions) != len(wantStatuses) {
		t.Fatalf("transitions=%+v", transitions)
	}
	for i, want := range wantStatuses {
		if transitions[i].Sequence != i+1 || transitions[i].ToStatus != want {
			t.Fatalf("transition[%d]=%+v, want status %q", i, transitions[i], want)
		}
	}
	if _, err := store.db.Exec(`
		UPDATE task_episode_transitions
		SET to_status = 'abandoned'
		WHERE id = ?`,
		active.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("transition update error=%v", err)
	}

	parent, _, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"episode-parent",
		500,
		600,
		TaskEpisodeStatusActive,
	))
	if err != nil {
		t.Fatal(err)
	}
	mergeTarget, _, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"episode-merge-target",
		1_100,
		2_050,
		TaskEpisodeStatusActive,
	))
	if err != nil {
		t.Fatal(err)
	}
	splitChild, _, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"episode-split-child",
		2_400,
		2_450,
		TaskEpisodeStatusCandidate,
	))
	if err != nil {
		t.Fatal(err)
	}
	links := []TaskEpisodeLink{
		{
			SourceEpisodeID: mainEpisode.ID,
			TargetEpisodeID: parent.ID,
			Relation:        TaskEpisodeLinkParent,
			CreatedAt:       3_100,
		},
		{
			SourceEpisodeID: mainEpisode.ID,
			TargetEpisodeID: mergeTarget.ID,
			Relation:        TaskEpisodeLinkMergedInto,
			CreatedAt:       3_200,
		},
		{
			SourceEpisodeID: mainEpisode.ID,
			TargetEpisodeID: splitChild.ID,
			Relation:        TaskEpisodeLinkSplitInto,
			CreatedAt:       3_300,
		},
	}
	for _, link := range links {
		persisted, added, err := store.AppendTaskEpisodeLink(link)
		if err != nil {
			t.Fatal(err)
		}
		if !added {
			t.Fatalf("link was not added: %+v", persisted)
		}
		replayed, replayAdded, err := store.AppendTaskEpisodeLink(link)
		if err != nil || replayAdded || replayed.ID != persisted.ID {
			t.Fatalf("link replay=%+v added=%v err=%v", replayed, replayAdded, err)
		}
	}
	persistedLinks, err := store.ListTaskEpisodeLinks(mainEpisode.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedLinks) != 3 ||
		persistedLinks[0].Relation != TaskEpisodeLinkParent ||
		persistedLinks[1].Relation != TaskEpisodeLinkMergedInto ||
		persistedLinks[2].Relation != TaskEpisodeLinkSplitInto {
		t.Fatalf("links=%+v", persistedLinks)
	}
}

func TestTaskEpisodePersistenceHasNoInitiativeOrMemorySideEffects(t *testing.T) {
	store := newTestStore(t)
	tables := []string{
		"initiatives",
		"prediction_proposals",
		"self_model_entries",
		"activity_summaries",
	}
	before := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s before: %v", table, err)
		}
		before[table] = count
	}

	episode, _, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"episode-isolated",
		1_000,
		2_000,
		TaskEpisodeStatusActive,
	))
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: episode.ID,
		Role:          TaskEpisodeEvidenceResult,
		SourceKind:    "test",
		SourceID:      "isolated-result",
		Summary:       "A result was observed without promotion.",
		ObservedAt:    3_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendTaskEpisodeHypothesis(TaskEpisodeHypothesisRevision{
		TaskEpisodeID:           episode.ID,
		TaskHypothesis:          "The user completed an isolated test task",
		DesiredResultHypothesis: "The result remains episode evidence only",
		TaskConfidence:          0.9,
		DesiredResultConfidence: 0.9,
		EvidenceIDs:             []string{result.ID},
	}); err != nil {
		t.Fatal(err)
	}

	for _, table := range tables {
		var after int
		if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&after); err != nil {
			t.Fatalf("count %s after: %v", table, err)
		}
		if after != before[table] {
			t.Fatalf("%s row count changed from %d to %d", table, before[table], after)
		}
	}
}

func TestTaskEpisodeMigrationIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 2; i++ {
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateTaskEpisodes(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration run %d: %v", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var tableCount int
	if err := store.db.QueryRow(`
		SELECT count(*)
		FROM sqlite_master
		WHERE type = 'table' AND name LIKE 'task_episode%'`,
	).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	// Six task-episode tables, plus task_episode_read_revision: the revision
	// counter that the triggers on the rows of the repeated-onset set maintain.
	if tableCount != 7 {
		t.Fatalf("task episode tables=%d, want 7 (six task-episode tables and task_episode_read_revision)", tableCount)
	}
}
