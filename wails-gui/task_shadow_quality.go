package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	taskShadowQualityCampaignVersion = "task-shadow-quality-campaign-v1"
	taskShadowQualityProcessLimit    = 8
	taskShadowQualityTimeout         = 20 * time.Minute
)

type TaskShadowQualityCounts struct {
	DetectedEpisodes         int `json:"detectedEpisodes"`
	EligibleEpisodes         int `json:"eligibleEpisodes"`
	IneligibleEpisodes       int `json:"ineligibleEpisodes"`
	ShadowRuns               int `json:"shadowRuns"`
	CurrentPromptRuns        int `json:"currentPromptRuns"`
	HistoricalRuns           int `json:"historicalRuns"`
	CompletedShadowAttempts  int `json:"completedShadowAttempts"`
	IndependentResults       int `json:"independentResults"`
	ObservedResults          int `json:"observedResults"`
	AwaitingNaturalOutcomes  int `json:"awaitingNaturalOutcomes"`
	PairReady                int `json:"pairReady"`
	Evaluated                int `json:"evaluated"`
	CurrentPromptEvaluations int `json:"currentPromptEvaluations"`
	HistoricalEvaluations    int `json:"historicalEvaluations"`
	SafeEvaluations          int `json:"safeEvaluations"`
	SafetyConcerns           int `json:"safetyConcerns"`
	ShadowFailures           int `json:"shadowFailures"`
	TerminalWithoutShadowRun int `json:"terminalWithoutShadowRun"`
	ProcessablePairs         int `json:"processablePairs"`
}

type TaskShadowQualityBlocker struct {
	TaskEpisodeID string   `json:"taskEpisodeId"`
	ShadowRunID   string   `json:"shadowRunId,omitempty"`
	State         string   `json:"state"`
	Reasons       []string `json:"reasons"`
	OccurredAt    int64    `json:"occurredAt"`
}

type TaskShadowQualityCampaign struct {
	Version                   string                              `json:"version"`
	CapabilityKey             string                              `json:"capabilityKey"`
	PromptVersion             string                              `json:"promptVersion"`
	EvidenceHash              string                              `json:"evidenceHash,omitempty"`
	Stage                     string                              `json:"stage"`
	Complete                  bool                                `json:"complete"`
	ExistingEvidenceExhausted bool                                `json:"existingEvidenceExhausted"`
	RequiredAction            string                              `json:"requiredAction"`
	Counts                    TaskShadowQualityCounts             `json:"counts"`
	Qualification             ShadowTaskLearningQualification     `json:"qualification"`
	PendingExecutionRunIDs    []string                            `json:"pendingExecutionRunIds"`
	ReadyPairRunIDs           []string                            `json:"readyPairRunIds"`
	Blockers                  []TaskShadowQualityBlocker          `json:"blockers"`
	Release                   TaskShadowCapabilityReleaseSnapshot `json:"release"`
	GeneratedAt               int64                               `json:"generatedAt"`
}

type TaskShadowQualityCampaignRunResult struct {
	Version               string                    `json:"version"`
	CapabilityKey         string                    `json:"capabilityKey"`
	InspectedRunIDs       []string                  `json:"inspectedRunIds"`
	ExecutedRunIDs        []string                  `json:"executedRunIds"`
	EvaluatedRunIDs       []string                  `json:"evaluatedRunIds"`
	ExistingEvaluationIDs []string                  `json:"existingEvaluationIds"`
	DeferredRunIDs        []string                  `json:"deferredRunIds"`
	Errors                []string                  `json:"errors"`
	ReleaseEventID        string                    `json:"releaseEventId,omitempty"`
	ReleaseEventAdded     bool                      `json:"releaseEventAdded"`
	StartedAt             int64                     `json:"startedAt"`
	CompletedAt           int64                     `json:"completedAt"`
	Campaign              TaskShadowQualityCampaign `json:"campaign"`
}

func defaultTaskShadowQualityQualification() ShadowTaskLearningQualification {
	return ShadowTaskLearningQualification{
		Decision: ShadowTaskLearningDecisionCollecting,
		Reasons: []string{
			"need_context_diversity",
			"need_qualified_evaluations",
			"need_source_backed_safety_coverage",
			"need_task_episode_diversity",
			"result_quality_below_threshold",
			"task_reliability_below_threshold",
		},
		RequiredEvaluationCount:  shadowTaskLearningMinCapabilityRuns,
		RequiredTaskEpisodeCount: shadowTaskLearningMinCapabilityRuns,
		RequiredContextCount:     shadowTaskLearningMinCapabilityContexts,
		RequiredSafetyCount:      shadowTaskLearningMinCapabilitySafety,
	}
}

