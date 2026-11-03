package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"wails-gui/internal/platform/activitymon"
)

const (
	TaskShadowObserverTraceContractVersionV1 = "task-shadow-observer-trace-v1"
	TaskShadowObserverTraceContractVersion   = "task-shadow-observer-trace-v2"
	TaskShadowObserverInputIdentityVersion   = "task-shadow-observer-input-v1"
	TaskShadowObserverDuplicatePolicyVersion = "task-shadow-observer-exact-input-v1"

	TaskShadowObserverSourceActivityTriager = "activity_triager"

	TaskShadowObserverFlowFullOnset    = "full_onset"
	TaskShadowObserverFlowContinuation = "lifecycle_continuation"

	TaskShadowObserverAdmissionEligible    = "eligible"
	TaskShadowObserverAdmissionPrefiltered = "prefiltered"
	TaskShadowObserverAdmissionCooldown    = "cooldown"

	TaskShadowObserverEventStarted             = "started"
	TaskShadowObserverEventCompleted           = "completed"
	TaskShadowObserverEventCompletedWithErrors = "completed_with_errors"
	TaskShadowObserverEventSkipped             = "skipped"
	TaskShadowObserverEventCancelled           = "cancelled"
	TaskShadowObserverEventFailed              = "failed"

	TaskShadowObserverDerivedRunning     = "running"
	TaskShadowObserverDerivedInterrupted = "interrupted"
	TaskShadowObserverDerivedCorrupt     = "corrupt"

	TaskShadowObserverFirstRunIncluded = "included"
	TaskShadowObserverFirstRunExcluded = "excluded"
	TaskShadowObserverFirstRunError    = "error"
	TaskShadowObserverFirstRunUnknown  = "not_checked"

	TaskShadowWorkflowDecisionCapturedOnset  = "captured_onset"
	TaskShadowWorkflowDecisionReconciliation = "reconciliation"

	TaskShadowWorkflowStateOutsideRegistry        = "outside_registry"
	TaskShadowWorkflowStateFrozenContextNotReady  = "frozen_context_not_ready"
	TaskShadowWorkflowStateFirstRunExcluded       = "first_run_excluded"
	TaskShadowWorkflowStateRepeatedOnsetExcluded  = "repeated_onset_excluded"
	TaskShadowWorkflowStateExistingRun            = "existing_run"
	TaskShadowWorkflowStateRunCreated             = "run_created"
	TaskShadowWorkflowStateRunCreateFailed        = "run_create_failed"
	TaskShadowWorkflowStateCandidateLimitDeferred = "candidate_limit_deferred"
	TaskShadowWorkflowStateReadinessError         = "readiness_error"

	taskShadowObserverDefaultListLimit = 50
	taskShadowObserverMaxListLimit     = 200
	taskShadowObserverMaxSessions      = 256
	taskShadowObserverMaxLinkedIDs     = 256
	taskShadowObserverMaxDecisions     = 128
	taskShadowObserverMaxMessages      = 128
	taskShadowObserverMaxReasonBytes   = 4096
	taskShadowObserverMaxMessageBytes  = 8192
	taskShadowObserverMaxIDBytes       = 512
)

var taskShadowObserverRuntimeInstanceID = uuid.NewString()

type TaskShadowWorkflowReadinessDecision struct {
	Source                   string `json:"source"`
	TaskEpisodeID            string `json:"taskEpisodeId"`
	ProductionOnsetAttemptID string `json:"productionOnsetAttemptId,omitempty"`
	Workflow                 string `json:"workflow"`
	State                    string `json:"state"`
	Reason                   string `json:"reason,omitempty"`
	FrozenContextHash        string `json:"frozenContextHash,omitempty"`
	FrozenManifestCount      int    `json:"frozenManifestCount"`
	FrozenIncludedCount      int    `json:"frozenIncludedCount"`
	HasExactActivitySnapshot bool   `json:"hasExactActivitySnapshot"`
	ShadowRunID              string `json:"shadowRunId,omitempty"`
	ExecutionDispatched      bool   `json:"executionDispatched"`
}

type TaskShadowObserverContinuationSummary struct {
	Version                     string   `json:"version,omitempty"`
	Trigger                     string   `json:"trigger,omitempty"`
	ScannedRunCount             int      `json:"scannedRunCount"`
	ExecutionDispatchedIDs      []string `json:"executionDispatchedIds"`
	EvaluationDispatchedIDs     []string `json:"evaluationDispatchedIds"`
	MaterializationRecoveredIDs []string `json:"materializationRecoveredRunIds,omitempty"`
	ErrorCount                  int      `json:"errorCount"`
}

type TaskShadowObserverDuplicateReceipt struct {
	PolicyVersion     string `json:"policyVersion"`
	OriginalTraceID   string `json:"originalTraceId"`
	OriginalRootHash  string `json:"originalRootHash"`
	OriginalEventHash string `json:"originalEventHash"`
	OriginalStatus    string `json:"originalStatus"`
	InputIdentityHash string `json:"inputIdentityHash"`
}

type TaskShadowObserverTraceEventPayload struct {
	Reason                     string                                `json:"reason,omitempty"`
	ShadowEnabledAtExecution   *bool                                 `json:"shadowEnabledAtExecution,omitempty"`
	FirstRunDecision           string                                `json:"firstRunDecision,omitempty"`
	FirstRunReason             string                                `json:"firstRunReason,omitempty"`
	OnsetWindowCount           int                                   `json:"onsetWindowCount"`
	RetainedOnsetEvidenceCount int                                   `json:"retainedOnsetEvidenceCount"`
	ProductionOnsetAttemptIDs  []string                              `json:"productionOnsetAttemptIds"`
	DiagnosticOnsetAttemptIDs  []string                              `json:"diagnosticOnsetAttemptIds"`
	LifecycleAttemptIDs        []string                              `json:"lifecycleAttemptIds"`
	TaskEpisodeIDs             []string                              `json:"taskEpisodeIds"`
	ExpiredTaskEpisodeIDs      []string                              `json:"expiredTaskEpisodeIds"`
	ShadowRunIDs               []string                              `json:"shadowRunIds"`
	WorkflowDecisions          []TaskShadowWorkflowReadinessDecision `json:"workflowDecisions"`
	Continuation               TaskShadowObserverContinuationSummary `json:"continuation"`
	Duplicate                  *TaskShadowObserverDuplicateReceipt   `json:"duplicate,omitempty"`
	Diagnostics                []string                              `json:"diagnostics"`
	Errors                     []string                              `json:"errors"`
}

type TaskShadowObserverTraceInput struct {
	ID                        string
	RuntimeInstanceID         string
	GroupID                   string
	SessionIDs                []string
	SessionManifestHash       string
	Source                    string
	Flow                      string
	Admission                 string
	AdmissionReason           string
	WindowStart               int64
	WindowEnd                 int64
	SessionCount              int
	DeclaredEventCount        int
	RetainedEventCount        int
	RetainedEventManifestHash string
	InputIdentityHash         string
	ShadowEnabledAtAdmission  bool
	SourcePipelineRunID       int64
	CreatedAt                 int64
}

