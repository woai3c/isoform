package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

const (
	taskShadowPipelineTimeout        = 8 * time.Minute
	taskShadowOnsetDedupeWindow      = 30 * time.Second
	taskShadowReconcileLookback      = 30 * time.Minute
	taskShadowReconcileMaxCandidates = 8

	TaskShadowObserverDispatchPending          = "pending"
	TaskShadowObserverDispatchRecorded         = "recorded"
	TaskShadowObserverDispatchStoreUnavailable = "store_unavailable"
	TaskShadowObserverDispatchInputInvalid     = "input_invalid"
	TaskShadowObserverDispatchRecordFailed     = "record_failed"
)

type TaskShadowObserverDispatchReceipt struct {
	Status  string
	Reason  string
	TraceID string
}

func applyTaskShadowObserverDispatchReceipt(
	run *ActivityPipelineRunRow,
	admission string,
	receipt TaskShadowObserverDispatchReceipt,
) {
	if run == nil {
		return
	}
	run.ShadowObserverExpected = true
	run.ShadowObserverAdmission = admission
	run.ShadowObserverDispatchStatus = receipt.Status
	run.ShadowObserverDispatchReason = receipt.Reason
	run.ShadowObserverTraceID = receipt.TraceID
}

type TaskShadowPipelineResult struct {
	FirstRunDecision           string                                `json:"firstRunDecision"`
	FirstRunReason             string                                `json:"firstRunReason,omitempty"`
	OnsetWindowCount           int                                   `json:"onsetWindowCount"`
	RetainedOnsetEvidenceCount int                                   `json:"retainedOnsetEvidenceCount"`
	OnsetAttemptIDs            []string                              `json:"onsetAttemptIds"`
	ProductionOnsetAttemptIDs  []string                              `json:"productionOnsetAttemptIds"`
	DiagnosticOnsetAttemptIDs  []string                              `json:"diagnosticOnsetAttemptIds"`
	LifecycleAttemptIDs        []string                              `json:"lifecycleAttemptIds"`
	TaskEpisodeIDs             []string                              `json:"taskEpisodeIds"`
	ExpiredTaskEpisodeIDs      []string                              `json:"expiredTaskEpisodeIds"`
	ShadowRunIDs               []string                              `json:"shadowRunIds"`
	WorkflowDecisions          []TaskShadowWorkflowReadinessDecision `json:"workflowDecisions"`
	LifecycleExpiry            TaskLifecycleExpiryResult             `json:"lifecycleExpiry"`
	Continuation               TaskShadowContinuationRecoveryResult  `json:"continuation"`
	Diagnostics                []string                              `json:"diagnostics,omitempty"`
	Errors                     []string                              `json:"errors"`
}

func finalizeTaskShadowPipelineResult(
	result TaskShadowPipelineResult,
) TaskShadowPipelineResult {
	result.OnsetAttemptIDs = uniqueSortedStrings(result.OnsetAttemptIDs)
	result.ProductionOnsetAttemptIDs = uniqueSortedStrings(
		result.ProductionOnsetAttemptIDs,
	)
	result.DiagnosticOnsetAttemptIDs = uniqueSortedStrings(
		result.DiagnosticOnsetAttemptIDs,
	)
	result.LifecycleAttemptIDs = uniqueSortedStrings(result.LifecycleAttemptIDs)
	result.TaskEpisodeIDs = uniqueSortedStrings(result.TaskEpisodeIDs)
	result.ExpiredTaskEpisodeIDs = uniqueSortedStrings(
		result.ExpiredTaskEpisodeIDs,
	)
	result.ShadowRunIDs = uniqueSortedStrings(result.ShadowRunIDs)
	sort.Slice(result.WorkflowDecisions, func(i, j int) bool {
		left, right := result.WorkflowDecisions[i], result.WorkflowDecisions[j]
		return strings.Join(
			[]string{left.Source, left.TaskEpisodeID, left.State, left.ShadowRunID},
			"\x00",
		) < strings.Join(
			[]string{right.Source, right.TaskEpisodeID, right.State, right.ShadowRunID},
			"\x00",
		)
	})
	sort.Strings(result.Diagnostics)
	sort.Strings(result.Errors)
	return result
}

func taskShadowWorkflowDecision(
	source string,
	episode TaskEpisode,
) TaskShadowWorkflowReadinessDecision {
	decision := TaskShadowWorkflowReadinessDecision{
		Source:                   source,
		TaskEpisodeID:            episode.ID,
		Workflow:                 taskEpisodeProductionShadowCapability(episode),
		FrozenContextHash:        episode.FrozenContext.SHA256,
		FrozenManifestCount:      len(episode.FrozenContext.Manifest),
		HasExactActivitySnapshot: taskEpisodeHasFrozenSourceContext(episode),
	}
	for _, entry := range episode.FrozenContext.Manifest {
		if entry.Included {
			decision.FrozenIncludedCount++
		}
	}
	return decision
}

func taskShadowWorkflowReadinessState(err error) string {
	switch {
	case errors.Is(err, ErrTaskWorkflowOutsideProductionRollout):
		return TaskShadowWorkflowStateOutsideRegistry
	case errors.Is(err, ErrTaskWorkflowInsufficientFrozenContext):
		return TaskShadowWorkflowStateFrozenContextNotReady
	default:
		return TaskShadowWorkflowStateReadinessError
	}
}

type taskShadowReconciliationResult struct {
	RunIDs      []string
	Decisions   []TaskShadowWorkflowReadinessDecision
	Diagnostics []string
	Errors      []string
}

func taskShadowAttemptStatus(err error) string {
	if err == nil {
		return TaskAgentAttemptStatusSucceeded
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "parse ") ||
		strings.Contains(message, "decode ") ||
		strings.Contains(message, "validate ") {
		return TaskAgentAttemptStatusParseFailed
	}
	return TaskAgentAttemptStatusFailed
}