func taskShadowQualityQualification(
	projection ShadowTaskLearningProjection,
) (ShadowTaskLearningQualification, string) {
	if candidate, ok := taskShadowQualityCapabilityCandidate(projection); ok {
		return candidate.Qualification, candidate.EvidenceHash
	}
	return defaultTaskShadowQualityQualification(), ""
}

func taskShadowQualityEvaluationSafety(
	record ShadowTaskLearningEvaluationRecord,
) (safe, concern bool) {
	normalized, exclusion, err := shadowTaskLearningNormalizeRecord(record)
	if err != nil || exclusion != nil ||
		!shadowTaskLearningKnownSafetyDimension(
			normalized,
			normalized.Evaluation.Safety,
		) {
		return false, false
	}
	switch normalized.Evaluation.Safety.Assessment {
	case ShadowTaskSafetySafe:
		return true, false
	case ShadowTaskSafetyConcern, ShadowTaskSafetyUnsafe:
		return false, true
	default:
		return false, false
	}
}

func taskShadowQualityTerminal(status string) bool {
	return status == TaskEpisodeStatusCompleted ||
		status == TaskEpisodeStatusAbandoned
}

func taskShadowQualityAppendBlocker(
	result *TaskShadowQualityCampaign,
	limit int,
	blocker TaskShadowQualityBlocker,
) {
	if result == nil || len(result.Blockers) >= limit {
		return
	}
	blocker.Reasons = uniqueSortedStrings(blocker.Reasons)
	result.Blockers = append(result.Blockers, blocker)
}

func buildTaskShadowQualityCampaign(
	store *ActivityStore,
	limit int,
	now int64,
) (TaskShadowQualityCampaign, error) {
	if store == nil || store.db == nil {
		return TaskShadowQualityCampaign{}, errors.New(
			"task shadow quality campaign requires activity storage",
		)
	}
	episodes, err := taskShadowDebugEpisodeIndexRows(
		store,
		taskEpisodeMaxListLimit,
	)
	if err != nil {
		return TaskShadowQualityCampaign{}, err
	}
	episodeIDs := taskShadowDebugEpisodeIndexIDs(episodes)
	runsByEpisode, _, err := taskShadowDebugIndexRuns(store, episodeIDs)
	if err != nil {
		return TaskShadowQualityCampaign{}, err
	}
	return buildTaskShadowQualityCampaignFromIndex(
		store,
		limit,
		now,
		episodes,
		runsByEpisode,
	)
}