type TaskShadowObserverTraceEvent struct {
	ID           string                              `json:"id"`
	TraceID      string                              `json:"traceId"`
	Sequence     int                                 `json:"sequence"`
	Status       string                              `json:"status"`
	Payload      TaskShadowObserverTraceEventPayload `json:"payload"`
	PayloadHash  string                              `json:"payloadHash"`
	PreviousHash string                              `json:"previousHash"`
	OccurredAt   int64                               `json:"occurredAt"`
	CreatedAt    int64                               `json:"createdAt"`
}

type TaskShadowObserverTrace struct {
	ID                        string                       `json:"id"`
	ContractVersion           string                       `json:"contractVersion"`
	RuntimeInstanceID         string                       `json:"runtimeInstanceId"`
	GroupID                   string                       `json:"groupId"`
	SessionIDs                []string                     `json:"sessionIds"`
	SessionManifestHash       string                       `json:"sessionManifestHash"`
	Source                    string                       `json:"source"`
	Flow                      string                       `json:"flow"`
	Admission                 string                       `json:"admission"`
	AdmissionReason           string                       `json:"admissionReason,omitempty"`
	WindowStart               int64                        `json:"windowStart"`
	WindowEnd                 int64                        `json:"windowEnd"`
	SessionCount              int                          `json:"sessionCount"`
	DeclaredEventCount        int                          `json:"declaredEventCount"`
	RetainedEventCount        int                          `json:"retainedEventCount"`
	RetainedEventManifestHash string                       `json:"retainedEventManifestHash"`
	InputIdentityHash         string                       `json:"inputIdentityHash,omitempty"`
	ShadowEnabledAtAdmission  bool                         `json:"shadowEnabledAtAdmission"`
	SourcePipelineRunID       int64                        `json:"sourcePipelineRunId"`
	RootHash                  string                       `json:"rootHash"`
	CreatedAt                 int64                        `json:"createdAt"`
	LatestEvent               TaskShadowObserverTraceEvent `json:"latestEvent"`
}

type taskShadowObserverRootPayload struct {
	ID                        string   `json:"id"`
	ContractVersion           string   `json:"contractVersion"`
	RuntimeInstanceID         string   `json:"runtimeInstanceId"`
	GroupID                   string   `json:"groupId"`
	SessionIDs                []string `json:"sessionIds"`
	SessionManifestHash       string   `json:"sessionManifestHash"`
	Source                    string   `json:"source"`
	Flow                      string   `json:"flow"`
	Admission                 string   `json:"admission"`
	AdmissionReason           string   `json:"admissionReason,omitempty"`
	WindowStart               int64    `json:"windowStart"`
	WindowEnd                 int64    `json:"windowEnd"`
	SessionCount              int      `json:"sessionCount"`
	DeclaredEventCount        int      `json:"declaredEventCount"`
	RetainedEventCount        int      `json:"retainedEventCount"`
	RetainedEventManifestHash string   `json:"retainedEventManifestHash"`
	ShadowEnabledAtAdmission  bool     `json:"shadowEnabledAtAdmission"`
	SourcePipelineRunID       *int64   `json:"sourcePipelineRunId,omitempty"`
	CreatedAt                 int64    `json:"createdAt"`
}

type taskShadowObserverSessionManifestEntry struct {
	Ordinal    int    `json:"ordinal"`
	ID         string `json:"id"`
	StartTime  int64  `json:"startTime"`
	EndTime    int64  `json:"endTime"`
	EventCount int    `json:"eventCount"`
	CreatedAt  int64  `json:"createdAt"`
}

type taskShadowObserverEventManifestEntry struct {
	Ordinal     int                   `json:"ordinal"`
	Timestamp   int64                 `json:"timestamp"`
	Kind        activitymon.EventKind `json:"kind"`
	ContentHash string                `json:"contentHash"`
}

type taskShadowObserverInputIdentityPayload struct {
	Version                   string `json:"version"`
	Flow                      string `json:"flow"`
	GroupID                   string `json:"groupId"`
	SessionManifestHash       string `json:"sessionManifestHash"`
	WindowStart               int64  `json:"windowStart"`
	WindowEnd                 int64  `json:"windowEnd"`
	SessionCount              int    `json:"sessionCount"`
	DeclaredEventCount        int    `json:"declaredEventCount"`
	RetainedEventCount        int    `json:"retainedEventCount"`
	RetainedEventManifestHash string `json:"retainedEventManifestHash"`
}

type taskShadowObserverEventEnvelope struct {
	Status     string                              `json:"status"`
	Payload    TaskShadowObserverTraceEventPayload `json:"payload"`
	OccurredAt int64                               `json:"occurredAt"`
	CreatedAt  int64                               `json:"createdAt"`
}

func taskShadowObserverHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func taskShadowObserverCanonicalHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return taskShadowObserverHash(encoded), nil
}

func taskShadowObserverInputIdentityHash(
	flow, groupID, sessionManifestHash string,
	windowStart, windowEnd int64,
	sessionCount, declaredEventCount, retainedEventCount int,
	retainedEventManifestHash string,
) (string, error) {
	return taskShadowObserverCanonicalHash(taskShadowObserverInputIdentityPayload{
		Version:                   TaskShadowObserverInputIdentityVersion,
		Flow:                      flow,
		GroupID:                   groupID,
		SessionManifestHash:       sessionManifestHash,
		WindowStart:               windowStart,
		WindowEnd:                 windowEnd,
		SessionCount:              sessionCount,
		DeclaredEventCount:        declaredEventCount,
		RetainedEventCount:        retainedEventCount,
		RetainedEventManifestHash: retainedEventManifestHash,
	})
}

func taskShadowObserverInputIdentityFromInput(
	input TaskShadowObserverTraceInput,
) (string, error) {
	return taskShadowObserverInputIdentityHash(
		input.Flow,
		input.GroupID,
		input.SessionManifestHash,
		input.WindowStart,
		input.WindowEnd,
		input.SessionCount,
		input.DeclaredEventCount,
		input.RetainedEventCount,
		input.RetainedEventManifestHash,
	)
}

func taskShadowObserverInputIdentityFromTrace(
	trace TaskShadowObserverTrace,
) (string, error) {
	return taskShadowObserverInputIdentityHash(
		trace.Flow,
		trace.GroupID,
		trace.SessionManifestHash,
		trace.WindowStart,
		trace.WindowEnd,
		trace.SessionCount,
		trace.DeclaredEventCount,
		trace.RetainedEventCount,
		trace.RetainedEventManifestHash,
	)
}

func taskShadowObserverBool(value bool) *bool {
	result := value
	return &result
}

func normalizeTaskShadowObserverListLimit(limit int) int {
	switch {
	case limit <= 0:
		return taskShadowObserverDefaultListLimit
	case limit > taskShadowObserverMaxListLimit:
		return taskShadowObserverMaxListLimit
	default:
		return limit
	}
}

