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

func addSelfModelEntryTx(
	tx *sql.Tx,
	item SelfModelEntry,
) (SelfModelEntry, bool, error) {
	if tx == nil {
		return SelfModelEntry{}, false, errors.New(
			"canonical Self transaction is unavailable",
		)
	}
	var err error
	if item.Kind, err = normalizeSelfModelKind(item.Kind); err != nil {
		return SelfModelEntry{}, false, err
	}
	if item.Status, err = normalizeSelfModelStatus(item.Status); err != nil {
		return SelfModelEntry{}, false, err
	}
	item.Scope = strings.TrimSpace(item.Scope)
	item.Statement = strings.TrimSpace(item.Statement)
	item.ReasoningStatus = strings.TrimSpace(item.ReasoningStatus)
	item.Confidence = clampConfidence(item.Confidence)
	if item.Statement == "" || item.ReasoningStatus == "" {
		return SelfModelEntry{}, false, errors.New(
			"canonical Self statement and reasoning status are required",
		)
	}
	if item.EntryKey == "" {
		item.EntryKey = selfModelEntryKey(
			item.Kind,
			item.Scope,
			item.Statement,
		)
	}
	current, currentErr := scanSelfModelEntry(tx.QueryRow(`
		SELECT `+selfModelSelectColumns+`
		FROM self_model_entries
		WHERE entry_key = ?
		ORDER BY revision DESC
		LIMIT 1`,
		item.EntryKey,
	))
	if currentErr == nil {
		item.Evidence = mergeSelfModelEvidence(current.Evidence, item.Evidence)
		item.Contradictions = mergeSelfModelEvidence(
			current.Contradictions,
			item.Contradictions,
		)
		item.Revision = current.Revision + 1
		if selfModelStatusPriority(current.Status) >
			selfModelStatusPriority(item.Status) {
			item.Status = current.Status
		}
		if current.Confidence > item.Confidence {
			item.Confidence = current.Confidence
		}
	} else if errors.Is(currentErr, sql.ErrNoRows) {
		item.Revision = 1
	} else {
		return SelfModelEntry{}, false, currentErr
	}
	item.CreatedAt = selfDreamNow(item.CreatedAt)
	item.SourceHash, err = buildSelfModelSourceHash(item)
	if err != nil {
		return SelfModelEntry{}, false, err
	}
	if currentErr == nil && current.SourceHash == item.SourceHash {
		return current, false, nil
	}
	item.ID = deterministicAuthorityID(
		"self-model",
		item.EntryKey+"\x00"+item.SourceHash,
	)
	evidenceJSON, err := json.Marshal(item.Evidence)
	if err != nil {
		return SelfModelEntry{}, false, err
	}
	contradictionsJSON, err := json.Marshal(item.Contradictions)
	if err != nil {
		return SelfModelEntry{}, false, err
	}
	_, err = tx.Exec(`
		INSERT INTO self_model_entries (
			id, entry_key, revision, kind, scope, statement, status, confidence,
			reasoning_status, evidence_json, contradictions_json, source_hash,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.EntryKey,
		item.Revision,
		item.Kind,
		item.Scope,
		item.Statement,
		item.Status,
		item.Confidence,
		item.ReasoningStatus,
		string(evidenceJSON),
		string(contradictionsJSON),
		item.SourceHash,
		item.CreatedAt,
	)
	if err != nil {
		return SelfModelEntry{}, false, err
	}
	return item, true, nil
}

// errSelfDreamStaleTarget marks a compilation refused because canonical Self
// changed under the operation since its run.
var errSelfDreamStaleTarget = errors.New("stale Self Dream target")

func selfDreamLoadCurrentTargetsTx(
	tx *sql.Tx,
	targetIDs []string,
) ([]SelfModelEntry, error) {
	targets := make([]SelfModelEntry, 0, len(targetIDs))
	for _, targetID := range uniqueSortedStrings(targetIDs) {
		target, err := scanSelfModelEntry(tx.QueryRow(`
			SELECT `+selfModelSelectColumns+`
			FROM self_model_entries
			WHERE id = ?`,
			targetID,
		))
		if err != nil {
			return nil, fmt.Errorf(
				"load Self Dream target %s: %w",
				targetID,
				err,
			)
		}
		latest, err := scanSelfModelEntry(tx.QueryRow(`
			SELECT `+selfModelSelectColumns+`
			FROM self_model_entries
			WHERE entry_key = ?
			ORDER BY revision DESC
			LIMIT 1`,
			target.EntryKey,
		))
		if err != nil {
			return nil, err
		}
		if latest.ID != target.ID {
			return nil, fmt.Errorf(
				"%w %s: canonical Self has revision %d",
				errSelfDreamStaleTarget,
				target.ID,
				latest.Revision,
			)
		}
		if latest.Status != selfModelStatusActive {
			return nil, fmt.Errorf(
				"%w %s is no longer active",
				errSelfDreamStaleTarget,
				target.ID,
			)
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func selfDreamMergedTargetEvidence(
	targets []SelfModelEntry,
) ([]SelfModelEvidence, []SelfModelEvidence) {
	var evidence []SelfModelEvidence
	var contradictions []SelfModelEvidence
	for _, target := range targets {
		evidence = mergeSelfModelEvidence(evidence, target.Evidence)
		contradictions = mergeSelfModelEvidence(
			contradictions,
			target.Contradictions,
		)
	}
	return evidence, contradictions
}

func selfDreamOutputScope(
	operation SelfDreamOperationRecord,
	targets []SelfModelEntry,
) string {
	if operation.Source == "deterministic" && len(targets) > 0 {
		scope := strings.TrimSpace(targets[0].Scope)
		for _, target := range targets[1:] {
			if strings.TrimSpace(target.Scope) != scope {
				return selfDreamStoredScope(operation.Scope)
			}
		}
		return scope
	}
	return selfDreamStoredScope(operation.Scope)
}

func selfDreamDecisionEvidence(
	decision SelfDreamDecisionRecord,
	operation SelfDreamOperationRecord,
) SelfModelEvidence {
	sourceKind := "self_dream_user_decision"
	confidence := 1.0
	if decision.ActorKind == "rollout_gate" {
		sourceKind = "self_dream_rollout_decision"
		confidence = operation.Confidence
	}
	statement := strings.TrimSpace(decision.Note)
	if statement == "" {
		statement = strings.TrimSpace(operation.Statement)
	}
	return SelfModelEvidence{
		SourceKind: sourceKind,
		SourceID:   decision.ID,
		Statement:  statement,
		Confidence: confidence,
		SourceRefs: append([]string(nil), operation.EvidenceRefs...),
		OccurredAt: decision.CreatedAt,
	}
}

func selfDreamOutputStatements(
	operation SelfDreamOperationRecord,
	decision SelfDreamDecisionRecord,
) []string {
	if decision.Decision == SelfDreamDecisionCorrect {
		return []string{strings.TrimSpace(decision.Note)}
	}
	if operation.Operation == SelfDreamOperationSplit {
		return append([]string(nil), operation.SplitStatements...)
	}
	return []string{strings.TrimSpace(operation.Statement)}
}

func (s *ActivityStore) compileSelfDreamOperation(
	operation SelfDreamOperationRecord,
	decision SelfDreamDecisionRecord,
) (SelfDreamCompilationRecord, error) {
	if existing, err := s.GetSelfDreamCompilation(operation.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SelfDreamCompilationRecord{}, err
	}
	if operation.ValidationStatus != SelfDreamValidationQualified &&
		operation.ValidationStatus != SelfDreamValidationReviewRequired {
		return SelfDreamCompilationRecord{}, errors.New(
			"Self Dream operation is not qualified for compilation",
		)
	}
	if decision.Decision != SelfDreamDecisionAccept &&
		decision.Decision != SelfDreamDecisionAutoPromote &&
		decision.Decision != SelfDreamDecisionCorrect {
		return SelfDreamCompilationRecord{}, errors.New(
			"Self Dream decision does not permit compilation",
		)
	}
	if decision.Decision == SelfDreamDecisionCorrect &&
		strings.TrimSpace(decision.Note) == "" {
		return SelfDreamCompilationRecord{}, errors.New(
			"a corrected understanding is required",
		)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	defer tx.Rollback()
	targets, err := selfDreamLoadCurrentTargetsTx(
		tx,
		operation.TargetEntryIDs,
	)
	if err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	for _, target := range targets {
		if selfModelIsUserDirective(target) {
			return SelfDreamCompilationRecord{}, errors.New("revise user directives in Assistant settings")
		}
		if selfModelHasUserEdits(target) && decision.ActorKind != "user" {
			return SelfDreamCompilationRecord{}, errors.New("user-edited Assistant text requires review")
		}
	}
	evidence, contradictions := selfDreamMergedTargetEvidence(targets)
	// A reviewed replacement ends the live edit protection, while retaining
	// source evidence and the previous revisions' immutable edit provenance.
	if decision.ActorKind == "user" {
		kept := evidence[:0]
		for _, e := range evidence {
			if e.SourceKind != "assistant_field_edit" && e.SourceKind != "assistant_field_reset" {
				kept = append(kept, e)
			}
		}
		evidence = kept
	}
	decisionEvidence := selfDreamDecisionEvidence(decision, operation)
	evidence = mergeSelfModelEvidence(
		evidence,
		[]SelfModelEvidence{decisionEvidence},
	)
	reasoningStatus := "user_confirmed"
	compilationKind := operation.Operation
	if decision.ActorKind == "rollout_gate" {
		reasoningStatus = "source_backed"
	}
	if decision.Decision == SelfDreamDecisionCorrect {
		reasoningStatus = "self_dream_user_correction"
		compilationKind = SelfDreamOperationContradict
		contradictions = mergeSelfModelEvidence(
			contradictions,
			[]SelfModelEvidence{decisionEvidence},
		)
	}
	scope := selfDreamOutputScope(operation, targets)
	outputIDs := make([]string, 0)
	for _, statement := range selfDreamOutputStatements(operation, decision) {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			return SelfDreamCompilationRecord{}, errors.New(
				"Self Dream compilation output is empty",
			)
		}
		output, _, err := addSelfModelEntryTx(tx, SelfModelEntry{
			EntryKey: deterministicAuthorityID(
				"self-dream-canonical",
				operation.ID+"\x00"+statement,
			),
			Kind:            operation.SelfKind,
			Scope:           scope,
			Statement:       statement,
			Status:          selfModelStatusActive,
			Confidence:      operation.Confidence,
			ReasoningStatus: reasoningStatus,
			Evidence:        evidence,
			Contradictions:  contradictions,
			CreatedAt:       decision.CreatedAt,
		})
		if err != nil {
			return SelfDreamCompilationRecord{}, err
		}
		outputIDs = append(outputIDs, output.ID)
	}
	for _, target := range targets {
		target.Status = selfModelStatusRetracted
		target.ReasoningStatus = "self_dream_superseded"
		target.Evidence = mergeSelfModelEvidence(
			target.Evidence,
			[]SelfModelEvidence{{
				SourceKind: "self_dream_supersession",
				SourceID:   decision.ID,
				Statement:  "Superseded by reviewed canonical Self synthesis.",
				Confidence: 1,
				SourceRefs: append([]string(nil), outputIDs...),
				OccurredAt: decision.CreatedAt,
			}},
		)
		target.CreatedAt = decision.CreatedAt
		if _, _, err := addSelfModelEntryTx(tx, target); err != nil {
			return SelfDreamCompilationRecord{}, err
		}
	}
	sourceJSON, err := json.Marshal(operation.TargetEntryIDs)
	if err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	outputJSON, err := json.Marshal(outputIDs)
	if err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	compilation := SelfDreamCompilationRecord{
		ID:              uuid.NewString(),
		OperationID:     operation.ID,
		DecisionID:      decision.ID,
		CompilationKind: compilationKind,
		SourceEntryIDs:  append([]string(nil), operation.TargetEntryIDs...),
		OutputEntryIDs:  outputIDs,
		CreatedAt:       decision.CreatedAt,
	}
	_, err = tx.Exec(`
		INSERT INTO self_dream_compilations_v1 (
			id, operation_id, decision_id, compilation_kind,
			source_entry_ids_json, output_entry_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		compilation.ID,
		compilation.OperationID,
		compilation.DecisionID,
		compilation.CompilationKind,
		string(sourceJSON),
		string(outputJSON),
		compilation.CreatedAt,
	)
	if err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return SelfDreamCompilationRecord{}, err
	}
	return compilation, nil
}

