package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"yansu-agent/agent"
)

type workPatternAblationFakeRunner struct {
	responses []string
	inputs    []*agent.TaskInput
}

func (r *workPatternAblationFakeRunner) Init(context.Context) error { return nil }
func (r *workPatternAblationFakeRunner) IsReady() bool              { return true }
func (r *workPatternAblationFakeRunner) ExecuteTask(
	_ context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	r.inputs = append(r.inputs, input)
	response := `{"episodes":[]}`
	if index := len(r.inputs) - 1; index < len(r.responses) {
		response = r.responses[index]
	}
	if err := callback(&agent.StreamEvent{
		Type:    "assistant",
		Content: response,
	}); err != nil {
		return "", err
	}
	return "work-pattern-ablation", nil
}
func (r *workPatternAblationFakeRunner) CancelRun(string) error { return nil }
func (r *workPatternAblationFakeRunner) RespondToPermission(string, string) error {
	return nil
}
func (r *workPatternAblationFakeRunner) CancelPermission(string) {}
func (r *workPatternAblationFakeRunner) Shutdown() error         { return nil }

func workPatternAblationFixture(
	t *testing.T,
	store *ActivityStore,
) (WorkPatternProfileContext, TaskOnsetInput) {
	t.Helper()
	snapshot := qualifiedWorkPatternFixture(t, store)
	profile, err := buildWorkPatternProfileContext(
		store,
		snapshot,
		TaskContextSourceResult{},
		"review a pull request patch",
		[]string{"test.safari", "test.terminal"},
		[]string{"test.safari->test.terminal"},
		WorkPatternInfluenceProfile,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !workPatternProfileEvidenceQualified(profile) {
		t.Fatalf("profile is not eligible: %+v", profile)
	}
	windowStart := snapshot.SourceCutoffAt + 1
	input := TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "ablation-window",
		WindowStart: windowStart,
		CutoffAt:    windowStart + 1_000,
		Evidence: []TaskOnsetEvidence{{
			Ref:        "event:review",
			Kind:       "focus",
			SourceKind: "activity_event",
			SourceID:   "review",
			OccurredAt: windowStart,
			AppKey:     "test.safari",
			AppName:    "Safari",
			Summary:    "Opened a pull request review",
		}},
	}
	return profile, input
}

func workPatternOnsetResponse(
	localKey, task, desired string,
	startedAt int64,
) string {
	return `{"episodes":[{` +
		`"localKey":"` + localKey + `",` +
		`"task":"` + task + `",` +
		`"desiredResult":"` + desired + `",` +
		`"inferredStartedAt":` + fmt.Sprint(startedAt) + `,` +
		`"taskConfidence":0.8,` +
		`"boundaryConfidence":0.7,` +
		`"resultConfidence":0.7,` +
		`"evidenceRefs":["event:review"],` +
		`"alternatives":[]` +
		`}]}`
}

