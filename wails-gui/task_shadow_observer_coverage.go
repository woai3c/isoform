package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	TaskShadowObserverCoverageVersion = "task-shadow-observer-coverage-v2"

	TaskShadowObserverCoverageHealthAwaiting  = "awaiting_instrumented_activity"
	TaskShadowObserverCoverageHealthHealthy   = "healthy"
	TaskShadowObserverCoverageHealthAttention = "attention"

	TaskShadowObserverCoverageCovered         = "covered"
	TaskShadowObserverCoverageCoveredRootOnly = "covered_root_only"
	TaskShadowObserverCoveragePendingDispatch = "pending_dispatch"
	TaskShadowObserverCoverageDispatchFailed  = "dispatch_failed"
	TaskShadowObserverCoverageTraceMissing    = "trace_missing"
	TaskShadowObserverCoverageLinkMismatch    = "link_mismatch"
	TaskShadowObserverCoverageTraceCorrupt    = "trace_corrupt"
	TaskShadowObserverCoverageDuplicateTrace  = "duplicate_trace"
	TaskShadowObserverCoverageSourcePruned    = "source_pruned_or_cleared"
	TaskShadowObserverCoverageUnlinkedTrace   = "unlinked_current_trace"
	TaskShadowObserverCoverageLegacyTrace     = "legacy_trace"
	TaskShadowObserverCoverageScanTruncated   = "scan_truncated"

	taskShadowObserverCoverageTraceScanLimit = 4096
)

type taskShadowObserverCoverageSource struct {
	ID             int64
	StartedAt      int64
	CompletedAt    int64
	GroupID        string
	SessionIDsJSON string
	Expected       bool
	Admission      string
	DispatchStatus string
	DispatchReason string
	TraceID        string
}

type TaskShadowObserverCoverageCounts struct {
	InstrumentedSources int `json:"instrumentedSources"`
	LegacySources       int `json:"legacySources"`
	CurrentTraces       int `json:"currentTraces"`
	LegacyTraces        int `json:"legacyTraces"`
	Covered             int `json:"covered"`
	CoveredRootOnly     int `json:"coveredRootOnly"`
	PendingDispatch     int `json:"pendingDispatch"`
	DispatchFailed      int `json:"dispatchFailed"`
	TraceMissing        int `json:"traceMissing"`
	LinkMismatch        int `json:"linkMismatch"`
	TraceCorrupt        int `json:"traceCorrupt"`
	DuplicateTrace      int `json:"duplicateTrace"`
	SourcePruned        int `json:"sourcePruned"`
	UnlinkedTrace       int `json:"unlinkedTrace"`
	ScanTruncated       int `json:"scanTruncated"`
}

type TaskShadowObserverCoverageItem struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	State              string `json:"state"`
	Reason             string `json:"reason"`
	PipelineRunID      int64  `json:"pipelineRunId,omitempty"`
	TraceID            string `json:"traceId,omitempty"`
	GroupID            string `json:"groupId,omitempty"`
	Admission          string `json:"admission,omitempty"`
	ExpectedFlow       string `json:"expectedFlow,omitempty"`
	TraceFlow          string `json:"traceFlow,omitempty"`
	DispatchStatus     string `json:"dispatchStatus,omitempty"`
	DispatchReason     string `json:"dispatchReason,omitempty"`
	TraceState         string `json:"traceState,omitempty"`
	IntegrityValid     bool   `json:"integrityValid"`
	IntegrityReason    string `json:"integrityReason,omitempty"`
	SourceStartedAt    int64  `json:"sourceStartedAt,omitempty"`
	SourceCompletedAt  int64  `json:"sourceCompletedAt,omitempty"`
	TraceCreatedAt     int64  `json:"traceCreatedAt,omitempty"`
	ObserverRecordedAt int64  `json:"observerRecordedAt,omitempty"`
}

