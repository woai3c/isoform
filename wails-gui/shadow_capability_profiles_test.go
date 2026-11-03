package main

import (
	"strings"
	"testing"
)

func addCapabilityProfileEvaluation(
	t *testing.T,
	store *ActivityStore,
	index int,
	actionAssessment string,
	actionScore float64,
	actionConfidence float64,
	safetyAssessment string,
	safetyScore float64,
	safetyConfidence float64,
) (ShadowRun, ShadowRunObservation, ShadowRunEvaluation) {
	t.Helper()
	suffix := string(rune('a' + index))
	run := newShadowRunFixture(
		"shadow-profile-run-"+suffix,
		"shadow-profile-decision-"+suffix,
	)
	run.SourceID = "prediction-source-" + suffix
	run.Context.PromptContext = "Distinct frozen decision context " + suffix
	run.CreatedAt += int64(index * 10_000)
	run.Context.CapturedAt = run.CreatedAt
	run.Context.ProductionEventAt = run.CreatedAt - 100
	run.ObservationHorizonStart = run.CreatedAt
	run.ObservationHorizonEnd = run.CreatedAt + shadowObservationWindow.Milliseconds()
	run.Context.References[0].SourceID = run.SourceID
	run.Context.References[0].ObservedAt = run.CreatedAt - 100

	createdRun, added, err := store.CreateShadowRun(run)
	if err != nil || !added {
		t.Fatalf("create run %d: added=%v err=%v", index, added, err)
	}
	observation, added, err := store.AddShadowRunObservation(ShadowRunObservation{
		ShadowRunID:    createdRun.ID,
		SourceKind:     "activity",
		SourceID:       "observation-source-" + suffix,
		ObservedAction: "Drafted follow-up " + suffix,
		ObservedResult: "Saved draft " + suffix,
		ObservedAt:     createdRun.CreatedAt + 1_000,
		CreatedAt:      createdRun.CreatedAt + 1_100,
	})
	if err != nil || !added {
		t.Fatalf("create observation %d: added=%v err=%v", index, added, err)
	}
	evaluation, added, err := store.AddShadowRunEvaluation(ShadowRunEvaluation{
		ShadowRunID:       createdRun.ID,
		ObservationID:     observation.ID,
		IdempotencyKey:    "shadow-profile-evaluation-" + suffix,
		ComparisonVersion: shadowCorrelationPromptVersion,
		EvaluatorModel:    "gpt-5.6-terra",
		Action: ShadowEvaluationDimension{
			Assessment: actionAssessment,
			Score:      shadowScore(actionScore),
			Confidence: actionConfidence,
		},
		Outcome: ShadowEvaluationDimension{
			Assessment: "matched",
			Score:      shadowScore(1),
			Confidence: 0.85,
		},
		Timing: ShadowEvaluationDimension{
			Assessment: "partial",
			Score:      shadowScore(0.5),
			Confidence: 0.75,
		},
		Reasoning: ShadowEvaluationDimension{
			Assessment: "unknown",
			Confidence: 0.2,
		},
		Safety: ShadowEvaluationDimension{
			Assessment: safetyAssessment,
			Score:      shadowScore(safetyScore),
			Confidence: safetyConfidence,
		},
		CreatedAt: createdRun.CreatedAt + 1_200,
	})
	if err != nil || !added {
		t.Fatalf("create evaluation %d: added=%v err=%v", index, added, err)
	}
	return createdRun, observation, evaluation
}

func TestShadowCapabilityProfileBecomesReadyFromDiverseQualifiedEvidence(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 5; i++ {
		addCapabilityProfileEvaluation(
			t,
			store,
			i,
			"matched",
			1,
			0.9,
			"safe",
			1,
			0.9,
		)
	}

	profile, added, err := store.RebuildShadowCapabilityProfile(
		"customer.follow_up.draft",
		2_000_000,
	)
	if err != nil || !added {
		t.Fatalf("rebuild profile: added=%v err=%v", added, err)
	}
	if !profile.Readiness.Ready || profile.Readiness.Status != "ready" {
		t.Fatalf("readiness=%+v", profile.Readiness)
	}
	if profile.QualifiedEvaluationCount != 5 ||
		profile.PredictionSourceCount != 5 ||
		profile.ObservationSourceCount != 5 ||
		profile.ContextCount != 5 {
		t.Fatalf("profile diversity=%+v", profile)
	}
	if profile.Action.Reliability == nil || *profile.Action.Reliability != 1 {
		t.Fatalf("action profile=%+v", profile.Action)
	}
	if profile.Reasoning.UnknownCount != 5 ||
		profile.Reasoning.QualifiedCount != 0 ||
		profile.Reasoning.Reliability != nil {
		t.Fatalf("unknown reasoning was scored: %+v", profile.Reasoning)
	}
	if len(profile.Evidence) != 5 || !profile.Evidence[0].Qualified {
		t.Fatalf("profile evidence=%+v", profile.Evidence)
	}

	var initiativeCount, authorityCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM initiatives`).Scan(&initiativeCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM authority_decisions`).Scan(&authorityCount); err != nil {
		t.Fatal(err)
	}
	if initiativeCount != 0 || authorityCount != 0 {
		t.Fatalf(
			"profile readiness changed production state: initiatives=%d authority=%d",
			initiativeCount,
			authorityCount,
		)
	}
}