func TestWorkPatternProfileAblationIsPairedPrivateAndIdempotent(
	t *testing.T,
) {
	store := newTestStore(t)
	profile, input := workPatternAblationFixture(t, store)
	runner := &workPatternAblationFakeRunner{responses: []string{
		workPatternOnsetResponse(
			"neutral",
			"Check notifications",
			"Notifications are reviewed",
			input.WindowStart,
		),
		workPatternOnsetResponse(
			"profile",
			"Review a pull request patch",
			"A review conclusion exists",
			input.WindowStart,
		),
	}}

	result := runTaskOnsetWithWorkPatternAblation(
		context.Background(),
		store,
		runner,
		"gpt-5.6-terra",
		input,
		profile,
		false,
	)
	if result.ProductionErr != nil {
		t.Fatal(result.ProductionErr)
	}
	if len(runner.inputs) != 2 || len(result.AttemptIDs) != 2 {
		t.Fatalf(
			"calls=%d attempts=%v",
			len(runner.inputs),
			result.AttemptIDs,
		)
	}
	if result.PrimaryMode != WorkPatternInfluenceNeutral ||
		result.ProfileUsed {
		t.Fatalf("unexpected production mode: %+v", result)
	}
	if len(result.Response.Episodes) != 1 ||
		result.Response.Episodes[0].LocalKey != "neutral" {
		t.Fatalf("production response=%+v", result.Response)
	}
	if result.AblationRun.ID == "" ||
		result.AblationRun.NeutralAttemptID == "" ||
		result.AblationRun.ProfileAttemptID == "" {
		t.Fatalf("ablation receipt=%+v", result.AblationRun)
	}
	if result.ProductionAttemptID != result.AblationRun.NeutralAttemptID ||
		result.ProductionAttemptID == result.AblationRun.ProfileAttemptID {
		t.Fatalf(
			"neutral production attempt=%q receipt=%+v",
			result.ProductionAttemptID,
			result.AblationRun,
		)
	}
	if strings.Contains(runner.inputs[0].Prompt, profile.SnapshotID) {
		t.Fatal("neutral prompt unexpectedly contains the profile")
	}
	if !strings.Contains(runner.inputs[1].Prompt, profile.SnapshotID) {
		t.Fatal("profile prompt does not contain the frozen profile")
	}

	for _, table := range []string{
		"task_episodes",
		"shadow_runs",
		"initiatives",
		"authority_decisions",
		"initiative_action_receipts",
		"self_model_entries",
		"agency_permission_decision_receipts_v1",
	} {
		var count int
		if err := store.db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("diagnostic ablation created %d rows in %s", count, table)
		}
	}

	replayed := runTaskOnsetWithWorkPatternAblation(
		context.Background(),
		store,
		runner,
		"gpt-5.6-terra",
		input,
		profile,
		false,
	)
	if replayed.ProductionErr != nil {
		t.Fatal(replayed.ProductionErr)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("idempotent replay made %d model calls, want 2", len(runner.inputs))
	}
	if replayed.AblationRun.ID != result.AblationRun.ID {
		t.Fatalf(
			"replay run=%s, want %s",
			replayed.AblationRun.ID,
			result.AblationRun.ID,
		)
	}
	if replayed.ProductionAttemptID != result.ProductionAttemptID {
		t.Fatalf(
			"replay production attempt=%q, want %q",
			replayed.ProductionAttemptID,
			result.ProductionAttemptID,
		)
	}
}

func TestWorkPatternProfileAblationIdentifiesProfileProductionAttempt(t *testing.T) {
	store := newTestStore(t)
	profile, input := workPatternAblationFixture(t, store)
	input.SourceID = "profile-production-window"
	runner := &workPatternAblationFakeRunner{responses: []string{
		workPatternOnsetResponse(
			"neutral",
			"Check notifications",
			"Notifications are reviewed",
			input.WindowStart,
		),
		workPatternOnsetResponse(
			"profile",
			"Review a pull request patch",
			"A review conclusion exists",
			input.WindowStart,
		),
	}}

	result := runTaskOnsetWithWorkPatternAblation(
		context.Background(),
		store,
		runner,
		"gpt-5.6-terra",
		input,
		profile,
		true,
	)
	if result.ProductionErr != nil {
		t.Fatal(result.ProductionErr)
	}
	if result.PrimaryMode != WorkPatternInfluenceProfile || !result.ProfileUsed ||
		result.ProductionAttemptID != result.AblationRun.ProfileAttemptID ||
		result.ProductionAttemptID == result.AblationRun.NeutralAttemptID {
		t.Fatalf("profile production result=%+v", result)
	}
}