type TaskShadowObserverCoverageSnapshot struct {
	Version       string                           `json:"version"`
	GeneratedAt   int64                            `json:"generatedAt"`
	Limit         int                              `json:"limit"`
	Health        string                           `json:"health"`
	HealthReason  string                           `json:"healthReason"`
	Counts        TaskShadowObserverCoverageCounts `json:"counts"`
	Items         []TaskShadowObserverCoverageItem `json:"items"`
	LoadedItems   int                              `json:"loadedItems"`
	ScannedTraces int                              `json:"scannedTraces"`
}

func taskShadowObserverExpectedFlow(admission string) string {
	if admission == TaskShadowObserverAdmissionEligible {
		return TaskShadowObserverFlowFullOnset
	}
	return TaskShadowObserverFlowContinuation
}

func taskShadowObserverCoverageDispatchStatus(status string) string {
	switch strings.TrimSpace(status) {
	case TaskShadowObserverDispatchPending,
		TaskShadowObserverDispatchRecorded,
		TaskShadowObserverDispatchStoreUnavailable,
		TaskShadowObserverDispatchInputInvalid,
		TaskShadowObserverDispatchRecordFailed:
		return strings.TrimSpace(status)
	default:
		return "invalid"
	}
}

func taskShadowObserverCoverageDispatchReason(status string) string {
	switch status {
	case TaskShadowObserverDispatchPending:
		return "observer dispatch has not finished"
	case TaskShadowObserverDispatchRecorded:
		return "observer trace recorded"
	case TaskShadowObserverDispatchStoreUnavailable:
		return "observer store was unavailable"
	case TaskShadowObserverDispatchInputInvalid:
		return "observer trace input was invalid"
	case TaskShadowObserverDispatchRecordFailed:
		return "observer trace persistence failed"
	default:
		return "observer dispatch state is invalid"
	}
}

func taskShadowObserverCoverageSessionIDs(raw string) ([]string, bool) {
	var result []string
	if err := json.Unmarshal([]byte(raw), &result); err != nil || len(result) == 0 {
		return nil, false
	}
	rawCount := len(result)
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
		if result[index] == "" {
			return nil, false
		}
	}
	// The pipeline row retains session traversal order while observer roots
	// freeze the canonical membership set. Ordering is not provenance; missing,
	// extra, blank, or duplicate session identities still fail closed.
	result = normalizeTaskShadowObserverStrings(
		result,
		taskShadowObserverMaxSessions,
	)
	if len(result) != rawCount {
		return nil, false
	}
	return result, true
}

func taskShadowObserverCoverageStringsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func taskShadowObserverCoverageItemID(
	kind string,
	pipelineRunID int64,
	traceID string,
) string {
	return fmt.Sprintf("%s:%d:%s", kind, pipelineRunID, traceID)
}

