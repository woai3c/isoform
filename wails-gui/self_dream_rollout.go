package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type selfDreamRolloutEvent struct {
	ID        string                 `json:"id"`
	Enabled   bool                   `json:"enabled"`
	Reason    string                 `json:"reason"`
	Metrics   SelfDreamRolloutStatus `json:"metrics"`
	CreatedAt int64                  `json:"createdAt"`
}

func (s *ActivityStore) addSelfDreamRolloutEvent(
	enabled bool,
	reason string,
	metrics SelfDreamRolloutStatus,
) error {
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	_, err = s.db.Exec(`
		INSERT INTO self_dream_rollout_events_v1 (
			id, enabled, reason, metrics_json, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(),
		enabledValue,
		agencyBoundedText(reason, 1_000),
		string(metricsJSON),
		time.Now().UnixMilli(),
	)
	return err
}

func (s *ActivityStore) latestSelfDreamRolloutEvent() (
	selfDreamRolloutEvent,
	error,
) {
	var item selfDreamRolloutEvent
	var enabled int
	var metricsJSON string
	err := s.db.QueryRow(`
		SELECT id, enabled, reason, metrics_json, created_at
		FROM self_dream_rollout_events_v1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
	).Scan(
		&item.ID,
		&enabled,
		&item.Reason,
		&metricsJSON,
		&item.CreatedAt,
	)
	if err != nil {
		return selfDreamRolloutEvent{}, err
	}
	item.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(metricsJSON), &item.Metrics); err != nil {
		return selfDreamRolloutEvent{}, err
	}
	return item, nil
}

func (s *ActivityStore) SelfDreamRolloutStatus() (
	SelfDreamRolloutStatus,
	error,
) {
	if s == nil || s.db == nil {
		return SelfDreamRolloutStatus{}, errors.New(
			"activity store is unavailable",
		)
	}
	status := SelfDreamRolloutStatus{Reasons: []string{}}
	var improvementSum float64
	err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(evaluation.improvement), 0),
			COALESCE(SUM(CASE WHEN evaluation.coverage_loss > 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(evaluation.unsupported_reasoning), 0)
		FROM self_dream_evaluations_v1 evaluation
		WHERE evaluation.real_evidence = 1
			AND EXISTS (
				SELECT 1
				FROM self_dream_events_v1 event
				WHERE event.run_id = evaluation.run_id
					AND event.event_kind = 'completed'
			)`,
	).Scan(
		&status.CompletedRealRuns,
		&improvementSum,
		&status.CoverageLossCount,
		&status.UnsupportedReasoningCount,
	)
	if err != nil {
		return SelfDreamRolloutStatus{}, err
	}
	if status.CompletedRealRuns > 0 {
		status.MeanImprovement = improvementSum /
			float64(status.CompletedRealRuns)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM self_dream_operations_v1 operation
		JOIN self_dream_evaluations_v1 evaluation
			ON evaluation.run_id = operation.run_id
		WHERE evaluation.real_evidence = 1
			AND EXISTS (
				SELECT 1
				FROM self_dream_events_v1 event
				WHERE event.run_id = evaluation.run_id
					AND event.event_kind = 'completed'
			)
			AND operation.validation_status IN ('qualified', 'review_required')`,
	).Scan(&status.ScoredCandidates); err != nil {
		return SelfDreamRolloutStatus{}, err
	}
	var decisions, negative int
	if err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN latest.decision_kind IN (
				'keep_separate', 'not_enough_evidence', 'correct'
			) THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT decision.decision_kind
			FROM self_dream_decisions_v1 decision
			JOIN self_dream_operations_v1 operation
				ON operation.id = decision.operation_id
			JOIN self_dream_evaluations_v1 evaluation
				ON evaluation.run_id = operation.run_id
			WHERE decision.id = (
				SELECT candidate.id
				FROM self_dream_decisions_v1 candidate
				WHERE candidate.operation_id = decision.operation_id
				ORDER BY candidate.created_at DESC, candidate.id DESC
				LIMIT 1
			)
				AND evaluation.real_evidence = 1
				AND EXISTS (
					SELECT 1
					FROM self_dream_events_v1 event
					WHERE event.run_id = evaluation.run_id
						AND event.event_kind = 'completed'
				)
		) latest`,
	).Scan(&decisions, &negative); err != nil {
		return SelfDreamRolloutStatus{}, err
	}
	if decisions > 0 {
		status.CorrectionOrRejectRate = float64(negative) / float64(decisions)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM self_dream_events_v1
		WHERE event_kind = 'promotion_skipped'
			AND lower(detail_json) LIKE '%stale%'`,
	).Scan(&status.StaleCompilationErrors); err != nil {
		return SelfDreamRolloutStatus{}, err
	}
	if status.CompletedRealRuns < 20 {
		status.Reasons = append(status.Reasons, fmt.Sprintf(
			"Need %d more completed real shadow evaluations.",
			20-status.CompletedRealRuns,
		))
	}
	if status.MeanImprovement < 0.03 {
		status.Reasons = append(
			status.Reasons,
			"Mean paired improvement is below 0.03.",
		)
	}
	if status.CoverageLossCount > 0 {
		status.Reasons = append(
			status.Reasons,
			"At least one evaluation lost evidence coverage.",
		)
	}
	if status.UnsupportedReasoningCount > 0 {
		status.Reasons = append(
			status.Reasons,
			"At least one evaluation contained unsupported reasoning.",
		)
	}
	if status.CorrectionOrRejectRate > 0.10 {
		status.Reasons = append(
			status.Reasons,
			"Correction or rejection rate is above 10%.",
		)
	}
	if status.StaleCompilationErrors > 0 {
		status.Reasons = append(
			status.Reasons,
			"Stale compilation errors require review.",
		)
	}
	status.Ready = len(status.Reasons) == 0
	event, err := s.latestSelfDreamRolloutEvent()
	if err == nil {
		status.Enabled = event.Enabled && status.Ready
		status.UpdatedAt = event.CreatedAt
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SelfDreamRolloutStatus{}, err
	}
	if !status.Enabled {
		status.Reasons = append(
			status.Reasons,
			"Automatic promotion is disabled.",
		)
	}
	return status, nil
}

