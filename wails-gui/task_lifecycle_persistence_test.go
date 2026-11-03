package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func createLifecycleEpisode(
	t *testing.T,
	store *ActivityStore,
	id, status string,
	start int64,
) TaskEpisode {
	t.Helper()
	input := taskEpisodeFixture(id, start, start+10, status)
	input.InitialHypothesis.BoundaryConfidence = 0.7
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create episode %s: added=%v err=%v", id, added, err)
	}
	return episode
}

func TestPersistTaskLifecycleCompletionAndLateResult(t *testing.T) {
	store := newTestStore(t)
	episode := createLifecycleEpisode(t, store, "lifecycle-complete", TaskEpisodeStatusActive, 1_000)
	input := TaskLifecycleInput{
		SourceKind: "activity", SourceID: "window",
		PrefixCutoffAt: 2_000, CutoffAt: 4_000,
		Episodes: []TaskLifecycleEpisodeSnapshot{{
			EpisodeID: episode.ID, CurrentStatus: episode.CurrentStatus,
			InferredStartedAt:  episode.InferredStartAt,
			Task:               episode.LatestHypothesis.TaskHypothesis,
			DesiredResult:      episode.LatestHypothesis.DesiredResultHypothesis,
			TaskConfidence:     episode.LatestHypothesis.TaskConfidence,
			BoundaryConfidence: episode.LatestHypothesis.BoundaryConfidence,
			EvidenceRefs:       []string{"prefix"},
		}},
		PrefixEvidence: []TaskLifecycleEvidence{{
			Ref: "prefix", Kind: "onset", SourceKind: "event", SourceID: "onset",
			OccurredAt: 1_000, Summary: "Task began",
		}},
		SuffixEvidence: []TaskLifecycleEvidence{{
			Ref: "result", Kind: "submission", SourceKind: "event", SourceID: "submit",
			OccurredAt: 3_000, Summary: "The result was submitted",
		}},
	}
	response := TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
		LocalKey: "completed", Kind: TaskLifecycleKindCompleted,
		EpisodeIDs: []string{episode.ID}, Confidence: 0.9,
		BoundaryConfidence: 0.8, ResultSummary: "Submitted result",
		ResultConfidence: 0.9, EvidenceRefs: []string{"result"},
	}}}
	results, err := PersistTaskLifecycleObservations(store, input, response, 4_100)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].TransitionIDs) != 1 {
		t.Fatalf("completion persistence=%+v", results)
	}
	episode, err = store.GetTaskEpisode(episode.ID)
	if err != nil || episode.CurrentStatus != TaskEpisodeStatusCompleted {
		t.Fatalf("episode=%+v err=%v", episode, err)
	}

	input.PrefixCutoffAt = 4_000
	input.CutoffAt = 5_000
	input.Episodes[0].CurrentStatus = TaskEpisodeStatusCompleted
	input.SuffixEvidence = []TaskLifecycleEvidence{{
		Ref: "late", Kind: "test_result", SourceKind: "test", SourceID: "verify",
		OccurredAt: 4_500, Summary: "Verification passed",
	}}
	response = TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
		LocalKey: "late-result", Kind: TaskLifecycleKindResult,
		EpisodeIDs: []string{episode.ID}, Confidence: 0.9,
		ResultSummary: "Verification passed", ResultConfidence: 0.95,
		EvidenceRefs: []string{"late"},
	}}}
	results, err = PersistTaskLifecycleObservations(store, input, response, 5_100)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].TransitionIDs) != 0 {
		t.Fatalf("late result rewrote lifecycle: %+v", results)
	}
	evidence, err := store.ListTaskEpisodeEvidence(episode.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var resultCount int
	for _, item := range evidence {
		if item.Role == TaskEpisodeEvidenceResult {
			resultCount++
		}
	}
	if resultCount != 2 {
		t.Fatalf("result evidence count=%d, want completion and late result", resultCount)
	}
}