func recordTaskShadowModelAttempt(
	store *ActivityStore,
	phase, promptVersion, model string,
	normalizedInput any,
	parsedOutput any,
	raw, prompt string,
	runErr error,
	startedAt, completedAt int64,
	taskEpisodeID, shadowRunID, shadowEvaluationID string,
) (TaskAgentAttempt, error) {
	if store == nil || strings.TrimSpace(prompt) == "" {
		return TaskAgentAttempt{}, errors.New(
			"task model attempt requires a store and exact prompt",
		)
	}
	inputJSON, err := json.Marshal(normalizedInput)
	if err != nil {
		return TaskAgentAttempt{}, err
	}
	parsedJSON := ""
	if runErr == nil {
		payload, err := json.Marshal(parsedOutput)
		if err != nil {
			return TaskAgentAttempt{}, err
		}
		parsedJSON = string(payload)
	}
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	}
	idempotencyPayload := strings.Join([]string{
		phase,
		taskEpisodeID,
		shadowRunID,
		fmt.Sprint(startedAt),
		taskAgentAttemptHash([]byte(prompt)),
	}, "\x00")
	attempt, _, err := store.RecordTaskAgentAttempt(TaskAgentAttemptInput{
		IdempotencyKey:      "task-model:" + taskAgentAttemptHash([]byte(idempotencyPayload)),
		Phase:               phase,
		TaskEpisodeID:       taskEpisodeID,
		ShadowRunID:         shadowRunID,
		ShadowEvaluationID:  shadowEvaluationID,
		ModelName:           model,
		ModelVersion:        model,
		PromptVersion:       promptVersion,
		PromptText:          prompt,
		NormalizedInputJSON: string(inputJSON),
		Status:              taskShadowAttemptStatus(runErr),
		RawOutput:           raw,
		ParsedOutputJSON:    parsedJSON,
		Error:               errorText,
		StartedAt:           startedAt,
		CompletedAt:         completedAt,
		CreatedAt:           completedAt,
	})
	return attempt, err
}

func taskShadowOnsetWindows(
	group sessionGroup,
	events []activitymon.ActivityEvent,
) []TaskOnsetInput {
	start := group.startTime()
	end := group.endTime()
	if start <= 0 || end < start {
		return nil
	}
	build := func(windowStart, cutoffAt int64) TaskOnsetInput {
		input, err := normalizeTaskOnsetInput(TaskOnsetInput{
			SourceKind: "activity",
			SourceID: fmt.Sprintf(
				"%s:window:%d:%d",
				group.primaryID(),
				windowStart,
				cutoffAt,
			),
			WindowStart: windowStart,
			CutoffAt:    cutoffAt,
			Evidence: buildTaskOnsetEvidenceFromEvents(
				events,
				windowStart,
				cutoffAt,
			),
		})
		if err != nil {
			return TaskOnsetInput{}
		}
		return input
	}
	firstCutoff := min(end, start+taskOnsetObservationHorizon.Milliseconds())
	result := []TaskOnsetInput{build(start, firstCutoff)}
	trailingStart := max(start, end-taskOnsetObservationHorizon.Milliseconds())
	if trailingStart > start && trailingStart < end {
		result = append(result, build(trailingStart, end))
	}
	filtered := result[:0]
	for _, input := range result {
		if input.SourceID != "" && len(input.Evidence) > 0 {
			filtered = append(filtered, input)
		}
	}
	return filtered
}

func taskShadowOnsetRecentContext(
	input TaskOnsetInput,
	prediction TaskOnsetPrediction,
) []TaskContextCandidate {
	result := make([]TaskContextCandidate, 0, len(input.Evidence))
	for _, evidence := range input.Evidence {
		if evidence.OccurredAt > prediction.InferredStartedAt {
			continue
		}
		result = append(result, TaskContextCandidate{
			Ref:             evidence.Ref,
			Section:         TaskContextSectionActivity,
			SourceKind:      evidence.SourceKind,
			SourceID:        evidence.SourceID,
			ObservedAt:      evidence.OccurredAt,
			Content:         evidence.Summary,
			EvidenceClass:   TaskContextEvidenceObservedUser,
			PrivacyDecision: TaskContextPrivacyInclude,
			Relevance:       1,
		})
	}
	return result
}

func taskShadowPredictionDuplicate(
	seen []TaskOnsetPrediction,
	candidate TaskOnsetPrediction,
) bool {
	task := strings.ToLower(strings.Join(strings.Fields(candidate.Task), " "))
	for _, previous := range seen {
		previousTask := strings.ToLower(strings.Join(strings.Fields(previous.Task), " "))
		delta := previous.InferredStartedAt - candidate.InferredStartedAt
		if delta < 0 {
			delta = -delta
		}
		if task == previousTask &&
			delta <= taskShadowOnsetDedupeWindow.Milliseconds() {
			return true
		}
	}
	return false
}

func taskShadowEpisodeEligibleForReconciliation(
	episode TaskEpisode,
	horizonEnd int64,
) bool {
	if !taskShadowEpisodeReconciliationCandidate(episode, horizonEnd) {
		return false
	}
	ready, _ := taskEpisodeProductionShadowReady(episode)
	return ready
}

func taskShadowEpisodeReconciliationCandidate(
	episode TaskEpisode,
	horizonEnd int64,
) bool {
	if horizonEnd <= 0 || episode.InferredStartAt <= 0 ||
		episode.InferredStartAt > horizonEnd {
		return false
	}
	switch episode.CurrentStatus {
	case TaskEpisodeStatusCandidate,
		TaskEpisodeStatusActive,
		TaskEpisodeStatusPaused:
	default:
		return false
	}
	if horizonEnd-episode.InferredStartAt >
		taskShadowReconcileLookback.Milliseconds() {
		return false
	}
	return true
}