func taskShadowObserverCoverageSourceItem(
	source taskShadowObserverCoverageSource,
	trace *TaskShadowObserverTrace,
	events []TaskShadowObserverTraceEvent,
	duplicate bool,
) TaskShadowObserverCoverageItem {
	dispatchStatus := taskShadowObserverCoverageDispatchStatus(
		source.DispatchStatus,
	)
	item := TaskShadowObserverCoverageItem{
		ID:                taskShadowObserverCoverageItemID("source", source.ID, source.TraceID),
		Kind:              "source",
		PipelineRunID:     source.ID,
		TraceID:           source.TraceID,
		GroupID:           source.GroupID,
		Admission:         source.Admission,
		ExpectedFlow:      taskShadowObserverExpectedFlow(source.Admission),
		DispatchStatus:    dispatchStatus,
		DispatchReason:    taskShadowObserverCoverageDispatchReason(dispatchStatus),
		SourceStartedAt:   source.StartedAt,
		SourceCompletedAt: source.CompletedAt,
	}
	if duplicate {
		item.State = TaskShadowObserverCoverageDuplicateTrace
		item.Reason = "more than one observer root claims this source row"
		return item
	}
	if trace == nil {
		switch dispatchStatus {
		case TaskShadowObserverDispatchPending, "":
			item.State = TaskShadowObserverCoveragePendingDispatch
			item.Reason = "source row persisted before observer dispatch completed"
		case TaskShadowObserverDispatchRecorded:
			item.State = TaskShadowObserverCoverageTraceMissing
			item.Reason = "source row records a trace that is not present"
		case TaskShadowObserverDispatchStoreUnavailable,
			TaskShadowObserverDispatchInputInvalid,
			TaskShadowObserverDispatchRecordFailed:
			item.State = TaskShadowObserverCoverageDispatchFailed
			item.Reason = "observer dispatch ended without an immutable root"
		default:
			item.State = TaskShadowObserverCoverageLinkMismatch
			item.Reason = "source row has an unsupported observer dispatch state"
		}
		return item
	}
	item.TraceID = trace.ID
	item.ID = taskShadowObserverCoverageItemID("source", source.ID, trace.ID)
	item.TraceFlow = trace.Flow
	item.TraceCreatedAt = trace.CreatedAt
	item.ObserverRecordedAt = trace.LatestEvent.OccurredAt
	integrityValid, _ := verifyTaskShadowObserverTrace(*trace, events)
	item.IntegrityValid = integrityValid
	if !integrityValid {
		item.IntegrityReason = "observer integrity verification failed"
	}
	item.TraceState = taskShadowObserverDerivedStatus(
		*trace,
		taskShadowObserverRuntimeInstanceID,
		integrityValid,
	)
	if !integrityValid {
		item.State = TaskShadowObserverCoverageTraceCorrupt
		item.Reason = "linked observer root or event chain failed verification"
		return item
	}
	sessionIDs, sessionsValid := taskShadowObserverCoverageSessionIDs(
		source.SessionIDsJSON,
	)
	if trace.SourcePipelineRunID != source.ID ||
		trace.GroupID != source.GroupID ||
		trace.Admission != source.Admission ||
		trace.Flow != item.ExpectedFlow ||
		!sessionsValid ||
		!taskShadowObserverCoverageStringsEqual(sessionIDs, trace.SessionIDs) {
		item.State = TaskShadowObserverCoverageLinkMismatch
		item.Reason = "source and immutable observer provenance do not match"
		return item
	}
	if source.TraceID == trace.ID &&
		dispatchStatus == TaskShadowObserverDispatchRecorded {
		item.State = TaskShadowObserverCoverageCovered
		item.Reason = "source row and immutable observer trace match exactly"
		return item
	}
	if source.TraceID == "" || dispatchStatus == TaskShadowObserverDispatchPending {
		item.State = TaskShadowObserverCoverageCoveredRootOnly
		item.Reason = "immutable observer root committed before source writeback"
		return item
	}
	item.State = TaskShadowObserverCoverageLinkMismatch
	item.Reason = "source trace receipt differs from the immutable observer root"
	return item
}

func taskShadowObserverCoverageTraceItem(
	trace TaskShadowObserverTrace,
	events []TaskShadowObserverTraceEvent,
	state, reason string,
) TaskShadowObserverCoverageItem {
	integrityValid, integrityReason := verifyTaskShadowObserverTrace(trace, events)
	if !integrityValid {
		state = TaskShadowObserverCoverageTraceCorrupt
		reason = "observer root or event chain failed verification"
		integrityReason = "observer integrity verification failed"
	}
	return TaskShadowObserverCoverageItem{
		ID:                 taskShadowObserverCoverageItemID("trace", trace.SourcePipelineRunID, trace.ID),
		Kind:               "trace",
		State:              state,
		Reason:             reason,
		PipelineRunID:      trace.SourcePipelineRunID,
		TraceID:            trace.ID,
		GroupID:            trace.GroupID,
		Admission:          trace.Admission,
		ExpectedFlow:       taskShadowObserverExpectedFlow(trace.Admission),
		TraceFlow:          trace.Flow,
		TraceState:         taskShadowObserverDerivedStatus(trace, taskShadowObserverRuntimeInstanceID, integrityValid),
		IntegrityValid:     integrityValid,
		IntegrityReason:    integrityReason,
		TraceCreatedAt:     trace.CreatedAt,
		ObserverRecordedAt: trace.LatestEvent.OccurredAt,
	}
}

