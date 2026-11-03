package main

import (
	"errors"
	"math"
	"time"
)

const (
	workPatternRolloutMinimumScoredPairs = 20
	workPatternRolloutMinimumImprovement = 0.03
	workPatternRolloutMinimumWinRate     = 0.55
	workPatternRolloutMaximumUnknownRate = 0.20
	workPatternRolloutMaximumCorrection  = 0.10
)

type WorkPatternProfileRolloutStatus struct {
	InfluenceEnabled        bool    `json:"influenceEnabled"`
	AblationRunCount        int     `json:"ablationRunCount"`
	EvaluationCount         int     `json:"evaluationCount"`
	ScoredPairCount         int     `json:"scoredPairCount"`
	ProfileWins             int     `json:"profileWins"`
	NeutralWins             int     `json:"neutralWins"`
	Ties                    int     `json:"ties"`
	Unknown                 int     `json:"unknown"`
	CorrectionCount         int     `json:"correctionCount"`
	IntegrityErrorCount     int     `json:"integrityErrorCount"`
	AverageImprovement      float64 `json:"averageImprovement"`
	ProfileWinRate          float64 `json:"profileWinRate"`
	UnknownRate             float64 `json:"unknownRate"`
	CorrectionRate          float64 `json:"correctionRate"`
	MinimumScoredPairs      int     `json:"minimumScoredPairs"`
	MinimumImprovement      float64 `json:"minimumImprovement"`
	MinimumProfileWinRate   float64 `json:"minimumProfileWinRate"`
	MaximumUnknownRate      float64 `json:"maximumUnknownRate"`
	MaximumCorrectionRate   float64 `json:"maximumCorrectionRate"`
	MeetsEvidenceGate       bool    `json:"meetsEvidenceGate"`
	RecommendedDecision     string  `json:"recommendedDecision"`
	Reason                  string  `json:"reason"`
	PolicyVersion           string  `json:"policyVersion"`
	CollectionAlwaysEnabled bool    `json:"collectionAlwaysEnabled"`
}

func roundWorkPatternRolloutMetric(value float64) float64 {
	return math.Round(value*10_000) / 10_000
}

func buildWorkPatternProfileRolloutStatus(
	influenceEnabled bool,
	runCount int,
	evaluations []WorkPatternProfileAblationEvaluation,
	correctionCount int,
) WorkPatternProfileRolloutStatus {
	result := WorkPatternProfileRolloutStatus{
		InfluenceEnabled:        influenceEnabled,
		AblationRunCount:        runCount,
		EvaluationCount:         len(evaluations),
		MinimumScoredPairs:      workPatternRolloutMinimumScoredPairs,
		MinimumImprovement:      workPatternRolloutMinimumImprovement,
		MinimumProfileWinRate:   workPatternRolloutMinimumWinRate,
		MaximumUnknownRate:      workPatternRolloutMaximumUnknownRate,
		MaximumCorrectionRate:   workPatternRolloutMaximumCorrection,
		CorrectionCount:         max(correctionCount, 0),
		RecommendedDecision:     "hold",
		Reason:                  "more independently observed outcomes are required",
		PolicyVersion:           workPatternProfileAblationPolicyVersion,
		CollectionAlwaysEnabled: true,
	}
	totalImprovement := 0.0
	for _, evaluation := range evaluations {
		switch evaluation.Winner {
		case WorkPatternInfluenceProfile:
			result.ProfileWins++
		case WorkPatternInfluenceNeutral:
			result.NeutralWins++
		case "tie":
			result.Ties++
		default:
			result.Unknown++
		}
		if evaluation.NeutralScore != nil && evaluation.ProfileScore != nil {
			result.ScoredPairCount++
			totalImprovement += *evaluation.ProfileScore - *evaluation.NeutralScore
		}
	}
	if result.ScoredPairCount > 0 {
		result.AverageImprovement = roundWorkPatternRolloutMetric(
			totalImprovement / float64(result.ScoredPairCount),
		)
	}
	decisive := result.ProfileWins + result.NeutralWins
	if decisive > 0 {
		result.ProfileWinRate = roundWorkPatternRolloutMetric(
			float64(result.ProfileWins) / float64(decisive),
		)
	}
	if result.EvaluationCount > 0 {
		result.UnknownRate = roundWorkPatternRolloutMetric(
			float64(result.Unknown) / float64(result.EvaluationCount),
		)
	}
	correctionOpportunities := result.EvaluationCount + result.CorrectionCount
	if correctionOpportunities > 0 {
		result.CorrectionRate = roundWorkPatternRolloutMetric(
			float64(result.CorrectionCount) /
				float64(correctionOpportunities),
		)
	}
	switch {
	case result.ScoredPairCount < result.MinimumScoredPairs:
		result.Reason = "minimum paired outcome evidence has not been reached"
	case result.AverageImprovement < result.MinimumImprovement:
		result.Reason = "profile-aware matching has not improved enough over neutral"
	case result.ProfileWinRate < result.MinimumProfileWinRate:
		result.Reason = "profile-aware predictions do not win consistently enough"
	case result.UnknownRate > result.MaximumUnknownRate:
		result.Reason = "too many paired outcomes remain unscorable"
	case result.CorrectionRate > result.MaximumCorrectionRate:
		result.Reason = "explicit profile correction rate remains too high"
	default:
		result.MeetsEvidenceGate = true
		result.RecommendedDecision = "eligible_for_manual_rollout"
		result.Reason = "paired outcome evidence meets the manual rollout gate"
	}
	if influenceEnabled && !result.MeetsEvidenceGate {
		result.RecommendedDecision = "disable_and_collect"
	}
	return result
}

