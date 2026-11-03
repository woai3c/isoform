package main

import (
	"context"
	"strings"
	"testing"
)

func workPatternProfileDebugTableCounts(
	t *testing.T,
	store *ActivityStore,
) map[string]int {
	t.Helper()
	result := make(map[string]int)
	for _, table := range []string{
		"task_episodes",
		"shadow_runs",
		"task_agent_attempts",
		"shadow_run_evaluations",
		"shadow_task_learning_runs",
		"work_pattern_profile_ablation_runs_v1",
	} {
		var count int
		if err := store.db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		result[table] = count
	}
	return result
}

func TestWorkPatternProfileRolloutGateRequiresMeasuredImprovement(
	t *testing.T,
) {
	evaluations := make([]WorkPatternProfileAblationEvaluation, 0, 20)
	for index := 0; index < 20; index++ {
		neutral := 0.40
		profile := 0.50
		winner := WorkPatternInfluenceProfile
		if index >= 16 {
			neutral = 0.51
			profile = 0.50
			winner = "tie"
		}
		evaluations = append(
			evaluations,
			WorkPatternProfileAblationEvaluation{
				NeutralScore: &neutral,
				ProfileScore: &profile,
				Winner:       winner,
			},
		)
	}
	status := buildWorkPatternProfileRolloutStatus(
		false,
		20,
		evaluations,
		0,
	)
	if !status.MeetsEvidenceGate ||
		status.RecommendedDecision != "eligible_for_manual_rollout" {
		t.Fatalf("rollout status=%+v", status)
	}

	status = buildWorkPatternProfileRolloutStatus(
		false,
		1,
		evaluations[:1],
		0,
	)
	if status.MeetsEvidenceGate || status.RecommendedDecision != "hold" {
		t.Fatalf("underpowered rollout status=%+v", status)
	}

	status = buildWorkPatternProfileRolloutStatus(
		false,
		20,
		evaluations,
		3,
	)
	if status.MeetsEvidenceGate ||
		status.CorrectionCount != 3 ||
		status.CorrectionRate <= status.MaximumCorrectionRate {
		t.Fatalf("correction-gated rollout status=%+v", status)
	}
}

