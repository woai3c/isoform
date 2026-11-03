package main

import (
	"strings"
	"testing"
)

func taskShadowReleaseFixture(
	decision string,
	evidenceHash string,
) (TaskShadowQualityCampaign, ShadowTaskLearningCandidate) {
	qualification := ShadowTaskLearningQualification{
		Decision:                 decision,
		ObservedEvaluationCount:  5,
		QualifiedEvaluationCount: 5,
		ObservedTaskEpisodeCount: 5,
		ObservedContextCount:     3,
		RequiredEvaluationCount:  5,
		RequiredTaskEpisodeCount: 5,
		RequiredContextCount:     3,
		RequiredSafetyCount:      3,
	}
	complete := decision == ShadowTaskLearningDecisionQualified
	evidence := make([]ShadowTaskLearningProvenance, 0, 5)
	for index := 1; index <= 5; index++ {
		suffix := string(rune('0' + index))
		evidence = append(evidence, ShadowTaskLearningProvenance{
			EvaluationID:  "evaluation-" + suffix,
			TaskEpisodeID: "episode-" + suffix,
			ShadowRunID:   "run-" + suffix,
			ContextHash:   "context-" + suffix,
		})
	}
	return TaskShadowQualityCampaign{
			Version:       taskShadowQualityCampaignVersion,
			CapabilityKey: TaskWorkflowComposeText,
			PromptVersion: shadowTaskPromptVersion,
			EvidenceHash:  evidenceHash,
			Complete:      complete,
			Qualification: qualification,
		}, ShadowTaskLearningCandidate{
			PolicyVersion:   ShadowTaskLearningPolicyVersion,
			TargetKind:      ShadowTaskLearningTargetCapabilityProfile,
			LearningKind:    ShadowTaskLearningKindCapabilityEvidence,
			CapabilityKey:   TaskWorkflowComposeText,
			EvidenceHash:    evidenceHash,
			Qualification:   qualification,
			AuthorityEffect: ShadowTaskLearningAuthorityNone,
			Evidence:        evidence,
		}
}

func TestTaskShadowCapabilityReleaseQualifiesAndSuspendsAppendOnly(
	t *testing.T,
) {
	store := newTestStore(t)
	qualifiedCampaign, qualifiedCandidate := taskShadowReleaseFixture(
		ShadowTaskLearningDecisionQualified,
		strings.Repeat("a", 64),
	)
	qualified, added, err := reconcileTaskShadowCapabilityRelease(
		store,
		qualifiedCampaign,
		qualifiedCandidate,
		1_000,
	)
	if err != nil || !added ||
		qualified.Decision != taskShadowReleaseDecisionQualified ||
		qualified.AuthorityEffect != taskShadowReleaseAuthorityNone {
		t.Fatalf("qualified release: added=%v event=%+v err=%v", added, qualified, err)
	}
	if _, added, err := reconcileTaskShadowCapabilityRelease(
		store,
		qualifiedCampaign,
		qualifiedCandidate,
		1_100,
	); err != nil || added {
		t.Fatalf("idempotent qualified release: added=%v err=%v", added, err)
	}
	snapshot, err := buildTaskShadowCapabilityReleaseSnapshot(
		store,
		qualifiedCampaign,
		20,
	)
	if err != nil || !snapshot.Active ||
		snapshot.State != taskShadowReleaseDecisionQualified ||
		snapshot.LatestCurrentPromptEvent == nil {
		t.Fatalf("qualified snapshot=%+v err=%v", snapshot, err)
	}

	suspendedCampaign, suspendedCandidate := taskShadowReleaseFixture(
		ShadowTaskLearningDecisionCollecting,
		strings.Repeat("b", 64),
	)
	suspendedCampaign.Qualification.Reasons = []string{
		"result_quality_below_threshold",
	}
	suspendedCandidate.Qualification = suspendedCampaign.Qualification
	suspended, added, err := reconcileTaskShadowCapabilityRelease(
		store,
		suspendedCampaign,
		suspendedCandidate,
		2_000,
	)
	if err != nil || !added ||
		suspended.Decision != taskShadowReleaseDecisionSuspended ||
		suspended.PreviousEventID != qualified.ID {
		t.Fatalf("suspended release: added=%v event=%+v err=%v", added, suspended, err)
	}
	snapshot, err = buildTaskShadowCapabilityReleaseSnapshot(
		store,
		suspendedCampaign,
		20,
	)
	if err != nil || snapshot.Active ||
		snapshot.State != taskShadowReleaseDecisionSuspended ||
		snapshot.CurrentPromptEventCount != 2 {
		t.Fatalf("suspended snapshot=%+v err=%v", snapshot, err)
	}

	requalifiedCampaign, requalifiedCandidate := taskShadowReleaseFixture(
		ShadowTaskLearningDecisionQualified,
		strings.Repeat("f", 64),
	)
	requalified, added, err := reconcileTaskShadowCapabilityRelease(
		store,
		requalifiedCampaign,
		requalifiedCandidate,
		3_000,
	)
	if err != nil || !added ||
		requalified.Decision != taskShadowReleaseDecisionQualified ||
		requalified.PreviousEventID != suspended.ID {
		t.Fatalf("requalified release: added=%v event=%+v err=%v", added, requalified, err)
	}
	snapshot, err = buildTaskShadowCapabilityReleaseSnapshot(
		store,
		requalifiedCampaign,
		20,
	)
	if err != nil || !snapshot.Active ||
		snapshot.State != taskShadowReleaseDecisionQualified ||
		snapshot.CurrentPromptEventCount != 3 {
		t.Fatalf("requalified snapshot=%+v err=%v", snapshot, err)
	}

	for _, statement := range []string{
		`UPDATE task_shadow_capability_release_events_v1 SET decision = 'qualified' WHERE id = ?`,
		`DELETE FROM task_shadow_capability_release_events_v1 WHERE id = ?`,
	} {
		if _, err := store.db.Exec(statement, requalified.ID); err == nil {
			t.Fatalf("release ledger accepted mutation: %s", statement)
		}
	}
	for _, table := range []string{
		"authority_decisions",
		"authority_grants",
	} {
		var count int
		if err := store.db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("release changed %s: count=%d", table, count)
		}
	}
}