func taskShadowObserverCoverageSeverity(state string) int {
	switch state {
	case TaskShadowObserverCoverageTraceCorrupt,
		TaskShadowObserverCoverageDuplicateTrace,
		TaskShadowObserverCoverageLinkMismatch:
		return 0
	case TaskShadowObserverCoverageTraceMissing,
		TaskShadowObserverCoverageDispatchFailed,
		TaskShadowObserverCoverageUnlinkedTrace,
		TaskShadowObserverCoverageScanTruncated:
		return 1
	case TaskShadowObserverCoveragePendingDispatch,
		TaskShadowObserverCoverageCoveredRootOnly,
		TaskShadowObserverCoverageSourcePruned:
		return 2
	case TaskShadowObserverCoverageCovered:
		return 3
	default:
		return 4
	}
}

func taskShadowObserverCoverageCountItem(
	counts *TaskShadowObserverCoverageCounts,
	state string,
) {
	switch state {
	case TaskShadowObserverCoverageCovered:
		counts.Covered++
	case TaskShadowObserverCoverageCoveredRootOnly:
		counts.CoveredRootOnly++
	case TaskShadowObserverCoveragePendingDispatch:
		counts.PendingDispatch++
	case TaskShadowObserverCoverageDispatchFailed:
		counts.DispatchFailed++
	case TaskShadowObserverCoverageTraceMissing:
		counts.TraceMissing++
	case TaskShadowObserverCoverageLinkMismatch:
		counts.LinkMismatch++
	case TaskShadowObserverCoverageTraceCorrupt:
		counts.TraceCorrupt++
	case TaskShadowObserverCoverageDuplicateTrace:
		counts.DuplicateTrace++
	case TaskShadowObserverCoverageSourcePruned:
		counts.SourcePruned++
	case TaskShadowObserverCoverageUnlinkedTrace:
		counts.UnlinkedTrace++
	case TaskShadowObserverCoverageScanTruncated:
		counts.ScanTruncated++
	}
}