func buildTaskShadowQualityCampaignFromIndex(
	store *ActivityStore,
	limit int,
	now int64,
	episodes []taskShadowDebugEpisodeIndexRow,
	runsByEpisode map[string][]taskShadowDebugRunIndexRow,
) (TaskShadowQualityCampaign, error) {
	if store == nil || store.db == nil {
		return TaskShadowQualityCampaign{}, errors.New(
			"task shadow quality campaign requires activity storage",
		)
	}
	limit = normalizeTaskShadowDebugLimit(limit)
	result := TaskShadowQualityCampaign{
		Version:                taskShadowQualityCampaignVersion,
		CapabilityKey:          TaskWorkflowComposeText,
		PromptVersion:          shadowTaskPromptVersion,
		Stage:                  "waiting_for_natural_task",
		RequiredAction:         "Continue ordinary text-composition work while observation is active.",
		Qualification:          defaultTaskShadowQualityQualification(),
		PendingExecutionRunIDs: []string{},
		ReadyPairRunIDs:        []string{},
		Blockers:               []TaskShadowQualityBlocker{},
		GeneratedAt:            now,
	}
	seenRuns := make(map[string]struct{})
	seenEvaluations := make(map[string]struct{})
	for _, episode := range episodes {
		summary := episode.Summary
		workflow := taskWorkflowCapabilityKey(
			summary.TaskHypothesis,
			summary.DesiredResultHypothesis,
			summary.ID,
		)
		if workflow != TaskWorkflowComposeText {
			continue
		}
		result.Counts.DetectedEpisodes++
		ready, readinessErr := taskWorkflowProductionShadowReadiness(
			workflow,
			summary.TaskHypothesis,
			episode.HasFrozenSourceContext,
		)
		if ready {
			result.Counts.EligibleEpisodes++
		} else {
			result.Counts.IneligibleEpisodes++
		}
		runs := runsByEpisode[summary.ID]
		if len(runs) > taskShadowDebugMaxRuns {
			runs = taskShadowDebugLast(runs, taskShadowDebugMaxRuns)
		}
		composeRuns := make([]taskShadowDebugRunIndexRow, 0, len(runs))
		for _, run := range runs {
			if run.CapabilityKey == TaskWorkflowComposeText {
				composeRuns = append(composeRuns, run)
			}
		}
		if len(composeRuns) == 0 {
			state := "no_shadow_run"
			reasons := []string{"no independent shadow run was frozen at task onset"}
			if readinessErr != nil {
				state = "ineligible_frozen_context"
				reasons = []string{readinessErr.Error()}
			}
			if taskShadowQualityTerminal(summary.Status) {
				result.Counts.TerminalWithoutShadowRun++
			}
			taskShadowQualityAppendBlocker(&result, limit, TaskShadowQualityBlocker{
				TaskEpisodeID: summary.ID,
				State:         state,
				Reasons:       reasons,
				OccurredAt:    summary.CreatedAt,
			})
			continue
		}
		for _, run := range composeRuns {
			if _, duplicate := seenRuns[run.ID]; duplicate {
				continue
			}
			seenRuns[run.ID] = struct{}{}
			result.Counts.ShadowRuns++
			if run.PromptVersion != shadowTaskPromptVersion {
				result.Counts.HistoricalRuns++
				taskShadowQualityAppendBlocker(&result, limit, TaskShadowQualityBlocker{
					TaskEpisodeID: summary.ID,
					ShadowRunID:   run.ID,
					State:         "historical_prompt_generation",
					Reasons: []string{fmt.Sprintf(
						"prompt %q is retained history and cannot qualify current prompt %q",
						run.PromptVersion,
						shadowTaskPromptVersion,
					)},
					OccurredAt: run.CreatedAt,
				})
				continue
			}
			result.Counts.CurrentPromptRuns++
			pair, err := BuildShadowTaskPairReadiness(store, run.ID)
			if err != nil {
				result.Counts.ShadowFailures++
				taskShadowQualityAppendBlocker(&result, limit, TaskShadowQualityBlocker{
					TaskEpisodeID: summary.ID,
					ShadowRunID:   run.ID,
					State:         "readiness_error",
					Reasons:       []string{err.Error()},
					OccurredAt:    run.CreatedAt,
				})
				continue
			}
			if pair.ShadowAttemptStatus == "completed" {
				result.Counts.CompletedShadowAttempts++
			}
			if pair.ShadowResultMaterialized {
				result.Counts.IndependentResults++
			}
			if pair.UserResultMaterialized {
				result.Counts.ObservedResults++
			}
			for _, evaluationID := range pair.CurrentEvaluationIDs {
				seenEvaluations[evaluationID] = struct{}{}
			}
			switch pair.State {
			case ShadowTaskPairStateEvaluated:
			case ShadowTaskPairStateReady:
				result.Counts.PairReady++
				result.ReadyPairRunIDs = append(result.ReadyPairRunIDs, run.ID)
			case ShadowTaskPairStateShadowPending:
				if run.ObservationHorizonEnd > 0 &&
					now > run.ObservationHorizonEnd {
					result.Counts.ShadowFailures++
					taskShadowQualityAppendBlocker(&result, limit, TaskShadowQualityBlocker{
						TaskEpisodeID: summary.ID,
						ShadowRunID:   run.ID,
						State:         "observation_horizon_expired",
						Reasons:       []string{"pending shadow run exceeded its frozen observation horizon"},
						OccurredAt:    run.CreatedAt,
					})
					continue
				}
				result.PendingExecutionRunIDs = append(
					result.PendingExecutionRunIDs,
					run.ID,
				)
			case ShadowTaskPairStateAwaitingUserResult:
				result.Counts.AwaitingNaturalOutcomes++
				taskShadowQualityAppendBlocker(&result, limit, TaskShadowQualityBlocker{
					TaskEpisodeID: summary.ID,
					ShadowRunID:   run.ID,
					State:         pair.State,
					Reasons:       pair.Reasons,
					OccurredAt:    run.CreatedAt,
				})
			default:
				result.Counts.ShadowFailures++
				taskShadowQualityAppendBlocker(&result, limit, TaskShadowQualityBlocker{
					TaskEpisodeID: summary.ID,
					ShadowRunID:   run.ID,
					State:         pair.State,
					Reasons:       pair.Reasons,
					OccurredAt:    run.CreatedAt,
				})
			}
		}
	}

	records, err := store.ListShadowTaskLearningEvaluationRecords(
		TaskWorkflowComposeText,
	)
	if err != nil {
		return TaskShadowQualityCampaign{}, err
	}
	projection, err := BuildShadowTaskLearningProjection(records)
	if err != nil {
		return TaskShadowQualityCampaign{}, err
	}
	result.Qualification, result.EvidenceHash =
		taskShadowQualityQualification(projection)
	for _, record := range records {
		if record.ShadowPromptVersion == shadowTaskPromptVersion &&
			record.Input.Version == ShadowTaskEvaluationInputVersion &&
			record.Evaluation != nil &&
			record.Evaluation.Version == ShadowTaskEvaluationOutputVersion &&
			record.ComparisonVersion == ShadowTaskComparatorVersion &&
			record.EvaluatorVersion == ShadowTaskEvaluatorVersion {
			result.Counts.CurrentPromptEvaluations++
		} else {
			result.Counts.HistoricalEvaluations++
		}
		safe, concern := taskShadowQualityEvaluationSafety(record)
		if safe {
			result.Counts.SafeEvaluations++
		}
		if concern {
			result.Counts.SafetyConcerns++
		}
	}
	result.Counts.Evaluated = len(seenEvaluations)
	result.PendingExecutionRunIDs = uniqueSortedStrings(
		result.PendingExecutionRunIDs,
	)
	result.ReadyPairRunIDs = uniqueSortedStrings(result.ReadyPairRunIDs)
	result.Counts.ProcessablePairs = len(result.PendingExecutionRunIDs) +
		len(result.ReadyPairRunIDs)
	result.ExistingEvidenceExhausted = result.Counts.ProcessablePairs == 0

	sort.Slice(result.Blockers, func(i, j int) bool {
		if result.Blockers[i].OccurredAt != result.Blockers[j].OccurredAt {
			return result.Blockers[i].OccurredAt > result.Blockers[j].OccurredAt
		}
		return result.Blockers[i].TaskEpisodeID >
			result.Blockers[j].TaskEpisodeID
	})
	result.Complete = result.Qualification.Decision ==
		ShadowTaskLearningDecisionQualified &&
		result.Counts.SafetyConcerns == 0
	switch {
	case result.Complete:
		result.Stage = "qualified"
		result.RequiredAction = "The compose.text shadow has enough diverse, safe production evidence for capability learning."
	case result.Qualification.Decision == ShadowTaskLearningDecisionRejected:
		result.Stage = "rejected"
		result.RequiredAction = "Inspect the source-backed safety or quality evidence before collecting more comparisons."
	case result.Counts.ProcessablePairs > 0:
		result.Stage = "ready_to_process"
		result.RequiredAction = fmt.Sprintf(
			"Process %d isolated run or complete pair already supported by durable evidence.",
			result.Counts.ProcessablePairs,
		)
	case result.Counts.AwaitingNaturalOutcomes > 0:
		result.Stage = "collecting_natural_outcomes"
		result.RequiredAction = "Wait for source-backed outcomes from ordinary composition work; replay cannot substitute for user evidence."
	case result.Counts.DetectedEpisodes > 0:
		result.Stage = "evidence_exhausted"
		result.RequiredAction = "Existing safe evidence is exhausted. Keep observing new ordinary composition tasks."
	}
	result.Release, err = buildTaskShadowCapabilityReleaseSnapshot(
		store,
		result,
		limit,
	)
	if err != nil {
		return TaskShadowQualityCampaign{}, err
	}
	return result, nil
}

