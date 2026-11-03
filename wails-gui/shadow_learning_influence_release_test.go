package main

import (
	"context"
	"testing"

	"yansu-agent/agent/authority"
)

func shadowLearningInfluenceSourceFixture(
	t *testing.T,
	store *ActivityStore,
) (VerifiedShadowLearningSource, WorkPatternProfileContext) {
	t.Helper()
	records := []ShadowTaskLearningEvaluationRecord{
		shadowTaskLearningTestRecord(201),
		shadowTaskLearningTestRecord(202),
		shadowTaskLearningTestRecord(203),
		shadowTaskLearningTestRecord(204),
		shadowTaskLearningTestRecord(205),
	}
	projection, candidate := verifiedShadowLearningTestCandidate(
		t,
		records,
		ShadowTaskLearningTargetCapabilityProfile,
		ShadowTaskLearningKindCapabilityEvidence,
	)
	_, receipt := persistVerifiedShadowLearningTestCandidate(
		t,
		store,
		projection,
		candidate,
		10_000,
		10_100,
	)
	snapshot := qualifiedWorkPatternFixture(t, store)
	sources, err := GatherTaskContextSources(TaskContextSourceRequest{
		Store:    store,
		CutoffAt: snapshot.SourceCutoffAt,
		Query:    candidate.CapabilityKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := buildWorkPatternProfileContext(
		store,
		snapshot,
		sources,
		candidate.CapabilityKey,
		[]string{"test.safari", "test.terminal"},
		[]string{"test.safari->test.terminal"},
		WorkPatternInfluenceProfile,
	)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifiedShadowLearningSourcesAt(
		store,
		snapshot.SourceCutoffAt,
		candidate.CapabilityKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	source := verifiedShadowLearningSourceByRevision(
		verified,
		candidate.RevisionID,
	)
	if source == nil || source.Receipt.ID != receipt.ID {
		t.Fatalf("verified source missing: %+v", verified)
	}
	return *source, profile
}

func shadowLearningInfluenceAppendCompletedResult(
	t *testing.T,
	store *ActivityStore,
	episode TaskEpisode,
	run ShadowRun,
) TaskEpisode {
	t.Helper()
	activeAt := max(
		max(episode.InferredStartAt+100, episode.DetectedAt+1),
		run.CreatedAt+1,
	)
	if _, added, err := store.AppendTaskEpisodeTransition(
		TaskEpisodeTransition{
			TaskEpisodeID: episode.ID,
			ToStatus:      TaskEpisodeStatusActive,
			Reason:        "test task began",
			OccurredAt:    activeAt,
			CreatedAt:     activeAt,
		},
	); err != nil || !added {
		t.Fatalf("activate later task: added=%v err=%v", added, err)
	}
	appendCurrentTaskResultEvidence(
		t,
		store,
		episode.ID,
		run.CapabilityKey,
		"later-user-result",
		"A review conclusion exists with source-backed findings.",
		taskLifecycleTestResultDestination(),
		activeAt+100,
	)
	completedAt := activeAt + 200
	if _, added, err := store.AppendTaskEpisodeTransition(
		TaskEpisodeTransition{
			TaskEpisodeID: episode.ID,
			ToStatus:      TaskEpisodeStatusCompleted,
			Reason:        "test task completed",
			OccurredAt:    completedAt,
			CreatedAt:     completedAt,
		},
	); err != nil || !added {
		t.Fatalf("complete later task: added=%v err=%v", added, err)
	}
	completed, err := store.GetTaskEpisode(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func shadowLearningInfluencePersistEvaluation(
	t *testing.T,
	store *ActivityStore,
	episode TaskEpisode,
	run ShadowRun,
	createdAt int64,
) ShadowRunEvaluation {
	t.Helper()
	plan := run.Prediction.CandidateExecutionPlan
	if plan == nil {
		t.Fatal("Shadow run has no execution plan")
	}
	if _, added, err := store.AddShadowExecutionAttempt(
		ShadowExecutionAttempt{
			ShadowRunID:     run.ID,
			IdempotencyKey:  run.ID + ":learning-influence-test",
			RuntimeVersion:  shadowExecutionRuntimeVersion,
			Status:          "completed",
			PolicyVersion:   authority.ShadowPolicyVersion,
			AgentRunID:      "contained-test-run",
			Model:           run.Model,
			Prompt:          plan.Prompt,
			Plan:            *plan,
			Decisions:       []authority.Evaluation{},
			Actions:         []ShadowExecutionAction{},
			Artifacts:       []ShadowExecutionArtifact{},
			AssistantOutput: "A review conclusion exists with concise findings.",
			StartedAt:       createdAt,
			CompletedAt:     createdAt + 100,
			CreatedAt:       createdAt + 100,
		},
	); err != nil || !added {
		t.Fatalf("persist contained attempt: added=%v err=%v", added, err)
	}
	input, err := BuildShadowTaskEvaluationInput(store, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, input, err := buildShadowTaskEvaluationPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	unknown := unknownTaskDimension()
	output := ShadowTaskEvaluationOutput{
		Version:          ShadowTaskEvaluationOutputVersion,
		Summary:          "The contained result was compared without an aggregate judgment.",
		Task:             unknown,
		Boundary:         unknown,
		Process:          unknown,
		ResultSimilarity: unknown,
		ResultQuality:    unknown,
		Timing:           unknown,
		Reasoning:        unknown,
		Safety:           unknown,
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
			EvaluatorModel:    "gpt-5.6-terra",
			EvaluatorRaw:      `{"test":true}`,
			Evaluation:        &output,
		},
		createdAt+200,
	)
	if err != nil || !added {
		t.Fatalf("persist Shadow comparison: added=%v err=%v", added, err)
	}
	if evaluation.TaskEvaluationInput == nil ||
		evaluation.TaskEvaluationInput.TaskEpisodeID != episode.ID {
		t.Fatalf("evaluation linkage=%+v", evaluation)
	}
	return evaluation
}

func TestShadowLearningInfluenceProofStartsWithoutInventedEvidence(t *testing.T) {
	store := newTestStore(t)
	proof, err := store.BuildShadowLearningInfluenceProof(20_000)
	if err != nil {
		t.Fatal(err)
	}
	if proof.State != ShadowLearningInfluenceAwaitingSourceLearning ||
		proof.SourceLearningCount != 0 || proof.LaterTaskCount != 0 ||
		proof.MeasuredChainCount != 0 || proof.Complete || proof.Improved ||
		proof.AuthorityEffect != ShadowTaskLearningAuthorityNone {
		t.Fatalf("empty proof invented progress: %+v", proof)
	}
}

func TestShadowLearningInfluenceProofWaitsForLaterTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	source, profile := shadowLearningInfluenceSourceFixture(t, store)
	proof, err := store.BuildShadowLearningInfluenceProof(
		profile.SourceCutoffAt + 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proof.State != ShadowLearningInfluenceAwaitingLaterTask ||
		proof.SourceLearningCount != 1 ||
		proof.LearningRevisionID != source.Learning.RevisionID ||
		proof.LaterTaskCount != 0 || proof.Complete {
		t.Fatalf("unexpected pre-later-task proof: %+v", proof)
	}
}

func TestShadowLearningInfluenceProofRejectsNeutralProductionOnset(
	t *testing.T,
) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	_, profile := shadowLearningInfluenceSourceFixture(t, store)
	windowStart := profile.SourceCutoffAt + 1
	input := TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "neutral-learning-influence-window",
		WindowStart: windowStart,
		CutoffAt:    windowStart + 1_000,
		Evidence: []TaskOnsetEvidence{{
			Ref:        "event:review",
			Kind:       "focus",
			SourceKind: "activity_event",
			SourceID:   "neutral-learning-influence-review",
			OccurredAt: windowStart,
			Summary:    "Opened a pull request review",
		}},
	}
	runner := &workPatternAblationFakeRunner{responses: []string{
		workPatternOnsetResponse(
			"neutral",
			"Check notifications",
			"Notifications are reviewed",
			windowStart,
		),
		workPatternOnsetResponse(
			"profile",
			"Draft a review summary",
			"A source-backed review summary is ready",
			windowStart,
		),
	}}
	ablation := runTaskOnsetWithWorkPatternAblation(
		context.Background(),
		store,
		runner,
		"gpt-5.6-terra",
		input,
		profile,
		false,
	)
	if ablation.ProductionErr != nil || ablation.ProfileUsed {
		t.Fatalf("neutral onset failed: %+v", ablation)
	}
	captures, err := PersistTaskOnsetEpisodesWithProfileSources(
		store,
		input,
		ablation.Response,
		ablation.CompletedAt,
		nil,
		profile,
		TaskContextSourceResult{},
	)
	if err != nil || len(captures) != 1 {
		t.Fatalf("capture neutral later task: count=%d err=%v", len(captures), err)
	}
	proof, err := store.BuildShadowLearningInfluenceProof(
		ablation.CompletedAt + 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proof.State != ShadowLearningInfluenceLearningOmitted ||
		proof.OnsetProfilePresent || proof.OnsetPromptIncluded == nil ||
		*proof.OnsetPromptIncluded ||
		proof.OnsetPromptReason != "onset_profile_not_supplied" ||
		proof.Complete {
		t.Fatalf("neutral production branch was treated as influenced: %+v", proof)
	}
}

func TestShadowLearningInfluenceProofMeasuresIndependentLaterTask(
	t *testing.T,
) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	source, profile := shadowLearningInfluenceSourceFixture(t, store)
	windowStart := profile.SourceCutoffAt + 1
	input := TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "learning-influence-window",
		WindowStart: windowStart,
		CutoffAt:    windowStart + 1_000,
		Evidence: []TaskOnsetEvidence{{
			Ref:        "event:review",
			Kind:       "focus",
			SourceKind: "activity_event",
			SourceID:   "learning-influence-review",
			OccurredAt: windowStart,
			AppKey:     "test.safari",
			AppName:    "Safari",
			Summary:    "Opened a pull request review",
		}},
	}
	runner := &workPatternAblationFakeRunner{responses: []string{
		workPatternOnsetResponse(
			"profile",
			"Draft a review summary",
			"A source-backed review summary is ready",
			windowStart,
		),
		workPatternOnsetResponse(
			"neutral",
			"Check notifications",
			"Notifications are reviewed",
			windowStart+500,
		),
	}}
	ablation := runTaskOnsetWithWorkPatternAblation(
		context.Background(),
		store,
		runner,
		"gpt-5.6-terra",
		input,
		profile,
		true,
	)
	if ablation.ProductionErr != nil || !ablation.ProfileUsed {
		t.Fatalf("profile onset failed: %+v", ablation)
	}
	captures, err := PersistTaskOnsetEpisodesWithProfileSources(
		store,
		input,
		ablation.Response,
		ablation.CompletedAt,
		&profile,
		profile,
		TaskContextSourceResult{},
	)
	if err != nil || len(captures) != 1 {
		t.Fatalf("capture later task: count=%d err=%v", len(captures), err)
	}
	episode := captures[0].Episode
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"gpt-5.6-sol",
		ablation.CompletedAt+1,
	)
	if err != nil || !added {
		t.Fatalf("create later Shadow run: added=%v err=%v", added, err)
	}
	episode = shadowLearningInfluenceAppendCompletedResult(
		t,
		store,
		episode,
		run,
	)
	evaluation := shadowLearningInfluencePersistEvaluation(
		t,
		store,
		episode,
		run,
		ablation.CompletedAt+10,
	)
	ablationEvaluations, err := store.EvaluateWorkPatternProfileAblationForTask(
		episode,
		evaluation,
		ablation.CompletedAt+1_000,
	)
	if err != nil || len(ablationEvaluations) != 1 {
		t.Fatalf(
			"evaluate later ablation: count=%d err=%v",
			len(ablationEvaluations),
			err,
		)
	}
	proof, err := store.BuildShadowLearningInfluenceProof(
		ablation.CompletedAt + 2_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proof.State != ShadowLearningInfluenceMeasuredImprovement ||
		!proof.Complete || !proof.Improved || proof.MeasuredChainCount != 1 ||
		proof.LearningRevisionID != source.Learning.RevisionID ||
		proof.LaterTaskEpisodeID != episode.ID ||
		proof.ShadowRunID != run.ID ||
		proof.ShadowEvaluationID != evaluation.ID ||
		proof.AblationEvaluationID != ablationEvaluations[0].ID ||
		proof.OnsetPromptIncluded == nil || !*proof.OnsetPromptIncluded ||
		proof.ShadowContextIncluded == nil ||
		!*proof.ShadowContextIncluded || proof.Lift == nil || *proof.Lift <= 0 ||
		proof.AuthorityEffect != ShadowTaskLearningAuthorityNone {
		t.Fatalf("measured influence proof=%+v", proof)
	}
	for _, step := range proof.Steps {
		if !step.Complete {
			t.Fatalf("measured proof has incomplete step: %+v", step)
		}
	}
	if _, err := store.db.Exec(`
		DROP TRIGGER work_pattern_profile_ablation_eval_no_update_v1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE work_pattern_profile_ablation_evaluations_v1
		SET policy_version = 'work-pattern-ablation-v2'
		WHERE id = ?`, ablationEvaluations[0].ID); err != nil {
		t.Fatal(err)
	}
	historical, err := store.BuildShadowLearningInfluenceProof(
		ablation.CompletedAt + 2_001,
	)
	if err != nil {
		t.Fatal(err)
	}
	if historical.State != ShadowLearningInfluenceAwaitingAblation ||
		historical.Complete || historical.Improved ||
		!shadowLearningInfluenceContainsReason(
			historical.Reasons,
			"independent_ablation_not_recorded",
		) {
		t.Fatalf("historical v2 ablation qualified: %+v", historical)
	}
	if _, err := store.db.Exec(`
		UPDATE work_pattern_profile_ablation_evaluations_v1
		SET policy_version = ?
		WHERE id = ?`,
		workPatternProfileAblationPolicyVersion,
		ablationEvaluations[0].ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE work_pattern_profile_ablation_evaluations_v1
		SET evidence_json = '{}'
		WHERE id = ?`, ablationEvaluations[0].ID); err != nil {
		t.Fatal(err)
	}
	tampered, err := store.BuildShadowLearningInfluenceProof(
		ablation.CompletedAt + 2_002,
	)
	if err != nil {
		t.Fatal(err)
	}
	if tampered.State != ShadowLearningInfluenceIntegrityError ||
		tampered.Complete || tampered.Improved ||
		len(tampered.Reasons) == 0 {
		t.Fatalf("tampered ablation was accepted: %+v", tampered)
	}
}
