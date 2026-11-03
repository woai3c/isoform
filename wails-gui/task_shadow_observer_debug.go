package main

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

const taskShadowObserverDebugVersion = "task-shadow-observer-debug-v1"

type TaskShadowObserverDebugSummary struct {
	ID                         string `json:"id"`
	State                      string `json:"state"`
	IntegrityValid             bool   `json:"integrityValid"`
	IntegrityReason            string `json:"integrityReason,omitempty"`
	GroupID                    string `json:"groupId"`
	Flow                       string `json:"flow"`
	Admission                  string `json:"admission"`
	AdmissionReason            string `json:"admissionReason,omitempty"`
	ShadowEnabledAtAdmission   bool   `json:"shadowEnabledAtAdmission"`
	SourcePipelineRunID        int64  `json:"sourcePipelineRunId"`
	WindowStart                int64  `json:"windowStart"`
	WindowEnd                  int64  `json:"windowEnd"`
	SessionCount               int    `json:"sessionCount"`
	DeclaredEventCount         int    `json:"declaredEventCount"`
	RetainedEventCount         int    `json:"retainedEventCount"`
	InputIdentityHash          string `json:"inputIdentityHash,omitempty"`
	DuplicateOfTraceID         string `json:"duplicateOfTraceId,omitempty"`
	DuplicatePolicyVersion     string `json:"duplicatePolicyVersion,omitempty"`
	FirstRunDecision           string `json:"firstRunDecision,omitempty"`
	OnsetWindowCount           int    `json:"onsetWindowCount"`
	RetainedOnsetEvidenceCount int    `json:"retainedOnsetEvidenceCount"`
	ProductionAttemptCount     int    `json:"productionAttemptCount"`
	DiagnosticAttemptCount     int    `json:"diagnosticAttemptCount"`
	TaskEpisodeCount           int    `json:"taskEpisodeCount"`
	ShadowRunCount             int    `json:"shadowRunCount"`
	WorkflowDecisionCount      int    `json:"workflowDecisionCount"`
	TerminalReason             string `json:"terminalReason,omitempty"`
	LatestEventAt              int64  `json:"latestEventAt"`
	CreatedAt                  int64  `json:"createdAt"`
}

type TaskShadowObserverDebugCounts struct {
	Total               int `json:"total"`
	Running             int `json:"running"`
	Interrupted         int `json:"interrupted"`
	Completed           int `json:"completed"`
	CompletedWithErrors int `json:"completedWithErrors"`
	Skipped             int `json:"skipped"`
	Cancelled           int `json:"cancelled"`
	Failed              int `json:"failed"`
	Corrupt             int `json:"corrupt"`
}

type TaskShadowObserverDebugSnapshot struct {
	Version     string                           `json:"version"`
	GeneratedAt int64                            `json:"generatedAt"`
	Limit       int                              `json:"limit"`
	Counts      TaskShadowObserverDebugCounts    `json:"counts"`
	Traces      []TaskShadowObserverDebugSummary `json:"traces"`
}