func TestPersistTaskLifecycleReasoningIsAppendOnlyAndStateNeutral(t *testing.T) {
	store := newTestStore(t)
	episode := createLifecycleEpisode(
		t,
		store,
		"lifecycle-reasoning",
		TaskEpisodeStatusActive,
		1_000,
	)
	input := TaskLifecycleInput{
		SourceKind:     "activity",
		SourceID:       "reasoning-window",
		PrefixCutoffAt: 2_000,
		CutoffAt:       4_000,
		Episodes: []TaskLifecycleEpisodeSnapshot{{
			EpisodeID:          episode.ID,
			CurrentStatus:      episode.CurrentStatus,
			InferredStartedAt:  episode.InferredStartAt,
			Task:               episode.LatestHypothesis.TaskHypothesis,
			DesiredResult:      episode.LatestHypothesis.DesiredResultHypothesis,
			TaskConfidence:     episode.LatestHypothesis.TaskConfidence,
			BoundaryConfidence: episode.LatestHypothesis.BoundaryConfidence,
			EvidenceRefs:       []string{"prefix"},
		}},
		PrefixEvidence: []TaskLifecycleEvidence{{
			Ref:        "prefix",
			Kind:       "onset",
			SourceKind: "event",
			SourceID:   "onset",
			OccurredAt: 1_000,
			Summary:    "Task began",
		}},
		SuffixEvidence: []TaskLifecycleEvidence{{
			Ref:         "reason",
			Kind:        "keyboard",
			SourceKind:  "activity_event",
			SourceID:    "reason-event",
			OccurredAt:  3_000,
			Summary:     "The user entered a rationale.",
			ContentKind: TaskLifecycleContentCommittedText,
			Content:     "I am verifying this because the customer needs certainty.",
			Actor:       TaskLifecycleActorUser,
		}},
	}
	response := TaskLifecycleResponse{
		ReasoningObservations: []TaskLifecycleReasoningObservation{{
			LocalKey:    "reasoning",
			EpisodeID:   episode.ID,
			Kind:        TaskLifecycleReasoningStatedRationale,
			Statement:   "A model-authored paraphrase.",
			SourceQuote: "because the customer needs certainty",
			Confidence:  0.91,
			EvidenceRefs: []string{
				"reason",
			},
		}},
	}
	first, err := PersistTaskLifecycleObservations(store, input, response, 4_100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PersistTaskLifecycleObservations(store, input, response, 4_200)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].ReasoningKind !=
		TaskLifecycleReasoningStatedRationale ||
		len(first[0].TransitionIDs) != 0 || len(second) != 1 ||
		first[0].EvidenceIDs[0] != second[0].EvidenceIDs[0] {
		t.Fatalf("reasoning persistence first=%+v second=%+v", first, second)
	}
	current, err := store.GetTaskEpisode(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CurrentStatus != TaskEpisodeStatusActive {
		t.Fatalf("reasoning changed lifecycle state: %+v", current)
	}
	evidence, err := store.ListTaskEpisodeEvidence(
		episode.ID,
		TaskEpisodeEvidenceReasoning,
		10,
	)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("reasoning evidence=%+v err=%v", evidence, err)
	}
	if evidence[0].Summary != "because the customer needs certainty" ||
		strings.Contains(evidence[0].PayloadJSON, "model-authored") ||
		strings.Contains(evidence[0].PayloadJSON, "0.91") {
		t.Fatalf("model interpretation entered observed evidence: %+v", evidence[0])
	}
	var payload taskLifecycleReasoningEvidencePayload
	if err := json.Unmarshal([]byte(evidence[0].PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != taskLifecyclePromptVersion ||
		payload.Kind != TaskLifecycleReasoningStatedRationale ||
		len(payload.EvidenceRefs) != 1 || payload.EvidenceRefs[0] != "reason" {
		t.Fatalf("reasoning payload=%+v", payload)
	}
}

func TestPersistTaskLifecycleLateDetectionKeepsEvidenceTimeAndClampsTransition(
	t *testing.T,
) {
	store := newTestStore(t)
	input := taskEpisodeFixture(
		"lifecycle-late-detection",
		1_000,
		5_000,
		TaskEpisodeStatusCandidate,
	)
	episode, added, err := store.CreateTaskEpisode(input)
	if err != nil || !added {
		t.Fatalf("create late-detected episode: added=%v err=%v", added, err)
	}
	lifecycleInput := TaskLifecycleInput{
		SourceKind:     "activity",
		SourceID:       "late-detection-window",
		PrefixCutoffAt: 2_000,
		CutoffAt:       4_000,
		Episodes: []TaskLifecycleEpisodeSnapshot{{
			EpisodeID:          episode.ID,
			CurrentStatus:      episode.CurrentStatus,
			InferredStartedAt:  episode.InferredStartAt,
			Task:               episode.LatestHypothesis.TaskHypothesis,
			DesiredResult:      episode.LatestHypothesis.DesiredResultHypothesis,
			TaskConfidence:     episode.LatestHypothesis.TaskConfidence,
			BoundaryConfidence: episode.LatestHypothesis.BoundaryConfidence,
			EvidenceRefs:       []string{"prefix"},
		}},
		PrefixEvidence: []TaskLifecycleEvidence{{
			Ref:        "prefix",
			Kind:       "onset",
			SourceKind: "event",
			SourceID:   "onset",
			OccurredAt: 1_000,
			Summary:    "The task began before it was detected.",
		}},
		SuffixEvidence: []TaskLifecycleEvidence{{
			Ref:        "result",
			Kind:       "submission",
			SourceKind: "event",
			SourceID:   "submit",
			OccurredAt: 3_000,
			Summary:    "The result was submitted before detection completed.",
		}},
	}
	response := TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
		LocalKey:           "completed-before-detection",
		Kind:               TaskLifecycleKindCompleted,
		EpisodeIDs:         []string{episode.ID},
		OccurredAt:         3_000,
		Confidence:         0.9,
		BoundaryConfidence: 0.8,
		ResultSummary:      "Submitted result",
		ResultConfidence:   0.9,
		EvidenceRefs:       []string{"result"},
	}}}
	results, err := PersistTaskLifecycleObservations(
		store,
		lifecycleInput,
		response,
		5_100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].TransitionIDs) != 2 {
		t.Fatalf("late-detection persistence=%+v", results)
	}
	transitions, err := store.ListTaskEpisodeTransitions(episode.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 3 ||
		transitions[1].OccurredAt != 5_000 ||
		transitions[2].OccurredAt != 5_000 {
		t.Fatalf("late-detection transitions=%+v", transitions)
	}
	evidence, err := store.ListTaskEpisodeEvidence(
		episode.ID,
		TaskEpisodeEvidenceResult,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].ObservedAt != 3_000 {
		t.Fatalf("late-detection evidence=%+v", evidence)
	}
}