func validateTaskShadowObserverID(value, field string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > taskShadowObserverMaxIDBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", field, taskShadowObserverMaxIDBytes)
	}
	return value, nil
}

func truncateTaskShadowObserverText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	suffix := "\n... <observer diagnostic truncated> ..."
	budget := max(0, limit-len(suffix))
	value = value[:budget]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + suffix
}

func normalizeTaskShadowObserverStrings(values []string, limit int) []string {
	values = uniqueSortedStrings(values)
	if len(values) > limit {
		values = append([]string(nil), values[:limit]...)
	}
	return values
}

func normalizeTaskShadowObserverMessages(values []string) []string {
	result := make([]string, 0, min(len(values), taskShadowObserverMaxMessages))
	for _, value := range values {
		value = taskShadowObserverDiagnosticReceipt(value)
		if value != "" {
			result = append(result, value)
		}
	}
	result = uniqueSortedStrings(result)
	if len(result) > taskShadowObserverMaxMessages {
		result = result[:taskShadowObserverMaxMessages]
	}
	return result
}

func taskShadowObserverSafeDiagnosticCategory(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "capture",
		"context",
		"existing lifecycle",
		"first-run episode exclusion",
		"first-run exclusion",
		"lifecycle expiry",
		"load frozen work pattern profile",
		"new lifecycle",
		"observer diagnostic",
		"onset",
		"onset ablation",
		"panic",
		"pre-onset activity context",
		"reconcile episodes",
		"reconcile first-run exclusion",
		"reconcile shadow run",
		"reconcile shadow runs",
		"shadow run",
		"work profile",
		"work profile context",
		"work profile snapshot":
		return value
	default:
		return "observer diagnostic"
	}
}

func taskShadowObserverDiagnosticReceipt(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	const markerText = " [detail sha256:"
	if marker := strings.Index(value, markerText); marker > 0 &&
		strings.LastIndex(value, markerText) == marker &&
		strings.HasSuffix(value, "]") &&
		taskShadowObserverSafeDiagnosticCategory(value[:marker]) == value[:marker] {
		digest := value[marker+len(markerText) : len(value)-1]
		if len(digest) == 64 {
			if _, err := hex.DecodeString(digest); err == nil {
				return value
			}
		}
	}
	switch value {
	case "activity store unavailable",
		"agent runner unavailable",
		"continued existing Task Episodes without onset detection",
		"task lifecycle expiry backlog exceeded the bounded per-group batch":
		return value
	}
	category := taskShadowObserverSafeDiagnosticCategory("")
	if prefix, _, found := strings.Cut(value, ":"); found {
		category = taskShadowObserverSafeDiagnosticCategory(prefix)
	}
	digest := taskShadowObserverHash([]byte(value))
	return truncateTaskShadowObserverText(
		fmt.Sprintf("%s [detail sha256:%s]", category, digest),
		taskShadowObserverMaxMessageBytes,
	)
}

func normalizeTaskShadowObserverHash(value, field string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return "", fmt.Errorf("%s must be a SHA-256 digest", field)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("%s must be hexadecimal: %w", field, err)
	}
	return value, nil
}

func normalizeTaskShadowWorkflowDecision(
	decision TaskShadowWorkflowReadinessDecision,
) (TaskShadowWorkflowReadinessDecision, error) {
	decision.Source = strings.TrimSpace(decision.Source)
	switch decision.Source {
	case TaskShadowWorkflowDecisionCapturedOnset,
		TaskShadowWorkflowDecisionReconciliation:
	default:
		return decision, fmt.Errorf("unsupported workflow decision source %q", decision.Source)
	}
	var err error
	decision.TaskEpisodeID, err = validateTaskShadowObserverID(
		decision.TaskEpisodeID,
		"workflow decision task episode ID",
		true,
	)
	if err != nil {
		return decision, err
	}
	decision.ProductionOnsetAttemptID, err = validateTaskShadowObserverID(
		decision.ProductionOnsetAttemptID,
		"workflow decision production onset attempt ID",
		false,
	)
	if err != nil {
		return decision, err
	}
	decision.Workflow, err = validateTaskShadowObserverID(
		decision.Workflow,
		"workflow decision workflow",
		true,
	)
	if err != nil {
		return decision, err
	}
	decision.State = strings.TrimSpace(decision.State)
	switch decision.State {
	case TaskShadowWorkflowStateOutsideRegistry,
		TaskShadowWorkflowStateFrozenContextNotReady,
		TaskShadowWorkflowStateFirstRunExcluded,
		TaskShadowWorkflowStateRepeatedOnsetExcluded,
		TaskShadowWorkflowStateExistingRun,
		TaskShadowWorkflowStateRunCreated,
		TaskShadowWorkflowStateRunCreateFailed,
		TaskShadowWorkflowStateCandidateLimitDeferred,
		TaskShadowWorkflowStateReadinessError:
	default:
		return decision, fmt.Errorf("unsupported workflow decision state %q", decision.State)
	}
	decision.Reason = truncateTaskShadowObserverText(
		decision.Reason,
		taskShadowObserverMaxReasonBytes,
	)
	if decision.State == TaskShadowWorkflowStateRunCreateFailed ||
		decision.State == TaskShadowWorkflowStateReadinessError {
		decision.Reason = taskShadowObserverDiagnosticReceipt(decision.Reason)
	}
	if decision.FrozenContextHash != "" {
		decision.FrozenContextHash, err = normalizeTaskShadowObserverHash(
			decision.FrozenContextHash,
			"workflow decision frozen context hash",
		)
		if err != nil {
			return decision, err
		}
	}
	if decision.FrozenManifestCount < 0 || decision.FrozenIncludedCount < 0 ||
		decision.FrozenIncludedCount > decision.FrozenManifestCount {
		return decision, errors.New("workflow decision has invalid frozen context counts")
	}
	decision.ShadowRunID, err = validateTaskShadowObserverID(
		decision.ShadowRunID,
		"workflow decision shadow run ID",
		false,
	)
	return decision, err
}