type TaskShadowObserverDebugAttemptLink struct {
	ID            string `json:"id"`
	Role          string `json:"role"`
	Found         bool   `json:"found"`
	Phase         string `json:"phase,omitempty"`
	Status        string `json:"status,omitempty"`
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"promptVersion,omitempty"`
	PromptHash    string `json:"promptHash,omitempty"`
	InputHash     string `json:"inputHash,omitempty"`
	StartedAt     int64  `json:"startedAt,omitempty"`
	CompletedAt   int64  `json:"completedAt,omitempty"`
	MissingReason string `json:"missingReason,omitempty"`
}

type TaskShadowObserverDebugDetail struct {
	Version      string                               `json:"version"`
	GeneratedAt  int64                                `json:"generatedAt"`
	Summary      TaskShadowObserverDebugSummary       `json:"summary"`
	Trace        TaskShadowObserverTrace              `json:"trace"`
	Events       []TaskShadowObserverTraceEvent       `json:"events"`
	AttemptLinks []TaskShadowObserverDebugAttemptLink `json:"attemptLinks"`
}

func init() {
	browserRPCAllowlist["GetTaskShadowObserverDebugSnapshot"] = true
	browserRPCAllowlist["GetTaskShadowObserverDebugDetail"] = true
}

func taskShadowObserverDebugSummary(
	trace TaskShadowObserverTrace,
	events []TaskShadowObserverTraceEvent,
) TaskShadowObserverDebugSummary {
	integrityValid, integrityReason := verifyTaskShadowObserverTrace(trace, events)
	state := taskShadowObserverDerivedStatus(
		trace,
		taskShadowObserverRuntimeInstanceID,
		integrityValid,
	)
	payload := trace.LatestEvent.Payload
	duplicateOfTraceID := ""
	duplicatePolicyVersion := ""
	if payload.Duplicate != nil {
		duplicateOfTraceID = payload.Duplicate.OriginalTraceID
		duplicatePolicyVersion = payload.Duplicate.PolicyVersion
	}
	return TaskShadowObserverDebugSummary{
		ID:                         trace.ID,
		State:                      state,
		IntegrityValid:             integrityValid,
		IntegrityReason:            integrityReason,
		GroupID:                    trace.GroupID,
		Flow:                       trace.Flow,
		Admission:                  trace.Admission,
		AdmissionReason:            trace.AdmissionReason,
		ShadowEnabledAtAdmission:   trace.ShadowEnabledAtAdmission,
		SourcePipelineRunID:        trace.SourcePipelineRunID,
		WindowStart:                trace.WindowStart,
		WindowEnd:                  trace.WindowEnd,
		SessionCount:               trace.SessionCount,
		DeclaredEventCount:         trace.DeclaredEventCount,
		RetainedEventCount:         trace.RetainedEventCount,
		InputIdentityHash:          trace.InputIdentityHash,
		DuplicateOfTraceID:         duplicateOfTraceID,
		DuplicatePolicyVersion:     duplicatePolicyVersion,
		FirstRunDecision:           payload.FirstRunDecision,
		OnsetWindowCount:           payload.OnsetWindowCount,
		RetainedOnsetEvidenceCount: payload.RetainedOnsetEvidenceCount,
		ProductionAttemptCount:     len(payload.ProductionOnsetAttemptIDs),
		DiagnosticAttemptCount:     len(payload.DiagnosticOnsetAttemptIDs),
		TaskEpisodeCount:           len(payload.TaskEpisodeIDs),
		ShadowRunCount:             len(payload.ShadowRunIDs),
		WorkflowDecisionCount:      len(payload.WorkflowDecisions),
		TerminalReason:             payload.Reason,
		LatestEventAt:              trace.LatestEvent.OccurredAt,
		CreatedAt:                  trace.CreatedAt,
	}
}

func taskShadowObserverDebugCount(
	counts *TaskShadowObserverDebugCounts,
	state string,
) {
	if counts == nil {
		return
	}
	counts.Total++
	switch state {
	case TaskShadowObserverDerivedRunning:
		counts.Running++
	case TaskShadowObserverDerivedInterrupted:
		counts.Interrupted++
	case TaskShadowObserverEventCompleted:
		counts.Completed++
	case TaskShadowObserverEventCompletedWithErrors:
		counts.CompletedWithErrors++
	case TaskShadowObserverEventSkipped:
		counts.Skipped++
	case TaskShadowObserverEventCancelled:
		counts.Cancelled++
	case TaskShadowObserverEventFailed:
		counts.Failed++
	default:
		counts.Corrupt++
	}
}

func (a *App) GetTaskShadowObserverDebugSnapshot(
	limit int,
) (TaskShadowObserverDebugSnapshot, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return TaskShadowObserverDebugSnapshot{}, err
	}
	limit = normalizeTaskShadowObserverListLimit(limit)
	traces, err := store.ListTaskShadowObserverTraces(limit)
	if err != nil {
		return TaskShadowObserverDebugSnapshot{}, err
	}
	result := TaskShadowObserverDebugSnapshot{
		Version:     taskShadowObserverDebugVersion,
		GeneratedAt: time.Now().UnixMilli(),
		Limit:       limit,
		Traces:      make([]TaskShadowObserverDebugSummary, 0, len(traces)),
	}
	for _, trace := range traces {
		events, eventErr := store.ListTaskShadowObserverTraceEvents(trace.ID)
		if eventErr != nil {
			summary := taskShadowObserverDebugSummary(trace, nil)
			summary.IntegrityValid = false
			summary.IntegrityReason = eventErr.Error()
			summary.State = TaskShadowObserverDerivedCorrupt
			result.Traces = append(result.Traces, summary)
			taskShadowObserverDebugCount(&result.Counts, summary.State)
			continue
		}
		summary := taskShadowObserverDebugSummary(trace, events)
		result.Traces = append(result.Traces, summary)
		taskShadowObserverDebugCount(&result.Counts, summary.State)
	}
	return result, nil
}

func taskShadowObserverAttemptRoles(
	payload TaskShadowObserverTraceEventPayload,
) map[string]string {
	result := make(map[string]string)
	for _, id := range payload.ProductionOnsetAttemptIDs {
		result[id] = "production_onset"
	}
	for _, id := range payload.DiagnosticOnsetAttemptIDs {
		result[id] = "diagnostic_onset"
	}
	for _, id := range payload.LifecycleAttemptIDs {
		result[id] = "lifecycle"
	}
	return result
}

func taskShadowObserverDebugAttemptLinks(
	store *ActivityStore,
	payload TaskShadowObserverTraceEventPayload,
) []TaskShadowObserverDebugAttemptLink {
	roles := taskShadowObserverAttemptRoles(payload)
	ids := make([]string, 0, len(roles))
	for id := range roles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]TaskShadowObserverDebugAttemptLink, 0, len(ids))
	for _, id := range ids {
		link := TaskShadowObserverDebugAttemptLink{ID: id, Role: roles[id]}
		attempt, err := store.GetTaskAgentAttempt(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				link.MissingReason = "linked immutable attempt was not found"
			} else {
				link.MissingReason = err.Error()
			}
			result = append(result, link)
			continue
		}
		link.Found = true
		link.Phase = attempt.Phase
		link.Status = attempt.Status
		link.Model = attempt.ModelName
		link.PromptVersion = attempt.PromptVersion
		link.PromptHash = attempt.PromptHash
		link.InputHash = attempt.InputHash
		link.StartedAt = attempt.StartedAt
		link.CompletedAt = attempt.CompletedAt
		result = append(result, link)
	}
	return result
}

func (a *App) GetTaskShadowObserverDebugDetail(
	traceID string,
) (TaskShadowObserverDebugDetail, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return TaskShadowObserverDebugDetail{}, err
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return TaskShadowObserverDebugDetail{}, errors.New("observer trace ID is required")
	}
	trace, err := store.GetTaskShadowObserverTrace(traceID)
	if err != nil {
		return TaskShadowObserverDebugDetail{}, err
	}
	events, err := store.ListTaskShadowObserverTraceEvents(trace.ID)
	if err != nil {
		return TaskShadowObserverDebugDetail{}, err
	}
	return TaskShadowObserverDebugDetail{
		Version:      taskShadowObserverDebugVersion,
		GeneratedAt:  time.Now().UnixMilli(),
		Summary:      taskShadowObserverDebugSummary(trace, events),
		Trace:        trace,
		Events:       events,
		AttemptLinks: taskShadowObserverDebugAttemptLinks(store, trace.LatestEvent.Payload),
	}, nil
}