func TestWorkPatternProfileAblationEvaluatesAgainstObservedOutcome(
	t *testing.T,
) {
	store := newTestStore(t)
	profile, input := workPatternAblationFixture(t, store)
	input.Evidence[0].Summary = "Opened notes to write a review conclusion"
	runner := &workPatternAblationFakeRunner{responses: []string{
		workPatternOnsetResponse(
			"profile",
			"Write a review conclusion",
			"A review conclusion summary exists with source-backed findings",
			input.WindowStart,
		),
		workPatternOnsetResponse(
			"neutral",
			"Check notifications",
			"Notifications are reviewed",
			input.WindowStart+500,
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
	if ablation.ProductionErr != nil {
		t.Fatal(ablation.ProductionErr)
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
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 {
		t.Fatalf("captures=%d, want 1", len(captures))
	}
	episode := captures[0].Episode
	run, added, err := store.CreateShadowTaskRunFromEpisode(
		episode,
		"gpt-5.6-sol",
		ablation.CompletedAt+1,
	)
	if err != nil || !added {
		t.Fatalf("create Shadow run: added=%v err=%v", added, err)
	}
	if !taskWorkflowTextResult(run.CapabilityKey) {
		t.Fatalf("Shadow run capability %q cannot materialize a text result", run.CapabilityKey)
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
	observedReference, err := buildWorkPatternProfileAblationObservedReference(
		store,
		episode,
		evaluation,
		ablation.AblationRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observedReference.EvidenceStrength !=
		workPatternProfileAblationEvidenceOnsetResult ||
		observedReference.ResultSHA256 == "" ||
		observedReference.EvidenceSHA256 == "" {
		t.Fatalf("observed reference=%+v", observedReference)
	}
	evaluations, err := store.EvaluateWorkPatternProfileAblationForTask(
		episode,
		evaluation,
		input.CutoffAt+20_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluations) != 1 {
		t.Fatalf("evaluations=%d, want 1", len(evaluations))
	}
	if evaluations[0].Winner != WorkPatternInfluenceProfile ||
		evaluations[0].NeutralScore == nil ||
		evaluations[0].ProfileScore == nil ||
		*evaluations[0].ProfileScore <= *evaluations[0].NeutralScore {
		t.Fatalf("unexpected evaluation: %+v", evaluations[0])
	}
	if evaluations[0].Metrics.Version !=
		workPatternProfileAblationPolicyVersion ||
		evaluations[0].Metrics.ObservedReferenceVersion !=
			workPatternProfileAblationObservedReferenceVersion ||
		evaluations[0].Metrics.ObservedEvidenceStrength !=
			workPatternProfileAblationEvidenceOnsetResult ||
		evaluations[0].Metrics.ObservedEvidenceSHA256 !=
			observedReference.EvidenceSHA256 ||
		evaluations[0].Metrics.MergeScope !=
			"independent_observed_evidence" ||
		evaluations[0].Metrics.Profile.OnsetPrecision != 1 ||
		evaluations[0].Metrics.Profile.OnsetRecall != 1 ||
		evaluations[0].Metrics.Profile.ResultMatch == nil ||
		evaluations[0].Metrics.Profile.CalibrationError == nil {
		t.Fatalf("dimensioned metrics=%+v", evaluations[0].Metrics)
	}
	if strings.Contains(
		evaluations[0].EvidenceJSON,
		"Opened notes to write a review conclusion",
	) || strings.Contains(
		evaluations[0].EvidenceJSON,
		"A review conclusion exists with source-backed findings.",
	) {
		t.Fatal("ablation evidence persisted raw observed text")
	}

	// The episode hypothesis is a prediction made by the profile-influenced
	// branch. Changing it must not change the independent observed reference or
	// the score used to judge that branch.
	mutatedEpisode := episode
	mutatedEpisode.LatestHypothesis.TaskHypothesis =
		"Check notifications"
	mutatedEpisode.LatestHypothesis.DesiredResultHypothesis =
		"Notifications are reviewed"
	mutatedReference, err := buildWorkPatternProfileAblationObservedReference(
		store,
		mutatedEpisode,
		evaluation,
		ablation.AblationRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, neutralScore, profileScore, neutralBoundary, profileBoundary, winner :=
		workPatternProfileAblationMetricsForEvidence(
			ablation.AblationRun,
			mutatedReference,
		)
	if mutatedReference.EvidenceSHA256 != observedReference.EvidenceSHA256 ||
		winner != evaluations[0].Winner ||
		neutralScore == nil || profileScore == nil ||
		*neutralScore != *evaluations[0].NeutralScore ||
		*profileScore != *evaluations[0].ProfileScore ||
		!sameOptionalInt64(
			neutralBoundary,
			evaluations[0].NeutralBoundaryErrorMS,
		) || !sameOptionalInt64(
		profileBoundary,
		evaluations[0].ProfileBoundaryErrorMS,
	) {
		t.Fatalf(
			"profile-generated hypothesis contaminated v3 scoring: reference=%+v evaluation=%+v",
			mutatedReference,
			evaluations[0],
		)
	}
	if _, err := store.db.Exec(`
		UPDATE work_pattern_profile_ablation_evaluations_v1
		SET winner = 'neutral'
		WHERE id = ?`,
		evaluations[0].ID,
	); err == nil {
		t.Fatal("ablation evaluation update unexpectedly succeeded")
	}
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func TestWorkPatternEvidencePredictionMetricsPenalizeShotgunPredictions(
	t *testing.T,
) {
	observed := workPatternProfileAblationObservedReference{
		EvidenceStrength: workPatternProfileAblationEvidenceActionResult,
		BehaviorText:     "Draft a source-backed pull request review",
		ResultText:       "A source-backed review summary is ready",
		StartedAt:        10_000,
	}
	exact := TaskOnsetPrediction{
		Task:              "Draft a source-backed pull request review",
		DesiredResult:     "A source-backed review summary is ready",
		InferredStartedAt: 10_000,
		TaskConfidence:    0.95,
	}
	single, known := workPatternEvidencePredictionMetrics(
		TaskOnsetResponse{Episodes: []TaskOnsetPrediction{exact}},
		observed,
	)
	if !known || single.OnsetPrecision != 1 || single.CompositeScore <= 0 {
		t.Fatalf("single prediction metrics=%+v known=%v", single, known)
	}
	shotgun, known := workPatternEvidencePredictionMetrics(
		TaskOnsetResponse{Episodes: []TaskOnsetPrediction{
			exact,
			{
				Task:              "Check notifications",
				DesiredResult:     "Notifications are reviewed",
				InferredStartedAt: 11_000,
				TaskConfidence:    0.80,
			},
		}},
		observed,
	)
	if !known || shotgun.PredictionCount != 2 ||
		shotgun.OnsetPrecision != 0.5 ||
		shotgun.CompositeScore >= single.CompositeScore {
		t.Fatalf(
			"shotgun prediction escaped false-positive penalty: single=%+v shotgun=%+v",
			single,
			shotgun,
		)
	}
}

func TestWorkPatternAblationWinnerFollowsIndependentObservedEvidence(
	t *testing.T,
) {
	windowStart := int64(10_000)
	run := WorkPatternProfileAblationRun{
		NeutralResponse: TaskOnsetResponse{Episodes: []TaskOnsetPrediction{{
			Task:              "Check notifications",
			DesiredResult:     "Notifications are reviewed",
			InferredStartedAt: windowStart,
			TaskConfidence:    0.90,
		}}},
		ProfileResponse: TaskOnsetResponse{Episodes: []TaskOnsetPrediction{{
			Task:              "Draft a source-backed pull request review",
			DesiredResult:     "A source-backed review summary is ready",
			InferredStartedAt: windowStart,
			TaskConfidence:    0.90,
		}}},
	}
	reviewEvidence := workPatternProfileAblationObservedReference{
		EvidenceStrength: workPatternProfileAblationEvidenceActionResult,
		BehaviorText:     "Draft a source-backed pull request review",
		ResultText:       "A source-backed review summary is ready",
		StartedAt:        windowStart,
	}
	_, reviewNeutral, reviewProfile, _, _, reviewWinner :=
		workPatternProfileAblationMetricsForEvidence(run, reviewEvidence)
	if reviewWinner != WorkPatternInfluenceProfile || reviewNeutral == nil ||
		reviewProfile == nil || *reviewProfile <= *reviewNeutral {
		t.Fatalf(
			"review evidence did not select profile: neutral=%v profile=%v winner=%s",
			reviewNeutral,
			reviewProfile,
			reviewWinner,
		)
	}
	notificationEvidence := workPatternProfileAblationObservedReference{
		EvidenceStrength: workPatternProfileAblationEvidenceActionResult,
		BehaviorText:     "Check notifications",
		ResultText:       "Notifications are reviewed",
		StartedAt:        windowStart,
	}
	_, notificationNeutral, notificationProfile, _, _, notificationWinner :=
		workPatternProfileAblationMetricsForEvidence(run, notificationEvidence)
	if notificationWinner != WorkPatternInfluenceNeutral ||
		notificationNeutral == nil || notificationProfile == nil ||
		*notificationNeutral <= *notificationProfile {
		t.Fatalf(
			"observed evidence did not reverse winner: neutral=%v profile=%v winner=%s",
			notificationNeutral,
			notificationProfile,
			notificationWinner,
		)
	}
}

func TestWorkPatternIndependentObservedSourcesAreAllowlisted(t *testing.T) {
	for _, sourceKind := range []string{
		"activity_event",
		"terminal_event",
		"accessibility_event",
		"browser_event",
	} {
		if !workPatternIndependentObservedSource(sourceKind) {
			t.Fatalf("independent source %q was rejected", sourceKind)
		}
	}
	for _, sourceKind := range []string{
		"shadow_output",
		"memory",
		"knowledge",
		"self_model",
		"task_hypothesis",
	} {
		if workPatternIndependentObservedSource(sourceKind) {
			t.Fatalf("derived source %q was accepted", sourceKind)
		}
	}
}

func TestWorkPatternPredictionMetricsExposeSplitAndCalibration(t *testing.T) {
	observed := ShadowTaskEvaluationWork{
		Task:          "Review pull request patch",
		DesiredResult: "Write review findings",
		Actions:       []string{"Review patch and write findings"},
		Result:        "Review findings written",
		StartedAt:     10_000,
	}
	response := TaskOnsetResponse{Episodes: []TaskOnsetPrediction{
		{
			Task:              "Review pull request patch",
			DesiredResult:     "Write review findings",
			InferredStartedAt: 10_000,
			TaskConfidence:    0.95,
		},
		{
			Task:              "Review the pull request",
			DesiredResult:     "Write findings",
			InferredStartedAt: 10_500,
			TaskConfidence:    0.80,
		},
	}}

	metrics, known := workPatternPredictionMetrics(response, observed)
	if !known {
		t.Fatal("observed task was treated as unknown")
	}
	if metrics.PredictionCount != 2 ||
		metrics.MatchedPredictionCount != 2 ||
		metrics.OnsetPrecision != 1 ||
		metrics.OnsetRecall != 1 ||
		metrics.SplitErrorCount != 1 ||
		metrics.MergeErrorCount != 0 {
		t.Fatalf("onset metrics=%+v", metrics)
	}
	if metrics.BoundaryErrorMS == nil || *metrics.BoundaryErrorMS != 0 ||
		metrics.ActionMatch == nil ||
		metrics.ResultMatch == nil ||
		metrics.CalibrationError == nil ||
		metrics.CompositeScore <= 0 {
		t.Fatalf("quality metrics=%+v", metrics)
	}
}

func TestWorkPatternPredictionMetricsTreatNoPredictionAsZero(t *testing.T) {
	metrics, known := workPatternPredictionMetrics(
		TaskOnsetResponse{},
		ShadowTaskEvaluationWork{
			Task:          "Prepare launch note",
			DesiredResult: "A launch note exists",
		},
	)
	if !known {
		t.Fatal("known observed work was treated as unavailable")
	}
	if metrics.PredictionCount != 0 ||
		metrics.OnsetPrecision != 0 ||
		metrics.OnsetRecall != 0 ||
		metrics.CompositeScore != 0 {
		t.Fatalf("no-prediction metrics=%+v", metrics)
	}
}