func normalizeTaskShadowObserverDuplicateReceipt(
	receipt TaskShadowObserverDuplicateReceipt,
) (TaskShadowObserverDuplicateReceipt, error) {
	receipt.PolicyVersion = strings.TrimSpace(receipt.PolicyVersion)
	if receipt.PolicyVersion != TaskShadowObserverDuplicatePolicyVersion {
		return receipt, fmt.Errorf(
			"unsupported observer duplicate policy %q",
			receipt.PolicyVersion,
		)
	}
	var err error
	receipt.OriginalTraceID, err = validateTaskShadowObserverID(
		receipt.OriginalTraceID,
		"observer duplicate original trace ID",
		true,
	)
	if err != nil {
		return receipt, err
	}
	receipt.OriginalRootHash, err = normalizeTaskShadowObserverHash(
		receipt.OriginalRootHash,
		"observer duplicate original root hash",
	)
	if err != nil {
		return receipt, err
	}
	receipt.OriginalEventHash, err = normalizeTaskShadowObserverHash(
		receipt.OriginalEventHash,
		"observer duplicate original event hash",
	)
	if err != nil {
		return receipt, err
	}
	receipt.InputIdentityHash, err = normalizeTaskShadowObserverHash(
		receipt.InputIdentityHash,
		"observer duplicate input identity hash",
	)
	if err != nil {
		return receipt, err
	}
	receipt.OriginalStatus = strings.TrimSpace(receipt.OriginalStatus)
	switch receipt.OriginalStatus {
	case TaskShadowObserverEventStarted,
		TaskShadowObserverEventCompleted,
		TaskShadowObserverEventCompletedWithErrors,
		TaskShadowObserverEventSkipped:
	default:
		return receipt, fmt.Errorf(
			"unsupported observer duplicate original status %q",
			receipt.OriginalStatus,
		)
	}
	return receipt, nil
}

func normalizeTaskShadowObserverEventPayload(
	payload TaskShadowObserverTraceEventPayload,
) (TaskShadowObserverTraceEventPayload, error) {
	payload.Reason = truncateTaskShadowObserverText(
		payload.Reason,
		taskShadowObserverMaxReasonBytes,
	)
	payload.FirstRunReason = truncateTaskShadowObserverText(
		payload.FirstRunReason,
		taskShadowObserverMaxReasonBytes,
	)
	if payload.FirstRunDecision != "" {
		payload.FirstRunDecision = strings.TrimSpace(payload.FirstRunDecision)
		switch payload.FirstRunDecision {
		case TaskShadowObserverFirstRunIncluded,
			TaskShadowObserverFirstRunExcluded,
			TaskShadowObserverFirstRunError,
			TaskShadowObserverFirstRunUnknown:
		default:
			return payload, fmt.Errorf(
				"unsupported observer First Run decision %q",
				payload.FirstRunDecision,
			)
		}
		if payload.FirstRunDecision == TaskShadowObserverFirstRunError {
			payload.FirstRunReason = taskShadowObserverDiagnosticReceipt(
				payload.FirstRunReason,
			)
		}
	}
	if payload.OnsetWindowCount < 0 || payload.RetainedOnsetEvidenceCount < 0 {
		return payload, errors.New("observer onset counts cannot be negative")
	}
	for _, target := range []*[]string{
		&payload.ProductionOnsetAttemptIDs,
		&payload.DiagnosticOnsetAttemptIDs,
		&payload.LifecycleAttemptIDs,
		&payload.TaskEpisodeIDs,
		&payload.ExpiredTaskEpisodeIDs,
		&payload.ShadowRunIDs,
		&payload.Continuation.ExecutionDispatchedIDs,
		&payload.Continuation.EvaluationDispatchedIDs,
		&payload.Continuation.MaterializationRecoveredIDs,
	} {
		*target = normalizeTaskShadowObserverStrings(
			*target,
			taskShadowObserverMaxLinkedIDs,
		)
		for _, value := range *target {
			if _, err := validateTaskShadowObserverID(value, "observer linked ID", true); err != nil {
				return payload, err
			}
		}
	}
	payload.Continuation.Version = strings.TrimSpace(payload.Continuation.Version)
	payload.Continuation.Trigger = strings.TrimSpace(payload.Continuation.Trigger)
	if payload.Continuation.ScannedRunCount < 0 || payload.Continuation.ErrorCount < 0 {
		return payload, errors.New("observer continuation counts cannot be negative")
	}
	decisions := make([]TaskShadowWorkflowReadinessDecision, 0, len(payload.WorkflowDecisions))
	for _, decision := range payload.WorkflowDecisions {
		normalized, err := normalizeTaskShadowWorkflowDecision(decision)
		if err != nil {
			return payload, err
		}
		decisions = append(decisions, normalized)
	}
	sort.Slice(decisions, func(i, j int) bool {
		left, right := decisions[i], decisions[j]
		return strings.Join([]string{left.Source, left.TaskEpisodeID, left.State, left.ShadowRunID}, "\x00") <
			strings.Join([]string{right.Source, right.TaskEpisodeID, right.State, right.ShadowRunID}, "\x00")
	})
	if len(decisions) > taskShadowObserverMaxDecisions {
		decisions = decisions[:taskShadowObserverMaxDecisions]
	}
	payload.WorkflowDecisions = decisions
	payload.Diagnostics = normalizeTaskShadowObserverMessages(payload.Diagnostics)
	payload.Errors = normalizeTaskShadowObserverMessages(payload.Errors)
	if payload.Duplicate != nil {
		duplicate, err := normalizeTaskShadowObserverDuplicateReceipt(
			*payload.Duplicate,
		)
		if err != nil {
			return payload, err
		}
		payload.Duplicate = &duplicate
		if payload.ShadowEnabledAtExecution != nil ||
			payload.FirstRunDecision != TaskShadowObserverFirstRunUnknown ||
			payload.FirstRunReason != "" ||
			payload.OnsetWindowCount != 0 ||
			payload.RetainedOnsetEvidenceCount != 0 ||
			len(payload.ProductionOnsetAttemptIDs) != 0 ||
			len(payload.DiagnosticOnsetAttemptIDs) != 0 ||
			len(payload.LifecycleAttemptIDs) != 0 ||
			len(payload.TaskEpisodeIDs) != 0 ||
			len(payload.ExpiredTaskEpisodeIDs) != 0 ||
			len(payload.ShadowRunIDs) != 0 ||
			len(payload.WorkflowDecisions) != 0 ||
			payload.Continuation.Version != "" ||
			payload.Continuation.Trigger != "" ||
			payload.Continuation.ScannedRunCount != 0 ||
			len(payload.Continuation.ExecutionDispatchedIDs) != 0 ||
			len(payload.Continuation.EvaluationDispatchedIDs) != 0 ||
			len(payload.Continuation.MaterializationRecoveredIDs) != 0 ||
			payload.Continuation.ErrorCount != 0 ||
			len(payload.Diagnostics) != 0 || len(payload.Errors) != 0 {
			return payload, errors.New(
				"observer duplicate receipt cannot contain task or execution effects",
			)
		}
	}
	return payload, nil
}

