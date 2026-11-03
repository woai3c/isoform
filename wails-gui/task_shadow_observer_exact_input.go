package main

import (
	"errors"
	"strings"
)

const taskShadowObserverExactInputLookupLimit = 32

var errTaskShadowObserverDuplicateOriginalNotReusable = errors.New(
	"observer duplicate original is no longer reusable",
)

func (s *ActivityStore) loadVerifiedTaskShadowObserverTrace(
	traceID string,
) (TaskShadowObserverTrace, bool, string, error) {
	const projectionAttempts = 3
	lastReason := "observer trace failed integrity verification"
	for attempt := 0; attempt < projectionAttempts; attempt++ {
		trace, err := s.GetTaskShadowObserverTrace(traceID)
		if err != nil {
			return TaskShadowObserverTrace{}, false, "", err
		}
		events, err := s.ListTaskShadowObserverTraceEvents(trace.ID)
		if err != nil {
			return TaskShadowObserverTrace{}, false, "", err
		}
		if valid, reason := verifyTaskShadowObserverTrace(trace, events); valid {
			return trace, true, "", nil
		} else {
			lastReason = reason
		}
		if lastReason != "observer latest-event projection mismatch" {
			break
		}
	}
	return TaskShadowObserverTrace{}, false, lastReason, nil
}

func taskShadowObserverHasPartialEffect(
	payload TaskShadowObserverTraceEventPayload,
) bool {
	return len(payload.TaskEpisodeIDs) > 0 ||
		len(payload.ExpiredTaskEpisodeIDs) > 0 ||
		len(payload.ShadowRunIDs) > 0 ||
		len(payload.WorkflowDecisions) > 0 ||
		len(payload.Continuation.ExecutionDispatchedIDs) > 0 ||
		len(payload.Continuation.EvaluationDispatchedIDs) > 0 ||
		len(payload.Continuation.MaterializationRecoveredIDs) > 0
}

func taskShadowObserverReusableOriginal(
	trace TaskShadowObserverTrace,
) (bool, string) {
	if trace.InputIdentityHash == "" || trace.LatestEvent.Payload.Duplicate != nil {
		return false, "trace does not establish an original exact-input result"
	}
	switch trace.LatestEvent.Status {
	case TaskShadowObserverEventCompleted:
		return true, "exact input completed"
	case TaskShadowObserverEventSkipped:
		return true, "exact input was intentionally skipped in production"
	case TaskShadowObserverEventStarted:
		if trace.RuntimeInstanceID == taskShadowObserverRuntimeInstanceID {
			return true, "an exact input check is already queued or running"
		}
		return false, "prior-process check was interrupted"
	case TaskShadowObserverEventCompletedWithErrors:
		if taskShadowObserverHasPartialEffect(trace.LatestEvent.Payload) {
			return true, "exact input already produced a partial contained effect"
		}
		return false, "error-only check remains retryable"
	case TaskShadowObserverEventCancelled:
		return false, "canceled check remains retryable"
	case TaskShadowObserverEventFailed:
		return false, "failed check remains retryable"
	default:
		return false, "unsupported observer state"
	}
}

func (s *ActivityStore) FindReusableTaskShadowObserverTrace(
	input TaskShadowObserverTraceInput,
) (TaskShadowObserverTrace, bool, string, error) {
	if s == nil || s.db == nil {
		return TaskShadowObserverTrace{}, false, "", errors.New(
			"activity store unavailable",
		)
	}
	input, err := normalizeTaskShadowObserverTraceInput(input)
	if err != nil {
		return TaskShadowObserverTrace{}, false, "", err
	}
	rows, err := s.db.Query(`
		SELECT `+taskShadowObserverTraceSelect+`
		FROM task_shadow_observer_traces_v1 t
		JOIN task_shadow_observer_trace_events_v1 e
			ON e.trace_id = t.id
			AND e.sequence = (
				SELECT MAX(latest.sequence)
				FROM task_shadow_observer_trace_events_v1 latest
				WHERE latest.trace_id = t.id
			)
		WHERE t.contract_version = ?
			AND t.input_identity_hash = ?
			AND json_type(e.payload_json, '$.duplicate') IS NULL
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT ?`,
		TaskShadowObserverTraceContractVersion,
		input.InputIdentityHash,
		taskShadowObserverExactInputLookupLimit,
	)
	if err != nil {
		return TaskShadowObserverTrace{}, false, "", err
	}
	candidates := make([]TaskShadowObserverTrace, 0)
	for rows.Next() {
		trace, scanErr := scanTaskShadowObserverTrace(rows)
		if scanErr != nil {
			_ = rows.Close()
			return TaskShadowObserverTrace{}, false, "", scanErr
		}
		candidates = append(candidates, trace)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return TaskShadowObserverTrace{}, false, "", err
	}
	if err := rows.Close(); err != nil {
		return TaskShadowObserverTrace{}, false, "", err
	}
	for _, trace := range candidates {
		current, valid, _, eventErr := s.loadVerifiedTaskShadowObserverTrace(
			trace.ID,
		)
		if eventErr != nil || !valid {
			continue
		}
		if current.InputIdentityHash != input.InputIdentityHash ||
			current.Flow != input.Flow ||
			current.GroupID != input.GroupID ||
			current.SessionManifestHash != input.SessionManifestHash ||
			current.WindowStart != input.WindowStart ||
			current.WindowEnd != input.WindowEnd ||
			current.SessionCount != input.SessionCount ||
			current.DeclaredEventCount != input.DeclaredEventCount ||
			current.RetainedEventCount != input.RetainedEventCount ||
			current.RetainedEventManifestHash != input.RetainedEventManifestHash {
			continue
		}
		if reusable, reason := taskShadowObserverReusableOriginal(current); reusable {
			return current, true, strings.TrimSpace(reason), nil
		}
	}
	return TaskShadowObserverTrace{}, false, "", nil
}
