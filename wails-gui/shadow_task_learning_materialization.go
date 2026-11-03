package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ShadowTaskLearningMaterializationVersion = "shadow-task-learning-materialization-v1"

	ShadowTaskLearningMaterializationMaterialized = "materialized"
	ShadowTaskLearningMaterializationSkipped      = "skipped"
	ShadowTaskLearningMaterializationDenied       = "denied"
	ShadowTaskLearningMaterializationError        = "error"
	ShadowTaskLearningMaterializationFailed       = "failed"
)

type ShadowTaskLearningMaterializationResolution struct {
	Version             string   `json:"version"`
	RunID               string   `json:"runId"`
	Complete            bool     `json:"complete"`
	CandidateCount      int      `json:"candidateCount"`
	TerminalCount       int      `json:"terminalCount"`
	MaterializedCount   int      `json:"materializedCount"`
	SkippedCount        int      `json:"skippedCount"`
	DeniedCount         int      `json:"deniedCount"`
	ErrorCount          int      `json:"errorCount"`
	InvalidCount        int      `json:"invalidCount"`
	MissingCount        int      `json:"missingCount"`
	PendingRevisionIDs  []string `json:"pendingRevisionIds"`
	ErrorRevisionIDs    []string `json:"errorRevisionIds"`
	TerminalRevisionIDs []string `json:"terminalRevisionIds"`
}

type ShadowTaskLearningMaterializationRecovery struct {
	Version              string                                      `json:"version"`
	RunID                string                                      `json:"runId"`
	AttemptedRevisionIDs []string                                    `json:"attemptedRevisionIds"`
	ResolvedRevisionIDs  []string                                    `json:"resolvedRevisionIds"`
	Resolution           ShadowTaskLearningMaterializationResolution `json:"resolution"`
	Errors               []string                                    `json:"errors"`
}

func shadowTaskLearningMaterializationStatus(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case ShadowTaskLearningMaterializationMaterialized,
		ShadowTaskLearningMaterializationSkipped,
		ShadowTaskLearningMaterializationDenied,
		ShadowTaskLearningMaterializationError,
		ShadowTaskLearningMaterializationFailed:
		return value, nil
	default:
		return "", fmt.Errorf(
			"unsupported shadow task learning materialization status %q",
			value,
		)
	}
}

func shadowTaskLearningMaterializationTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case ShadowTaskLearningMaterializationMaterialized,
		ShadowTaskLearningMaterializationSkipped,
		ShadowTaskLearningMaterializationDenied:
		return true
	default:
		return false
	}
}

func shadowTaskLearningCandidateTerminalStatus(
	candidate ShadowTaskLearningCandidate,
	status string,
) bool {
	if status == ShadowTaskLearningMaterializationDenied {
		return true
	}
	if candidate.Qualification.Decision == ShadowTaskLearningDecisionQualified {
		return status == ShadowTaskLearningMaterializationMaterialized
	}
	return status == ShadowTaskLearningMaterializationSkipped
}