func (a *App) taskShadowQualityCampaign(
	limit int,
) (TaskShadowQualityCampaign, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return TaskShadowQualityCampaign{}, err
	}
	return buildTaskShadowQualityCampaign(
		store,
		limit,
		time.Now().UnixMilli(),
	)
}

// RunTaskShadowQualityCampaign advances only already-frozen compose.text
// mirrors. It may resume isolated execution and evaluate a materialized pair;
// it never creates an episode, invents an observed outcome, or touches a
// production application.
func (a *App) RunTaskShadowQualityCampaign() (
	TaskShadowQualityCampaignRunResult,
	error,
) {
	startedAt := time.Now().UnixMilli()
	result := TaskShadowQualityCampaignRunResult{
		Version:               taskShadowQualityCampaignVersion,
		CapabilityKey:         TaskWorkflowComposeText,
		InspectedRunIDs:       []string{},
		ExecutedRunIDs:        []string{},
		EvaluatedRunIDs:       []string{},
		ExistingEvaluationIDs: []string{},
		DeferredRunIDs:        []string{},
		Errors:                []string{},
		StartedAt:             startedAt,
	}
	campaign, err := a.taskShadowQualityCampaign(taskShadowDebugMaxLimit)
	if err != nil {
		return result, err
	}
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, taskShadowQualityTimeout)
	defer cancel()

	processable := append(
		append([]string{}, campaign.PendingExecutionRunIDs...),
		campaign.ReadyPairRunIDs...,
	)
	processable = uniqueSortedStrings(processable)
	if len(processable) > taskShadowQualityProcessLimit {
		result.DeferredRunIDs = append(
			result.DeferredRunIDs,
			processable[taskShadowQualityProcessLimit:]...,
		)
		processable = processable[:taskShadowQualityProcessLimit]
	}
	for _, runID := range processable {
		result.InspectedRunIDs = append(result.InspectedRunIDs, runID)
		run, runErr := a.activity.store.GetShadowRun(runID)
		if runErr != nil {
			result.Errors = append(result.Errors, runID+": "+runErr.Error())
			continue
		}
		if run.Mode != "task_mirror" ||
			run.CapabilityKey != TaskWorkflowComposeText {
			result.Errors = append(
				result.Errors,
				runID+": run is outside the compose.text production campaign",
			)
			continue
		}
		if run.PromptVersion != shadowTaskPromptVersion {
			result.Errors = append(
				result.Errors,
				runID+": run belongs to historical prompt generation "+run.PromptVersion,
			)
			continue
		}
		episode, episodeErr := a.activity.store.GetTaskEpisode(run.TaskEpisodeID)
		if episodeErr != nil {
			result.Errors = append(result.Errors, runID+": "+episodeErr.Error())
			continue
		}
		if safetyErr := taskEpisodeFrozenContextSafetyError(episode); safetyErr != nil {
			result.Errors = append(result.Errors, runID+": "+safetyErr.Error())
			continue
		}
		pair, pairErr := BuildShadowTaskPairReadiness(a.activity.store, runID)
		if pairErr != nil {
			result.Errors = append(result.Errors, runID+": "+pairErr.Error())
			continue
		}
		if pair.State == ShadowTaskPairStateShadowPending {
			if _, _, executionErr := a.executeShadowCandidatePlan(ctx, run); executionErr != nil {
				result.Errors = append(
					result.Errors,
					runID+": "+executionErr.Error(),
				)
				continue
			}
			result.ExecutedRunIDs = append(result.ExecutedRunIDs, runID)
			pair, pairErr = BuildShadowTaskPairReadiness(a.activity.store, runID)
			if pairErr != nil {
				result.Errors = append(result.Errors, runID+": "+pairErr.Error())
				continue
			}
		}
		if pair.State == ShadowTaskPairStateEvaluated {
			result.ExistingEvaluationIDs = append(
				result.ExistingEvaluationIDs,
				pair.CurrentEvaluationIDs...,
			)
			continue
		}
		if pair.State != ShadowTaskPairStateReady {
			result.DeferredRunIDs = append(result.DeferredRunIDs, runID)
			continue
		}
		evaluation, added, evaluationErr := a.tryEvaluateShadowTaskRun(ctx, runID)
		if evaluationErr != nil {
			result.Errors = append(
				result.Errors,
				runID+": "+evaluationErr.Error(),
			)
			continue
		}
		if added {
			result.EvaluatedRunIDs = append(result.EvaluatedRunIDs, runID)
		} else if evaluation.Evaluation.ID != "" {
			result.ExistingEvaluationIDs = append(
				result.ExistingEvaluationIDs,
				evaluation.Evaluation.ID,
			)
		}
	}
	result.InspectedRunIDs = uniqueSortedStrings(result.InspectedRunIDs)
	result.ExecutedRunIDs = uniqueSortedStrings(result.ExecutedRunIDs)
	result.EvaluatedRunIDs = uniqueSortedStrings(result.EvaluatedRunIDs)
	result.ExistingEvaluationIDs = uniqueSortedStrings(
		result.ExistingEvaluationIDs,
	)
	result.DeferredRunIDs = uniqueSortedStrings(result.DeferredRunIDs)
	sort.Strings(result.Errors)
	result.CompletedAt = time.Now().UnixMilli()
	result.Campaign, err = a.taskShadowQualityCampaign(taskShadowDebugMaxLimit)
	if err != nil {
		return result, err
	}
	releaseEvent, releaseAdded, releaseErr :=
		reconcileTaskShadowCapabilityReleaseFromStore(
			a.activity.store,
			result.CompletedAt,
		)
	if releaseErr != nil {
		return result, releaseErr
	}
	result.ReleaseEventID = releaseEvent.ID
	result.ReleaseEventAdded = releaseAdded
	if releaseAdded {
		result.Campaign, err = a.taskShadowQualityCampaign(
			taskShadowDebugMaxLimit,
		)
		if err != nil {
			return result, err
		}
	}
	a.emitBrowserEvent("shadow_agent:quality_campaign", result)
	return result, nil
}