func TestShadowCapabilityProfilePreservesExcludedAndUnsafeEvidence(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 5; i++ {
		actionConfidence := 0.9
		if i == 0 {
			actionConfidence = 0.6
		}
		safetyAssessment := "safe"
		safetyScore := 1.0
		if i == 4 {
			safetyAssessment = "unsafe"
			safetyScore = 0
		}
		addCapabilityProfileEvaluation(
			t,
			store,
			i,
			"matched",
			1,
			actionConfidence,
			safetyAssessment,
			safetyScore,
			0.9,
		)
	}

	profile, _, err := store.RebuildShadowCapabilityProfile(
		"customer.follow_up.draft",
		2_000_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Readiness.Ready {
		t.Fatalf("unsafe, insufficiently diverse profile was ready: %+v", profile.Readiness)
	}
	if profile.QualifiedEvaluationCount != 4 ||
		profile.ExcludedEvaluationCount != 1 ||
		profile.Action.LowConfidenceCount != 1 {
		t.Fatalf("excluded evidence disappeared: %+v", profile)
	}
	if profile.Safety.QualifiedAssessmentCounts["unsafe"] != 1 ||
		!containsString(profile.Readiness.Reasons, "qualified_unsafe_evidence") {
		t.Fatalf("unsafe evidence disappeared: %+v", profile)
	}
	if profile.Evidence[0].Qualified ||
		profile.Evidence[0].ExclusionReason != "low_confidence" {
		t.Fatalf("low-confidence evidence=%+v", profile.Evidence[0])
	}
}

func TestShadowCapabilityProfileSnapshotsAreIdempotentAndImmutable(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 5; i++ {
		addCapabilityProfileEvaluation(t, store, i, "matched", 1, 0.9, "safe", 1, 0.9)
	}
	first, added, err := store.RebuildShadowCapabilityProfile(
		"customer.follow_up.draft",
		2_000_000,
	)
	if err != nil || !added {
		t.Fatalf("first profile: added=%v err=%v", added, err)
	}
	replayed, added, err := store.RebuildShadowCapabilityProfile(
		"customer.follow_up.draft",
		2_100_000,
	)
	if err != nil || added || replayed.ID != first.ID {
		t.Fatalf("idempotent profile: profile=%+v added=%v err=%v", replayed, added, err)
	}
	if _, err := store.db.Exec(
		`UPDATE shadow_capability_profile_snapshots SET ready = 0 WHERE id = ?`,
		first.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("profile update error=%v", err)
	}

	addCapabilityProfileEvaluation(t, store, 5, "matched", 1, 0.9, "safe", 1, 0.9)
	second, added, err := store.RebuildShadowCapabilityProfile(
		"customer.follow_up.draft",
		2_200_000,
	)
	if err != nil || !added || second.ID == first.ID ||
		second.EvidenceHash == first.EvidenceHash {
		t.Fatalf("new profile snapshot: profile=%+v added=%v err=%v", second, added, err)
	}
	profiles, err := store.ListLatestShadowCapabilityProfiles(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ID != second.ID {
		t.Fatalf("latest profiles=%+v", profiles)
	}
	var snapshotCount int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM shadow_capability_profile_snapshots`,
	).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("snapshot count=%d, want 2", snapshotCount)
	}
}

func TestShadowCapabilityProfileUsesNewestEvaluationPerObservation(t *testing.T) {
	store := newTestStore(t)
	run, observation, first := addCapabilityProfileEvaluation(
		t,
		store,
		0,
		"different",
		0,
		0.9,
		"safe",
		1,
		0.9,
	)
	second, added, err := store.AddShadowRunEvaluation(ShadowRunEvaluation{
		ShadowRunID:       run.ID,
		ObservationID:     observation.ID,
		IdempotencyKey:    "shadow-profile-evaluation-a-v2",
		ComparisonVersion: "shadow-comparator-v2",
		EvaluatorModel:    "gpt-5.6-terra",
		Action:            ShadowEvaluationDimension{Assessment: "matched", Score: shadowScore(1), Confidence: 0.9},
		Outcome:           ShadowEvaluationDimension{Assessment: "matched", Score: shadowScore(1), Confidence: 0.9},
		Timing:            ShadowEvaluationDimension{Assessment: "unknown", Confidence: 0.2},
		Reasoning:         ShadowEvaluationDimension{Assessment: "unknown", Confidence: 0.2},
		Safety:            ShadowEvaluationDimension{Assessment: "safe", Score: shadowScore(1), Confidence: 0.9},
		CreatedAt:         first.CreatedAt + 100,
	})
	if err != nil || !added {
		t.Fatalf("second evaluation: added=%v err=%v", added, err)
	}

	profile, _, err := store.RebuildShadowCapabilityProfile(
		run.CapabilityKey,
		2_000_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.EvaluationCount != 1 ||
		len(profile.Evidence) != 1 ||
		profile.Evidence[0].EvaluationID != second.ID ||
		profile.Action.Reliability == nil ||
		*profile.Action.Reliability != 1 {
		t.Fatalf("latest evaluation selection=%+v", profile)
	}
}
