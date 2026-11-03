package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type TaskLifecyclePersistenceResult struct {
	ObservationKey string   `json:"observationKey"`
	ReasoningKind  string   `json:"reasoningKind,omitempty"`
	EvidenceIDs    []string `json:"evidenceIds"`
	TransitionIDs  []string `json:"transitionIds"`
	LinkIDs        []string `json:"linkIds"`
}

type taskLifecycleReasoningEvidencePayload struct {
	Version      string   `json:"version"`
	Kind         string   `json:"kind"`
	SourceQuote  string   `json:"sourceQuote"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

func validateTaskLifecycleReasoningEvidence(
	item TaskEpisodeEvidence,
) (taskLifecycleReasoningEvidencePayload, error) {
	var payload taskLifecycleReasoningEvidencePayload
	if item.Role != TaskEpisodeEvidenceReasoning ||
		item.SourceKind != "task_lifecycle_reasoning" {
		return payload, errors.New("reasoning evidence is not canonical lifecycle evidence")
	}
	if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
		return payload, errors.New("canonical lifecycle reasoning payload is malformed")
	}
	payload.Version = strings.TrimSpace(payload.Version)
	payload.Kind = normalizeTaskLifecycleReasoningKind(payload.Kind)
	payload.SourceQuote = strings.TrimSpace(payload.SourceQuote)
	payload.EvidenceRefs = uniqueSortedStrings(payload.EvidenceRefs)
	if payload.Version != taskLifecyclePromptVersion {
		return payload, fmt.Errorf(
			"unsupported lifecycle reasoning version %q",
			payload.Version,
		)
	}
	if payload.Kind == "" || payload.SourceQuote == "" ||
		len(payload.EvidenceRefs) == 0 {
		return payload, errors.New("canonical lifecycle reasoning payload is incomplete")
	}
	if normalizeTaskReasoningText(payload.SourceQuote) !=
		normalizeTaskReasoningText(item.Summary) {
		return payload, errors.New("canonical lifecycle reasoning quote does not match evidence")
	}
	switch payload.Kind {
	case TaskLifecycleReasoningStatedRationale:
		if !taskOnsetReasoningHasExplicitCue(payload.SourceQuote) {
			return payload, errors.New("canonical lifecycle rationale has no explicit reason cue")
		}
	case TaskLifecycleReasoningCorrection:
		if !taskLifecycleReasoningHasCorrectionCue(payload.SourceQuote) {
			return payload, errors.New("canonical lifecycle correction has no correction cue")
		}
	}
	expectedSourceID := deterministicTaskEpisodeID(
		"task-lifecycle-reasoning-source",
		strings.Join([]string{
			item.TaskEpisodeID,
			payload.Kind,
			normalizeTaskReasoningText(payload.SourceQuote),
			strings.Join(payload.EvidenceRefs, ","),
		}, "\x00"),
	)
	if item.SourceID != expectedSourceID {
		return payload, errors.New("canonical lifecycle reasoning source identity does not match payload")
	}
	return payload, nil
}

func appendLifecycleReasoningEvidence(
	store *ActivityStore,
	observation TaskLifecycleReasoningObservation,
	createdAt int64,
) (string, error) {
	payload, err := json.Marshal(taskLifecycleReasoningEvidencePayload{
		Version:      taskLifecyclePromptVersion,
		Kind:         observation.Kind,
		SourceQuote:  observation.SourceQuote,
		EvidenceRefs: observation.EvidenceRefs,
	})
	if err != nil {
		return "", err
	}
	sourceID := deterministicTaskEpisodeID(
		"task-lifecycle-reasoning-source",
		strings.Join([]string{
			observation.EpisodeID,
			observation.Kind,
			normalizeTaskReasoningText(observation.SourceQuote),
			strings.Join(observation.EvidenceRefs, ","),
		}, "\x00"),
	)
	item, _, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: observation.EpisodeID,
		Role:          TaskEpisodeEvidenceReasoning,
		SourceKind:    "task_lifecycle_reasoning",
		SourceID:      sourceID,
		Summary:       observation.SourceQuote,
		PayloadJSON:   string(payload),
		ObservedAt:    observation.OccurredAt,
		CreatedAt:     createdAt,
	})
	if err != nil {
		return "", err
	}
	return item.ID, nil
}

func taskLifecycleEvidenceRole(
	observation TaskLifecycleObservation,
	evidence TaskLifecycleEvidence,
) string {
	switch observation.Kind {
	case TaskLifecycleKindCompleted:
		if taskLifecycleStrongCompletionKind(evidence.Kind) {
			return TaskEpisodeEvidenceResult
		}
		return TaskEpisodeEvidenceAction
	case TaskLifecycleKindResult:
		if taskLifecycleStrongResultKind(evidence.Kind) {
			return TaskEpisodeEvidenceResult
		}
		return TaskEpisodeEvidenceAction
	case TaskLifecycleKindUnknown:
		return TaskEpisodeEvidenceContradiction
	default:
		return TaskEpisodeEvidenceAction
	}
}

func taskLifecycleEvidenceSummary(
	observation TaskLifecycleObservation,
	evidence TaskLifecycleEvidence,
	role string,
) string {
	if role == TaskEpisodeEvidenceResult &&
		observation.ResultSummary != "" {
		return observation.ResultSummary + ": " + evidence.Summary
	}
	return evidence.Summary
}

func appendLifecycleEvidence(
	store *ActivityStore,
	episodeID string,
	observation TaskLifecycleObservation,
	evidenceByRef map[string]TaskLifecycleEvidence,
	createdAt int64,
) ([]string, error) {
	payload, err := json.Marshal(observation)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(observation.EvidenceRefs))
	for _, ref := range observation.EvidenceRefs {
		evidence, ok := evidenceByRef[ref]
		if !ok {
			return nil, fmt.Errorf("lifecycle evidence %q is unavailable", ref)
		}
		role := taskLifecycleEvidenceRole(observation, evidence)
		summary := taskLifecycleEvidenceSummary(observation, evidence, role)
		item, _, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
			TaskEpisodeID: episodeID,
			Role:          role,
			SourceKind:    evidence.SourceKind,
			SourceID:      evidence.SourceID,
			Summary:       summary,
			PayloadJSON:   string(payload),
			ObservedAt:    evidence.OccurredAt,
			CreatedAt:     createdAt,
		})
		if err != nil {
			return nil, err
		}
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func appendLifecycleTransition(
	store *ActivityStore,
	episodeID, toStatus, reason string,
	occurredAt, createdAt int64,
) (string, error) {
	episode, err := store.GetTaskEpisode(episodeID)
	if err != nil {
		return "", err
	}
	if episode.CurrentStatus == toStatus {
		return "", nil
	}
	// Detection is allowed to happen after the observed user event. Preserve
	// that event time on TaskEpisode evidence, while keeping state-transition
	// sequence timestamps monotonic for the append-only lifecycle ledger.
	var latestOccurredAt int64
	if err := store.db.QueryRow(`
		SELECT occurred_at
		FROM task_episode_transitions
		WHERE task_episode_id = ?
		ORDER BY sequence DESC
		LIMIT 1`,
		episodeID,
	).Scan(&latestOccurredAt); err != nil {
		return "", err
	}
	occurredAt = max(occurredAt, latestOccurredAt)
	transition, _, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: episodeID,
		ToStatus:      toStatus,
		Reason:        reason,
		OccurredAt:    occurredAt,
		CreatedAt:     createdAt,
	})
	if err != nil {
		return "", err
	}
	return transition.ID, nil
}

func ensureLifecycleEpisodeActive(
	store *ActivityStore,
	episodeID, reason string,
	occurredAt, createdAt int64,
) ([]string, error) {
	episode, err := store.GetTaskEpisode(episodeID)
	if err != nil {
		return nil, err
	}
	switch episode.CurrentStatus {
	case TaskEpisodeStatusCandidate, TaskEpisodeStatusPaused:
		id, err := appendLifecycleTransition(
			store,
			episodeID,
			TaskEpisodeStatusActive,
			reason,
			occurredAt,
			createdAt,
		)
		if err != nil {
			return nil, err
		}
		if id != "" {
			return []string{id}, nil
		}
	case TaskEpisodeStatusActive:
		return []string{}, nil
	default:
		return nil, fmt.Errorf(
			"task episode %s cannot become active from %s",
			episodeID,
			episode.CurrentStatus,
		)
	}
	return []string{}, nil
}

func persistLifecycleState(
	store *ActivityStore,
	observation TaskLifecycleObservation,
	createdAt int64,
) ([]string, []string, error) {
	reason := strings.TrimSpace(strings.Join([]string{
		taskLifecyclePromptVersion,
		observation.LocalKey,
		observation.Reason,
	}, ": "))
	transitionIDs := make([]string, 0)
	linkIDs := make([]string, 0)
	appendTransition := func(episodeID, status string) error {
		id, err := appendLifecycleTransition(
			store,
			episodeID,
			status,
			reason,
			observation.OccurredAt,
			createdAt,
		)
		if err != nil {
			return err
		}
		if id != "" {
			transitionIDs = append(transitionIDs, id)
		}
		return nil
	}

	switch observation.Kind {
	case TaskLifecycleKindContinued:
		for _, episodeID := range observation.EpisodeIDs {
			activated, err := ensureLifecycleEpisodeActive(
				store,
				episodeID,
				reason+": task continuation confirmed",
				observation.OccurredAt,
				createdAt,
			)
			if err != nil {
				return nil, nil, err
			}
			transitionIDs = append(transitionIDs, activated...)
		}
	case TaskLifecycleKindCompleted:
		for _, episodeID := range observation.EpisodeIDs {
			episode, err := store.GetTaskEpisode(episodeID)
			if err != nil {
				return nil, nil, err
			}
			if episode.CurrentStatus == TaskEpisodeStatusCompleted {
				continue
			}
			activated, err := ensureLifecycleEpisodeActive(
				store,
				episodeID,
				reason+": task continuation confirmed",
				observation.OccurredAt,
				createdAt,
			)
			if err != nil {
				return nil, nil, err
			}
			transitionIDs = append(transitionIDs, activated...)
			if err := appendTransition(episodeID, TaskEpisodeStatusCompleted); err != nil {
				return nil, nil, err
			}
		}
	case TaskLifecycleKindPaused:
		for _, episodeID := range observation.EpisodeIDs {
			activated, err := ensureLifecycleEpisodeActive(
				store,
				episodeID,
				reason+": task continuation confirmed",
				observation.OccurredAt,
				createdAt,
			)
			if err != nil {
				return nil, nil, err
			}
			transitionIDs = append(transitionIDs, activated...)
			if err := appendTransition(episodeID, TaskEpisodeStatusPaused); err != nil {
				return nil, nil, err
			}
		}
	case TaskLifecycleKindResumed:
		for _, episodeID := range observation.EpisodeIDs {
			if err := appendTransition(episodeID, TaskEpisodeStatusActive); err != nil {
				return nil, nil, err
			}
		}
	case TaskLifecycleKindAbandoned:
		for _, episodeID := range observation.EpisodeIDs {
			if err := appendTransition(episodeID, TaskEpisodeStatusAbandoned); err != nil {
				return nil, nil, err
			}
		}
	case TaskLifecycleKindSwitched:
		for _, episodeID := range observation.EpisodeIDs {
			episode, err := store.GetTaskEpisode(episodeID)
			if err != nil {
				return nil, nil, err
			}
			if episode.CurrentStatus == TaskEpisodeStatusActive {
				if err := appendTransition(episodeID, TaskEpisodeStatusPaused); err != nil {
					return nil, nil, err
				}
			}
		}
		for _, targetID := range observation.TargetEpisodeIDs {
			target, err := store.GetTaskEpisode(targetID)
			if err != nil {
				return nil, nil, err
			}
			if target.CurrentStatus == TaskEpisodeStatusCandidate ||
				target.CurrentStatus == TaskEpisodeStatusPaused {
				if err := appendTransition(targetID, TaskEpisodeStatusActive); err != nil {
					return nil, nil, err
				}
			}
		}
	case TaskLifecycleKindMerged:
		if len(observation.TargetEpisodeIDs) != 1 {
			return nil, nil, errors.New("merge observation requires one target episode")
		}
		targetID := observation.TargetEpisodeIDs[0]
		for _, episodeID := range observation.EpisodeIDs {
			if err := appendTransition(episodeID, TaskEpisodeStatusMerged); err != nil {
				return nil, nil, err
			}
			link, _, err := store.AppendTaskEpisodeLink(TaskEpisodeLink{
				SourceEpisodeID: episodeID,
				TargetEpisodeID: targetID,
				Relation:        TaskEpisodeLinkMergedInto,
				CreatedAt:       createdAt,
			})
			if err != nil {
				return nil, nil, err
			}
			linkIDs = append(linkIDs, link.ID)
		}
	case TaskLifecycleKindSplit:
		if len(observation.EpisodeIDs) != 1 {
			return nil, nil, errors.New("split observation requires one source episode")
		}
		sourceID := observation.EpisodeIDs[0]
		if err := appendTransition(sourceID, TaskEpisodeStatusSplit); err != nil {
			return nil, nil, err
		}
		for _, targetID := range observation.TargetEpisodeIDs {
			link, _, err := store.AppendTaskEpisodeLink(TaskEpisodeLink{
				SourceEpisodeID: sourceID,
				TargetEpisodeID: targetID,
				Relation:        TaskEpisodeLinkSplitInto,
				CreatedAt:       createdAt,
			})
			if err != nil {
				return nil, nil, err
			}
			linkIDs = append(linkIDs, link.ID)
		}
	case TaskLifecycleKindResult, TaskLifecycleKindUnknown:
		// Evidence is append-only; these observations do not rewrite state.
	}
	sort.Strings(transitionIDs)
	sort.Strings(linkIDs)
	return transitionIDs, linkIDs, nil
}

func PersistTaskLifecycleObservations(
	store *ActivityStore,
	input TaskLifecycleInput,
	response TaskLifecycleResponse,
	createdAt int64,
) ([]TaskLifecyclePersistenceResult, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	if createdAt <= 0 {
		return nil, errors.New("task lifecycle persistence requires a stable creation timestamp")
	}
	normalizedInput, err := normalizeTaskLifecycleInput(input)
	if err != nil {
		return nil, err
	}
	response = normalizeTaskLifecycleResponse(normalizedInput, response)
	evidenceByRef := make(map[string]TaskLifecycleEvidence, len(normalizedInput.SuffixEvidence))
	for _, evidence := range normalizedInput.SuffixEvidence {
		evidenceByRef[evidence.Ref] = evidence
	}

	results := make([]TaskLifecyclePersistenceResult, 0, len(response.Observations))
	for _, observation := range response.Observations {
		result := TaskLifecyclePersistenceResult{
			ObservationKey: observation.LocalKey,
			EvidenceIDs:    []string{},
			TransitionIDs:  []string{},
			LinkIDs:        []string{},
		}
		episodeIDs := append([]string(nil), observation.EpisodeIDs...)
		for _, targetID := range observation.TargetEpisodeIDs {
			if !containsString(episodeIDs, targetID) {
				episodeIDs = append(episodeIDs, targetID)
			}
		}
		sort.Strings(episodeIDs)
		for _, episodeID := range episodeIDs {
			ids, err := appendLifecycleEvidence(
				store,
				episodeID,
				observation,
				evidenceByRef,
				createdAt,
			)
			if err != nil {
				return results, err
			}
			result.EvidenceIDs = append(result.EvidenceIDs, ids...)
		}
		result.TransitionIDs, result.LinkIDs, err = persistLifecycleState(
			store,
			observation,
			createdAt,
		)
		if err != nil {
			return results, err
		}
		sort.Strings(result.EvidenceIDs)
		results = append(results, result)
	}
	for _, observation := range response.ReasoningObservations {
		evidenceID, err := appendLifecycleReasoningEvidence(
			store,
			observation,
			createdAt,
		)
		if err != nil {
			return results, err
		}
		results = append(results, TaskLifecyclePersistenceResult{
			ObservationKey: observation.LocalKey,
			ReasoningKind:  observation.Kind,
			EvidenceIDs:    []string{evidenceID},
			TransitionIDs:  []string{},
			LinkIDs:        []string{},
		})
	}
	return results, nil
}