func normalizeTaskShadowObserverTraceInput(
	input TaskShadowObserverTraceInput,
) (TaskShadowObserverTraceInput, error) {
	var err error
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	input.ID, err = validateTaskShadowObserverID(input.ID, "observer trace ID", true)
	if err != nil {
		return input, err
	}
	if input.RuntimeInstanceID == "" {
		input.RuntimeInstanceID = taskShadowObserverRuntimeInstanceID
	}
	input.RuntimeInstanceID, err = validateTaskShadowObserverID(
		input.RuntimeInstanceID,
		"observer runtime instance ID",
		true,
	)
	if err != nil {
		return input, err
	}
	input.GroupID, err = validateTaskShadowObserverID(input.GroupID, "observer group ID", true)
	if err != nil {
		return input, err
	}
	input.SessionIDs = normalizeTaskShadowObserverStrings(input.SessionIDs, taskShadowObserverMaxSessions)
	if len(input.SessionIDs) == 0 || len(input.SessionIDs) != input.SessionCount {
		return input, errors.New("observer session IDs must match the session count")
	}
	for _, id := range input.SessionIDs {
		if _, err := validateTaskShadowObserverID(id, "observer session ID", true); err != nil {
			return input, err
		}
	}
	input.SessionManifestHash, err = normalizeTaskShadowObserverHash(
		input.SessionManifestHash,
		"observer session manifest hash",
	)
	if err != nil {
		return input, err
	}
	input.RetainedEventManifestHash, err = normalizeTaskShadowObserverHash(
		input.RetainedEventManifestHash,
		"observer retained event manifest hash",
	)
	if err != nil {
		return input, err
	}
	input.Source = strings.TrimSpace(input.Source)
	if input.Source != TaskShadowObserverSourceActivityTriager {
		return input, fmt.Errorf("unsupported observer source %q", input.Source)
	}
	input.Flow = strings.TrimSpace(input.Flow)
	switch input.Flow {
	case TaskShadowObserverFlowFullOnset, TaskShadowObserverFlowContinuation:
	default:
		return input, fmt.Errorf("unsupported observer flow %q", input.Flow)
	}
	input.Admission = strings.TrimSpace(input.Admission)
	switch input.Admission {
	case TaskShadowObserverAdmissionEligible,
		TaskShadowObserverAdmissionPrefiltered,
		TaskShadowObserverAdmissionCooldown:
	default:
		return input, fmt.Errorf("unsupported observer admission %q", input.Admission)
	}
	if input.Flow == TaskShadowObserverFlowFullOnset &&
		input.Admission != TaskShadowObserverAdmissionEligible {
		return input, errors.New("full onset requires eligible admission")
	}
	if input.Flow == TaskShadowObserverFlowContinuation &&
		input.Admission == TaskShadowObserverAdmissionEligible {
		return input, errors.New("continuation-only flow requires a filtered admission")
	}
	input.AdmissionReason = truncateTaskShadowObserverText(
		input.AdmissionReason,
		taskShadowObserverMaxReasonBytes,
	)
	if input.WindowStart <= 0 || input.WindowEnd < input.WindowStart {
		return input, errors.New("observer trace requires a valid source window")
	}
	if input.SessionCount <= 0 || input.SessionCount > taskShadowObserverMaxSessions ||
		input.DeclaredEventCount < 0 || input.RetainedEventCount < 0 {
		return input, errors.New("observer trace has invalid source counts")
	}
	if input.SourcePipelineRunID < 0 {
		return input, errors.New("observer source pipeline run ID cannot be negative")
	}
	expectedIdentity, err := taskShadowObserverInputIdentityFromInput(input)
	if err != nil {
		return input, err
	}
	if input.InputIdentityHash != "" {
		input.InputIdentityHash, err = normalizeTaskShadowObserverHash(
			input.InputIdentityHash,
			"observer input identity hash",
		)
		if err != nil {
			return input, err
		}
		if input.InputIdentityHash != expectedIdentity {
			return input, errors.New("observer input identity hash mismatch")
		}
	}
	input.InputIdentityHash = expectedIdentity
	if input.CreatedAt <= 0 {
		input.CreatedAt = time.Now().UnixMilli()
	}
	return input, nil
}

func taskShadowObserverRootPayloadFromInput(
	input TaskShadowObserverTraceInput,
) taskShadowObserverRootPayload {
	sourcePipelineRunID := input.SourcePipelineRunID
	return taskShadowObserverRootPayload{
		ID:                        input.ID,
		ContractVersion:           TaskShadowObserverTraceContractVersion,
		RuntimeInstanceID:         input.RuntimeInstanceID,
		GroupID:                   input.GroupID,
		SessionIDs:                input.SessionIDs,
		SessionManifestHash:       input.SessionManifestHash,
		Source:                    input.Source,
		Flow:                      input.Flow,
		Admission:                 input.Admission,
		AdmissionReason:           input.AdmissionReason,
		WindowStart:               input.WindowStart,
		WindowEnd:                 input.WindowEnd,
		SessionCount:              input.SessionCount,
		DeclaredEventCount:        input.DeclaredEventCount,
		RetainedEventCount:        input.RetainedEventCount,
		RetainedEventManifestHash: input.RetainedEventManifestHash,
		ShadowEnabledAtAdmission:  input.ShadowEnabledAtAdmission,
		SourcePipelineRunID:       &sourcePipelineRunID,
		CreatedAt:                 input.CreatedAt,
	}
}

func taskShadowObserverRootPayloadFromTrace(
	trace TaskShadowObserverTrace,
) taskShadowObserverRootPayload {
	var sourcePipelineRunID *int64
	if trace.ContractVersion == TaskShadowObserverTraceContractVersion {
		value := trace.SourcePipelineRunID
		sourcePipelineRunID = &value
	}
	return taskShadowObserverRootPayload{
		ID:                        trace.ID,
		ContractVersion:           trace.ContractVersion,
		RuntimeInstanceID:         trace.RuntimeInstanceID,
		GroupID:                   trace.GroupID,
		SessionIDs:                trace.SessionIDs,
		SessionManifestHash:       trace.SessionManifestHash,
		Source:                    trace.Source,
		Flow:                      trace.Flow,
		Admission:                 trace.Admission,
		AdmissionReason:           trace.AdmissionReason,
		WindowStart:               trace.WindowStart,
		WindowEnd:                 trace.WindowEnd,
		SessionCount:              trace.SessionCount,
		DeclaredEventCount:        trace.DeclaredEventCount,
		RetainedEventCount:        trace.RetainedEventCount,
		RetainedEventManifestHash: trace.RetainedEventManifestHash,
		ShadowEnabledAtAdmission:  trace.ShadowEnabledAtAdmission,
		SourcePipelineRunID:       sourcePipelineRunID,
		CreatedAt:                 trace.CreatedAt,
	}
}

func taskShadowObserverEvent(
	traceID string,
	sequence int,
	status string,
	payload TaskShadowObserverTraceEventPayload,
	previousHash string,
	occurredAt int64,
) (TaskShadowObserverTraceEvent, string, error) {
	payload, err := normalizeTaskShadowObserverEventPayload(payload)
	if err != nil {
		return TaskShadowObserverTraceEvent{}, "", err
	}
	payloadEncoded, err := json.Marshal(payload)
	if err != nil {
		return TaskShadowObserverTraceEvent{}, "", err
	}
	envelope := taskShadowObserverEventEnvelope{
		Status:     status,
		Payload:    payload,
		OccurredAt: occurredAt,
		CreatedAt:  occurredAt,
	}
	hash, err := taskShadowObserverCanonicalHash(envelope)
	if err != nil {
		return TaskShadowObserverTraceEvent{}, "", err
	}
	return TaskShadowObserverTraceEvent{
		ID: uuid.NewSHA1(
			uuid.NameSpaceOID,
			[]byte("task-shadow-observer-event\x00"+traceID+"\x00"+fmt.Sprint(sequence)+"\x00"+hash),
		).String(),
		TraceID:      traceID,
		Sequence:     sequence,
		Status:       status,
		Payload:      payload,
		PayloadHash:  hash,
		PreviousHash: previousHash,
		OccurredAt:   occurredAt,
		CreatedAt:    occurredAt,
	}, string(payloadEncoded), nil
}

