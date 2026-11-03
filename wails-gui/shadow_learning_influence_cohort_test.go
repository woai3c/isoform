package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func shadowLearningInfluenceCohortTestSource(
	sourceCount int,
) VerifiedShadowLearningSource {
	evidence := make([]ShadowTaskLearningProvenance, 0, sourceCount)
	for index := 0; index < sourceCount; index++ {
		evidence = append(evidence, ShadowTaskLearningProvenance{
			EvaluationID:  fmt.Sprintf("source-evaluation-%d", index),
			TaskEpisodeID: fmt.Sprintf("source-episode-%d", index),
			ShadowRunID:   fmt.Sprintf("source-run-%d", index),
		})
	}
	return VerifiedShadowLearningSource{
		Candidate: ShadowTaskLearningCandidate{Evidence: evidence},
		Learning: TaskContextLearningProvenance{
			RevisionID:               "learning-revision",
			MaterializationReceiptID: "learning-receipt",
			TargetKind:               ShadowTaskLearningTargetCapabilityProfile,
			LearningKind:             ShadowTaskLearningKindCapabilityEvidence,
			CapabilityKey:            "review.pull.request",
			AvailableAt:              1_000,
		},
		Receipt: ShadowTaskLearningMaterialization{CreatedAt: 1_000},
	}
}

func shadowLearningInfluenceCohortTestChain(
	index int,
	neutralScore, profileScore float64,
) ShadowLearningInfluenceChain {
	winner := "tie"
	state := ShadowLearningInfluenceMeasuredNoLift
	if profileScore > neutralScore {
		winner = WorkPatternInfluenceProfile
		state = ShadowLearningInfluenceMeasuredImprovement
	} else if neutralScore > profileScore {
		winner = WorkPatternInfluenceNeutral
	}
	lift := profileScore - neutralScore
	return ShadowLearningInfluenceChain{
		ID:                 fmt.Sprintf("chain-%d", index),
		LearningRevisionID: "learning-revision",
		LaterTaskEpisodeID: fmt.Sprintf("later-episode-%d", index),
		LaterTaskStatus:    TaskEpisodeStatusCompleted,
		LaterTaskStartedAt: int64(2_000 + index),
		State:              state,
		OnsetIncluded:      true,
		ShadowIncluded:     true,
		ShadowRunID:        fmt.Sprintf("later-run-%d", index),
		NeutralScore:       &neutralScore,
		ProfileScore:       &profileScore,
		Lift:               &lift,
		Winner:             winner,
		CorrectionIDs:      []string{},
		Reasons:            []string{},
		AuthorityEffect:    ShadowTaskLearningAuthorityNone,
	}
}