func TestPersistTaskLifecycleCandidateContinuationAndCompletion(t *testing.T) {
	t.Run("continued activates candidate", func(t *testing.T) {
		store := newTestStore(t)
		episode := createLifecycleEpisode(
			t,
			store,
			"lifecycle-candidate-continued",
			TaskEpisodeStatusCandidate,
			1_000,
		)
		input := lifecycleCandidateInput(episode, "action", "The user kept working.")
		response := TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
			LocalKey:     "continued",
			Kind:         TaskLifecycleKindContinued,
			EpisodeIDs:   []string{episode.ID},
			Confidence:   0.9,
			EvidenceRefs: []string{"suffix"},
		}}}

		results, err := PersistTaskLifecycleObservations(store, input, response, 4_100)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || len(results[0].TransitionIDs) != 1 {
			t.Fatalf("continuation persistence=%+v", results)
		}
		got, err := store.GetTaskEpisode(episode.ID)
		if err != nil || got.CurrentStatus != TaskEpisodeStatusActive {
			t.Fatalf("episode=%+v err=%v, want active", got, err)
		}
	})

	t.Run("strong completion activates then completes candidate", func(t *testing.T) {
		store := newTestStore(t)
		episode := createLifecycleEpisode(
			t,
			store,
			"lifecycle-candidate-completed",
			TaskEpisodeStatusCandidate,
			1_000,
		)
		input := lifecycleCandidateInput(
			episode,
			"submission",
			"The user submitted the result.",
		)
		response := TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
			LocalKey:         "completed",
			Kind:             TaskLifecycleKindCompleted,
			EpisodeIDs:       []string{episode.ID},
			Confidence:       0.9,
			ResultSummary:    "Submitted result",
			ResultConfidence: 0.9,
			EvidenceRefs:     []string{"suffix"},
		}}}

		results, err := PersistTaskLifecycleObservations(store, input, response, 4_100)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || len(results[0].TransitionIDs) != 2 {
			t.Fatalf("candidate completion persistence=%+v", results)
		}
		got, err := store.GetTaskEpisode(episode.ID)
		if err != nil || got.CurrentStatus != TaskEpisodeStatusCompleted {
			t.Fatalf("episode=%+v err=%v, want completed", got, err)
		}
	})
}

func lifecycleCandidateInput(
	episode TaskEpisode,
	suffixKind, suffixSummary string,
) TaskLifecycleInput {
	return TaskLifecycleInput{
		SourceKind:     "activity",
		SourceID:       "candidate-window",
		PrefixCutoffAt: 2_000,
		CutoffAt:       4_000,
		Episodes: []TaskLifecycleEpisodeSnapshot{{
			EpisodeID:          episode.ID,
			CurrentStatus:      episode.CurrentStatus,
			InferredStartedAt:  episode.InferredStartAt,
			Task:               episode.LatestHypothesis.TaskHypothesis,
			DesiredResult:      episode.LatestHypothesis.DesiredResultHypothesis,
			TaskConfidence:     episode.LatestHypothesis.TaskConfidence,
			BoundaryConfidence: episode.LatestHypothesis.BoundaryConfidence,
			EvidenceRefs:       []string{"prefix"},
		}},
		PrefixEvidence: []TaskLifecycleEvidence{{
			Ref:        "prefix",
			Kind:       "onset",
			SourceKind: "event",
			SourceID:   "onset",
			OccurredAt: 1_000,
			Summary:    "Task began",
		}},
		SuffixEvidence: []TaskLifecycleEvidence{{
			Ref:        "suffix",
			Kind:       suffixKind,
			SourceKind: "event",
			SourceID:   "suffix",
			OccurredAt: 3_000,
			Summary:    suffixSummary,
		}},
	}
}