func (a *App) autoPromoteSelfDreamOperations(
	run SelfDreamRunRecord,
	operations []SelfDreamOperationRecord,
	evaluation SelfDreamEvaluationRecord,
) error {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return errors.New("activity store is unavailable")
	}
	store := a.activity.store
	rollout, err := store.SelfDreamRolloutStatus()
	if err != nil {
		return err
	}
	if !rollout.Enabled || !rollout.Ready {
		return nil
	}
	if evaluation.CoverageLoss > 0 ||
		evaluation.UnsupportedReasoning > 0 ||
		evaluation.ContradictionRisk > 0 {
		a.appendSelfDreamEvent(run.ID, "promotion_skipped", map[string]any{
			"reason": "run_failed_promotion_safety_gate",
		})
		return nil
	}
	for _, operation := range operations {
		if !operation.AutoEligible ||
			operation.ValidationStatus != SelfDreamValidationQualified {
			continue
		}
		decision, added, err := store.AddSelfDreamDecision(
			SelfDreamDecisionRecord{
				OperationID: operation.ID,
				IdempotencyKey: strings.Join([]string{
					"self-dream-auto",
					SelfDreamPolicyVersion,
					operation.OperationHash,
				}, ":"),
				ActorKind: "rollout_gate",
				Decision:  SelfDreamDecisionAutoPromote,
				Note: "Automatic exact-duplicate merge after the real paired " +
					"rollout gate cleared.",
				CreatedAt: time.Now().UnixMilli(),
			},
		)
		if err != nil {
			return err
		}
		if !added {
			continue
		}
		if _, err := store.compileSelfDreamOperation(
			operation,
			decision,
		); err != nil {
			return err
		}
	}
	return nil
}

// setSelfDreamRollout is deliberately unexported. Product and Dev Tools cannot
// enable automatic promotion; release engineering must append an audited
// rollout event after reviewing real paired evidence.
func (s *ActivityStore) setSelfDreamRollout(
	enabled bool,
	reason string,
) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("Self Dream rollout reason is required")
	}
	metrics, err := s.SelfDreamRolloutStatus()
	if err != nil {
		return err
	}
	if enabled && !metrics.Ready {
		return errors.New("Self Dream rollout gate is not ready")
	}
	return s.addSelfDreamRolloutEvent(enabled, reason, metrics)
}