func TestShadowLearningInfluenceCohortReliabilityPolicy(t *testing.T) {
	source := shadowLearningInfluenceCohortTestSource(5)
	reliableChains := make([]ShadowLearningInfluenceChain, 0, 5)
	for index := 0; index < 5; index++ {
		reliableChains = append(
			reliableChains,
			shadowLearningInfluenceCohortTestChain(index, 0.40, 0.50),
		)
	}

	empty := buildShadowLearningInfluenceCohortSummary(source, nil)
	if empty.State != ShadowLearningInfluenceCohortAwaitingMeasurement ||
		empty.RelevantLaterTaskCount != 0 ||
		empty.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
		empty.ProductionInfluenceEffect != ShadowTaskLearningAuthorityNone {
		t.Fatalf("empty cohort=%+v", empty)
	}

	early := buildShadowLearningInfluenceCohortSummary(
		source,
		reliableChains[:4],
	)
	if early.State != ShadowLearningInfluenceCohortEarly ||
		early.ScoredChainCount != 4 ||
		!shadowLearningInfluenceContainsReason(
			early.Blockers,
			"4 of 5 independently scored later tasks",
		) {
		t.Fatalf("early cohort=%+v", early)
	}

	reliable := buildShadowLearningInfluenceCohortSummary(
		source,
		reliableChains,
	)
	if reliable.State != ShadowLearningInfluenceCohortReliable ||
		reliable.ScoredChainCount != 5 || reliable.ProfileWins != 5 ||
		reliable.ProfileWinRate != 1 || reliable.MeanLift != 0.1 ||
		len(reliable.Blockers) != 0 {
		t.Fatalf("reliable cohort=%+v", reliable)
	}

	insufficientSource := buildShadowLearningInfluenceCohortSummary(
		shadowLearningInfluenceCohortTestSource(4),
		reliableChains,
	)
	if insufficientSource.State !=
		ShadowLearningInfluenceCohortInsufficientQuality ||
		!shadowLearningInfluenceContainsReason(
			insufficientSource.Blockers,
			"4 of 5 independent source tasks",
		) {
		t.Fatalf("source-diversity gate=%+v", insufficientSource)
	}

	correctedChains := append(
		[]ShadowLearningInfluenceChain(nil),
		reliableChains...,
	)
	correctedChains[0].Corrected = true
	correctedChains[0].CorrectionIDs = []string{"correction-1"}
	corrected := buildShadowLearningInfluenceCohortSummary(
		source,
		correctedChains,
	)
	if corrected.State != ShadowLearningInfluenceCohortInsufficientQuality ||
		corrected.CorrectionRate != 0.2 || corrected.CorrectionCount != 1 ||
		corrected.CorrectedChainCount != 1 {
		t.Fatalf("correction gate=%+v", corrected)
	}

	unknownChains := append(
		[]ShadowLearningInfluenceChain(nil),
		reliableChains...,
	)
	for index := 0; index < 2; index++ {
		unknownChains = append(unknownChains, ShadowLearningInfluenceChain{
			ID:                 fmt.Sprintf("unknown-%d", index),
			LaterTaskEpisodeID: fmt.Sprintf("unknown-episode-%d", index),
			State:              ShadowLearningInfluenceMeasuredUnknown,
			OnsetIncluded:      true,
			ShadowIncluded:     true,
			CorrectionIDs:      []string{},
			Reasons:            []string{"independent_observed_evidence_unscorable"},
			AuthorityEffect:    ShadowTaskLearningAuthorityNone,
		})
	}
	unknown := buildShadowLearningInfluenceCohortSummary(source, unknownChains)
	if unknown.State != ShadowLearningInfluenceCohortInsufficientQuality ||
		unknown.UnknownChainCount != 2 || unknown.UnknownRate <= 0.2 {
		t.Fatalf("unknown gate=%+v", unknown)
	}

	integrityChains := append(
		[]ShadowLearningInfluenceChain(nil),
		reliableChains...,
	)
	integrityChains = append(integrityChains, ShadowLearningInfluenceChain{
		ID:                 "integrity-chain",
		LaterTaskEpisodeID: "integrity-episode",
		State:              ShadowLearningInfluenceIntegrityError,
		IntegrityFailure:   true,
		CorrectionIDs:      []string{},
		Reasons:            []string{"immutable provenance failed"},
		AuthorityEffect:    ShadowTaskLearningAuthorityNone,
	})
	integrity := buildShadowLearningInfluenceCohortSummary(
		source,
		integrityChains,
	)
	if integrity.State != ShadowLearningInfluenceCohortIntegrityError ||
		integrity.IntegrityExclusionCount != 1 {
		t.Fatalf("integrity gate=%+v", integrity)
	}
}

