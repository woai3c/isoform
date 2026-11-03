package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	taskLifecycleExpiryPolicyVersion = "task-lifecycle-state-bound-lease-v2"
	taskLifecycleCandidateHorizon    = 90 * time.Minute
	taskLifecycleActiveHorizon       = 24 * time.Hour
	taskLifecyclePausedHorizon       = 72 * time.Hour
	taskLifecycleExpiryBatchSize     = 500
)

type TaskLifecycleObservationLease struct {
	PolicyVersion      string `json:"policyVersion"`
	Status             string `json:"status"`
	Open               bool   `json:"open"`
	AnchorKind         string `json:"anchorKind"`
	AnchorTransitionID string `json:"anchorTransitionId,omitempty"`
	AnchorAt           int64  `json:"anchorAt"`
	HorizonMs          int64  `json:"horizonMs"`
	DeadlineAt         int64  `json:"deadlineAt"`
	Due                bool   `json:"due"`
	EvidenceRenews     bool   `json:"evidenceRenews"`
}

type TaskLifecycleExpiryResult struct {
	PolicyVersion   string   `json:"policyVersion"`
	ScannedCount    int      `json:"scannedCount"`
	ExpiredIDs      []string `json:"expiredIds"`
	Errors          []string `json:"errors"`
	BacklogDeferred bool     `json:"backlogDeferred"`
}

func taskLifecycleObservationHorizon(status string) (time.Duration, bool) {
	switch status {
	case TaskEpisodeStatusCandidate:
		return taskLifecycleCandidateHorizon, true
	case TaskEpisodeStatusActive:
		return taskLifecycleActiveHorizon, true
	case TaskEpisodeStatusPaused:
		return taskLifecyclePausedHorizon, true
	default:
		return 0, false
	}
}

func taskLifecycleStateBoundary(
	detail TaskEpisodeDetail,
) (int64, string, string) {
	anchorAt := detail.Episode.InferredStartAt
	anchorKind := "inferred_start"
	if detail.Episode.DetectedAt >= anchorAt {
		anchorAt = detail.Episode.DetectedAt
		anchorKind = "detected"
	}
	var current *TaskEpisodeTransition
	for i := range detail.Transitions {
		transition := &detail.Transitions[i]
		if transition.ToStatus != detail.Episode.CurrentStatus {
			continue
		}
		if current == nil || transition.Sequence > current.Sequence {
			current = transition
		}
	}
	if current != nil && current.OccurredAt >= anchorAt {
		return current.OccurredAt, "state_transition", current.ID
	}
	return anchorAt, anchorKind, ""
}

func taskLifecycleObservationLease(
	detail TaskEpisodeDetail,
	cutoffAt int64,
) TaskLifecycleObservationLease {
	anchorAt, anchorKind, transitionID := taskLifecycleStateBoundary(detail)
	lease := TaskLifecycleObservationLease{
		PolicyVersion:      taskLifecycleExpiryPolicyVersion,
		Status:             detail.Episode.CurrentStatus,
		AnchorKind:         anchorKind,
		AnchorTransitionID: transitionID,
		AnchorAt:           anchorAt,
		EvidenceRenews:     false,
	}
	horizon, open := taskLifecycleObservationHorizon(detail.Episode.CurrentStatus)
	lease.Open = open
	if !open {
		return lease
	}
	lease.HorizonMs = horizon.Milliseconds()
	lease.DeadlineAt = anchorAt + lease.HorizonMs
	lease.Due = cutoffAt > 0 && anchorAt > 0 && cutoffAt >= lease.DeadlineAt
	return lease
}

func taskLifecycleEpisodeExpiryDue(
	detail TaskEpisodeDetail,
	cutoffAt int64,
) (bool, int64, time.Duration) {
	lease := taskLifecycleObservationLease(detail, cutoffAt)
	return lease.Due, lease.AnchorAt, time.Duration(lease.HorizonMs) * time.Millisecond
}

func (s *ActivityStore) listTaskEpisodeExpiryCandidates(
	limit int,
) ([]string, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("activity store is unavailable")
	}
	if limit <= 0 || limit > taskLifecycleExpiryBatchSize {
		limit = taskLifecycleExpiryBatchSize
	}
	rows, err := s.db.Query(`
		SELECT episode.id
		FROM task_episodes episode
		JOIN task_episode_transitions transition
			ON transition.task_episode_id = episode.id
			AND transition.sequence = (
				SELECT MAX(latest.sequence)
				FROM task_episode_transitions latest
				WHERE latest.task_episode_id = episode.id
			)
		WHERE transition.to_status IN ('candidate', 'active', 'paused')
		ORDER BY episode.inferred_start_at ASC, episode.id ASC
		LIMIT ?`,
		limit+1,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	deferred := len(ids) > limit
	if deferred {
		ids = ids[:limit]
	}
	return ids, deferred, nil
}

func expireStaleTaskEpisodes(
	store *ActivityStore,
	cutoffAt, createdAt int64,
) TaskLifecycleExpiryResult {
	result := TaskLifecycleExpiryResult{
		PolicyVersion: taskLifecycleExpiryPolicyVersion,
		ExpiredIDs:    []string{},
		Errors:        []string{},
	}
	if store == nil || store.db == nil {
		result.Errors = append(result.Errors, "activity store is unavailable")
		return result
	}
	if cutoffAt <= 0 || createdAt < cutoffAt {
		result.Errors = append(result.Errors, "task lifecycle expiry timestamps are invalid")
		return result
	}
	ids, deferred, err := store.listTaskEpisodeExpiryCandidates(
		taskLifecycleExpiryBatchSize,
	)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.BacklogDeferred = deferred
	for _, id := range ids {
		result.ScannedCount++
		detail, err := store.GetTaskEpisodeDetail(id)
		if err != nil {
			result.Errors = append(result.Errors, id+": "+err.Error())
			continue
		}
		due, anchorAt, horizon := taskLifecycleEpisodeExpiryDue(
			detail,
			cutoffAt,
		)
		if !due {
			continue
		}
		priorStatus := detail.Episode.CurrentStatus
		reason := fmt.Sprintf(
			"%s: %s state-bound observation lease elapsed; lease_anchor_at=%d; horizon_ms=%d",
			taskLifecycleExpiryPolicyVersion,
			priorStatus,
			anchorAt,
			horizon.Milliseconds(),
		)
		_, _, err = store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
			TaskEpisodeID: id,
			FromStatus:    priorStatus,
			ToStatus:      TaskEpisodeStatusExpired,
			Reason:        reason,
			OccurredAt:    cutoffAt,
			CreatedAt:     createdAt,
		})
		if err != nil {
			current, readErr := store.GetTaskEpisode(id)
			if readErr == nil && current.CurrentStatus == TaskEpisodeStatusExpired {
				result.ExpiredIDs = append(result.ExpiredIDs, id)
				continue
			}
			result.Errors = append(
				result.Errors,
				strings.TrimSpace(id+": "+err.Error()),
			)
			continue
		}
		result.ExpiredIDs = append(result.ExpiredIDs, id)
	}
	result.ExpiredIDs = uniqueSortedStrings(result.ExpiredIDs)
	return result
}
