package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	TaskShadowReleaseObserverProvenanceVersion = "task-shadow-release-observer-provenance-v2"

	TaskShadowReleaseObserverAwaitingTaskEpisode     = "awaiting_task_episode"
	TaskShadowReleaseObserverAwaitingCurrentRun      = "awaiting_current_run"
	TaskShadowReleaseObserverScanTruncated           = "scan_truncated"
	TaskShadowReleaseObserverOriginMissing           = "origin_missing"
	TaskShadowReleaseObserverLegacyOrigin            = "legacy_origin"
	TaskShadowReleaseObserverDuplicateOrigin         = "duplicate_origin"
	TaskShadowReleaseObserverSourcePruned            = "source_pruned_or_cleared"
	TaskShadowReleaseObserverSourceNotCovered        = "source_not_covered"
	TaskShadowReleaseObserverTraceCorrupt            = "trace_corrupt"
	TaskShadowReleaseObserverBoundaryMismatch        = "boundary_mismatch"
	TaskShadowReleaseObserverTerminalIncomplete      = "terminal_incomplete"
	TaskShadowReleaseObserverProductionOnsetMissing  = "production_onset_missing"
	TaskShadowReleaseObserverEpisodeBindingMismatch  = "episode_binding_mismatch"
	TaskShadowReleaseObserverContextBindingMismatch  = "context_binding_mismatch"
	TaskShadowReleaseObserverWorkflowBindingMismatch = "workflow_binding_mismatch"
	TaskShadowReleaseObserverRunBindingMismatch      = "run_binding_mismatch"
	TaskShadowReleaseObserverCoveredSource           = "covered_source"
	TaskShadowReleaseObserverOriginVerified          = "origin_verified"
)

// TaskShadowReleaseObserverProvenance is a metadata-only, derived join. It is
// not Task evidence and cannot influence learning, qualification, or authority.
type TaskShadowReleaseObserverProvenance struct {
	Version                  string `json:"version"`
	State                    string `json:"state"`
	Reason                   string `json:"reason"`
	SourceComplete           bool   `json:"sourceComplete"`
	OriginComplete           bool   `json:"originComplete"`
	PipelineRunID            int64  `json:"pipelineRunId,omitempty"`
	TraceID                  string `json:"traceId,omitempty"`
	TraceContractVersion     string `json:"traceContractVersion,omitempty"`
	CoverageState            string `json:"coverageState,omitempty"`
	Admission                string `json:"admission,omitempty"`
	Flow                     string `json:"flow,omitempty"`
	TerminalStatus           string `json:"terminalStatus,omitempty"`
	ProductionOnsetAttemptID string `json:"productionOnsetAttemptId,omitempty"`
	ProductionOnsetPrompt    string `json:"productionOnsetPrompt,omitempty"`
	ProductionOnsetStatus    string `json:"productionOnsetStatus,omitempty"`
	TaskEpisodeID            string `json:"taskEpisodeId,omitempty"`
	Workflow                 string `json:"workflow,omitempty"`
	WorkflowDecisionState    string `json:"workflowDecisionState,omitempty"`
	FrozenContextHash        string `json:"frozenContextHash,omitempty"`
	ShadowRunID              string `json:"shadowRunId,omitempty"`
	ClaimTraceCount          int    `json:"claimTraceCount"`
	CandidateTraceCount      int    `json:"candidateTraceCount"`
	CandidateDecisionCount   int    `json:"candidateDecisionCount"`
	IntegrityValid           bool   `json:"integrityValid"`
}

type taskShadowReleaseObserverCorpus struct {
	Sources                 []taskShadowObserverCoverageSource
	Traces                  []TaskShadowObserverTrace
	EventsByTrace           map[string][]TaskShadowObserverTraceEvent
	TotalCurrentTraceCount  int
	LoadedCurrentTraceCount int
	AttemptLookup           func(string) (TaskAgentAttempt, error)
}

type taskShadowReleaseObserverOrigin struct {
	Trace    TaskShadowObserverTrace
	Decision TaskShadowWorkflowReadinessDecision
}