func (c *ActivityTriager) reconcileRecentEligibleTaskEpisodesDetailed(
	ctx context.Context,
	horizonEnd int64,
) taskShadowReconciliationResult {
	result := taskShadowReconciliationResult{}
	if c == nil || c.app == nil || c.app.activity == nil ||
		c.app.activity.store == nil {
		result.Errors = append(result.Errors, "activity store unavailable")
		return result
	}
	episodes, err := c.app.activity.store.ListTaskEpisodes(
		"",
		taskEpisodeMaxListLimit,
	)
	if err != nil {
		result.Errors = append(result.Errors, "reconcile episodes: "+err.Error())
		return result
	}
	repeatedOnsets, err := c.app.activity.store.taskEpisodeRepeatedOnsetSet()
	if err != nil {
		result.Errors = append(
			result.Errors,
			"reconcile repeated task starts: "+err.Error(),
		)
		return result
	}
	considered := 0
	for _, episode := range episodes {
		if !taskShadowEpisodeReconciliationCandidate(episode, horizonEnd) {
			continue
		}
		if _, repeated := repeatedOnsets[episode.ID]; repeated {
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateRepeatedOnsetExcluded
			decision.Reason = "one exact observed start has multiple immutable Task Episodes"
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		ready, readinessErr := taskEpisodeProductionShadowReady(episode)
		if !ready {
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = taskShadowWorkflowReadinessState(readinessErr)
			decision.Reason = readinessErr.Error()
			result.Decisions = append(result.Decisions, decision)
			// A no-op while background understanding is not running.
			c.app.offerBackgroundUnderstandingForShadowFallback(
				taskShadowBackgroundFallbackReason(readinessErr),
			)
			result.Diagnostics = append(
				result.Diagnostics,
				fmt.Sprintf(
					"recent episode %s is not production-ready: %v",
					episode.ID,
					readinessErr,
				),
			)
			continue
		}
		considered++
		if considered > taskShadowReconcileMaxCandidates {
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateCandidateLimitDeferred
			decision.Reason = "bounded reconciliation candidate limit reached"
			result.Decisions = append(result.Decisions, decision)
			break
		}
		excluded, exclusionErr := c.app.activity.store.
			isTaskEpisodeFirstRunLearningExcluded(episode.ID)
		if exclusionErr != nil {
			result.Errors = append(
				result.Errors,
				"reconcile first-run exclusion: "+exclusionErr.Error(),
			)
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateReadinessError
			decision.Reason = exclusionErr.Error()
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		if excluded {
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateFirstRunExcluded
			decision.Reason = "episode is excluded from natural Shadow mode learning"
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		existing, listErr := c.app.activity.store.ListShadowTaskRuns(
			episode.ID,
			1,
		)
		if listErr != nil {
			result.Errors = append(
				result.Errors,
				"reconcile shadow runs: "+listErr.Error(),
			)
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateReadinessError
			decision.Reason = listErr.Error()
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		if len(existing) > 0 {
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateExistingRun
			decision.Reason = "current episode already has a task-mirror run"
			decision.ShadowRunID = existing[0].ID
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		run, _, createErr := c.app.activity.store.
			CreateShadowTaskRunFromEpisode(
				episode,
				effectivePrimaryTaskModel(c.app),
				time.Now().UnixMilli(),
			)
		if createErr != nil {
			result.Errors = append(
				result.Errors,
				"reconcile shadow run: "+createErr.Error(),
			)
			decision := taskShadowWorkflowDecision(
				TaskShadowWorkflowDecisionReconciliation,
				episode,
			)
			decision.State = TaskShadowWorkflowStateRunCreateFailed
			decision.Reason = createErr.Error()
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		result.RunIDs = append(result.RunIDs, run.ID)
		decision := taskShadowWorkflowDecision(
			TaskShadowWorkflowDecisionReconciliation,
			episode,
		)
		decision.State = TaskShadowWorkflowStateRunCreated
		decision.Reason = "created from immutable onset context"
		decision.ShadowRunID = run.ID
		decision.ExecutionDispatched = true
		result.Decisions = append(result.Decisions, decision)
		c.app.clearBackgroundUnderstandingShadowFallback()
		result.Diagnostics = append(
			result.Diagnostics,
			fmt.Sprintf(
				"reconciled recent %s episode %s from immutable onset context",
				taskEpisodeProductionShadowCapability(episode),
				episode.ID,
			),
		)
		c.dispatchShadowCandidateExecution(ctx, run)
	}
	return result
}

func (c *ActivityTriager) reconcileRecentEligibleTaskEpisodes(
	ctx context.Context,
	horizonEnd int64,
) ([]string, []string, []string) {
	result := c.reconcileRecentEligibleTaskEpisodesDetailed(ctx, horizonEnd)
	return result.RunIDs, result.Diagnostics, result.Errors
}

func filterTaskLifecycleInputEpisodes(
	input TaskLifecycleInput,
	episodeIDs []string,
) (TaskLifecycleInput, error) {
	if len(episodeIDs) == 0 {
		return input, nil
	}
	allowed := make(map[string]struct{}, len(episodeIDs))
	for _, id := range episodeIDs {
		allowed[id] = struct{}{}
	}
	episodes := make([]TaskLifecycleEpisodeSnapshot, 0, len(allowed))
	evidenceRefs := make(map[string]struct{})
	for _, episode := range input.Episodes {
		if _, ok := allowed[episode.EpisodeID]; !ok {
			continue
		}
		episodes = append(episodes, episode)
		for _, ref := range episode.EvidenceRefs {
			evidenceRefs[ref] = struct{}{}
		}
	}
	prefix := make([]TaskLifecycleEvidence, 0, len(evidenceRefs))
	for _, evidence := range input.PrefixEvidence {
		if _, ok := evidenceRefs[evidence.Ref]; ok {
			prefix = append(prefix, evidence)
		}
	}
	input.Episodes = episodes
	input.PrefixEvidence = prefix
	return normalizeTaskLifecycleInput(input)
}

func (c *ActivityTriager) runTaskLifecyclePass(
	ctx context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
	prefixCutoffAt int64,
	episodeIDs []string,
) (string, error) {
	if group.endTime() <= prefixCutoffAt {
		return "", nil
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		c.app.activity.store,
		"activity",
		group.primaryID(),
		prefixCutoffAt,
		group.endTime(),
		events,
	)
	if err != nil {
		return "", err
	}
	input, err = filterTaskLifecycleInputEpisodes(input, episodeIDs)
	if err != nil {
		return "", err
	}
	if len(input.Episodes) == 0 || len(input.SuffixEvidence) == 0 {
		return "", nil
	}
	c.app.mu.Lock()
	runner := c.app.runner
	c.app.mu.Unlock()
	if runner == nil {
		return "", errors.New("agent runner unavailable")
	}
	model := effectiveLightTaskModel(c.app)
	startedAt := time.Now().UnixMilli()
	response, raw, prompt, runErr := runTaskLifecycleObservation(
		ctx,
		runner,
		model,
		input,
	)
	completedAt := time.Now().UnixMilli()
	attempt := TaskAgentAttempt{}
	if strings.TrimSpace(prompt) != "" {
		recorded, attemptErr := recordTaskShadowModelAttempt(
			c.app.activity.store,
			TaskAgentAttemptPhaseLifecycle,
			taskLifecyclePromptVersion,
			model,
			input,
			response,
			raw,
			prompt,
			runErr,
			startedAt,
			completedAt,
			"",
			"",
			"",
		)
		attempt = recorded
		if attemptErr != nil {
			log.Printf("[shadow-task] record lifecycle attempt: %v", attemptErr)
		}
	}
	if runErr != nil {
		return attempt.ID, runErr
	}
	persisted, err := PersistTaskLifecycleObservations(
		c.app.activity.store,
		input,
		response,
		completedAt,
	)
	if err != nil {
		return attempt.ID, err
	}
	if len(persisted) > 0 {
		episodeIDs := make([]string, 0, len(input.Episodes))
		for _, episode := range input.Episodes {
			episodeIDs = append(episodeIDs, episode.EpisodeID)
		}
		c.app.emitBrowserEvent("task_episode:updated", map[string]any{
			"episodeIds": episodeIDs,
			"attemptId":  attempt.ID,
		})
	}
	for _, episode := range input.Episodes {
		current, err := c.app.activity.store.GetTaskEpisode(episode.EpisodeID)
		if err != nil {
			continue
		}
		if current.CurrentStatus != TaskEpisodeStatusCompleted &&
			current.CurrentStatus != TaskEpisodeStatusAbandoned {
			continue
		}
		runs, err := c.app.activity.store.ListShadowTaskRuns(current.ID, 20)
		if err != nil {
			continue
		}
		for _, run := range runs {
			c.dispatchTaskShadowEvaluation(ctx, run.ID)
		}
	}
	return attempt.ID, nil
}

func (c *ActivityTriager) runTaskShadowFlow(
	ctx context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
	detectOnset bool,
) TaskShadowPipelineResult {
	result := TaskShadowPipelineResult{
		FirstRunDecision: TaskShadowObserverFirstRunUnknown,
	}
	if c == nil || c.app == nil || c.app.activity == nil ||
		c.app.activity.store == nil {
		result.Errors = append(result.Errors, "activity store unavailable")
		return result
	}
	excluded, err := c.app.activity.store.
		isFirstRunSessionGroupLearningExcluded(group)
	if err != nil {
		result.FirstRunDecision = TaskShadowObserverFirstRunError
		result.FirstRunReason = err.Error()
		result.Errors = append(result.Errors, "first-run exclusion: "+err.Error())
		return result
	}
	if excluded {
		result.FirstRunDecision = TaskShadowObserverFirstRunExcluded
		result.FirstRunReason = "guided First Run activity is excluded from natural Shadow mode learning"
		return result
	}
	result.FirstRunDecision = TaskShadowObserverFirstRunIncluded
	onsetInputs := []TaskOnsetInput(nil)
	if detectOnset {
		onsetInputs = taskShadowOnsetWindows(group, events)
		result.OnsetWindowCount = len(onsetInputs)
		for _, input := range onsetInputs {
			result.RetainedOnsetEvidenceCount += len(input.Evidence)
		}
	}
	result.LifecycleExpiry = expireStaleTaskEpisodes(
		c.app.activity.store,
		group.endTime(),
		time.Now().UnixMilli(),
	)
	result.ExpiredTaskEpisodeIDs = append(
		result.ExpiredTaskEpisodeIDs,
		result.LifecycleExpiry.ExpiredIDs...,
	)
	for _, expiryErr := range result.LifecycleExpiry.Errors {
		result.Errors = append(result.Errors, "lifecycle expiry: "+expiryErr)
	}
	if result.LifecycleExpiry.BacklogDeferred {
		result.Diagnostics = append(
			result.Diagnostics,
			"task lifecycle expiry backlog exceeded the bounded per-group batch",
		)
	}
	c.app.mu.Lock()
	runner := c.app.runner
	c.app.mu.Unlock()
	if runner == nil {
		result.Errors = append(result.Errors, "agent runner unavailable")
		return result
	}
	reconciliation := c.reconcileRecentEligibleTaskEpisodesDetailed(
		ctx,
		group.endTime(),
	)
	result.ShadowRunIDs = append(result.ShadowRunIDs, reconciliation.RunIDs...)
	result.WorkflowDecisions = append(
		result.WorkflowDecisions,
		reconciliation.Decisions...,
	)
	result.Diagnostics = append(result.Diagnostics, reconciliation.Diagnostics...)
	result.Errors = append(result.Errors, reconciliation.Errors...)
	if group.startTime() > 1 && group.endTime() > group.startTime() {
		attemptID, err := c.runTaskLifecyclePass(
			ctx,
			group,
			events,
			group.startTime()-1,
			nil,
		)
		if attemptID != "" {
			result.LifecycleAttemptIDs = append(
				result.LifecycleAttemptIDs,
				attemptID,
			)
		}
		if err != nil {
			result.Errors = append(result.Errors, "existing lifecycle: "+err.Error())
		}
	}
	if !detectOnset {
		result.Diagnostics = append(
			result.Diagnostics,
			"continued existing Task Episodes without onset detection",
		)
		result.Continuation = c.recoverTaskShadowContinuations(
			ctx,
			"activity_group_continuation",
		)
		result.Errors = append(
			result.Errors,
			result.Continuation.Errors...,
		)
		return finalizeTaskShadowPipelineResult(result)
	}

	model := effectiveLightTaskModel(c.app)
	profileInfluenceEnabled := c.app.workPatternProfileInfluenceEnabled()
	seenPredictions := make([]TaskOnsetPrediction, 0)
	for _, onsetInput := range onsetInputs {
		profileContext, profileErr := buildTaskOnsetWorkPatternContext(
			c.app.activity.store,
			onsetInput,
			profileInfluenceEnabled,
			time.Now().UnixMilli(),
		)
		if profileErr != nil {
			result.Errors = append(
				result.Errors,
				"work profile: "+profileErr.Error(),
			)
		}
		onsetResult := runTaskOnsetWithWorkPatternAblation(
			ctx,
			c.app.activity.store,
			runner,
			model,
			onsetInput,
			profileContext,
			profileInfluenceEnabled,
		)
		result.OnsetAttemptIDs = append(
			result.OnsetAttemptIDs,
			onsetResult.AttemptIDs...,
		)
		if onsetResult.ProductionAttemptID != "" {
			result.ProductionOnsetAttemptIDs = append(
				result.ProductionOnsetAttemptIDs,
				onsetResult.ProductionAttemptID,
			)
		}
		for _, attemptID := range onsetResult.AttemptIDs {
			if attemptID != "" && attemptID != onsetResult.ProductionAttemptID {
				result.DiagnosticOnsetAttemptIDs = append(
					result.DiagnosticOnsetAttemptIDs,
					attemptID,
				)
			}
		}
		for _, diagnostic := range onsetResult.Diagnostics {
			result.Diagnostics = append(
				result.Diagnostics,
				"onset ablation: "+diagnostic,
			)
		}
		if onsetResult.ProductionErr != nil {
			result.Errors = append(
				result.Errors,
				"onset: "+onsetResult.ProductionErr.Error(),
			)
			continue
		}
		response := onsetResult.Response
		completedAt := onsetResult.CompletedAt
		if completedAt <= 0 {
			completedAt = time.Now().UnixMilli()
		}

		for _, prediction := range response.Episodes {
			if taskShadowPredictionDuplicate(seenPredictions, prediction) {
				continue
			}
			seenPredictions = append(seenPredictions, prediction)
			recentActivity := taskShadowOnsetRecentContext(
				onsetInput,
				prediction,
			)
			snapshotContext, snapshotErr :=
				taskShadowPreOnsetActivityContext(
					c.app.activity.store,
					group,
					prediction.InferredStartedAt,
				)
			if snapshotErr != nil {
				result.Errors = append(
					result.Errors,
					"pre-onset activity context: "+snapshotErr.Error(),
				)
			}
			recentActivity = append(recentActivity, snapshotContext...)
			sources, err := GatherTaskContextSources(TaskContextSourceRequest{
				Store:    c.app.activity.store,
				CutoffAt: prediction.InferredStartedAt,
				Query: strings.Join([]string{
					prediction.Task,
					prediction.DesiredResult,
				}, " "),
				RecentActivity: recentActivity,
			})
			if err != nil {
				result.Errors = append(result.Errors, "context: "+err.Error())
				continue
			}
			episodeProfile := WorkPatternProfileContext{}
			if profileContext.SnapshotID != "" {
				snapshot, snapshotErr := c.app.activity.store.
					GetWorkPatternProfileSnapshot(profileContext.SnapshotID)
				if snapshotErr != nil {
					result.Errors = append(
						result.Errors,
						"work profile snapshot: "+snapshotErr.Error(),
					)
					continue
				}
				appKeys, transitionKeys := taskOnsetWorkPatternKeys(onsetInput)
				episodeProfile, snapshotErr = buildWorkPatternProfileContext(
					c.app.activity.store,
					snapshot,
					sources,
					strings.Join([]string{
						prediction.Task,
						prediction.DesiredResult,
					}, " "),
					appKeys,
					transitionKeys,
					profileContext.InfluenceMode,
				)
				if snapshotErr != nil {
					result.Errors = append(
						result.Errors,
						"work profile context: "+snapshotErr.Error(),
					)
					continue
				}
				if !workPatternProfileCanInfluence(episodeProfile) {
					episodeProfile = neutralWorkPatternProfileContext(
						episodeProfile,
					)
				}
			}
			var productionOnsetProfile *WorkPatternProfileContext
			if onsetResult.ProfileUsed {
				profileCopy := profileContext
				productionOnsetProfile = &profileCopy
			}
			captures, err := PersistTaskOnsetEpisodesWithProfileSources(
				c.app.activity.store,
				onsetInput,
				TaskOnsetResponse{Episodes: []TaskOnsetPrediction{prediction}},
				completedAt,
				productionOnsetProfile,
				episodeProfile,
				sources,
			)
			if err != nil {
				result.Errors = append(result.Errors, "capture: "+err.Error())
				continue
			}
			for _, capture := range captures {
				result.TaskEpisodeIDs = append(result.TaskEpisodeIDs, capture.Episode.ID)
				decision := taskShadowWorkflowDecision(
					TaskShadowWorkflowDecisionCapturedOnset,
					capture.Episode,
				)
				decision.ProductionOnsetAttemptID =
					onsetResult.ProductionAttemptID
				excluded, err := c.app.activity.store.
					isTaskEpisodeFirstRunLearningExcluded(capture.Episode.ID)
				if err != nil {
					decision.State = TaskShadowWorkflowStateReadinessError
					decision.Reason = err.Error()
					result.WorkflowDecisions = append(
						result.WorkflowDecisions,
						decision,
					)
					result.Errors = append(result.Errors, "first-run episode exclusion: "+err.Error())
					continue
				}
				if excluded {
					decision.State = TaskShadowWorkflowStateFirstRunExcluded
					decision.Reason = "episode is excluded from natural Shadow mode learning"
					result.WorkflowDecisions = append(
						result.WorkflowDecisions,
						decision,
					)
					continue
				}
				repeatedErr := c.app.activity.store.
					taskEpisodeRepeatedOnsetError(capture.Episode.ID)
				if repeatedErr != nil {
					if errors.Is(repeatedErr, ErrTaskEpisodeRepeatedOnset) {
						decision.State = TaskShadowWorkflowStateRepeatedOnsetExcluded
						decision.Reason = repeatedErr.Error()
						result.WorkflowDecisions = append(
							result.WorkflowDecisions,
							decision,
						)
						continue
					}
					decision.State = TaskShadowWorkflowStateReadinessError
					decision.Reason = repeatedErr.Error()
					result.WorkflowDecisions = append(
						result.WorkflowDecisions,
						decision,
					)
					result.Errors = append(
						result.Errors,
						"repeated onset check: "+repeatedErr.Error(),
					)
					continue
				}

				capabilityKey := taskEpisodeProductionShadowCapability(capture.Episode)
				if ready, readinessErr := taskEpisodeProductionShadowReady(
					capture.Episode,
				); ready {
					run, added, runErr := c.app.activity.store.
						CreateShadowTaskRunFromEpisode(
							capture.Episode,
							effectivePrimaryTaskModel(c.app),
							time.Now().UnixMilli(),
						)
					if runErr != nil {
						decision.State = TaskShadowWorkflowStateRunCreateFailed
						decision.Reason = runErr.Error()
						result.Errors = append(
							result.Errors,
							"shadow run: "+runErr.Error(),
						)
					} else {
						if added {
							decision.State = TaskShadowWorkflowStateRunCreated
							decision.Reason = "created from captured onset context"
						} else {
							decision.State = TaskShadowWorkflowStateExistingRun
							decision.Reason = "idempotent task-mirror run already existed"
						}
						decision.ShadowRunID = run.ID
						decision.ExecutionDispatched = true
						result.ShadowRunIDs = append(
							result.ShadowRunIDs,
							run.ID,
						)
						c.app.clearBackgroundUnderstandingShadowFallback()
						c.dispatchShadowCandidateExecution(ctx, run)
					}
				} else {
					decision.State = taskShadowWorkflowReadinessState(readinessErr)
					decision.Reason = readinessErr.Error()
					c.app.offerBackgroundUnderstandingForShadowFallback(
						taskShadowBackgroundFallbackReason(readinessErr),
					)
					result.Diagnostics = append(
						result.Diagnostics,
						fmt.Sprintf(
							"shadow execution skipped: workflow %q is not production-ready: %v",
							capabilityKey,
							readinessErr,
						),
					)
				}
				result.WorkflowDecisions = append(
					result.WorkflowDecisions,
					decision,
				)

				if group.endTime() > capture.Episode.InferredStartAt {
					attemptID, lifecycleErr := c.runTaskLifecyclePass(
						ctx,
						group,
						events,
						capture.Episode.InferredStartAt,
						[]string{capture.Episode.ID},
					)
					if attemptID != "" {
						result.LifecycleAttemptIDs = append(
							result.LifecycleAttemptIDs,
							attemptID,
						)
					}
					if lifecycleErr != nil {
						result.Errors = append(
							result.Errors,
							"new lifecycle: "+lifecycleErr.Error(),
						)
					}
				}
			}
		}
	}
	result.Continuation = c.recoverTaskShadowContinuations(
		ctx,
		"activity_group_full",
	)
	result.Errors = append(result.Errors, result.Continuation.Errors...)
	return finalizeTaskShadowPipelineResult(result)
}

func taskShadowObserverPayloadFromPipelineResult(
	result TaskShadowPipelineResult,
	shadowEnabledAtExecution bool,
) TaskShadowObserverTraceEventPayload {
	reason := "Shadow observer flow completed"
	switch {
	case result.FirstRunDecision == TaskShadowObserverFirstRunExcluded:
		reason = "First Run activity remained excluded from natural Shadow mode learning"
	case result.FirstRunDecision == TaskShadowObserverFirstRunError:
		reason = "First Run containment could not be verified"
	case result.OnsetWindowCount == 0 &&
		len(result.TaskEpisodeIDs) == 0 &&
		len(result.ShadowRunIDs) == 0:
		reason = "No onset window, Task Episode, or task-mirror run was produced"
	case len(result.ShadowRunIDs) == 0:
		reason = "Observer flow completed without a new task-mirror run"
	}
	return TaskShadowObserverTraceEventPayload{
		Reason:                     reason,
		ShadowEnabledAtExecution:   taskShadowObserverBool(shadowEnabledAtExecution),
		FirstRunDecision:           result.FirstRunDecision,
		FirstRunReason:             result.FirstRunReason,
		OnsetWindowCount:           result.OnsetWindowCount,
		RetainedOnsetEvidenceCount: result.RetainedOnsetEvidenceCount,
		ProductionOnsetAttemptIDs:  result.ProductionOnsetAttemptIDs,
		DiagnosticOnsetAttemptIDs:  result.DiagnosticOnsetAttemptIDs,
		LifecycleAttemptIDs:        result.LifecycleAttemptIDs,
		TaskEpisodeIDs:             result.TaskEpisodeIDs,
		ExpiredTaskEpisodeIDs:      result.ExpiredTaskEpisodeIDs,
		ShadowRunIDs:               result.ShadowRunIDs,
		WorkflowDecisions:          result.WorkflowDecisions,
		Continuation: TaskShadowObserverContinuationSummary{
			Version:                     result.Continuation.Version,
			Trigger:                     result.Continuation.Trigger,
			ScannedRunCount:             result.Continuation.ScannedRunCount,
			ExecutionDispatchedIDs:      result.Continuation.ExecutionDispatchedIDs,
			EvaluationDispatchedIDs:     result.Continuation.EvaluationDispatchedIDs,
			MaterializationRecoveredIDs: result.Continuation.MaterializationRecoveredRunIDs,
			ErrorCount:                  len(result.Continuation.Errors),
		},
		Diagnostics: result.Diagnostics,
		Errors:      result.Errors,
	}
}

func (c *ActivityTriager) runTaskShadowProductionFlow(
	ctx context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
) TaskShadowPipelineResult {
	return c.runTaskShadowFlow(ctx, group, events, true)
}

func (c *ActivityTriager) runTaskShadowContinuationFlow(
	ctx context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
) TaskShadowPipelineResult {
	return c.runTaskShadowFlow(ctx, group, events, false)
}

func (c *ActivityTriager) dispatchTaskShadowFlow(
	parent context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
	detectOnset bool,
	admission, admissionReason string,
	sourcePipelineRunID int64,
) TaskShadowObserverDispatchReceipt {
	receipt := TaskShadowObserverDispatchReceipt{
		Status: TaskShadowObserverDispatchStoreUnavailable,
		Reason: "observer application unavailable",
	}
	if c == nil || c.app == nil {
		return receipt
	}
	baseCtx := parent
	if baseCtx == nil {
		baseCtx = c.app.ctx
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	shadowEnabledAtAdmission := c.app.loadTriageFeatureFlags(baseCtx).Shadow
	flow := TaskShadowObserverFlowContinuation
	if detectOnset {
		flow = TaskShadowObserverFlowFullOnset
	}
	traceID := ""
	duplicateHandled := false
	if c.app.activity != nil && c.app.activity.store != nil {
		receipt.Status = TaskShadowObserverDispatchInputInvalid
		receipt.Reason = "observer trace input was invalid"
		input, inputErr := newTaskShadowObserverTraceInput(
			group,
			events,
			flow,
			admission,
			admissionReason,
			shadowEnabledAtAdmission,
			sourcePipelineRunID,
			time.Now().UnixMilli(),
		)
		if inputErr != nil {
			log.Printf(
				"[shadow-task-observer] build trace group=%s: %s",
				group.primaryID(),
				taskShadowObserverDiagnosticReceipt("observer input: "+inputErr.Error()),
			)
		} else {
			var original TaskShadowObserverTrace
			var reusableReason string
			var trace TaskShadowObserverTrace
			var traceErr error
			func() {
				c.taskShadowObserverAdmissionMu.Lock()
				defer c.taskShadowObserverAdmissionMu.Unlock()

				var reusable bool
				var lookupErr error
				original, reusable, reusableReason, lookupErr =
					c.app.activity.store.FindReusableTaskShadowObserverTrace(input)
				if lookupErr != nil {
					log.Printf(
						"[shadow-task-observer] exact-input lookup group=%s: %s",
						group.primaryID(),
						taskShadowObserverDiagnosticReceipt(
							"observer diagnostic: "+lookupErr.Error(),
						),
					)
				}
				if reusable {
					duplicateHandled = true
					trace, traceErr = c.app.activity.store.
						RecordTaskShadowObserverDuplicateTrace(input, original)
					if !errors.Is(
						traceErr,
						errTaskShadowObserverDuplicateOriginalNotReusable,
					) {
						return
					}
					duplicateHandled = false
				}
				trace, traceErr = c.app.activity.store.
					RecordTaskShadowObserverTrace(input)
			}()
			if traceErr != nil {
				receipt.Status = TaskShadowObserverDispatchRecordFailed
				receipt.Reason = "observer trace persistence failed"
				log.Printf(
					"[shadow-task-observer] record trace group=%s: %s",
					group.primaryID(),
					taskShadowObserverDiagnosticReceipt("observer record: "+traceErr.Error()),
				)
			} else {
				traceID = trace.ID
				receipt = TaskShadowObserverDispatchReceipt{
					Status:  TaskShadowObserverDispatchRecorded,
					Reason:  "observer trace recorded",
					TraceID: trace.ID,
				}
				if duplicateHandled {
					receipt.Reason = "observer trace recorded; exact input already handled"
					log.Printf(
						"[shadow-task-observer] exact input group=%s already handled by trace=%s (%s)",
						group.primaryID(),
						original.ID,
						reusableReason,
					)
				}
				c.app.emitBrowserEvent("shadow_agent:observer_trace", map[string]any{
					"traceId": trace.ID,
					"status":  trace.LatestEvent.Status,
				})
			}
		}
	}
	if !shadowEnabledAtAdmission || duplicateHandled {
		return receipt
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		terminalRecorded := false
		completeTrace := func(
			status string,
			payload TaskShadowObserverTraceEventPayload,
		) {
			if terminalRecorded || traceID == "" || c.app.activity == nil ||
				c.app.activity.store == nil {
				return
			}
			if _, _, err := c.app.activity.store.CompleteTaskShadowObserverTrace(
				traceID,
				status,
				payload,
				time.Now().UnixMilli(),
			); err != nil {
				log.Printf("[shadow-task-observer] complete trace=%s: %v", traceID, err)
				return
			}
			terminalRecorded = true
			c.app.emitBrowserEvent("shadow_agent:observer_trace", map[string]any{
				"traceId": traceID,
				"status":  status,
			})
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				panicReceipt := taskShadowObserverDiagnosticReceipt(
					fmt.Sprintf("panic: %v", recovered),
				)
				completeTrace(
					TaskShadowObserverEventFailed,
					TaskShadowObserverTraceEventPayload{
						Reason:                   "observer flow panicked before a terminal result",
						ShadowEnabledAtExecution: taskShadowObserverBool(true),
						FirstRunDecision:         TaskShadowObserverFirstRunUnknown,
						Errors:                   []string{panicReceipt},
					},
				)
				log.Printf(
					"[shadow-task-observer] group=%s failed: %s",
					group.primaryID(),
					panicReceipt,
				)
			}
		}()
		if c.taskShadowSem != nil {
			select {
			case c.taskShadowSem <- struct{}{}:
				defer func() { <-c.taskShadowSem }()
			case <-baseCtx.Done():
				completeTrace(
					TaskShadowObserverEventCancelled,
					TaskShadowObserverTraceEventPayload{
						Reason:                   "parent context ended before the serialized Shadow slot was acquired",
						ShadowEnabledAtExecution: taskShadowObserverBool(true),
						FirstRunDecision:         TaskShadowObserverFirstRunUnknown,
					},
				)
				return
			}
		}
		if !c.app.loadTriageFeatureFlags(baseCtx).Shadow {
			completeTrace(
				TaskShadowObserverEventSkipped,
				TaskShadowObserverTraceEventPayload{
					Reason:                   "Shadow was disabled after queueing and before model work",
					ShadowEnabledAtExecution: taskShadowObserverBool(false),
					FirstRunDecision:         TaskShadowObserverFirstRunUnknown,
				},
			)
			return
		}
		runCtx, cancel := newTaskShadowObserverContext(baseCtx)
		defer cancel()
		result := c.runTaskShadowFlow(runCtx, group, events, detectOnset)
		status := TaskShadowObserverEventCompleted
		if len(result.Errors) > 0 {
			status = TaskShadowObserverEventCompletedWithErrors
		}
		completeTrace(
			status,
			taskShadowObserverPayloadFromPipelineResult(result, true),
		)
		if len(result.Errors) > 0 {
			log.Printf(
				"[shadow-task] group=%s completed with errors: %s",
				group.primaryID(),
				activityPrivateLogField("error", strings.Join(result.Errors, "; ")),
			)
		}
		c.app.emitBrowserEvent("shadow_agent:task_pipeline", result)
	}()
	return receipt
}

func (c *ActivityTriager) dispatchTaskShadowProductionFlow(
	parent context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
	sourcePipelineRunIDs ...int64,
) TaskShadowObserverDispatchReceipt {
	sourcePipelineRunID := int64(0)
	if len(sourcePipelineRunIDs) > 0 {
		sourcePipelineRunID = sourcePipelineRunIDs[0]
	}
	return c.dispatchTaskShadowFlow(
		parent,
		group,
		events,
		true,
		TaskShadowObserverAdmissionEligible,
		"generic activity admission passed",
		sourcePipelineRunID,
	)
}

func (c *ActivityTriager) dispatchTaskShadowContinuationFlow(
	parent context.Context,
	group sessionGroup,
	events []activitymon.ActivityEvent,
	admission, admissionReason string,
	sourcePipelineRunIDs ...int64,
) TaskShadowObserverDispatchReceipt {
	sourcePipelineRunID := int64(0)
	if len(sourcePipelineRunIDs) > 0 {
		sourcePipelineRunID = sourcePipelineRunIDs[0]
	}
	return c.dispatchTaskShadowFlow(
		parent,
		group,
		events,
		false,
		admission,
		admissionReason,
		sourcePipelineRunID,
	)
}

func (c *ActivityTriager) dispatchTaskShadowEvaluation(
	parent context.Context,
	shadowRunID string,
) {
	if c == nil || c.app == nil || strings.TrimSpace(shadowRunID) == "" {
		return
	}
	if c.app.activity == nil || c.app.activity.store == nil {
		return
	}
	excluded, err := c.app.activity.store.
		isShadowTaskRunFirstRunLearningExcluded(shadowRunID)
	if err != nil {
		log.Printf("[shadow-task-evaluation] exclusion check run=%s failed: %v", shadowRunID, err)
		return
	}
	if excluded {
		return
	}
	baseCtx := parent
	if baseCtx == nil {
		baseCtx = c.app.ctx
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if !c.app.loadTriageFeatureFlags(baseCtx).Shadow {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		excluded, err := c.app.activity.store.
			isShadowTaskRunFirstRunLearningExcluded(shadowRunID)
		if err != nil {
			log.Printf("[shadow-task-evaluation] exclusion recheck run=%s failed: %v", shadowRunID, err)
			return
		}
		if excluded {
			return
		}
		if c.shadowExecutionSem != nil {
			select {
			case c.shadowExecutionSem <- struct{}{}:
				defer func() { <-c.shadowExecutionSem }()
			case <-baseCtx.Done():
				return
			}
		}
		if !c.app.loadTriageFeatureFlags(baseCtx).Shadow {
			return
		}
		runCtx, cancel := context.WithTimeout(baseCtx, shadowTaskEvaluationTimeout)
		defer cancel()
		if _, _, err := c.app.tryEvaluateShadowTaskRun(runCtx, shadowRunID); err != nil &&
			!errors.Is(err, ErrShadowTaskAwaitingObservedOutcome) {
			log.Printf("[shadow-task-evaluation] run=%s failed: %v", shadowRunID, err)
		}
	}()
}
