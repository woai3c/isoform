package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppendWorkPatternProfileFromEvaluation derives a new immutable revision only
// after the user's terminal work and persisted comparison exist. The frozen
// profile used by this run remains untouched and cannot contain this outcome.
func (a *App) AppendWorkPatternProfileFromEvaluation(
	taskEpisodeID string,
	shadowRunID string,
	shadowEvaluationID string,
	createdAt int64,
) (WorkPatternProfileSnapshot, bool, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"activity store is unavailable",
		)
	}
	store := a.activity.store
	taskEpisodeID = strings.TrimSpace(taskEpisodeID)
	shadowRunID = strings.TrimSpace(shadowRunID)
	shadowEvaluationID = strings.TrimSpace(shadowEvaluationID)
	if taskEpisodeID == "" || shadowRunID == "" || shadowEvaluationID == "" {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"profile outcome learning requires episode, run, and evaluation IDs",
		)
	}
	episode, err := store.GetTaskEpisode(taskEpisodeID)
	if err != nil {
		return WorkPatternProfileSnapshot{}, false, err
	}
	if err := store.taskEpisodeRepeatedOnsetError(episode.ID); err != nil {
		return WorkPatternProfileSnapshot{}, false, fmt.Errorf(
			"profile outcome learning excluded: %w",
			err,
		)
	}
	switch episode.CurrentStatus {
	case TaskEpisodeStatusCompleted, TaskEpisodeStatusAbandoned:
	default:
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"profile outcome learning requires a terminal observed task",
		)
	}
	run, err := store.GetShadowRun(shadowRunID)
	if err != nil {
		return WorkPatternProfileSnapshot{}, false, err
	}
	if run.TaskEpisodeID != episode.ID || run.Mode != "task_mirror" {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"profile outcome learning linkage is invalid",
		)
	}
	if run.WorkProfileSnapshotID != "" {
		frozen, err := store.GetWorkPatternProfileSnapshot(
			run.WorkProfileSnapshotID,
		)
		if err != nil {
			return WorkPatternProfileSnapshot{}, false, err
		}
		if frozen.SHA256 != run.WorkProfileSHA256 ||
			frozen.SourceCutoffAt >= episode.InferredStartAt {
			return WorkPatternProfileSnapshot{}, false, errors.New(
				"profile outcome learning rejected an invalid frozen prior",
			)
		}
	}
	excluded, err := store.isShadowTaskRunFirstRunLearningExcluded(run.ID)
	if err != nil {
		return WorkPatternProfileSnapshot{}, false, err
	}
	if excluded {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			firstRunContaminationReason,
		)
	}
	evaluations, err := store.ListShadowRunEvaluations(run.ID)
	if err != nil {
		return WorkPatternProfileSnapshot{}, false, err
	}
	var evaluation *ShadowRunEvaluation
	for index := range evaluations {
		if evaluations[index].ID == shadowEvaluationID {
			evaluation = &evaluations[index]
			break
		}
	}
	if evaluation == nil ||
		evaluation.TaskEvaluationInput == nil ||
		evaluation.TaskEvaluation == nil {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"profile outcome learning requires a persisted task evaluation",
		)
	}
	if evaluation.TaskEvaluationInput.TaskEpisodeID != episode.ID ||
		evaluation.TaskEvaluationInput.ShadowRunID != run.ID {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"profile outcome learning evaluation linkage is invalid",
		)
	}
	learning, _, err := a.BuildAndPersistShadowTaskLearning(
		run.CapabilityKey,
		max(createdAt, evaluation.CreatedAt),
	)
	if err != nil {
		return WorkPatternProfileSnapshot{}, false, fmt.Errorf(
			"materialize evaluated Shadow learning: %w",
			err,
		)
	}
	cutoffAt := max(learning.CreatedAt, evaluation.CreatedAt)
	if cutoffAt <= 0 {
		cutoffAt = time.Now().UnixMilli()
	}
	snapshot, added, err := store.EnsureWorkPatternProfileSnapshot(
		cutoffAt,
		max(createdAt, time.Now().UnixMilli()),
	)
	if err != nil {
		return WorkPatternProfileSnapshot{}, false, err
	}
	if snapshot.SourceCutoffAt <= episode.InferredStartAt {
		return WorkPatternProfileSnapshot{}, false, errors.New(
			"learned profile revision does not follow the observed outcome",
		)
	}
	a.emitBrowserEvent("work_pattern_profile:revised", map[string]any{
		"profileSnapshotId":   snapshot.ID,
		"taskEpisodeId":       episode.ID,
		"shadowRunId":         run.ID,
		"evaluationId":        evaluation.ID,
		"shadowLearningRunId": learning.ID,
		"added":               added,
	})
	return snapshot, added, nil
}