func newTaskShadowReleaseObserverProvenance(
	taskEpisodeID string,
) TaskShadowReleaseObserverProvenance {
	return TaskShadowReleaseObserverProvenance{
		Version:       TaskShadowReleaseObserverProvenanceVersion,
		State:         TaskShadowReleaseObserverAwaitingTaskEpisode,
		Reason:        "no selected Task Episode has exact observer provenance",
		TaskEpisodeID: strings.TrimSpace(taskEpisodeID),
	}
}

func loadTaskShadowReleaseObserverCorpus(
	store *ActivityStore,
	now int64,
) (taskShadowReleaseObserverCorpus, error) {
	if store == nil || store.db == nil {
		return taskShadowReleaseObserverCorpus{}, fmt.Errorf(
			"release observer provenance requires an initialized activity store",
		)
	}
	if now <= 0 {
		now = time.Now().UnixMilli()
	}
	cutoff := now - activityPipelineMaxAge.Milliseconds()
	sources, err := store.listTaskShadowObserverCoverageSources()
	if err != nil {
		return taskShadowReleaseObserverCorpus{}, err
	}
	traces, currentCount, _, err :=
		store.listTaskShadowObserverCoverageTraces(cutoff)
	if err != nil {
		return taskShadowReleaseObserverCorpus{}, err
	}
	eventsByTrace, err := store.listTaskShadowObserverCoverageEvents(cutoff)
	if err != nil {
		return taskShadowReleaseObserverCorpus{}, err
	}
	loadedCurrent := 0
	for _, trace := range traces {
		if trace.ContractVersion == TaskShadowObserverTraceContractVersion {
			loadedCurrent++
		}
	}
	retainedSources := make([]taskShadowObserverCoverageSource, 0, len(sources))
	for _, source := range sources {
		if max(source.StartedAt, source.CompletedAt) >= cutoff {
			retainedSources = append(retainedSources, source)
		}
	}
	return taskShadowReleaseObserverCorpus{
		Sources:                 retainedSources,
		Traces:                  traces,
		EventsByTrace:           eventsByTrace,
		TotalCurrentTraceCount:  currentCount,
		LoadedCurrentTraceCount: loadedCurrent,
		AttemptLookup:           store.GetTaskAgentAttempt,
	}, nil
}