func TestPersistTaskLifecycleSwitchKeepsEpisodesIndependent(t *testing.T) {
	store := newTestStore(t)
	source := createLifecycleEpisode(t, store, "switch-source", TaskEpisodeStatusActive, 1_000)
	target := createLifecycleEpisode(t, store, "switch-target", TaskEpisodeStatusPaused, 1_100)
	input := TaskLifecycleInput{
		SourceKind: "activity", SourceID: "switch-window",
		PrefixCutoffAt: 2_000, CutoffAt: 3_000,
		Episodes: []TaskLifecycleEpisodeSnapshot{
			{EpisodeID: source.ID, CurrentStatus: source.CurrentStatus, InferredStartedAt: 1_000, Task: "Source", DesiredResult: "Source result", TaskConfidence: 0.8, BoundaryConfidence: 0.8, EvidenceRefs: []string{"source-prefix"}},
			{EpisodeID: target.ID, CurrentStatus: target.CurrentStatus, InferredStartedAt: 1_100, Task: "Target", DesiredResult: "Target result", TaskConfidence: 0.8, BoundaryConfidence: 0.8, EvidenceRefs: []string{"target-prefix"}},
		},
		PrefixEvidence: []TaskLifecycleEvidence{
			{Ref: "source-prefix", Kind: "onset", SourceKind: "event", SourceID: "s", OccurredAt: 1_000, Summary: "Source began"},
			{Ref: "target-prefix", Kind: "onset", SourceKind: "event", SourceID: "t", OccurredAt: 1_100, Summary: "Target began"},
		},
		SuffixEvidence: []TaskLifecycleEvidence{{
			Ref: "switch", Kind: "task_switch", SourceKind: "event", SourceID: "switch",
			OccurredAt: 2_500, Summary: "Attention moved to target",
		}},
	}
	response := TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
		LocalKey: "switch", Kind: TaskLifecycleKindSwitched,
		EpisodeIDs: []string{source.ID}, TargetEpisodeIDs: []string{target.ID},
		Confidence: 0.9, BoundaryConfidence: 0.8, EvidenceRefs: []string{"switch"},
	}}}
	if _, err := PersistTaskLifecycleObservations(store, input, response, 3_100); err != nil {
		t.Fatal(err)
	}
	source, _ = store.GetTaskEpisode(source.ID)
	target, _ = store.GetTaskEpisode(target.ID)
	if source.CurrentStatus != TaskEpisodeStatusPaused ||
		target.CurrentStatus != TaskEpisodeStatusActive {
		t.Fatalf("source=%s target=%s", source.CurrentStatus, target.CurrentStatus)
	}
}

func TestPersistTaskLifecycleUnknownAddsEvidenceWithoutStateChange(t *testing.T) {
	store := newTestStore(t)
	episode := createLifecycleEpisode(t, store, "unknown-episode", TaskEpisodeStatusActive, 1_000)
	input := TaskLifecycleInput{
		SourceKind: "activity", SourceID: "unknown-window",
		PrefixCutoffAt: 2_000, CutoffAt: 3_000,
		Episodes: []TaskLifecycleEpisodeSnapshot{{
			EpisodeID: episode.ID, CurrentStatus: episode.CurrentStatus,
			InferredStartedAt: 1_000, Task: "Task", DesiredResult: "Result",
			TaskConfidence: 0.8, BoundaryConfidence: 0.8, EvidenceRefs: []string{"prefix"},
		}},
		PrefixEvidence: []TaskLifecycleEvidence{{
			Ref: "prefix", Kind: "onset", SourceKind: "event", SourceID: "p",
			OccurredAt: 1_000, Summary: "Task began",
		}},
		SuffixEvidence: []TaskLifecycleEvidence{{
			Ref: "ambiguous", Kind: "app_switch", SourceKind: "event", SourceID: "a",
			OccurredAt: 2_500, Summary: "The user changed apps",
		}},
	}
	response := TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
		LocalKey: "unclear", Kind: TaskLifecycleKindUnknown,
		EpisodeIDs: []string{episode.ID}, EvidenceRefs: []string{"ambiguous"},
	}}}
	results, err := PersistTaskLifecycleObservations(store, input, response, 3_100)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].TransitionIDs) != 0 {
		t.Fatalf("unknown observation changed lifecycle: %+v", results)
	}
	got, _ := store.GetTaskEpisode(episode.ID)
	if got.CurrentStatus != TaskEpisodeStatusActive {
		t.Fatalf("status=%s, want active", got.CurrentStatus)
	}
}