func shadowLearningInfluenceMeasuredCohortFixture(
	t *testing.T,
) (
	*ActivityStore,
	VerifiedShadowLearningSource,
	TaskEpisode,
	ShadowRun,
	int64,
) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	source, profile := shadowLearningInfluenceSourceFixture(t, store)
	windowStart := profile.SourceCutoffAt + 1
	input := TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "learning-cohort-window",
		WindowStart: windowStart,
		CutoffAt:    windowStart + 1_000,
		Evidence: []TaskOnsetEvidence{{
			Ref:        "event:review",
			Kind:       "focus",
			SourceKind: "activity_event",
			SourceID:   "learning-cohort-review",
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
	if ablation.ProductionErr != nil || !ablation.ProfileUsed ||
		ablation.AblationRun.ID == "" {
		t.Fatalf("profile onset failed: %+v", ablation)
	}
	// CompletedAt belongs to the primary attempt; the neutral branch runs
	// afterward. Later observed work must follow both completed predictions,
	// regardless of how long the second branch takes under load.
	completedAt := max(ablation.CompletedAt, ablation.AblationRun.CompletedAt)
	captures, err := PersistTaskOnsetEpisodesWithProfileSources(
		store,
		input,
		ablation.Response,
		completedAt,
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
		completedAt+1,
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
		completedAt+10,
	)
	evaluations, err := store.EvaluateWorkPatternProfileAblationForTask(
		episode,
		evaluation,
		completedAt+1_000,
	)
	if err != nil || len(evaluations) != 1 {
		t.Fatalf("evaluate later ablation: count=%d err=%v", len(evaluations), err)
	}
	return store, source, episode, run, completedAt + 2_000
}

func TestShadowLearningInfluenceCohortSnapshotIsCompleteAndReadOnly(
	t *testing.T,
) {
	store, source, episode, run, cutoffAt :=
		shadowLearningInfluenceMeasuredCohortFixture(t)
	countsBefore := workPatternProfileDebugTableCounts(t, store)
	snapshot, err := store.BuildShadowLearningInfluenceCohortSnapshot(cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	countsAfter := workPatternProfileDebugTableCounts(t, store)
	for table, before := range countsBefore {
		if after := countsAfter[table]; after != before {
			t.Fatalf(
				"cohort read mutated %s: before=%d after=%d",
				table,
				before,
				after,
			)
		}
	}
	if snapshot.CohortCount != 1 || snapshot.EarlyCount != 1 ||
		snapshot.ReliableCount != 0 || len(snapshot.Cohorts) != 1 ||
		snapshot.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
		snapshot.ProductionInfluenceEffect != ShadowTaskLearningAuthorityNone ||
		len(snapshot.ManifestSHA256) != 64 ||
		!strings.HasSuffix(snapshot.ID, snapshot.ManifestSHA256) {
		t.Fatalf("cohort snapshot=%+v", snapshot)
	}
	cohort := snapshot.Cohorts[0]
	if cohort.LearningRevisionID != source.Learning.RevisionID ||
		cohort.SourceTaskEpisodeCount != 5 ||
		cohort.RelevantLaterTaskCount != 1 || cohort.ScoredChainCount != 1 ||
		cohort.ChainCount != 1 || len(cohort.Chains) != 1 ||
		len(cohort.ManifestSHA256) != 64 ||
		cohort.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
		cohort.ProductionInfluenceEffect != ShadowTaskLearningAuthorityNone {
		t.Fatalf("cohort summary=%+v", cohort)
	}
	chain := cohort.Chains[0]
	if chain.LaterTaskEpisodeID != episode.ID || chain.ShadowRunID != run.ID ||
		chain.State != ShadowLearningInfluenceMeasuredImprovement ||
		chain.Lift == nil || *chain.Lift <= 0 ||
		chain.AuthorityEffect != ShadowTaskLearningAuthorityNone {
		t.Fatalf("cohort chain=%+v", chain)
	}
	if _, added, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: episode.ID,
		Role:          TaskEpisodeEvidenceAction,
		SourceKind:    "activity_event",
		SourceID:      "future-cohort-action",
		Summary:       "This action was observed after the frozen cohort cutoff.",
		ObservedAt:    cutoffAt + 100,
		CreatedAt:     cutoffAt + 100,
	}); err != nil || !added {
		t.Fatalf("append future evidence: added=%v err=%v", added, err)
	}
	replayed, err := store.BuildShadowLearningInfluenceCohortSnapshot(cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != snapshot.ID ||
		replayed.ManifestSHA256 != snapshot.ManifestSHA256 ||
		replayed.Cohorts[0].ManifestSHA256 != cohort.ManifestSHA256 {
		t.Fatalf("same-cutoff snapshot drifted: first=%+v replay=%+v", snapshot, replayed)
	}
	advanced, err := store.BuildShadowLearningInfluenceCohortSnapshot(
		cutoffAt + 200,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(advanced.Cohorts) != 1 ||
		advanced.Cohorts[0].ScoredChainCount != 0 ||
		len(advanced.Cohorts[0].Chains) != 1 ||
		advanced.Cohorts[0].Chains[0].State !=
			ShadowLearningInfluenceAwaitingShadowResult {
		t.Fatalf("stale comparison remained measured: %+v", advanced)
	}
}

func TestShadowLearningInfluenceCohortCountsRepeatedOnsetAsOneIntegrityFailure(
	t *testing.T,
) {
	store, source, episode, _, cutoffAt :=
		shadowLearningInfluenceMeasuredCohortFixture(t)
	createHistoricalOnsetEpisodeFixture(
		t,
		store,
		"cohort-repeated-onset",
		taskEpisodeOnsetAnchor{
			Ref:        "event:review",
			SourceKind: "activity_event",
			SourceID:   "learning-cohort-review",
			ObservedAt: episode.InferredStartAt,
		},
		cutoffAt-100,
	)
	snapshot, err := store.BuildShadowLearningInfluenceCohortSnapshot(cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Cohorts) != 1 {
		t.Fatalf("cohort snapshot=%+v", snapshot)
	}
	cohort := snapshot.Cohorts[0]
	if cohort.LearningRevisionID != source.Learning.RevisionID ||
		cohort.State != ShadowLearningInfluenceCohortIntegrityError ||
		cohort.RelevantLaterTaskCount != 1 ||
		cohort.IntegrityExclusionCount != 1 || len(cohort.Chains) != 1 ||
		cohort.Chains[0].State != ShadowLearningInfluenceIntegrityError ||
		!shadowLearningInfluenceContainsReason(
			cohort.Chains[0].Reasons,
			taskEpisodeRepeatedOnsetReason,
		) {
		t.Fatalf("repeated onset cohort=%+v", cohort)
	}
}

func TestShadowLearningInfluenceCohortRejectsAmbiguousEpisodeGraph(
	t *testing.T,
) {
	source := shadowLearningInfluenceCohortTestSource(5)
	base := newShadowLearningInfluenceProof(3_000)
	candidate := shadowLearningInfluenceCandidateForEpisode(
		nil,
		base,
		source,
		TaskEpisodeDetail{
			Episode: TaskEpisode{
				ID:              "later-episode",
				InferredStartAt: 2_000,
			},
			Links: []TaskEpisodeLink{{
				SourceEpisodeID: "later-episode",
				TargetEpisodeID: "canonical-episode",
				Relation:        TaskEpisodeLinkMergedInto,
			}},
		},
	)
	if candidate.proof.State != ShadowLearningInfluenceIntegrityError ||
		!shadowLearningInfluenceContainsReason(
			candidate.proof.Reasons,
			"later_task_component_is_ambiguous",
		) {
		t.Fatalf("ambiguous task graph was accepted: %+v", candidate.proof)
	}
}

func TestWorkPatternProfileRolloutReadsCompleteValidatedLedger(t *testing.T) {
	store, _, _, _, cutoffAt := shadowLearningInfluenceMeasuredCohortFixture(t)
	app := &App{activity: &activityMonitorState{store: store}}
	baseline, err := app.workPatternProfileRolloutStatusAt(cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.EvaluationCount != 1 || baseline.AblationRunCount != 1 ||
		baseline.IntegrityErrorCount != 0 {
		t.Fatalf("validated rollout baseline=%+v", baseline)
	}
	var validRunID, validEpisodeID string
	if err := store.db.QueryRow(`
		SELECT ablation_run_id, task_episode_id
		FROM work_pattern_profile_ablation_evaluations_v1
		LIMIT 1`).Scan(&validRunID, &validEpisodeID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 201; index++ {
		if _, err := store.db.Exec(`
			INSERT INTO work_pattern_profile_ablation_evaluations_v1 (
				id, ablation_run_id, task_episode_id, shadow_evaluation_id,
				policy_version, neutral_score, profile_score,
				neutral_boundary_error_ms, profile_boundary_error_ms,
				winner, evidence_json, created_at
			) VALUES (?, ?, ?, ?, ?, NULL, NULL, NULL, NULL, 'unknown', '{}', ?)`,
			fmt.Sprintf("malformed-current-evaluation-%03d", index),
			validRunID,
			validEpisodeID,
			fmt.Sprintf("malformed-current-shadow-evaluation-%03d", index),
			workPatternProfileAblationPolicyVersion,
			cutoffAt-int64(201-index),
		); err != nil {
			t.Fatalf("insert malformed evaluation %d: %v", index, err)
		}
	}
	status, err := app.workPatternProfileRolloutStatusAt(cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	if status.EvaluationCount != 1 || status.AblationRunCount != 1 ||
		status.IntegrityErrorCount != 201 || status.MeetsEvidenceGate ||
		status.RecommendedDecision != "hold" ||
		status.Reason !=
			"current paired outcomes failed immutable provenance checks" {
		t.Fatalf("complete validated rollout=%+v", status)
	}
}

func TestShadowLearningInfluenceEpisodeReaderHasNoEvidenceLimit(t *testing.T) {
	store := newTestStore(t)
	episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"unbounded-cohort-episode",
		1_000,
		2_000,
		TaskEpisodeStatusActive,
	))
	if err != nil || !added {
		t.Fatalf("create episode: added=%v err=%v", added, err)
	}
	for index := 0; index < 501; index++ {
		observedAt := int64(3_000 + index)
		if _, added, err := store.AppendTaskEpisodeEvidence(
			TaskEpisodeEvidence{
				TaskEpisodeID: episode.ID,
				Role:          TaskEpisodeEvidenceAction,
				SourceKind:    "activity_event",
				SourceID:      fmt.Sprintf("unbounded-action-%03d", index),
				Summary:       fmt.Sprintf("Observed action %03d", index),
				ObservedAt:    observedAt,
				CreatedAt:     observedAt,
			},
		); err != nil || !added {
			t.Fatalf("append evidence %d: added=%v err=%v", index, added, err)
		}
	}
	detail, err := getShadowLearningInfluenceEpisodeDetailAt(
		store,
		episode.ID,
		10_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Evidence) != 502 {
		t.Fatalf("evidence count=%d, want 502", len(detail.Evidence))
	}
}