func validateTaskShadowObserverEventStatus(status string, terminal bool) (string, error) {
	status = strings.TrimSpace(status)
	switch status {
	case TaskShadowObserverEventStarted:
		if terminal {
			return "", errors.New("observer terminal event cannot be started")
		}
	case TaskShadowObserverEventCompleted,
		TaskShadowObserverEventCompletedWithErrors,
		TaskShadowObserverEventSkipped,
		TaskShadowObserverEventCancelled,
		TaskShadowObserverEventFailed:
		if !terminal && status != TaskShadowObserverEventSkipped {
			return "", errors.New("observer initial event must be started or skipped")
		}
	default:
		return "", fmt.Errorf("unsupported observer event status %q", status)
	}
	return status, nil
}

func insertTaskShadowObserverEvent(
	tx *sql.Tx,
	event TaskShadowObserverTraceEvent,
	payloadJSON string,
) error {
	_, err := tx.Exec(`
		INSERT INTO task_shadow_observer_trace_events_v1 (
			id, trace_id, sequence, status, payload_json, payload_hash,
			previous_hash, occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.TraceID,
		event.Sequence,
		event.Status,
		payloadJSON,
		event.PayloadHash,
		event.PreviousHash,
		event.OccurredAt,
		event.CreatedAt,
	)
	return err
}

func (s *ActivityStore) recordTaskShadowObserverTrace(
	input TaskShadowObserverTraceInput,
	status string,
	payload TaskShadowObserverTraceEventPayload,
) (TaskShadowObserverTrace, error) {
	if s == nil || s.db == nil {
		return TaskShadowObserverTrace{}, errors.New("activity store unavailable")
	}
	input, err := normalizeTaskShadowObserverTraceInput(input)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	rootPayload := taskShadowObserverRootPayloadFromInput(input)
	rootHash, err := taskShadowObserverCanonicalHash(rootPayload)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	status, err = validateTaskShadowObserverEventStatus(status, false)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	event, payloadJSON, err := taskShadowObserverEvent(
		input.ID,
		1,
		status,
		payload,
		rootHash,
		input.CreatedAt,
	)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	sessionIDsJSON, err := json.Marshal(input.SessionIDs)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO task_shadow_observer_traces_v1 (
			id, contract_version, runtime_instance_id, group_id,
			session_ids_json, session_manifest_hash, source, flow, admission,
			admission_reason, window_start, window_end, session_count,
			declared_event_count, retained_event_count,
			retained_event_manifest_hash, input_identity_hash,
			shadow_enabled_at_admission,
			source_pipeline_run_id, root_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ID,
		TaskShadowObserverTraceContractVersion,
		input.RuntimeInstanceID,
		input.GroupID,
		string(sessionIDsJSON),
		input.SessionManifestHash,
		input.Source,
		input.Flow,
		input.Admission,
		input.AdmissionReason,
		input.WindowStart,
		input.WindowEnd,
		input.SessionCount,
		input.DeclaredEventCount,
		input.RetainedEventCount,
		input.RetainedEventManifestHash,
		input.InputIdentityHash,
		input.ShadowEnabledAtAdmission,
		input.SourcePipelineRunID,
		rootHash,
		input.CreatedAt,
	); err != nil {
		return TaskShadowObserverTrace{}, err
	}
	if err := insertTaskShadowObserverEvent(tx, event, payloadJSON); err != nil {
		return TaskShadowObserverTrace{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskShadowObserverTrace{}, err
	}
	return TaskShadowObserverTrace{
		ID:                        input.ID,
		ContractVersion:           TaskShadowObserverTraceContractVersion,
		RuntimeInstanceID:         input.RuntimeInstanceID,
		GroupID:                   input.GroupID,
		SessionIDs:                input.SessionIDs,
		SessionManifestHash:       input.SessionManifestHash,
		Source:                    input.Source,
		Flow:                      input.Flow,
		Admission:                 input.Admission,
		AdmissionReason:           input.AdmissionReason,
		WindowStart:               input.WindowStart,
		WindowEnd:                 input.WindowEnd,
		SessionCount:              input.SessionCount,
		DeclaredEventCount:        input.DeclaredEventCount,
		RetainedEventCount:        input.RetainedEventCount,
		RetainedEventManifestHash: input.RetainedEventManifestHash,
		InputIdentityHash:         input.InputIdentityHash,
		ShadowEnabledAtAdmission:  input.ShadowEnabledAtAdmission,
		SourcePipelineRunID:       input.SourcePipelineRunID,
		RootHash:                  rootHash,
		CreatedAt:                 input.CreatedAt,
		LatestEvent:               event,
	}, nil
}

func (s *ActivityStore) RecordTaskShadowObserverTrace(
	input TaskShadowObserverTraceInput,
) (TaskShadowObserverTrace, error) {
	status := TaskShadowObserverEventStarted
	payload := TaskShadowObserverTraceEventPayload{
		Reason:           "Shadow enabled at observer admission",
		FirstRunDecision: TaskShadowObserverFirstRunUnknown,
	}
	if !input.ShadowEnabledAtAdmission {
		status = TaskShadowObserverEventSkipped
		payload.Reason = "Shadow disabled at observer admission"
		payload.ShadowEnabledAtExecution = taskShadowObserverBool(false)
	}
	return s.recordTaskShadowObserverTrace(input, status, payload)
}

func (s *ActivityStore) RecordTaskShadowObserverDuplicateTrace(
	input TaskShadowObserverTraceInput,
	original TaskShadowObserverTrace,
) (TaskShadowObserverTrace, error) {
	input, err := normalizeTaskShadowObserverTraceInput(input)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	if original.ID == "" {
		return TaskShadowObserverTrace{}, fmt.Errorf(
			"%w: original trace ID is empty",
			errTaskShadowObserverDuplicateOriginalNotReusable,
		)
	}
	original, valid, reason, err := s.loadVerifiedTaskShadowObserverTrace(
		original.ID,
	)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	if !valid {
		return TaskShadowObserverTrace{}, fmt.Errorf(
			"%w: %s",
			errTaskShadowObserverDuplicateOriginalNotReusable,
			reason,
		)
	}
	if original.InputIdentityHash == "" ||
		original.InputIdentityHash != input.InputIdentityHash {
		return TaskShadowObserverTrace{}, fmt.Errorf(
			"%w: original does not match the exact input",
			errTaskShadowObserverDuplicateOriginalNotReusable,
		)
	}
	reusable, _ := taskShadowObserverReusableOriginal(original)
	if !reusable {
		return TaskShadowObserverTrace{}, fmt.Errorf(
			"%w: original state is retryable",
			errTaskShadowObserverDuplicateOriginalNotReusable,
		)
	}
	payload := TaskShadowObserverTraceEventPayload{
		Reason:           "Exact retained activity was already handled by an integrity-valid task check.",
		FirstRunDecision: TaskShadowObserverFirstRunUnknown,
		Duplicate: &TaskShadowObserverDuplicateReceipt{
			PolicyVersion:     TaskShadowObserverDuplicatePolicyVersion,
			OriginalTraceID:   original.ID,
			OriginalRootHash:  original.RootHash,
			OriginalEventHash: original.LatestEvent.PayloadHash,
			OriginalStatus:    original.LatestEvent.Status,
			InputIdentityHash: input.InputIdentityHash,
		},
	}
	return s.recordTaskShadowObserverTrace(
		input,
		TaskShadowObserverEventSkipped,
		payload,
	)
}

func (s *ActivityStore) CompleteTaskShadowObserverTrace(
	traceID, status string,
	payload TaskShadowObserverTraceEventPayload,
	occurredAt int64,
) (TaskShadowObserverTraceEvent, bool, error) {
	if s == nil || s.db == nil {
		return TaskShadowObserverTraceEvent{}, false, errors.New("activity store unavailable")
	}
	var err error
	traceID, err = validateTaskShadowObserverID(traceID, "observer trace ID", true)
	if err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	status, err = validateTaskShadowObserverEventStatus(status, true)
	if err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	if occurredAt <= 0 {
		occurredAt = time.Now().UnixMilli()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	defer tx.Rollback()
	latest, err := scanTaskShadowObserverEvent(tx.QueryRow(`
		SELECT id, trace_id, sequence, status, payload_json, payload_hash,
			previous_hash, occurred_at, created_at
		FROM task_shadow_observer_trace_events_v1
		WHERE trace_id = ?
		ORDER BY sequence DESC
		LIMIT 1`, traceID))
	if err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	event, payloadJSON, err := taskShadowObserverEvent(
		traceID,
		latest.Sequence+1,
		status,
		payload,
		latest.PayloadHash,
		occurredAt,
	)
	if err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	if latest.Status != TaskShadowObserverEventStarted {
		if latest.Status == event.Status {
			latestPayloadJSON, marshalErr := json.Marshal(latest.Payload)
			if marshalErr != nil {
				return TaskShadowObserverTraceEvent{}, false, marshalErr
			}
			if string(latestPayloadJSON) == payloadJSON {
				return latest, false, nil
			}
		}
		return TaskShadowObserverTraceEvent{}, false, errors.New(
			"task shadow observer trace is already terminal",
		)
	}
	if err := insertTaskShadowObserverEvent(tx, event, payloadJSON); err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TaskShadowObserverTraceEvent{}, false, err
	}
	return event, true, nil
}

type taskShadowObserverScanner interface {
	Scan(dest ...any) error
}

func scanTaskShadowObserverEvent(scanner taskShadowObserverScanner) (
	TaskShadowObserverTraceEvent,
	error,
) {
	var event TaskShadowObserverTraceEvent
	var payloadJSON string
	if err := scanner.Scan(
		&event.ID,
		&event.TraceID,
		&event.Sequence,
		&event.Status,
		&payloadJSON,
		&event.PayloadHash,
		&event.PreviousHash,
		&event.OccurredAt,
		&event.CreatedAt,
	); err != nil {
		return TaskShadowObserverTraceEvent{}, err
	}
	if err := json.Unmarshal([]byte(payloadJSON), &event.Payload); err != nil {
		return TaskShadowObserverTraceEvent{}, err
	}
	return event, nil
}

const taskShadowObserverTraceSelect = `
	t.id, t.contract_version, t.runtime_instance_id, t.group_id,
	t.session_ids_json, t.session_manifest_hash, t.source, t.flow, t.admission,
	t.admission_reason, t.window_start, t.window_end, t.session_count,
	t.declared_event_count, t.retained_event_count,
	t.retained_event_manifest_hash, t.input_identity_hash,
	t.shadow_enabled_at_admission,
	t.source_pipeline_run_id, t.root_hash, t.created_at,
	e.id, e.trace_id, e.sequence, e.status, e.payload_json, e.payload_hash,
	e.previous_hash, e.occurred_at, e.created_at`

func scanTaskShadowObserverTrace(scanner taskShadowObserverScanner) (
	TaskShadowObserverTrace,
	error,
) {
	var trace TaskShadowObserverTrace
	var sessionIDsJSON, payloadJSON string
	if err := scanner.Scan(
		&trace.ID,
		&trace.ContractVersion,
		&trace.RuntimeInstanceID,
		&trace.GroupID,
		&sessionIDsJSON,
		&trace.SessionManifestHash,
		&trace.Source,
		&trace.Flow,
		&trace.Admission,
		&trace.AdmissionReason,
		&trace.WindowStart,
		&trace.WindowEnd,
		&trace.SessionCount,
		&trace.DeclaredEventCount,
		&trace.RetainedEventCount,
		&trace.RetainedEventManifestHash,
		&trace.InputIdentityHash,
		&trace.ShadowEnabledAtAdmission,
		&trace.SourcePipelineRunID,
		&trace.RootHash,
		&trace.CreatedAt,
		&trace.LatestEvent.ID,
		&trace.LatestEvent.TraceID,
		&trace.LatestEvent.Sequence,
		&trace.LatestEvent.Status,
		&payloadJSON,
		&trace.LatestEvent.PayloadHash,
		&trace.LatestEvent.PreviousHash,
		&trace.LatestEvent.OccurredAt,
		&trace.LatestEvent.CreatedAt,
	); err != nil {
		return TaskShadowObserverTrace{}, err
	}
	if err := json.Unmarshal([]byte(sessionIDsJSON), &trace.SessionIDs); err != nil {
		return TaskShadowObserverTrace{}, err
	}
	if err := json.Unmarshal([]byte(payloadJSON), &trace.LatestEvent.Payload); err != nil {
		return TaskShadowObserverTrace{}, err
	}
	return trace, nil
}

func (s *ActivityStore) ListTaskShadowObserverTraces(
	limit int,
) ([]TaskShadowObserverTrace, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store unavailable")
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
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT ?`,
		normalizeTaskShadowObserverListLimit(limit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TaskShadowObserverTrace, 0)
	for rows.Next() {
		trace, err := scanTaskShadowObserverTrace(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, trace)
	}
	return result, rows.Err()
}

func (s *ActivityStore) GetTaskShadowObserverTrace(
	traceID string,
) (TaskShadowObserverTrace, error) {
	if s == nil || s.db == nil {
		return TaskShadowObserverTrace{}, errors.New("activity store unavailable")
	}
	var err error
	traceID, err = validateTaskShadowObserverID(traceID, "observer trace ID", true)
	if err != nil {
		return TaskShadowObserverTrace{}, err
	}
	return scanTaskShadowObserverTrace(s.db.QueryRow(`
		SELECT `+taskShadowObserverTraceSelect+`
		FROM task_shadow_observer_traces_v1 t
		JOIN task_shadow_observer_trace_events_v1 e
			ON e.trace_id = t.id
			AND e.sequence = (
				SELECT MAX(latest.sequence)
				FROM task_shadow_observer_trace_events_v1 latest
				WHERE latest.trace_id = t.id
			)
		WHERE t.id = ?`,
		traceID,
	))
}

func (s *ActivityStore) ListTaskShadowObserverTraceEvents(
	traceID string,
) ([]TaskShadowObserverTraceEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store unavailable")
	}
	var err error
	traceID, err = validateTaskShadowObserverID(traceID, "observer trace ID", true)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, trace_id, sequence, status, payload_json, payload_hash,
			previous_hash, occurred_at, created_at
		FROM task_shadow_observer_trace_events_v1
		WHERE trace_id = ?
		ORDER BY sequence ASC`,
		traceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TaskShadowObserverTraceEvent, 0, 2)
	for rows.Next() {
		event, err := scanTaskShadowObserverEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func taskShadowObserverDerivedStatus(
	trace TaskShadowObserverTrace,
	currentRuntimeInstanceID string,
	integrityValid bool,
) string {
	if !integrityValid {
		return TaskShadowObserverDerivedCorrupt
	}
	if trace.LatestEvent.Status != TaskShadowObserverEventStarted {
		return trace.LatestEvent.Status
	}
	if trace.RuntimeInstanceID == currentRuntimeInstanceID {
		return TaskShadowObserverDerivedRunning
	}
	return TaskShadowObserverDerivedInterrupted
}

func verifyTaskShadowObserverTrace(
	trace TaskShadowObserverTrace,
	events []TaskShadowObserverTraceEvent,
) (bool, string) {
	if trace.ContractVersion != TaskShadowObserverTraceContractVersion &&
		trace.ContractVersion != TaskShadowObserverTraceContractVersionV1 {
		return false, "unsupported observer trace contract"
	}
	rootHash, err := taskShadowObserverCanonicalHash(
		taskShadowObserverRootPayloadFromTrace(trace),
	)
	if err != nil || rootHash != trace.RootHash {
		return false, "observer root hash mismatch"
	}
	if trace.InputIdentityHash != "" {
		identityHash, identityErr := taskShadowObserverInputIdentityFromTrace(trace)
		if identityErr != nil || identityHash != trace.InputIdentityHash {
			return false, "observer input identity mismatch"
		}
	}
	if len(events) == 0 {
		return false, "observer trace has no events"
	}
	previousHash := trace.RootHash
	for index, event := range events {
		if event.TraceID != trace.ID || event.Sequence != index+1 {
			return false, "observer event identity or sequence mismatch"
		}
		if event.PreviousHash != previousHash {
			return false, "observer event chain mismatch"
		}
		payload, err := normalizeTaskShadowObserverEventPayload(event.Payload)
		if err != nil {
			return false, "observer event payload invalid: " + err.Error()
		}
		payloadHash, err := taskShadowObserverCanonicalHash(
			taskShadowObserverEventEnvelope{
				Status:     event.Status,
				Payload:    payload,
				OccurredAt: event.OccurredAt,
				CreatedAt:  event.CreatedAt,
			},
		)
		if err != nil || payloadHash != event.PayloadHash {
			return false, "observer event payload hash mismatch"
		}
		expectedID := uuid.NewSHA1(
			uuid.NameSpaceOID,
			[]byte("task-shadow-observer-event\x00"+trace.ID+"\x00"+fmt.Sprint(event.Sequence)+"\x00"+payloadHash),
		).String()
		if event.ID != expectedID {
			return false, "observer event ID mismatch"
		}
		previousHash = event.PayloadHash
	}
	if trace.LatestEvent.ID != events[len(events)-1].ID ||
		trace.LatestEvent.PayloadHash != events[len(events)-1].PayloadHash {
		return false, "observer latest-event projection mismatch"
	}
	return true, ""
}

func newTaskShadowObserverTraceInput(
	group sessionGroup,
	events []activitymon.ActivityEvent,
	flow, admission, admissionReason string,
	shadowEnabled bool,
	sourcePipelineRunID int64,
	createdAt int64,
) (TaskShadowObserverTraceInput, error) {
	if len(group.Sessions) == 0 {
		return TaskShadowObserverTraceInput{}, errors.New("observer trace requires a session group")
	}
	sessionIDs := make([]string, 0, len(group.Sessions))
	sessionManifest := make([]taskShadowObserverSessionManifestEntry, 0, len(group.Sessions))
	for index, session := range group.Sessions {
		sessionIDs = append(sessionIDs, session.ID)
		sessionManifest = append(sessionManifest, taskShadowObserverSessionManifestEntry{
			Ordinal:    index,
			ID:         session.ID,
			StartTime:  session.StartTime,
			EndTime:    session.EndTime,
			EventCount: session.EventCount,
			CreatedAt:  session.CreatedAt,
		})
	}
	sessionManifestHash, err := taskShadowObserverCanonicalHash(sessionManifest)
	if err != nil {
		return TaskShadowObserverTraceInput{}, err
	}
	eventManifest := make([]taskShadowObserverEventManifestEntry, 0, len(events))
	for index, event := range events {
		contentHash, hashErr := taskShadowObserverCanonicalHash(event)
		if hashErr != nil {
			return TaskShadowObserverTraceInput{}, hashErr
		}
		eventManifest = append(eventManifest, taskShadowObserverEventManifestEntry{
			Ordinal:     index,
			Timestamp:   event.Timestamp,
			Kind:        event.Kind,
			ContentHash: contentHash,
		})
	}
	eventManifestHash, err := taskShadowObserverCanonicalHash(eventManifest)
	if err != nil {
		return TaskShadowObserverTraceInput{}, err
	}
	return normalizeTaskShadowObserverTraceInput(TaskShadowObserverTraceInput{
		RuntimeInstanceID:         taskShadowObserverRuntimeInstanceID,
		GroupID:                   group.primaryID(),
		SessionIDs:                sessionIDs,
		SessionManifestHash:       sessionManifestHash,
		Source:                    TaskShadowObserverSourceActivityTriager,
		Flow:                      flow,
		Admission:                 admission,
		AdmissionReason:           admissionReason,
		WindowStart:               group.startTime(),
		WindowEnd:                 group.endTime(),
		SessionCount:              len(group.Sessions),
		DeclaredEventCount:        group.totalEventCount(),
		RetainedEventCount:        len(events),
		RetainedEventManifestHash: eventManifestHash,
		ShadowEnabledAtAdmission:  shadowEnabled,
		SourcePipelineRunID:       sourcePipelineRunID,
		CreatedAt:                 createdAt,
	})
}