func buildTaskShadowObserverCoverageSnapshot(
	sources []taskShadowObserverCoverageSource,
	traces []TaskShadowObserverTrace,
	eventsByTrace map[string][]TaskShadowObserverTraceEvent,
	legacyTraceCount, totalCurrentTraceCount, limit int,
	now int64,
) TaskShadowObserverCoverageSnapshot {
	result := TaskShadowObserverCoverageSnapshot{
		Version:       TaskShadowObserverCoverageVersion,
		GeneratedAt:   now,
		Limit:         normalizeTaskShadowObserverListLimit(limit),
		Health:        TaskShadowObserverCoverageHealthAwaiting,
		HealthReason:  "No instrumented activity group has reached observer dispatch yet.",
		Items:         make([]TaskShadowObserverCoverageItem, 0),
		ScannedTraces: len(traces),
	}
	result.Counts.LegacyTraces = legacyTraceCount
	result.Counts.CurrentTraces = totalCurrentTraceCount
	loadedCurrentTraceCount := 0
	sourcesByID := make(map[int64]taskShadowObserverCoverageSource)
	for _, source := range sources {
		if !source.Expected {
			result.Counts.LegacySources++
			continue
		}
		result.Counts.InstrumentedSources++
		sourcesByID[source.ID] = source
	}
	tracesBySource := make(map[int64][]TaskShadowObserverTrace)
	tracesByID := make(map[string]TaskShadowObserverTrace)
	for _, trace := range traces {
		tracesByID[trace.ID] = trace
		if trace.ContractVersion == TaskShadowObserverTraceContractVersion &&
			trace.CreatedAt > 0 {
			loadedCurrentTraceCount++
			if trace.SourcePipelineRunID > 0 {
				tracesBySource[trace.SourcePipelineRunID] = append(
					tracesBySource[trace.SourcePipelineRunID],
					trace,
				)
			}
		}
	}
	matchedTraceIDs := make(map[string]bool)
	for _, source := range sourcesByID {
		candidates := tracesBySource[source.ID]
		if len(candidates) == 0 && source.TraceID != "" {
			if trace, ok := tracesByID[source.TraceID]; ok {
				candidates = []TaskShadowObserverTrace{trace}
			}
		}
		var trace *TaskShadowObserverTrace
		if len(candidates) == 1 {
			copy := candidates[0]
			trace = &copy
			matchedTraceIDs[copy.ID] = true
		}
		item := taskShadowObserverCoverageSourceItem(
			source,
			trace,
			eventsByTrace[func() string {
				if trace == nil {
					return ""
				}
				return trace.ID
			}()],
			len(candidates) > 1,
		)
		result.Items = append(result.Items, item)
		taskShadowObserverCoverageCountItem(&result.Counts, item.State)
	}
	for _, trace := range traces {
		if trace.ContractVersion == TaskShadowObserverTraceContractVersionV1 {
			continue
		}
		if matchedTraceIDs[trace.ID] {
			continue
		}
		state := ""
		reason := ""
		switch {
		case trace.SourcePipelineRunID == 0:
			state = TaskShadowObserverCoverageUnlinkedTrace
			reason = "current observer root has no persisted source-row identity"
		case sourcesByID[trace.SourcePipelineRunID].ID == 0:
			state = TaskShadowObserverCoverageSourcePruned
			reason = "exact source row is outside retained pipeline diagnostics or was cleared"
		default:
			continue
		}
		item := taskShadowObserverCoverageTraceItem(
			trace,
			eventsByTrace[trace.ID],
			state,
			reason,
		)
		result.Items = append(result.Items, item)
		taskShadowObserverCoverageCountItem(&result.Counts, item.State)
	}
	if totalCurrentTraceCount > loadedCurrentTraceCount {
		item := TaskShadowObserverCoverageItem{
			ID:    "trace-scan-truncated",
			Kind:  "scan",
			State: TaskShadowObserverCoverageScanTruncated,
			Reason: fmt.Sprintf(
				"%d current traces exceed the bounded integrity scan",
				totalCurrentTraceCount-loadedCurrentTraceCount,
			),
		}
		result.Items = append(result.Items, item)
		taskShadowObserverCoverageCountItem(&result.Counts, item.State)
	}
	attentionCount := result.Counts.CoveredRootOnly +
		result.Counts.PendingDispatch + result.Counts.DispatchFailed +
		result.Counts.TraceMissing + result.Counts.LinkMismatch +
		result.Counts.TraceCorrupt + result.Counts.DuplicateTrace +
		result.Counts.UnlinkedTrace +
		result.Counts.ScanTruncated
	if attentionCount > 0 {
		result.Health = TaskShadowObserverCoverageHealthAttention
		result.HealthReason = "One or more production source-to-observer links require attention."
	} else if result.Counts.InstrumentedSources > 0 || totalCurrentTraceCount > 0 {
		result.Health = TaskShadowObserverCoverageHealthHealthy
		if result.Counts.SourcePruned > 0 {
			result.HealthReason = fmt.Sprintf(
				"Every retained instrumented source is covered; %d integrity-valid observer roots outlived bounded source diagnostics.",
				result.Counts.SourcePruned,
			)
		} else {
			result.HealthReason = "Every instrumented production source has one exact integrity-valid observer trace."
		}
	}
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		if severity := taskShadowObserverCoverageSeverity(left.State) -
			taskShadowObserverCoverageSeverity(right.State); severity != 0 {
			return severity < 0
		}
		leftAt := max(left.SourceStartedAt, left.TraceCreatedAt)
		rightAt := max(right.SourceStartedAt, right.TraceCreatedAt)
		if leftAt != rightAt {
			return leftAt > rightAt
		}
		return left.ID < right.ID
	})
	if len(result.Items) > result.Limit {
		result.Items = result.Items[:result.Limit]
	}
	result.LoadedItems = len(result.Items)
	return result
}

