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

func selfDreamMarshal(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func scanSelfDreamRun(
	scanner interface{ Scan(...any) error },
) (SelfDreamRunRecord, error) {
	var item SelfDreamRunRecord
	var baseJSON, contextJSON, manifestJSON string
	err := scanner.Scan(
		&item.ID,
		&item.IdempotencyKey,
		&item.TriggerKind,
		&item.PolicyVersion,
		&item.AssemblerVersion,
		&item.SourceCutoffAt,
		&baseJSON,
		&item.BaseSelfHash,
		&contextJSON,
		&item.ContextHash,
		&manifestJSON,
		&item.InputHash,
		&item.PromptVersion,
		&item.Prompt,
		&item.PromptHash,
		&item.RequestedModel,
		&item.CreatedAt,
	)
	if err != nil {
		return SelfDreamRunRecord{}, err
	}
	if err := json.Unmarshal([]byte(baseJSON), &item.BaseSelf); err != nil {
		return SelfDreamRunRecord{}, fmt.Errorf(
			"decode Self Dream base Self: %w",
			err,
		)
	}
	if err := json.Unmarshal([]byte(contextJSON), &item.Context); err != nil {
		return SelfDreamRunRecord{}, fmt.Errorf(
			"decode Self Dream context: %w",
			err,
		)
	}
	if err := json.Unmarshal([]byte(manifestJSON), &item.Manifest); err != nil {
		return SelfDreamRunRecord{}, fmt.Errorf(
			"decode Self Dream manifest: %w",
			err,
		)
	}
	return item, nil
}

const selfDreamRunColumns = `
	id, idempotency_key, trigger_kind, policy_version, assembler_version,
	source_cutoff_at, base_self_json, base_self_hash, context_json, context_hash,
	manifest_json, input_hash, prompt_version, prompt, prompt_hash,
	requested_model, created_at`

func (s *ActivityStore) AddSelfDreamRun(
	item SelfDreamRunRecord,
) (SelfDreamRunRecord, bool, error) {
	memoryMutationMu.RLock()
	defer memoryMutationMu.RUnlock()
	if s == nil || s.db == nil {
		return SelfDreamRunRecord{}, false, errors.New(
			"activity store is unavailable",
		)
	}
	for _, source := range item.Manifest {
		if !source.Included {
			continue
		}
		if err := validateMemoryForgetSource(s.db, source.SourceKind, source.SourceID, "", source.Ref); err != nil {
			return SelfDreamRunRecord{}, false, err
		}
	}
	for _, source := range item.BaseSelf {
		if err := validateMemoryForgetSource(s.db, taskContextSourceSelfKind, source.ID, ""); err != nil {
			return SelfDreamRunRecord{}, false, err
		}
	}
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.IdempotencyKey = strings.TrimSpace(item.IdempotencyKey)
	if item.IdempotencyKey == "" {
		return SelfDreamRunRecord{}, false, errors.New(
			"Self Dream idempotency key is required",
		)
	}
	if item.CreatedAt <= 0 {
		item.CreatedAt = time.Now().UnixMilli()
	}
	baseJSON, err := selfDreamMarshal(item.BaseSelf)
	if err != nil {
		return SelfDreamRunRecord{}, false, err
	}
	contextJSON, err := selfDreamMarshal(item.Context)
	if err != nil {
		return SelfDreamRunRecord{}, false, err
	}
	manifestJSON, err := selfDreamMarshal(item.Manifest)
	if err != nil {
		return SelfDreamRunRecord{}, false, err
	}
	result, err := s.db.Exec(`
		INSERT OR IGNORE INTO self_dream_runs_v1 (
			`+selfDreamRunColumns+`
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.IdempotencyKey,
		item.TriggerKind,
		item.PolicyVersion,
		item.AssemblerVersion,
		item.SourceCutoffAt,
		baseJSON,
		item.BaseSelfHash,
		contextJSON,
		item.ContextHash,
		manifestJSON,
		item.InputHash,
		item.PromptVersion,
		item.Prompt,
		item.PromptHash,
		item.RequestedModel,
		item.CreatedAt,
	)
	if err != nil {
		return SelfDreamRunRecord{}, false, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		existing, err := s.GetSelfDreamRunByIdempotency(item.IdempotencyKey)
		return existing, false, err
	}
	return item, true, nil
}

func (s *ActivityStore) GetSelfDreamRun(
	runID string,
) (SelfDreamRunRecord, error) {
	if s == nil || s.db == nil {
		return SelfDreamRunRecord{}, errors.New("activity store is unavailable")
	}
	return scanSelfDreamRun(s.db.QueryRow(`
		SELECT `+selfDreamRunColumns+`
		FROM self_dream_runs_v1
		WHERE id = ?`,
		strings.TrimSpace(runID),
	))
}

func (s *ActivityStore) GetSelfDreamRunByIdempotency(
	key string,
) (SelfDreamRunRecord, error) {
	return scanSelfDreamRun(s.db.QueryRow(`
		SELECT `+selfDreamRunColumns+`
		FROM self_dream_runs_v1
		WHERE idempotency_key = ?`,
		strings.TrimSpace(key),
	))
}

func (s *ActivityStore) GetCompletedSelfDreamRunByInput(
	policyVersion, inputHash string,
) (SelfDreamRunRecord, error) {
	return scanSelfDreamRun(s.db.QueryRow(`
		SELECT `+selfDreamRunColumns+`
		FROM self_dream_runs_v1 run
		WHERE run.policy_version = ?
			AND run.input_hash = ?
			AND EXISTS (
				SELECT 1
				FROM self_dream_events_v1 event
				WHERE event.run_id = run.id
					AND event.event_kind = 'completed'
			)
		ORDER BY run.created_at DESC, run.id DESC
		LIMIT 1`,
		strings.TrimSpace(policyVersion),
		strings.TrimSpace(inputHash),
	))
}

func (s *ActivityStore) AddSelfDreamEvent(
	item SelfDreamEventRecord,
) (SelfDreamEventRecord, error) {
	if s == nil || s.db == nil {
		return SelfDreamEventRecord{}, errors.New(
			"activity store is unavailable",
		)
	}
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.RunID = strings.TrimSpace(item.RunID)
	if item.RunID == "" {
		return SelfDreamEventRecord{}, errors.New("Self Dream run ID is required")
	}
	if strings.TrimSpace(item.DetailJSON) == "" {
		item.DetailJSON = "{}"
	}
	if !json.Valid([]byte(item.DetailJSON)) {
		return SelfDreamEventRecord{}, errors.New(
			"Self Dream event detail must be valid JSON",
		)
	}
	item.CreatedAt = selfDreamNow(item.CreatedAt)
	_, err := s.db.Exec(`
		INSERT INTO self_dream_events_v1 (
			id, run_id, event_kind, detail_json, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		item.ID,
		item.RunID,
		item.EventKind,
		item.DetailJSON,
		item.CreatedAt,
	)
	return item, err
}

func (s *ActivityStore) ListSelfDreamEvents(
	runID string,
) ([]SelfDreamEventRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, event_kind, detail_json, created_at
		FROM self_dream_events_v1
		WHERE run_id = ?
		ORDER BY created_at ASC, id ASC`,
		strings.TrimSpace(runID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SelfDreamEventRecord, 0)
	for rows.Next() {
		var item SelfDreamEventRecord
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.EventKind,
			&item.DetailJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanSelfDreamAttempt(
	scanner interface{ Scan(...any) error },
) (SelfDreamAttemptRecord, error) {
	var item SelfDreamAttemptRecord
	var learningEligible, productMutation int
	err := scanner.Scan(
		&item.ID,
		&item.RunID,
		&item.AttemptKind,
		&item.Status,
		&item.SafetyMode,
		&learningEligible,
		&productMutation,
		&item.Model,
		&item.PromptHash,
		&item.RawOutput,
		&item.RawOutputHash,
		&item.Error,
		&item.DurationMs,
		&item.CreatedAt,
	)
	item.LearningEligible = learningEligible != 0
	item.ProductMutation = productMutation != 0
	return item, err
}

const selfDreamAttemptColumns = `
	id, run_id, attempt_kind, status, safety_mode, learning_eligible,
	product_mutation, model, prompt_hash, raw_output, raw_output_hash,
	error, duration_ms, created_at`

func (s *ActivityStore) AddSelfDreamAttempt(
	item SelfDreamAttemptRecord,
) (SelfDreamAttemptRecord, error) {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.AttemptKind = "production_shadow"
	item.SafetyMode = SelfDreamSafetyMode
	item.LearningEligible = false
	item.ProductMutation = false
	item.RawOutput = agencyBoundedText(
		item.RawOutput,
		selfDreamRawOutputMaxChars,
	)
	item.Error = agencyBoundedText(item.Error, 2_000)
	item.CreatedAt = selfDreamNow(item.CreatedAt)
	if item.DurationMs < 0 {
		item.DurationMs = 0
	}
	_, err := s.db.Exec(`
		INSERT INTO self_dream_attempts_v1 (
			`+selfDreamAttemptColumns+`
		) VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.RunID,
		item.AttemptKind,
		item.Status,
		item.SafetyMode,
		item.Model,
		item.PromptHash,
		item.RawOutput,
		item.RawOutputHash,
		item.Error,
		item.DurationMs,
		item.CreatedAt,
	)
	if err != nil {
		return SelfDreamAttemptRecord{}, err
	}
	return item, nil
}

func (s *ActivityStore) GetSelfDreamAttempt(
	runID string,
) (SelfDreamAttemptRecord, error) {
	return scanSelfDreamAttempt(s.db.QueryRow(`
		SELECT `+selfDreamAttemptColumns+`
		FROM self_dream_attempts_v1
		WHERE run_id = ? AND attempt_kind = 'production_shadow'`,
		strings.TrimSpace(runID),
	))
}

func scanSelfDreamOperation(
	scanner interface{ Scan(...any) error },
) (SelfDreamOperationRecord, error) {
	var item SelfDreamOperationRecord
	var targetJSON, splitJSON, evidenceJSON string
	var autoEligible int
	err := scanner.Scan(
		&item.ID,
		&item.RunID,
		&item.AttemptID,
		&item.Ordinal,
		&item.Operation,
		&targetJSON,
		&item.SelfKind,
		&item.Scope.Kind,
		&item.Scope.Key,
		&item.Statement,
		&splitJSON,
		&item.ReasoningStatus,
		&item.Rationale,
		&item.Uncertainty,
		&item.Confidence,
		&evidenceJSON,
		&item.Source,
		&item.ValidationStatus,
		&item.ValidationReason,
		&autoEligible,
		&item.OperationHash,
		&item.CreatedAt,
	)
	if err != nil {
		return SelfDreamOperationRecord{}, err
	}
	item.AutoEligible = autoEligible != 0
	if err := json.Unmarshal([]byte(targetJSON), &item.TargetEntryIDs); err != nil {
		return SelfDreamOperationRecord{}, err
	}
	if err := json.Unmarshal([]byte(splitJSON), &item.SplitStatements); err != nil {
		return SelfDreamOperationRecord{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &item.EvidenceRefs); err != nil {
		return SelfDreamOperationRecord{}, err
	}
	return item, nil
}

const selfDreamOperationColumns = `
	id, run_id, attempt_id, ordinal, operation_kind, target_entry_ids_json,
	self_kind, scope_kind, scope_key, statement, split_statements_json,
	reasoning_status, rationale, uncertainty, confidence, evidence_refs_json,
	source, validation_status, validation_reason, auto_eligible, operation_hash,
	created_at`

func (s *ActivityStore) AddSelfDreamOperations(
	items []SelfDreamOperationRecord,
) ([]SelfDreamOperationRecord, error) {
	if len(items) == 0 {
		return []SelfDreamOperationRecord{}, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for index := range items {
		item := &items[index]
		if item.ID == "" {
			item.ID = deterministicAuthorityID(
				"self-dream-operation",
				item.RunID+"\x00"+item.OperationHash,
			)
		}
		item.Ordinal = index
		targetJSON, err := selfDreamMarshal(item.TargetEntryIDs)
		if err != nil {
			return nil, err
		}
		splitJSON, err := selfDreamMarshal(item.SplitStatements)
		if err != nil {
			return nil, err
		}
		evidenceJSON, err := selfDreamMarshal(item.EvidenceRefs)
		if err != nil {
			return nil, err
		}
		autoEligible := 0
		if item.AutoEligible {
			autoEligible = 1
		}
		_, err = tx.Exec(`
			INSERT INTO self_dream_operations_v1 (
				`+selfDreamOperationColumns+`
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.RunID,
			item.AttemptID,
			item.Ordinal,
			item.Operation,
			targetJSON,
			item.SelfKind,
			item.Scope.Kind,
			item.Scope.Key,
			item.Statement,
			splitJSON,
			item.ReasoningStatus,
			item.Rationale,
			item.Uncertainty,
			item.Confidence,
			evidenceJSON,
			item.Source,
			item.ValidationStatus,
			item.ValidationReason,
			autoEligible,
			item.OperationHash,
			item.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *ActivityStore) GetSelfDreamOperation(
	operationID string,
) (SelfDreamOperationRecord, error) {
	return scanSelfDreamOperation(s.db.QueryRow(`
		SELECT `+selfDreamOperationColumns+`
		FROM self_dream_operations_v1
		WHERE id = ?`,
		strings.TrimSpace(operationID),
	))
}

func (s *ActivityStore) ListSelfDreamOperations(
	runID string,
) ([]SelfDreamOperationRecord, error) {
	rows, err := s.db.Query(`
		SELECT `+selfDreamOperationColumns+`
		FROM self_dream_operations_v1
		WHERE run_id = ?
		ORDER BY ordinal ASC`,
		strings.TrimSpace(runID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SelfDreamOperationRecord, 0)
	for rows.Next() {
		item, err := scanSelfDreamOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanSelfDreamEvaluation(
	scanner interface{ Scan(...any) error },
) (SelfDreamEvaluationRecord, error) {
	var item SelfDreamEvaluationRecord
	var neutralJSON, dreamJSON string
	var realEvidence int
	err := scanner.Scan(
		&item.ID,
		&item.RunID,
		&item.EvaluationVersion,
		&neutralJSON,
		&dreamJSON,
		&item.Improvement,
		&item.CoverageLoss,
		&item.ContradictionRisk,
		&item.UnsupportedReasoning,
		&realEvidence,
		&item.CreatedAt,
	)
	if err != nil {
		return SelfDreamEvaluationRecord{}, err
	}
	item.RealEvidence = realEvidence != 0
	if err := json.Unmarshal([]byte(neutralJSON), &item.Neutral); err != nil {
		return SelfDreamEvaluationRecord{}, err
	}
	if err := json.Unmarshal([]byte(dreamJSON), &item.Dream); err != nil {
		return SelfDreamEvaluationRecord{}, err
	}
	return item, nil
}

func (s *ActivityStore) AddSelfDreamEvaluation(
	item SelfDreamEvaluationRecord,
) (SelfDreamEvaluationRecord, error) {
	neutralJSON, err := selfDreamMarshal(item.Neutral)
	if err != nil {
		return SelfDreamEvaluationRecord{}, err
	}
	dreamJSON, err := selfDreamMarshal(item.Dream)
	if err != nil {
		return SelfDreamEvaluationRecord{}, err
	}
	realEvidence := 0
	if item.RealEvidence {
		realEvidence = 1
	}
	item.CreatedAt = selfDreamNow(item.CreatedAt)
	_, err = s.db.Exec(`
		INSERT INTO self_dream_evaluations_v1 (
			id, run_id, evaluation_version, neutral_json, dream_json,
			improvement, coverage_loss, contradiction_risk,
			unsupported_reasoning, real_evidence, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.RunID,
		item.EvaluationVersion,
		neutralJSON,
		dreamJSON,
		item.Improvement,
		item.CoverageLoss,
		item.ContradictionRisk,
		item.UnsupportedReasoning,
		realEvidence,
		item.CreatedAt,
	)
	return item, err
}

func (s *ActivityStore) GetSelfDreamEvaluation(
	runID string,
) (SelfDreamEvaluationRecord, error) {
	return scanSelfDreamEvaluation(s.db.QueryRow(`
		SELECT
			id, run_id, evaluation_version, neutral_json, dream_json,
			improvement, coverage_loss, contradiction_risk,
			unsupported_reasoning, real_evidence, created_at
		FROM self_dream_evaluations_v1
		WHERE run_id = ? AND evaluation_version = ?`,
		strings.TrimSpace(runID),
		SelfDreamEvaluationVersion,
	))
}

func (s *ActivityStore) AddSelfDreamDecision(
	item SelfDreamDecisionRecord,
) (SelfDreamDecisionRecord, bool, error) {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.IdempotencyKey = strings.TrimSpace(item.IdempotencyKey)
	if item.IdempotencyKey == "" {
		return SelfDreamDecisionRecord{}, false, errors.New(
			"Self Dream decision idempotency key is required",
		)
	}
	item.Note = agencyBoundedText(item.Note, 4_000)
	item.CreatedAt = selfDreamNow(item.CreatedAt)
	result, err := s.db.Exec(`
		INSERT OR IGNORE INTO self_dream_decisions_v1 (
			id, operation_id, idempotency_key, actor_kind, decision_kind,
			note, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.OperationID,
		item.IdempotencyKey,
		item.ActorKind,
		item.Decision,
		item.Note,
		item.CreatedAt,
	)
	if err != nil {
		return SelfDreamDecisionRecord{}, false, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		existing, err := s.GetSelfDreamDecisionByIdempotency(
			item.IdempotencyKey,
		)
		return existing, false, err
	}
	return item, true, nil
}

func scanSelfDreamDecision(
	scanner interface{ Scan(...any) error },
) (SelfDreamDecisionRecord, error) {
	var item SelfDreamDecisionRecord
	err := scanner.Scan(
		&item.ID,
		&item.OperationID,
		&item.IdempotencyKey,
		&item.ActorKind,
		&item.Decision,
		&item.Note,
		&item.CreatedAt,
	)
	return item, err
}

func (s *ActivityStore) GetSelfDreamDecisionByIdempotency(
	key string,
) (SelfDreamDecisionRecord, error) {
	return scanSelfDreamDecision(s.db.QueryRow(`
		SELECT
			id, operation_id, idempotency_key, actor_kind, decision_kind,
			note, created_at
		FROM self_dream_decisions_v1
		WHERE idempotency_key = ?`,
		strings.TrimSpace(key),
	))
}

func (s *ActivityStore) ListSelfDreamDecisions(
	runID string,
) ([]SelfDreamDecisionRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			decision.id, decision.operation_id, decision.idempotency_key,
			decision.actor_kind, decision.decision_kind, decision.note,
			decision.created_at
		FROM self_dream_decisions_v1 decision
		JOIN self_dream_operations_v1 operation
			ON operation.id = decision.operation_id
		WHERE operation.run_id = ?
		ORDER BY decision.created_at ASC, decision.id ASC`,
		strings.TrimSpace(runID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SelfDreamDecisionRecord, 0)
	for rows.Next() {
		item, err := scanSelfDreamDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanSelfDreamCompilation(
	scanner interface{ Scan(...any) error },
) (SelfDreamCompilationRecord, error) {
	var item SelfDreamCompilationRecord
	var sourceJSON, outputJSON string
	err := scanner.Scan(
		&item.ID,
		&item.OperationID,
		&item.DecisionID,
		&item.CompilationKind,
		&sourceJSON,
		&outputJSON,
		&item.CreatedAt,
	)
	if err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	if err := json.Unmarshal([]byte(sourceJSON), &item.SourceEntryIDs); err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	if err := json.Unmarshal([]byte(outputJSON), &item.OutputEntryIDs); err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	return item, nil
}

func (s *ActivityStore) GetSelfDreamCompilation(
	operationID string,
) (SelfDreamCompilationRecord, error) {
	return scanSelfDreamCompilation(s.db.QueryRow(`
		SELECT
			id, operation_id, decision_id, compilation_kind,
			source_entry_ids_json, output_entry_ids_json, created_at
		FROM self_dream_compilations_v1
		WHERE operation_id = ?`,
		strings.TrimSpace(operationID),
	))
}

func (s *ActivityStore) ListSelfDreamCompilations(
	runID string,
) ([]SelfDreamCompilationRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			compilation.id, compilation.operation_id, compilation.decision_id,
			compilation.compilation_kind, compilation.source_entry_ids_json,
			compilation.output_entry_ids_json, compilation.created_at
		FROM self_dream_compilations_v1 compilation
		JOIN self_dream_operations_v1 operation
			ON operation.id = compilation.operation_id
		WHERE operation.run_id = ?
		ORDER BY compilation.created_at ASC, compilation.id ASC`,
		strings.TrimSpace(runID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SelfDreamCompilationRecord, 0)
	for rows.Next() {
		item, err := scanSelfDreamCompilation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanSelfDreamReplay(
	scanner interface{ Scan(...any) error },
) (SelfDreamReplayRecord, error) {
	var item SelfDreamReplayRecord
	var learningEligible, productMutation, contextMatches int
	var operationsJSON string
	err := scanner.Scan(
		&item.ID,
		&item.RunID,
		&item.Status,
		&item.SafetyMode,
		&learningEligible,
		&productMutation,
		&item.ContextHash,
		&contextMatches,
		&item.PromptVersion,
		&item.Model,
		&item.PromptHash,
		&item.RawOutput,
		&item.RawOutputHash,
		&operationsJSON,
		&item.Error,
		&item.DurationMs,
		&item.CreatedAt,
	)
	if err != nil {
		return SelfDreamReplayRecord{}, err
	}
	item.LearningEligible = learningEligible != 0
	item.ProductMutation = productMutation != 0
	item.ContextMatchesSource = contextMatches != 0
	if err := json.Unmarshal(
		[]byte(operationsJSON),
		&item.Operations,
	); err != nil {
		return SelfDreamReplayRecord{}, err
	}
	return item, nil
}

const selfDreamReplayColumns = `
	id, run_id, status, safety_mode, learning_eligible, product_mutation,
	context_hash, context_matches_source, prompt_version, model, prompt_hash,
	raw_output, raw_output_hash, operations_json, error, duration_ms, created_at`

func (s *ActivityStore) AddSelfDreamReplay(
	item SelfDreamReplayRecord,
) (SelfDreamReplayRecord, error) {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.SafetyMode = SelfDreamSafetyMode
	item.LearningEligible = false
	item.ProductMutation = false
	item.RawOutput = agencyBoundedText(
		item.RawOutput,
		selfDreamRawOutputMaxChars,
	)
	item.Error = agencyBoundedText(item.Error, 2_000)
	item.CreatedAt = selfDreamNow(item.CreatedAt)
	operationsJSON, err := selfDreamMarshal(item.Operations)
	if err != nil {
		return SelfDreamReplayRecord{}, err
	}
	contextMatches := 0
	if item.ContextMatchesSource {
		contextMatches = 1
	}
	_, err = s.db.Exec(`
		INSERT INTO self_dream_replays_v1 (
			`+selfDreamReplayColumns+`
		) VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.RunID,
		item.Status,
		item.SafetyMode,
		item.ContextHash,
		contextMatches,
		item.PromptVersion,
		item.Model,
		item.PromptHash,
		item.RawOutput,
		item.RawOutputHash,
		operationsJSON,
		item.Error,
		item.DurationMs,
		item.CreatedAt,
	)
	if err != nil {
		return SelfDreamReplayRecord{}, err
	}
	return item, nil
}

func (s *ActivityStore) ListSelfDreamReplays(
	runID string,
	limit int,
) ([]SelfDreamReplayRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT `+selfDreamReplayColumns+`
		FROM self_dream_replays_v1
		WHERE run_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?`,
		strings.TrimSpace(runID),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SelfDreamReplayRecord, 0)
	for rows.Next() {
		item, err := scanSelfDreamReplay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ActivityStore) GetSelfDreamRunDetail(
	runID string,
) (SelfDreamRunDetail, error) {
	run, err := s.GetSelfDreamRun(runID)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	events, err := s.ListSelfDreamEvents(run.ID)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	detail := SelfDreamRunDetail{
		Run:          run,
		Status:       "created",
		Events:       events,
		Operations:   []SelfDreamOperationRecord{},
		Decisions:    []SelfDreamDecisionRecord{},
		Compilations: []SelfDreamCompilationRecord{},
		Replays:      []SelfDreamReplayRecord{},
	}
	for _, event := range events {
		if detail.Status == "created" {
			detail.Status = event.EventKind
		}
		if event.EventKind == "completed" || event.EventKind == "failed" {
			detail.Status = event.EventKind
		}
	}
	attempt, err := s.GetSelfDreamAttempt(run.ID)
	if err == nil {
		detail.Attempt = &attempt
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SelfDreamRunDetail{}, err
	}
	detail.Operations, err = s.ListSelfDreamOperations(run.ID)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	evaluation, err := s.GetSelfDreamEvaluation(run.ID)
	if err == nil {
		detail.Evaluation = &evaluation
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SelfDreamRunDetail{}, err
	}
	detail.Decisions, err = s.ListSelfDreamDecisions(run.ID)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	detail.Compilations, err = s.ListSelfDreamCompilations(run.ID)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	detail.Replays, err = s.ListSelfDreamReplays(run.ID, 100)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	return detail, nil
}

func (s *ActivityStore) ListSelfDreamRunSummaries(
	limit int,
) ([]SelfDreamRunSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT
			run.id,
			COALESCE((
				SELECT event.event_kind
				FROM self_dream_events_v1 event
				WHERE event.run_id = run.id
					AND event.event_kind IN ('completed', 'failed')
				ORDER BY event.created_at DESC, event.id DESC
				LIMIT 1
			), (
				SELECT event.event_kind
				FROM self_dream_events_v1 event
				WHERE event.run_id = run.id
				ORDER BY event.created_at DESC, event.id DESC
				LIMIT 1
			), 'created'),
			run.trigger_kind,
			run.source_cutoff_at,
			run.input_hash,
			COALESCE((
				SELECT attempt.model
				FROM self_dream_attempts_v1 attempt
				WHERE attempt.run_id = run.id
				LIMIT 1
			), ''),
			(SELECT COUNT(*) FROM self_dream_operations_v1 operation WHERE operation.run_id = run.id),
			(SELECT COUNT(*) FROM self_dream_operations_v1 operation WHERE operation.run_id = run.id AND operation.validation_status = 'qualified'),
			(SELECT COUNT(*) FROM self_dream_operations_v1 operation WHERE operation.run_id = run.id AND operation.validation_status = 'review_required'),
			(SELECT COUNT(*) FROM self_dream_operations_v1 operation WHERE operation.run_id = run.id AND operation.validation_status = 'rejected'),
			run.created_at
		FROM self_dream_runs_v1 run
		ORDER BY run.created_at DESC, run.id DESC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SelfDreamRunSummary, 0)
	for rows.Next() {
		var item SelfDreamRunSummary
		if err := rows.Scan(
			&item.ID,
			&item.Status,
			&item.TriggerKind,
			&item.SourceCutoffAt,
			&item.InputHash,
			&item.Model,
			&item.OperationCount,
			&item.QualifiedCount,
			&item.ReviewCount,
			&item.RejectedCount,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