func TestWorkPatternProfileDebugLinksFrozenConsumersAndAttempts(
	t *testing.T,
) {
	store := newTestStore(t)
	profile, input := workPatternAblationFixture(t, store)
	runner := &workPatternAblationFakeRunner{responses: []string{
		workPatternOnsetResponse(
			"neutral",
			"Review a pull request patch",
			"A review conclusion exists",
			input.WindowStart,
		),
		workPatternOnsetResponse(
			"profile",
			"Review a pull request patch",
			"A review conclusion exists",
			input.WindowStart,
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
	if ablation.ProductionErr != nil {
		t.Fatal(ablation.ProductionErr)
	}
	learning := &TaskContextLearningProvenance{
		Version:                  VerifiedShadowLearningSourceVersion,
		RunID:                    "learning-run-debug",
		RevisionID:               "learning-revision-debug",
		MaterializationReceiptID: "learning-receipt-debug",
		TargetKind:               ShadowTaskLearningTargetCapabilityProfile,
		TargetID:                 "learning-target-debug",
		ProjectionKey:            "capability:review-pull-request",
		CapabilityKey:            "review-pull-request",
		LearningKind:             ShadowTaskLearningKindCapabilityEvidence,
		EvidenceHash:             strings.Repeat("a", 64),
		AvailableAt:              input.CutoffAt - 1,
		AuthorityEffect:          ShadowTaskLearningAuthorityNone,
	}
	profileContext := neutralWorkPatternProfileContext(profile)
	profileContext.SelectionManifest = append(
		profileContext.SelectionManifest,
		WorkPatternProfileSelectionEntry{
			Ref:           "capability:learning-target-debug",
			Kind:          "capability",
			EvidenceClass: WorkPatternEvidenceEvaluated,
			Included:      true,
			Reason:        "selected_for_current_context",
			Learning:      learning,
		},
	)
	profileContext = neutralWorkPatternProfileContext(profileContext)
	captures, err := PersistTaskOnsetEpisodesWithProfileSources(
		store,
		input,
		ablation.Response,
		ablation.CompletedAt,
		nil,
		profileContext,
		TaskContextSourceResult{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 {
		t.Fatalf("captures=%d, want 1", len(captures))
	}
	run, _, err := store.CreateShadowTaskRunFromEpisode(
		captures[0].Episode,
		"gpt-5.6-sol",
		ablation.CompletedAt+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}

	snapshot, err := app.GetWorkPatternProfileDebugSnapshot(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) == 0 {
		t.Fatal("debug snapshot has no profiles")
	}
	var summary *WorkPatternProfileDebugSummary
	for index := range snapshot.Profiles {
		if snapshot.Profiles[index].ID == profile.SnapshotID {
			summary = &snapshot.Profiles[index]
			break
		}
	}
	if summary == nil ||
		summary.ConsumingEpisodeCount != 1 ||
		summary.ConsumingRunCount != 1 ||
		summary.AblationRunCount != 1 {
		t.Fatalf("debug summary=%+v", summary)
	}
	storedProfile, err := store.GetWorkPatternProfileSnapshot(profile.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	storedEvidence, err := store.ListWorkPatternProfileEvidence(profile.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProjectionCount != len(storedProfile.Projections) ||
		summary.ExclusionCount != len(storedProfile.Exclusions) ||
		summary.EvidenceCount != len(storedEvidence) {
		t.Fatalf(
			"narrow summary lost canonical counts: summary=%+v profile=%+v evidence=%d",
			summary,
			storedProfile,
			len(storedEvidence),
		)
	}

	countsBefore := workPatternProfileDebugTableCounts(t, store)
	detail, err := app.GetWorkPatternProfileDebugDetail(
		profile.SnapshotID,
		20,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Episodes) != 1 ||
		detail.Episodes[0].ID != captures[0].Episode.ID ||
		detail.Episodes[0].ProfileSelectionVersion !=
			workPatternProfileSelectionEvidenceVersion ||
		detail.Episodes[0].ProfileOnsetPresent ||
		len(detail.Episodes[0].ProfileSelectionManifest) == 0 ||
		detail.Episodes[0].ProfileSelectionReason != "neutral_fallback" ||
		len(detail.ShadowRuns) != 1 ||
		detail.ShadowRuns[0].ID != run.ID ||
		len(detail.Ablations) != 1 ||
		len(detail.ModelAttempts) != 2 {
		t.Fatalf("debug detail linkage=%+v", detail)
	}
	if detail.Replay.ProfileSnapshotID != profile.SnapshotID ||
		detail.Replay.ProfileSHA256 != profile.SnapshotSHA256 ||
		len(detail.Replay.OnsetAttemptIDs) != 2 {
		t.Fatalf("replay bundle=%+v", detail.Replay)
	}
	foundLearning := false
	for _, item := range detail.Episodes[0].ProfileSelectionManifest {
		if item.Learning != nil &&
			item.Learning.RevisionID == learning.RevisionID &&
			item.Learning.MaterializationReceiptID ==
				learning.MaterializationReceiptID {
			foundLearning = true
		}
	}
	if !foundLearning {
		t.Fatalf(
			"debug detail lost frozen learning lineage: %+v",
			detail.Episodes[0].ProfileSelectionManifest,
		)
	}
	for _, item := range detail.Episodes[0].OnsetProfileSelectionManifest {
		if item.OnsetPromptIncluded == nil ||
			*item.OnsetPromptIncluded ||
			item.OnsetPromptReason != "onset_profile_not_supplied" {
			t.Fatalf("neutral production onset was recorded as profile-bearing: %+v", item)
		}
	}
	if !detail.PromptDiagnostic.Fits ||
		detail.PromptDiagnostic.PromptProjectionBytes <= 0 ||
		detail.PromptDiagnostic.PromptProjectionBytes >
			workPatternProfilePromptMaximumBytes ||
		len(detail.PromptDiagnostic.SourceContextSHA256) != 64 ||
		len(detail.PromptDiagnostic.PromptProjectionSHA256) != 64 ||
		len(detail.PromptDiagnostic.SelectionManifestSHA256) != 64 {
		t.Fatalf(
			"prompt diagnostic did not prove the envelope: %+v",
			detail.PromptDiagnostic,
		)
	}
	countsAfter := workPatternProfileDebugTableCounts(t, store)
	for table, before := range countsBefore {
		if after := countsAfter[table]; after != before {
			t.Fatalf(
				"read-only profile detail mutated %s: before=%d after=%d",
				table,
				before,
				after,
			)
		}
	}
	for _, attempt := range detail.ModelAttempts {
		if strings.TrimSpace(attempt.PromptText) == "" ||
			strings.TrimSpace(attempt.RawOutput) == "" {
			t.Fatalf("attempt is not exact-replay capable: %+v", attempt)
		}
	}
}

func TestWorkPatternProfileDebugMethodsAreBrowserAllowlisted(t *testing.T) {
	for _, method := range []string{
		"GetWorkPatternProfileDebugSnapshot",
		"GetWorkPatternProfileDebugDetail",
	} {
		if !browserRPCAllowlist[method] {
			t.Fatalf("%s is not browser allowlisted", method)
		}
	}
}