func (s *ActivityStore) listTaskShadowObserverCoverageSources() (
	[]taskShadowObserverCoverageSource,
	error,
) {
	rows, err := s.db.Query(`
		SELECT id, started_at, completed_at, COALESCE(group_id, ''),
			COALESCE(session_ids, ''), COALESCE(shadow_observer_expected, 0),
			COALESCE(shadow_observer_admission, ''),
			COALESCE(shadow_observer_dispatch_status, ''),
			COALESCE(shadow_observer_dispatch_reason, ''),
			COALESCE(shadow_observer_trace_id, '')
		FROM activity_pipeline_runs
		WHERE kind = 'group'
		ORDER BY started_at DESC, id DESC
		LIMIT ?`, activityPipelineMaxEntries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]taskShadowObserverCoverageSource, 0)
	for rows.Next() {
		var source taskShadowObserverCoverageSource
		if err := rows.Scan(
			&source.ID,
			&source.StartedAt,
			&source.CompletedAt,
			&source.GroupID,
			&source.SessionIDsJSON,
			&source.Expected,
			&source.Admission,
			&source.DispatchStatus,
			&source.DispatchReason,
			&source.TraceID,
		); err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, rows.Err()
}

func (s *ActivityStore) listTaskShadowObserverCoverageTraces(
	cutoff int64,
) ([]TaskShadowObserverTrace, int, int, error) {
	var currentCount, legacyCount int
	if err := s.db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN contract_version = ? AND created_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN contract_version = ? THEN 1 ELSE 0 END), 0)
		FROM task_shadow_observer_traces_v1`,
		TaskShadowObserverTraceContractVersion,
		cutoff,
		TaskShadowObserverTraceContractVersionV1,
	).Scan(&currentCount, &legacyCount); err != nil {
		return nil, 0, 0, err
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
		WHERE t.created_at >= ?
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT ?`, cutoff, taskShadowObserverCoverageTraceScanLimit)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	result := make([]TaskShadowObserverTrace, 0)
	for rows.Next() {
		trace, err := scanTaskShadowObserverTrace(rows)
		if err != nil {
			return nil, 0, 0, err
		}
		result = append(result, trace)
	}
	return result, currentCount, legacyCount, rows.Err()
}

func (s *ActivityStore) listTaskShadowObserverCoverageEvents(
	cutoff int64,
) (map[string][]TaskShadowObserverTraceEvent, error) {
	// Scan event chains for the same bounded root set as the root query. A
	// truncated gate must not defeat its own memory bound by loading every event.
	rows, err := s.db.Query(`
		SELECT e.id, e.trace_id, e.sequence, e.status, e.payload_json,
			e.payload_hash, e.previous_hash, e.occurred_at, e.created_at
		FROM task_shadow_observer_trace_events_v1 e
		JOIN (
			SELECT id
			FROM task_shadow_observer_traces_v1
			WHERE created_at >= ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		) scanned ON scanned.id = e.trace_id
		ORDER BY e.trace_id ASC, e.sequence ASC`,
		cutoff,
		taskShadowObserverCoverageTraceScanLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]TaskShadowObserverTraceEvent)
	for rows.Next() {
		event, err := scanTaskShadowObserverEvent(rows)
		if err != nil {
			return nil, err
		}
		result[event.TraceID] = append(result[event.TraceID], event)
	}
	return result, rows.Err()
}

func (a *App) GetTaskShadowObserverCoverageSnapshot(
	limit int,
) (TaskShadowObserverCoverageSnapshot, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return TaskShadowObserverCoverageSnapshot{}, err
	}
	now := time.Now().UnixMilli()
	cutoff := now - activityPipelineMaxAge.Milliseconds()
	sources, err := store.listTaskShadowObserverCoverageSources()
	if err != nil {
		return TaskShadowObserverCoverageSnapshot{}, err
	}
	traces, currentCount, legacyCount, err :=
		store.listTaskShadowObserverCoverageTraces(cutoff)
	if err != nil {
		return TaskShadowObserverCoverageSnapshot{}, err
	}
	eventsByTrace, err := store.listTaskShadowObserverCoverageEvents(cutoff)
	if err != nil {
		return TaskShadowObserverCoverageSnapshot{}, err
	}
	return buildTaskShadowObserverCoverageSnapshot(
		sources,
		traces,
		eventsByTrace,
		legacyCount,
		currentCount,
		limit,
		now,
	), nil
}

func init() {
	browserRPCAllowlist["GetTaskShadowObserverCoverageSnapshot"] = true
}