func TestTaskShadowCapabilityReleaseKeepsOldPromptHistorical(t *testing.T) {
	store := newTestStore(t)
	campaign, candidate := taskShadowReleaseFixture(
		ShadowTaskLearningDecisionQualified,
		strings.Repeat("c", 64),
	)
	campaign.PromptVersion = "shadow-task-v4"
	if _, added, err := store.AppendTaskShadowCapabilityReleaseEvent(
		TaskShadowCapabilityReleaseEvent{
			Version:               taskShadowCapabilityReleaseVersion,
			CapabilityKey:         campaign.CapabilityKey,
			PromptVersion:         campaign.PromptVersion,
			CampaignVersion:       campaign.Version,
			LearningPolicyVersion: candidate.PolicyVersion,
			Decision:              taskShadowReleaseDecisionQualified,
			EvidenceHash:          campaign.EvidenceHash,
			Qualification:         campaign.Qualification,
			Evidence:              taskShadowReleaseEvidenceFromCandidate(candidate),
			AuthorityEffect:       taskShadowReleaseAuthorityNone,
			CreatedAt:             1_000,
		},
	); err != nil || !added {
		t.Fatalf("historical release: added=%v err=%v", added, err)
	}
	current, _ := taskShadowReleaseFixture(
		ShadowTaskLearningDecisionCollecting,
		strings.Repeat("d", 64),
	)
	snapshot, err := buildTaskShadowCapabilityReleaseSnapshot(
		store,
		current,
		20,
	)
	if err != nil || snapshot.Active || snapshot.State != "unqualified" ||
		snapshot.CurrentPromptEventCount != 0 ||
		snapshot.HistoricalEventCount != 1 {
		t.Fatalf("current snapshot=%+v err=%v", snapshot, err)
	}
}

func TestTaskShadowCapabilityReleaseRejectsAuthorityEffect(t *testing.T) {
	store := newTestStore(t)
	campaign, candidate := taskShadowReleaseFixture(
		ShadowTaskLearningDecisionQualified,
		strings.Repeat("e", 64),
	)
	event := TaskShadowCapabilityReleaseEvent{
		Version:               taskShadowCapabilityReleaseVersion,
		CapabilityKey:         campaign.CapabilityKey,
		PromptVersion:         campaign.PromptVersion,
		CampaignVersion:       campaign.Version,
		LearningPolicyVersion: candidate.PolicyVersion,
		Decision:              taskShadowReleaseDecisionQualified,
		EvidenceHash:          campaign.EvidenceHash,
		Qualification:         campaign.Qualification,
		Evidence:              taskShadowReleaseEvidenceFromCandidate(candidate),
		AuthorityEffect:       "grant",
		CreatedAt:             1_000,
	}
	if _, _, err := store.AppendTaskShadowCapabilityReleaseEvent(event); err == nil {
		t.Fatal("release ledger accepted an authority effect")
	}
}
