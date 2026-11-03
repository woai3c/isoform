package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	taskShadowContinuationVersion = "task-shadow-continuation-v2"
	taskShadowContinuationMaxRuns = 100

	taskShadowContinuationActionNone             = "none"
	taskShadowContinuationActionExecute          = "execute_shadow"
	taskShadowContinuationActionEvaluate         = "evaluate_pair"
	taskShadowContinuationActionMaterialize      = "materialize_learning"
	taskShadowContinuationExecutionPending       = "execution_pending"
	taskShadowContinuationAwaitingTerminal       = "awaiting_task_terminal"
	taskShadowContinuationAwaitingResult         = "awaiting_user_result"
	taskShadowContinuationEvaluationPending      = "evaluation_pending"
	taskShadowContinuationLearningPending        = "learning_pending"
	taskShadowContinuationMaterializationPending = "learning_materialization_pending"
	taskShadowContinuationComplete               = "complete"
	taskShadowContinuationShadowFailed           = "shadow_failed"
	taskShadowContinuationBlocked                = "blocked"
	taskShadowContinuationHistorical             = "historical"
	taskShadowContinuationFirstRunExcluded       = "first_run_excluded"
)

type TaskShadowContinuationItem struct {
	TaskEpisodeID           string                                       `json:"taskEpisodeId"`
	ShadowRunID             string                                       `json:"shadowRunId"`
	Workflow                string                                       `json:"workflow"`
	State                   string                                       `json:"state"`
	Action                  string                                       `json:"action"`
	Reason                  string                                       `json:"reason,omitempty"`
	EpisodeStatus           string                                       `json:"episodeStatus,omitempty"`
	AttemptID               string                                       `json:"attemptId,omitempty"`
	AttemptStatus           string                                       `json:"attemptStatus,omitempty"`
	PairState               string                                       `json:"pairState,omitempty"`
	CurrentEvaluationIDs    []string                                     `json:"currentEvaluationIds"`
	LearningRunID           string                                       `json:"learningRunId,omitempty"`
	LearningMaterialization *ShadowTaskLearningMaterializationResolution `json:"learningMaterialization,omitempty"`
	CreatedAt               int64                                        `json:"createdAt"`
}

type TaskShadowContinuationSnapshot struct {
	Version                     string                       `json:"version"`
	GeneratedAt                 int64                        `json:"generatedAt"`
	ScannedRunCount             int                          `json:"scannedRunCount"`
	CurrentRunCount             int                          `json:"currentRunCount"`
	ExecutionPendingCount       int                          `json:"executionPendingCount"`
	EvaluationPendingCount      int                          `json:"evaluationPendingCount"`
	LearningPendingCount        int                          `json:"learningPendingCount"`
	MaterializationPendingCount int                          `json:"materializationPendingCount"`
	AwaitingTerminalCount       int                          `json:"awaitingTerminalCount"`
	AwaitingResultCount         int                          `json:"awaitingResultCount"`
	CompleteCount               int                          `json:"completeCount"`
	BlockedCount                int                          `json:"blockedCount"`
	HistoricalCount             int                          `json:"historicalCount"`
	FirstRunExcludedCount       int                          `json:"firstRunExcludedCount"`
	Items                       []TaskShadowContinuationItem `json:"items"`
}

type TaskShadowContinuationRecoveryResult struct {
	Version                        string   `json:"version"`
	Trigger                        string   `json:"trigger"`
	GeneratedAt                    int64    `json:"generatedAt"`
	ScannedRunCount                int      `json:"scannedRunCount"`
	ExecutionDispatchedIDs         []string `json:"executionDispatchedIds"`
	EvaluationDispatchedIDs        []string `json:"evaluationDispatchedIds"`
	MaterializationRecoveredRunIDs []string `json:"materializationRecoveredRunIds"`
	Errors                         []string `json:"errors"`
}

type taskShadowContinuationRunRef struct {
	ID            string
	TaskEpisodeID string
	CapabilityKey string
	PromptVersion string
	CreatedAt     int64
}