func taskShadowReleaseObserverContains(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func taskShadowReleaseObserverClaimsEpisode(
	trace TaskShadowObserverTrace,
	taskEpisodeID string,
) bool {
	if taskShadowReleaseObserverContains(
		trace.LatestEvent.Payload.TaskEpisodeIDs,
		taskEpisodeID,
	) {
		return true
	}
	for _, decision := range trace.LatestEvent.Payload.WorkflowDecisions {
		if decision.TaskEpisodeID == taskEpisodeID {
			return true
		}
	}
	return false
}

func taskShadowReleaseObserverOrigins(
	corpus taskShadowReleaseObserverCorpus,
	taskEpisodeID string,
) (
	current []taskShadowReleaseObserverOrigin,
	legacy []taskShadowReleaseObserverOrigin,
	claimTraceCount int,
) {
	for _, trace := range corpus.Traces {
		if taskShadowReleaseObserverClaimsEpisode(trace, taskEpisodeID) {
			claimTraceCount++
		}
		for _, decision := range trace.LatestEvent.Payload.WorkflowDecisions {
			if decision.Source != TaskShadowWorkflowDecisionCapturedOnset ||
				decision.TaskEpisodeID != taskEpisodeID {
				continue
			}
			origin := taskShadowReleaseObserverOrigin{
				Trace:    trace,
				Decision: decision,
			}
			if trace.ContractVersion == TaskShadowObserverTraceContractVersion {
				current = append(current, origin)
			} else {
				legacy = append(legacy, origin)
			}
		}
	}
	return current, legacy, claimTraceCount
}

func taskShadowReleaseObserverSourceCoverage(
	corpus taskShadowReleaseObserverCorpus,
	trace TaskShadowObserverTrace,
) TaskShadowObserverCoverageItem {
	sourcesByID := make(map[int64]taskShadowObserverCoverageSource)
	for _, source := range corpus.Sources {
		sourcesByID[source.ID] = source
	}
	tracesForSource := 0
	for _, candidate := range corpus.Traces {
		if candidate.ContractVersion == TaskShadowObserverTraceContractVersion &&
			candidate.SourcePipelineRunID == trace.SourcePipelineRunID {
			tracesForSource++
		}
	}
	source, ok := sourcesByID[trace.SourcePipelineRunID]
	if !ok {
		return taskShadowObserverCoverageTraceItem(
			trace,
			corpus.EventsByTrace[trace.ID],
			TaskShadowObserverCoverageSourcePruned,
			"exact source row is outside retained pipeline diagnostics or was cleared",
		)
	}
	if !source.Expected {
		return taskShadowObserverCoverageTraceItem(
			trace,
			corpus.EventsByTrace[trace.ID],
			TaskShadowObserverCoverageLinkMismatch,
			"linked source row predates observer instrumentation",
		)
	}
	return taskShadowObserverCoverageSourceItem(
		source,
		&trace,
		corpus.EventsByTrace[trace.ID],
		tracesForSource > 1,
	)
}

func buildTaskShadowReleaseObserverProvenance(
	detail *TaskShadowDebugDetail,
	corpus taskShadowReleaseObserverCorpus,
) TaskShadowReleaseObserverProvenance {
	if detail == nil {
		return newTaskShadowReleaseObserverProvenance("")
	}
	taskEpisodeID := strings.TrimSpace(detail.Episode.Episode.ID)
	result := newTaskShadowReleaseObserverProvenance(taskEpisodeID)
	if taskEpisodeID == "" {
		return result
	}
	if corpus.TotalCurrentTraceCount > corpus.LoadedCurrentTraceCount {
		result.State = TaskShadowReleaseObserverScanTruncated
		result.Reason = "the bounded current observer integrity scan is truncated"
		return result
	}

	current, legacy, claimCount := taskShadowReleaseObserverOrigins(
		corpus,
		taskEpisodeID,
	)
	result.ClaimTraceCount = claimCount
	originTraceIDs := make(map[string]struct{}, len(current)+len(legacy))
	for _, origin := range append(append(
		[]taskShadowReleaseObserverOrigin{},
		current...,
	), legacy...) {
		originTraceIDs[origin.Trace.ID] = struct{}{}
	}
	result.CandidateTraceCount = len(originTraceIDs)
	result.CandidateDecisionCount = len(current) + len(legacy)
	if len(current) == 0 {
		if len(legacy) > 0 {
			result.State = TaskShadowReleaseObserverLegacyOrigin
			result.Reason = "only historical observer provenance claims this Task Episode"
			return result
		}
		result.State = TaskShadowReleaseObserverOriginMissing
		result.Reason = "no current captured-onset observer decision claims this Task Episode"
		return result
	}
	if len(current)+len(legacy) != 1 {
		result.State = TaskShadowReleaseObserverDuplicateOrigin
		result.Reason = "more than one captured-onset decision claims this Task Episode"
		return result
	}

	origin := current[0]
	trace := origin.Trace
	decision := origin.Decision
	result.PipelineRunID = trace.SourcePipelineRunID
	result.TraceID = trace.ID
	result.TraceContractVersion = trace.ContractVersion
	result.Admission = trace.Admission
	result.Flow = trace.Flow
	result.TerminalStatus = trace.LatestEvent.Status
	result.Workflow = decision.Workflow
	result.WorkflowDecisionState = decision.State
	result.FrozenContextHash = decision.FrozenContextHash
	result.ShadowRunID = decision.ShadowRunID

	coverage := taskShadowReleaseObserverSourceCoverage(corpus, trace)
	result.CoverageState = coverage.State
	result.IntegrityValid = coverage.IntegrityValid
	if trace.SourcePipelineRunID <= 0 {
		result.State = TaskShadowReleaseObserverSourceNotCovered
		result.Reason = "the current observer root has no exact source-row identity"
		return result
	}
	if coverage.State == TaskShadowObserverCoverageSourcePruned {
		result.State = TaskShadowReleaseObserverSourcePruned
		result.Reason = "the exact observer source row is no longer retained"
		return result
	}
	if coverage.State == TaskShadowObserverCoverageTraceCorrupt {
		result.State = TaskShadowReleaseObserverTraceCorrupt
		result.Reason = "the observer root or event chain failed integrity verification"
		return result
	}
	if coverage.State != TaskShadowObserverCoverageCovered {
		result.State = TaskShadowReleaseObserverSourceNotCovered
		result.Reason = "the production source-to-observer link is not exactly covered"
		return result
	}
	result.SourceComplete = true
	result.State = TaskShadowReleaseObserverCoveredSource
	result.Reason = "the retained production source and immutable observer trace match exactly"

	if trace.Admission != TaskShadowObserverAdmissionEligible ||
		trace.Flow != TaskShadowObserverFlowFullOnset ||
		trace.LatestEvent.Payload.FirstRunDecision !=
			TaskShadowObserverFirstRunIncluded {
		result.State = TaskShadowReleaseObserverBoundaryMismatch
		result.Reason = "the observer receipt is not an eligible included full-onset boundary"
		return result
	}
	if trace.LatestEvent.Status != TaskShadowObserverEventCompleted &&
		trace.LatestEvent.Status != TaskShadowObserverEventCompletedWithErrors {
		result.State = TaskShadowReleaseObserverTerminalIncomplete
		result.Reason = "the observer receipt did not reach a qualifying terminal state"
		return result
	}
	payload := trace.LatestEvent.Payload
	result.ProductionOnsetAttemptID = strings.TrimSpace(
		decision.ProductionOnsetAttemptID,
	)
	if result.ProductionOnsetAttemptID == "" ||
		!taskShadowReleaseObserverContains(
			payload.ProductionOnsetAttemptIDs,
			result.ProductionOnsetAttemptID,
		) {
		result.State = TaskShadowReleaseObserverProductionOnsetMissing
		result.Reason = "the captured onset decision has no exact production onset attempt"
		return result
	}
	if corpus.AttemptLookup == nil {
		result.State = TaskShadowReleaseObserverProductionOnsetMissing
		result.Reason = "the production onset attempt cannot be resolved"
		return result
	}
	onsetAttempt, err := corpus.AttemptLookup(result.ProductionOnsetAttemptID)
	if err != nil || onsetAttempt.Phase != TaskAgentAttemptPhaseOnset ||
		onsetAttempt.Status != TaskAgentAttemptStatusSucceeded ||
		onsetAttempt.PromptVersion != taskOnsetPromptVersion {
		result.State = TaskShadowReleaseObserverProductionOnsetMissing
		result.Reason = "the production onset attempt is missing or not current and successful"
		return result
	}
	result.ProductionOnsetPrompt = onsetAttempt.PromptVersion
	result.ProductionOnsetStatus = onsetAttempt.Status
	if !taskShadowReleaseObserverContains(payload.TaskEpisodeIDs, taskEpisodeID) {
		result.State = TaskShadowReleaseObserverEpisodeBindingMismatch
		result.Reason = "the observer terminal receipt does not bind the selected Task Episode"
		return result
	}
	episode := detail.Episode.Episode
	if decision.FrozenContextHash != episode.FrozenContext.SHA256 ||
		decision.FrozenManifestCount != episode.FrozenContext.ManifestEntryCount ||
		decision.FrozenIncludedCount != episode.FrozenContext.ManifestIncludedCount ||
		!decision.HasExactActivitySnapshot {
		result.State = TaskShadowReleaseObserverContextBindingMismatch
		result.Reason = "the observer onset decision does not match the frozen Task context"
		return result
	}
	run := taskShadowReleaseGateCurrentRun(detail)
	if run == nil {
		result.State = TaskShadowReleaseObserverAwaitingCurrentRun
		result.Reason = "the source is covered but no current-generation Shadow run is selected"
		return result
	}
	if decision.Workflow != run.Run.CapabilityKey ||
		decision.Workflow != detail.ProductionRollout.Workflow {
		result.State = TaskShadowReleaseObserverWorkflowBindingMismatch
		result.Reason = "the observer workflow does not match the selected production workflow"
		return result
	}
	if decision.State != TaskShadowWorkflowStateRunCreated ||
		decision.ShadowRunID != run.Run.ID ||
		run.Run.TaskEpisodeID != taskEpisodeID ||
		!decision.ExecutionDispatched ||
		!taskShadowReleaseObserverContains(payload.ShadowRunIDs, run.Run.ID) {
		result.State = TaskShadowReleaseObserverRunBindingMismatch
		result.Reason = "the observer onset decision does not bind the created current Shadow run"
		return result
	}

	result.OriginComplete = true
	result.State = TaskShadowReleaseObserverOriginVerified
	result.Reason = "one exact production observer source created this Task Episode and Shadow run"
	return result
}