func (s *ActivityStore) ListShadowTaskLearningMaterializationsForRun(
	runID string,
) ([]ShadowTaskLearningMaterialization, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("shadow task learning run ID is required")
	}
	rows, err := s.db.Query(`
		SELECT materialization.id, materialization.revision_id,
			materialization.target_kind, materialization.target_id,
			materialization.status, materialization.error,
			materialization.created_at
		FROM shadow_task_learning_materializations materialization
		JOIN shadow_task_learning_revisions revision
			ON revision.revision_id = materialization.revision_id
		WHERE revision.run_id = ?
		ORDER BY materialization.created_at ASC, materialization.id ASC`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ShadowTaskLearningMaterialization, 0)
	for rows.Next() {
		var item ShadowTaskLearningMaterialization
		if err := rows.Scan(
			&item.ID,
			&item.RevisionID,
			&item.TargetKind,
			&item.TargetID,
			&item.Status,
			&item.Error,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func BuildShadowTaskLearningMaterializationResolution(
	store *ActivityStore,
	runID string,
) (ShadowTaskLearningMaterializationResolution, error) {
	result := ShadowTaskLearningMaterializationResolution{
		Version:             ShadowTaskLearningMaterializationVersion,
		RunID:               strings.TrimSpace(runID),
		PendingRevisionIDs:  []string{},
		ErrorRevisionIDs:    []string{},
		TerminalRevisionIDs: []string{},
	}
	if store == nil || store.db == nil {
		return result, errors.New("activity store is unavailable")
	}
	run, err := store.GetShadowTaskLearningRun(result.RunID)
	if err != nil {
		return result, err
	}
	if run.PolicyVersion != ShadowTaskLearningPolicyVersion ||
		run.Projection.PolicyVersion != ShadowTaskLearningPolicyVersion ||
		run.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
		run.Projection.AuthorityEffect != ShadowTaskLearningAuthorityNone {
		return result, errors.New(
			"shadow task learning run is outside the current contained policy",
		)
	}
	receipts, err := store.ListShadowTaskLearningMaterializationsForRun(run.ID)
	if err != nil {
		return result, err
	}
	byRevision := make(map[string][]ShadowTaskLearningMaterialization)
	for _, receipt := range receipts {
		byRevision[receipt.RevisionID] = append(
			byRevision[receipt.RevisionID],
			receipt,
		)
	}
	result.CandidateCount = len(run.Projection.Candidates)
	for _, candidate := range run.Projection.Candidates {
		terminalStatuses := make(map[string]struct{})
		terminalIdentities := make(map[string]struct{})
		hasError := false
		hasReceipt := false
		hasInvalid := false
		for _, receipt := range byRevision[candidate.RevisionID] {
			hasReceipt = true
			if receipt.TargetKind != candidate.TargetKind {
				hasInvalid = true
				continue
			}
			if shadowTaskLearningCandidateTerminalStatus(candidate, receipt.Status) {
				terminalStatuses[receipt.Status] = struct{}{}
				terminalIdentities[receipt.Status+"\x00"+receipt.TargetID] = struct{}{}
				if receipt.Status == ShadowTaskLearningMaterializationMaterialized &&
					strings.TrimSpace(receipt.TargetID) == "" {
					hasInvalid = true
				}
				if receipt.Status != ShadowTaskLearningMaterializationMaterialized &&
					strings.TrimSpace(receipt.TargetID) != "" {
					hasInvalid = true
				}
			} else if shadowTaskLearningMaterializationTerminal(receipt.Status) {
				hasInvalid = true
			}
			if receipt.Status == ShadowTaskLearningMaterializationError ||
				receipt.Status == ShadowTaskLearningMaterializationFailed {
				hasError = true
			}
		}
		if len(terminalStatuses) == 1 && len(terminalIdentities) == 1 && !hasInvalid {
			terminalStatus := ""
			for status := range terminalStatuses {
				terminalStatus = status
			}
			result.TerminalCount++
			result.TerminalRevisionIDs = append(
				result.TerminalRevisionIDs,
				candidate.RevisionID,
			)
			switch terminalStatus {
			case ShadowTaskLearningMaterializationMaterialized:
				result.MaterializedCount++
			case ShadowTaskLearningMaterializationSkipped:
				result.SkippedCount++
			case ShadowTaskLearningMaterializationDenied:
				result.DeniedCount++
			}
			continue
		}
		result.PendingRevisionIDs = append(
			result.PendingRevisionIDs,
			candidate.RevisionID,
		)
		if hasInvalid || len(terminalStatuses) > 1 || len(terminalIdentities) > 1 {
			result.InvalidCount++
			result.ErrorRevisionIDs = append(
				result.ErrorRevisionIDs,
				candidate.RevisionID,
			)
		}
		if hasError {
			result.ErrorCount++
			result.ErrorRevisionIDs = append(
				result.ErrorRevisionIDs,
				candidate.RevisionID,
			)
		} else if !hasReceipt {
			result.MissingCount++
		}
	}
	result.PendingRevisionIDs = uniqueSortedStrings(result.PendingRevisionIDs)
	result.ErrorRevisionIDs = uniqueSortedStrings(result.ErrorRevisionIDs)
	result.TerminalRevisionIDs = uniqueSortedStrings(result.TerminalRevisionIDs)
	result.Complete = result.TerminalCount == result.CandidateCount
	return result, nil
}

func (a *App) reconcileShadowTaskLearningMaterializations(
	runID string,
	createdAt int64,
) (ShadowTaskLearningMaterializationRecovery, error) {
	result := ShadowTaskLearningMaterializationRecovery{
		Version:              ShadowTaskLearningMaterializationVersion,
		RunID:                strings.TrimSpace(runID),
		AttemptedRevisionIDs: []string{},
		ResolvedRevisionIDs:  []string{},
		Errors:               []string{},
	}
	if a == nil || a.activity == nil || a.activity.store == nil {
		return result, errors.New("activity store is unavailable")
	}
	if createdAt <= 0 {
		createdAt = time.Now().UnixMilli()
	}
	run, err := a.activity.store.GetShadowTaskLearningRun(result.RunID)
	if err != nil {
		return result, err
	}
	before, err := BuildShadowTaskLearningMaterializationResolution(
		a.activity.store,
		run.ID,
	)
	if err != nil {
		return result, err
	}
	if before.InvalidCount > 0 {
		result.Resolution = before
		return result, errors.New(
			"shadow task learning materialization has conflicting immutable receipts",
		)
	}
	pending := make(map[string]struct{}, len(before.PendingRevisionIDs))
	for _, id := range before.PendingRevisionIDs {
		pending[id] = struct{}{}
	}
	for _, candidate := range run.Projection.Candidates {
		if _, ok := pending[candidate.RevisionID]; !ok {
			continue
		}
		result.AttemptedRevisionIDs = append(
			result.AttemptedRevisionIDs,
			candidate.RevisionID,
		)
		receipt, materializeErr := a.materializeShadowTaskLearningCandidateAt(
			candidate,
			run.CreatedAt,
			createdAt,
		)
		if materializeErr != nil {
			result.Errors = append(
				result.Errors,
				candidate.RevisionID+": "+materializeErr.Error(),
			)
			continue
		}
		if shadowTaskLearningMaterializationTerminal(receipt.Status) {
			result.ResolvedRevisionIDs = append(
				result.ResolvedRevisionIDs,
				candidate.RevisionID,
			)
		}
	}
	result.AttemptedRevisionIDs = uniqueSortedStrings(
		result.AttemptedRevisionIDs,
	)
	result.ResolvedRevisionIDs = uniqueSortedStrings(
		result.ResolvedRevisionIDs,
	)
	result.Errors = uniqueSortedStrings(result.Errors)
	result.Resolution, err = BuildShadowTaskLearningMaterializationResolution(
		a.activity.store,
		run.ID,
	)
	if err != nil {
		return result, err
	}
	if len(result.Errors) > 0 {
		return result, fmt.Errorf(
			"shadow task learning materialization: %s",
			strings.Join(result.Errors, "; "),
		)
	}
	sort.Strings(result.AttemptedRevisionIDs)
	sort.Strings(result.ResolvedRevisionIDs)
	return result, nil
}
