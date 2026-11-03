package main

import (
	"testing"
)

func testShadowTaskLearningProjection() ShadowTaskLearningProjection {
	score := 0.9
	confidence := 0.88
	return ShadowTaskLearningProjection{
		PolicyVersion:   ShadowTaskLearningPolicyVersion,
		AuthorityEffect: ShadowTaskLearningAuthorityNone,
		EvaluationCount: 3,
		Candidates: []ShadowTaskLearningCandidate{{
			ProjectionKey: "projection:one",
			RevisionID:    "revision:one",
			RevisionMode:  ShadowTaskLearningRevisionAppendOnly,
			PolicyVersion: ShadowTaskLearningPolicyVersion,
			TargetKind:    ShadowTaskLearningTargetMemory,
			LearningKind:  ShadowTaskLearningKindProcessPattern,
			CapabilityKey: "coding.review",
			Statement:     "Runs focused tests before the full suite",
			EvidenceHash:  "evidence-hash",
			Evidence: []ShadowTaskLearningProvenance{{
				EvaluationID:  "evaluation-1",
				TaskEpisodeID: "episode-1",
				ShadowRunID:   "run-1",
				ContextHash:   "context",
				EvidenceID:    "evidence-1",
				Origin:        ShadowTaskEvidenceOriginObserved,
				Kind:          ShadowTaskEvidenceAction,
				Summary:       "Ran focused tests",
				OccurredAt:    100,
				PersistedAt:   200,
			}},
			Dimensions: []ShadowTaskLearningDimensionEvidence{{
				EvaluationID: "evaluation-1",
				Name:         "process",
				Assessment:   ShadowTaskAssessmentMatched,
				Score:        &score,
				Confidence:   confidence,
				EvidenceRefs: []string{"evidence-1"},
			}},
			Qualification: ShadowTaskLearningQualification{
				Decision:                  ShadowTaskLearningDecisionQualified,
				AverageEvidenceConfidence: &confidence,
			},
			AuthorityEffect: ShadowTaskLearningAuthorityNone,
		}},
	}
}

func TestPersistShadowTaskLearningProjectionIsImmutableAndIdempotent(t *testing.T) {
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	run, added, err := PersistShadowTaskLearningProjection(store, projection, 1_000)
	if err != nil {
		t.Fatalf("PersistShadowTaskLearningProjection: %v", err)
	}
	if !added {
		t.Fatal("first projection was not added")
	}
	existing, added, err := PersistShadowTaskLearningProjection(store, projection, 1_000)
	if err != nil {
		t.Fatalf("idempotent persistence: %v", err)
	}
	if added || existing.ID != run.ID {
		t.Fatalf("idempotent result = %#v, added=%v", existing, added)
	}
	if _, err := store.db.Exec(
		`UPDATE shadow_task_learning_runs SET created_at = created_at + 1 WHERE id = ?`,
		run.ID,
	); err == nil {
		t.Fatal("immutable run accepted UPDATE")
	}
	if _, err := store.db.Exec(
		`DELETE FROM shadow_task_learning_revisions WHERE revision_id = ?`,
		"revision:one",
	); err == nil {
		t.Fatal("immutable revision accepted DELETE")
	}
}

func TestPersistShadowTaskLearningProjectionRejectsAuthorityEffect(t *testing.T) {
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	projection.AuthorityEffect = "grant"
	if _, _, err := PersistShadowTaskLearningProjection(store, projection, 1_000); err == nil {
		t.Fatal("authority-changing projection was accepted")
	}
}

func TestShadowTaskLearningMaterializationIsAppendOnly(t *testing.T) {
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	if _, _, err := PersistShadowTaskLearningProjection(store, projection, 1_000); err != nil {
		t.Fatal(err)
	}
	item := ShadowTaskLearningMaterialization{
		RevisionID: "revision:one",
		TargetKind: ShadowTaskLearningTargetMemory,
		TargetID:   "memory-1",
		Status:     "materialized",
		CreatedAt:  1_100,
	}
	first, added, err := store.AddShadowTaskLearningMaterialization(item)
	if err != nil || !added {
		t.Fatalf("materialization: added=%v err=%v", added, err)
	}
	second, added, err := store.AddShadowTaskLearningMaterialization(item)
	if err != nil || added || first.ID != second.ID {
		t.Fatalf("idempotent materialization: %#v added=%v err=%v", second, added, err)
	}
	item.Status = "error"
	if _, added, err := store.AddShadowTaskLearningMaterialization(item); err != nil || !added {
		t.Fatalf("append-only retry materialization: added=%v err=%v", added, err)
	}
}