func normalizeSelfDreamDecision(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case SelfDreamDecisionAccept,
		SelfDreamDecisionKeepSeparate,
		SelfDreamDecisionNotEnoughEvidence,
		SelfDreamDecisionCorrect:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported Self Dream decision %q", value)
	}
}

// ReviewSelfDreamOperation appends a user decision. Accept and Correct compile
// transactionally into canonical Self; other decisions preserve the candidate
// and its evidence without product mutation.
func (a *App) ReviewSelfDreamOperation(
	input ReviewSelfDreamOperationInput,
) (SelfDreamRunDetail, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return SelfDreamRunDetail{}, errors.New(
			"activity store is unavailable",
		)
	}
	decisionKind, err := normalizeSelfDreamDecision(input.Decision)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	input.OperationID = strings.TrimSpace(input.OperationID)
	if input.OperationID == "" {
		return SelfDreamRunDetail{}, errors.New(
			"Self Dream operation ID is required",
		)
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return SelfDreamRunDetail{}, errors.New(
			"Self Dream review idempotency key is required",
		)
	}
	if decisionKind == SelfDreamDecisionCorrect &&
		strings.TrimSpace(input.Note) == "" {
		return SelfDreamRunDetail{}, errors.New(
			"enter the corrected understanding before saving",
		)
	}
	store := a.activity.store
	operation, err := store.GetSelfDreamOperation(input.OperationID)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	decision, added, err := store.AddSelfDreamDecision(
		SelfDreamDecisionRecord{
			OperationID:    operation.ID,
			IdempotencyKey: input.IdempotencyKey,
			ActorKind:      "user",
			Decision:       decisionKind,
			Note:           input.Note,
			CreatedAt:      time.Now().UnixMilli(),
		},
	)
	if err != nil {
		return SelfDreamRunDetail{}, err
	}
	if added && (decision.Decision == SelfDreamDecisionAccept ||
		decision.Decision == SelfDreamDecisionCorrect) {
		if _, err := store.compileSelfDreamOperation(
			operation,
			decision,
		); err != nil {
			a.appendSelfDreamEvent(operation.RunID, "promotion_skipped", map[string]any{
				"operationId": operation.ID,
				"reason":      err.Error(),
			})
			if errors.Is(err, errSelfDreamStaleTarget) {
				a.engagementRetireSelfDreamProposal(agencyContext(a), operation)
			}
			return SelfDreamRunDetail{}, err
		}
	}
	if added {
		// The proposal is answered now; it no longer needs announcing.
		a.engagementRetireSelfDreamProposal(agencyContext(a), operation)
	}
	a.emitBrowserEvent("self_dream:updated", operation.RunID)
	a.emitBrowserEvent("self_model:updated", operation.ID)
	a.emitBrowserEvent("agency:updated")
	return store.GetSelfDreamRunDetail(operation.RunID)
}