func normalizeTaskShadowContinuationLimit(limit int) int {
	if limit <= 0 || limit > taskShadowContinuationMaxRuns {
		return taskShadowContinuationMaxRuns
	}
	return limit
}

func (s *ActivityStore) listTaskShadowContinuationRunRefs(
	limit int,
) ([]taskShadowContinuationRunRef, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store unavailable")
	}
	rows, err := s.db.Query(`
		SELECT id, task_episode_id, capability_key, prompt_version, created_at
		FROM shadow_runs
		WHERE mode = 'task_mirror'
		ORDER BY created_at DESC, id DESC
		LIMIT ?`,
		normalizeTaskShadowContinuationLimit(limit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]taskShadowContinuationRunRef, 0)
	for rows.Next() {
		var run taskShadowContinuationRunRef
		if err := rows.Scan(
			&run.ID,
			&run.TaskEpisodeID,
			&run.CapabilityKey,
			&run.PromptVersion,
			&run.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func taskShadowContinuationLearningIndex(
	runs []ShadowTaskLearningRun,
) map[string]string {
	result := make(map[string]string)
	for _, run := range runs {
		if run.PolicyVersion != ShadowTaskLearningPolicyVersion ||
			run.Projection.PolicyVersion != ShadowTaskLearningPolicyVersion ||
			run.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
			run.Projection.AuthorityEffect != ShadowTaskLearningAuthorityNone {
			continue
		}
		for _, exclusion := range run.Projection.ExcludedEvaluations {
			if id := strings.TrimSpace(exclusion.EvaluationID); id != "" {
				if _, exists := result[id]; !exists {
					result[id] = run.ID
				}
			}
		}
		for _, candidate := range run.Projection.Candidates {
			for _, evidence := range candidate.Evidence {
				if id := strings.TrimSpace(evidence.EvaluationID); id != "" {
					if _, exists := result[id]; !exists {
						result[id] = run.ID
					}
				}
			}
			for _, dimension := range candidate.Dimensions {
				if id := strings.TrimSpace(dimension.EvaluationID); id != "" {
					if _, exists := result[id]; !exists {
						result[id] = run.ID
					}
				}
			}
		}
	}
	return result
}

func taskShadowContinuationTerminal(status string) bool {
	switch status {
	case TaskEpisodeStatusCompleted,
		TaskEpisodeStatusAbandoned,
		TaskEpisodeStatusExpired:
		return true
	default:
		return false
	}
}

func taskShadowContinuationReason(values []string, fallback string) string {
	values = uniqueSortedStrings(values)
	if len(values) == 0 {
		return fallback
	}
	return strings.Join(values, ", ")
}

func taskShadowContinuationCount(
	snapshot *TaskShadowContinuationSnapshot,
	state string,
) {
	if snapshot == nil {
		return
	}
	switch state {
	case taskShadowContinuationExecutionPending:
		snapshot.ExecutionPendingCount++
	case taskShadowContinuationEvaluationPending:
		snapshot.EvaluationPendingCount++
	case taskShadowContinuationLearningPending:
		snapshot.LearningPendingCount++
	case taskShadowContinuationMaterializationPending:
		snapshot.MaterializationPendingCount++
	case taskShadowContinuationAwaitingTerminal:
		snapshot.AwaitingTerminalCount++
	case taskShadowContinuationAwaitingResult:
		snapshot.AwaitingResultCount++
	case taskShadowContinuationComplete:
		snapshot.CompleteCount++
	case taskShadowContinuationHistorical:
		snapshot.HistoricalCount++
	case taskShadowContinuationFirstRunExcluded:
		snapshot.FirstRunExcludedCount++
	default:
		snapshot.BlockedCount++
	}
}

func BuildTaskShadowContinuationSnapshot(
	store *ActivityStore,
	limit int,
	generatedAt int64,
) (TaskShadowContinuationSnapshot, error) {
	if store == nil || store.db == nil {
		return TaskShadowContinuationSnapshot{}, errors.New(
			"activity store unavailable",
		)
	}
	if generatedAt <= 0 {
		generatedAt = time.Now().UnixMilli()
	}
	runRefs, err := store.listTaskShadowContinuationRunRefs(limit)
	if err != nil {
		return TaskShadowContinuationSnapshot{}, err
	}
	learningRuns, err := store.ListShadowTaskLearningRuns(
		shadowTaskLearningListMax,
	)
	if err != nil {
		return TaskShadowContinuationSnapshot{}, err
	}
	learningByEvaluation := taskShadowContinuationLearningIndex(learningRuns)
	repeatedOnsets, err := store.taskEpisodeRepeatedOnsetSet()
	if err != nil {
		return TaskShadowContinuationSnapshot{}, err
	}
	materializationByRun := make(map[string]ShadowTaskLearningMaterializationResolution)
	result := TaskShadowContinuationSnapshot{
		Version:         taskShadowContinuationVersion,
		GeneratedAt:     generatedAt,
		ScannedRunCount: len(runRefs),
		Items:           []TaskShadowContinuationItem{},
	}
	for _, runRef := range runRefs {
		item := TaskShadowContinuationItem{
			TaskEpisodeID:        runRef.TaskEpisodeID,
			ShadowRunID:          runRef.ID,
			Workflow:             runRef.CapabilityKey,
			Action:               taskShadowContinuationActionNone,
			CurrentEvaluationIDs: []string{},
			CreatedAt:            runRef.CreatedAt,
		}
		if strings.TrimSpace(runRef.PromptVersion) != shadowTaskPromptVersion {
			item.State = taskShadowContinuationHistorical
			taskShadowContinuationCount(&result, item.State)
			continue
		}
		run, err := store.GetShadowRun(runRef.ID)
		if err != nil {
			return TaskShadowContinuationSnapshot{}, fmt.Errorf(
				"load current shadow run %s: %w",
				runRef.ID,
				err,
			)
		}
		if run.Mode != "task_mirror" ||
			run.ID != runRef.ID ||
			run.TaskEpisodeID != runRef.TaskEpisodeID ||
			run.CapabilityKey != runRef.CapabilityKey ||
			strings.TrimSpace(run.PromptVersion) != strings.TrimSpace(runRef.PromptVersion) ||
			run.CreatedAt != runRef.CreatedAt {
			return TaskShadowContinuationSnapshot{}, fmt.Errorf(
				"current shadow run %s changed while deriving continuation",
				runRef.ID,
			)
		}
		result.CurrentRunCount++
		if !taskWorkflowProductionShadowEligible(run.CapabilityKey) {
			item.State = taskShadowContinuationBlocked
			item.Reason = "workflow is outside the closed production registry"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		episode, err := store.GetTaskEpisode(run.TaskEpisodeID)
		if err != nil {
			item.State = taskShadowContinuationBlocked
			item.Reason = "load task episode: " + err.Error()
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		item.EpisodeStatus = episode.CurrentStatus
		if _, repeated := repeatedOnsets[episode.ID]; repeated {
			item.State = taskShadowContinuationBlocked
			item.Reason = "the observed task start was recorded more than once and is excluded"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		excluded, err := store.isShadowTaskRunFirstRunLearningExcluded(run.ID)
		if err != nil {
			item.State = taskShadowContinuationBlocked
			item.Reason = "check First Run exclusion: " + err.Error()
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		if excluded {
			item.State = taskShadowContinuationFirstRunExcluded
			item.Reason = "guided First Run evidence is excluded from natural learning"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		if err := taskEpisodeFrozenContextSafetyError(episode); err != nil {
			item.State = taskShadowContinuationBlocked
			item.Reason = "frozen task context is unsafe: " + err.Error()
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		if strings.TrimSpace(run.Model) == "" {
			item.State = taskShadowContinuationBlocked
			item.Reason = "the immutable shadow run has no frozen model"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		if run.Prediction.CandidateExecutionPlan == nil ||
			strings.TrimSpace(run.Prediction.CandidateExecutionPlan.Prompt) == "" {
			item.State = taskShadowContinuationBlocked
			item.Reason = "the immutable shadow run has no executable frozen plan"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		attempt, found, err := store.GetShadowExecutionAttemptByIdempotencyKey(
			run.ID + ":" + shadowExecutionRuntimeVersion,
		)
		if err != nil {
			item.State = taskShadowContinuationBlocked
			item.Reason = "read current execution attempt: " + err.Error()
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		if !found {
			item.State = taskShadowContinuationExecutionPending
			item.Action = taskShadowContinuationActionExecute
			item.Reason = "no current-runtime execution attempt exists"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		item.AttemptID = attempt.ID
		item.AttemptStatus = attempt.Status
		if attempt.Status != "completed" {
			item.State = taskShadowContinuationShadowFailed
			item.Reason = strings.TrimSpace(attempt.Error)
			if item.Reason == "" {
				item.Reason = "current execution attempt is " + attempt.Status
			}
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		readiness, err := BuildShadowTaskPairReadiness(store, run.ID)
		if err != nil {
			item.State = taskShadowContinuationBlocked
			item.Reason = "build pair readiness: " + err.Error()
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		item.PairState = readiness.State
		item.CurrentEvaluationIDs = append(
			item.CurrentEvaluationIDs,
			readiness.CurrentEvaluationIDs...,
		)
		sort.Strings(item.CurrentEvaluationIDs)
		if !taskShadowContinuationTerminal(episode.CurrentStatus) {
			item.State = taskShadowContinuationAwaitingTerminal
			item.Reason = "the observed task is not terminal"
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		if readiness.State == ShadowTaskPairStateEvaluated &&
			len(item.CurrentEvaluationIDs) > 0 {
			for _, evaluationID := range item.CurrentEvaluationIDs {
				if learningID := learningByEvaluation[evaluationID]; learningID != "" {
					item.LearningRunID = learningID
					break
				}
			}
			if item.LearningRunID != "" {
				resolution, ok := materializationByRun[item.LearningRunID]
				if !ok {
					resolution, err = BuildShadowTaskLearningMaterializationResolution(
						store,
						item.LearningRunID,
					)
					if err == nil {
						materializationByRun[item.LearningRunID] = resolution
					}
				}
				if err != nil {
					item.State = taskShadowContinuationBlocked
					item.Reason = "resolve learning materialization: " + err.Error()
				} else {
					item.LearningMaterialization = &resolution
					if resolution.Complete {
						item.State = taskShadowContinuationComplete
						item.Reason = "current comparison, learning, and materialization are complete"
					} else if resolution.InvalidCount > 0 {
						item.State = taskShadowContinuationBlocked
						item.Reason = "linked learning projection has conflicting immutable materialization receipts"
					} else {
						item.State = taskShadowContinuationMaterializationPending
						item.Action = taskShadowContinuationActionMaterialize
						item.Reason = "linked learning projection has unresolved local materialization"
					}
				}
			} else {
				item.State = taskShadowContinuationLearningPending
				item.Action = taskShadowContinuationActionEvaluate
				item.Reason = "current evaluation has no linked learning projection"
			}
			taskShadowContinuationCount(&result, item.State)
			result.Items = append(result.Items, item)
			continue
		}
		switch readiness.State {
		case ShadowTaskPairStateReady:
			item.State = taskShadowContinuationEvaluationPending
			item.Action = taskShadowContinuationActionEvaluate
			item.Reason = "current terminal pair is ready for comparison"
		case ShadowTaskPairStateAwaitingUserResult:
			item.State = taskShadowContinuationAwaitingResult
			item.Reason = taskShadowContinuationReason(
				readiness.Reasons,
				"no current destination-bound user result exists",
			)
		case ShadowTaskPairStateUserResultUnavailable:
			if len(readiness.InvalidUserResultEvidenceIDs) > 0 {
				item.State = taskShadowContinuationBlocked
				item.Reason = taskShadowContinuationReason(
					readiness.Reasons,
					"observed result provenance is invalid",
				)
				break
			}
			item.State = taskShadowContinuationAwaitingResult
			item.Reason = taskShadowContinuationReason(
				readiness.Reasons,
				"no current destination-bound user result exists",
			)
		default:
			item.State = taskShadowContinuationBlocked
			item.Reason = taskShadowContinuationReason(
				readiness.Reasons,
				"pair state is "+readiness.State,
			)
		}
		taskShadowContinuationCount(&result, item.State)
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (c *ActivityTriager) recoverTaskShadowContinuations(
	ctx context.Context,
	trigger string,
) TaskShadowContinuationRecoveryResult {
	result := TaskShadowContinuationRecoveryResult{
		Version:                        taskShadowContinuationVersion,
		Trigger:                        strings.TrimSpace(trigger),
		GeneratedAt:                    time.Now().UnixMilli(),
		ExecutionDispatchedIDs:         []string{},
		EvaluationDispatchedIDs:        []string{},
		MaterializationRecoveredRunIDs: []string{},
		Errors:                         []string{},
	}
	if c == nil || c.app == nil || c.app.activity == nil ||
		c.app.activity.store == nil {
		result.Errors = append(result.Errors, "activity store unavailable")
		return result
	}
	if !c.app.loadTriageFeatureFlags(ctx).Shadow {
		return result
	}
	snapshot, err := BuildTaskShadowContinuationSnapshot(
		c.app.activity.store,
		taskShadowContinuationMaxRuns,
		result.GeneratedAt,
	)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.ScannedRunCount = snapshot.ScannedRunCount
	for _, item := range snapshot.Items {
		switch item.Action {
		case taskShadowContinuationActionExecute:
			excluded, err := c.app.activity.store.
				isShadowTaskRunFirstRunLearningExcluded(item.ShadowRunID)
			if err != nil {
				result.Errors = append(
					result.Errors,
					fmt.Sprintf("run %s exclusion: %v", item.ShadowRunID, err),
				)
				continue
			}
			if excluded {
				continue
			}
			run, err := c.app.activity.store.GetShadowRun(item.ShadowRunID)
			if err != nil {
				result.Errors = append(
					result.Errors,
					fmt.Sprintf("load run %s: %v", item.ShadowRunID, err),
				)
				continue
			}
			if !shadowTaskRunUsesCurrentGeneration(run) ||
				!taskWorkflowProductionShadowEligible(run.CapabilityKey) {
				continue
			}
			result.ExecutionDispatchedIDs = append(
				result.ExecutionDispatchedIDs,
				run.ID,
			)
			c.dispatchShadowCandidateExecution(ctx, run)
		case taskShadowContinuationActionEvaluate:
			result.EvaluationDispatchedIDs = append(
				result.EvaluationDispatchedIDs,
				item.ShadowRunID,
			)
			c.dispatchTaskShadowEvaluation(ctx, item.ShadowRunID)
		case taskShadowContinuationActionMaterialize:
			recovery, err := c.app.reconcileShadowTaskLearningMaterializations(
				item.LearningRunID,
				result.GeneratedAt,
			)
			if err != nil {
				result.Errors = append(
					result.Errors,
					fmt.Sprintf("materialize learning run %s: %v", item.LearningRunID, err),
				)
				continue
			}
			if recovery.Resolution.Complete {
				result.MaterializationRecoveredRunIDs = append(
					result.MaterializationRecoveredRunIDs,
					item.LearningRunID,
				)
			}
		}
	}
	result.ExecutionDispatchedIDs = uniqueSortedStrings(
		result.ExecutionDispatchedIDs,
	)
	result.EvaluationDispatchedIDs = uniqueSortedStrings(
		result.EvaluationDispatchedIDs,
	)
	result.MaterializationRecoveredRunIDs = uniqueSortedStrings(
		result.MaterializationRecoveredRunIDs,
	)
	result.Errors = uniqueSortedStrings(result.Errors)
	if len(result.ExecutionDispatchedIDs) > 0 ||
		len(result.EvaluationDispatchedIDs) > 0 ||
		len(result.MaterializationRecoveredRunIDs) > 0 ||
		len(result.Errors) > 0 {
		c.app.emitBrowserEvent("shadow_agent:task_continuation", result)
	}
	return result
}