func listAllWorkPatternProfileAblationEvaluationsAt(
	store *ActivityStore,
	cutoffAt int64,
) ([]WorkPatternProfileAblationEvaluation, error) {
	rows, err := store.db.Query(`
		SELECT
			id, ablation_run_id, task_episode_id, shadow_evaluation_id,
			policy_version, neutral_score, profile_score,
			neutral_boundary_error_ms, profile_boundary_error_ms,
			winner, evidence_json, created_at
		FROM work_pattern_profile_ablation_evaluations_v1
		WHERE created_at <= ?
		ORDER BY created_at ASC, id ASC`, cutoffAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkPatternProfileAblationEvaluation, 0)
	for rows.Next() {
		item, err := scanWorkPatternProfileAblationEvaluation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (a *App) workPatternProfileRolloutStatusAt(
	cutoffAt int64,
) (
	WorkPatternProfileRolloutStatus,
	error,
) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return WorkPatternProfileRolloutStatus{}, errors.New(
			"activity store is unavailable",
		)
	}
	if cutoffAt <= 0 {
		cutoffAt = time.Now().UnixMilli()
	}
	store := a.activity.store
	evaluations, err := listAllWorkPatternProfileAblationEvaluationsAt(
		store,
		cutoffAt,
	)
	if err != nil {
		return WorkPatternProfileRolloutStatus{}, err
	}
	corrections, err := store.ListWorkPatternProfileCorrections(cutoffAt)
	if err != nil {
		return WorkPatternProfileRolloutStatus{}, err
	}
	currentEvaluations := make(
		[]WorkPatternProfileAblationEvaluation,
		0,
		len(evaluations),
	)
	currentRuns := make(map[string]struct{})
	integrityErrors := 0
	for _, evaluation := range evaluations {
		if evaluation.PolicyVersion != workPatternProfileAblationPolicyVersion {
			continue
		}
		if evaluation.Metrics.Version != workPatternProfileAblationPolicyVersion ||
			evaluation.Metrics.ObservedReferenceVersion !=
				workPatternProfileAblationObservedReferenceVersion {
			integrityErrors++
			continue
		}
		detail, detailErr := getShadowLearningInfluenceEpisodeDetailAt(
			store,
			evaluation.TaskEpisodeID,
			cutoffAt,
		)
		if detailErr != nil {
			integrityErrors++
			continue
		}
		selection := workPatternProfileSelectionFromEpisodeDetail(detail)
		if _, _, _, validateErr := shadowLearningInfluenceValidateAblation(
			store,
			detail.Episode,
			selection,
			evaluation,
			cutoffAt,
		); validateErr != nil {
			integrityErrors++
			continue
		}
		currentEvaluations = append(currentEvaluations, evaluation)
		currentRuns[evaluation.AblationRunID] = struct{}{}
	}
	result := buildWorkPatternProfileRolloutStatus(
		a.workPatternProfileInfluenceEnabled(),
		len(currentRuns),
		currentEvaluations,
		len(corrections),
	)
	result.IntegrityErrorCount = integrityErrors
	if integrityErrors > 0 {
		result.MeetsEvidenceGate = false
		result.RecommendedDecision = "hold"
		result.Reason = "current paired outcomes failed immutable provenance checks"
		if result.InfluenceEnabled {
			result.RecommendedDecision = "disable_and_collect"
		}
	}
	return result, nil
}

func (a *App) workPatternProfileRolloutStatus() (
	WorkPatternProfileRolloutStatus,
	error,
) {
	return a.workPatternProfileRolloutStatusAt(0)
}
