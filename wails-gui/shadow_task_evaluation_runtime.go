package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

var ErrShadowTaskAwaitingObservedOutcome = errors.New(
	"shadow task is awaiting an observed user outcome",
)

const shadowTaskEvaluationTimeout = 3 * time.Minute

type ShadowTaskEvaluationRuntimeResult struct {
	Evaluation ShadowRunEvaluation   `json:"evaluation"`
	Learning   ShadowTaskLearningRun `json:"learning"`
	Added      bool                  `json:"added"`
}

func (a *App) tryEvaluateShadowTaskRun(
	ctx context.Context,
	shadowRunID string,
) (ShadowTaskEvaluationRuntimeResult, bool, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return ShadowTaskEvaluationRuntimeResult{}, false, errors.New(
			"activity store is unavailable",
		)
	}
	shadowRunID = strings.TrimSpace(shadowRunID)
	if shadowRunID == "" {
		return ShadowTaskEvaluationRuntimeResult{}, false, errors.New(
			"shadow run ID is required",
		)
	}
	value, err, _ := a.shadowTaskEvaluationSF.Do(shadowRunID, func() (any, error) {
		run, err := a.activity.store.GetShadowRun(shadowRunID)
		if err != nil {
			return nil, err
		}
		if err := requireCurrentShadowTaskRun(run); err != nil {
			return nil, err
		}
		episode, err := a.activity.store.GetTaskEpisode(run.TaskEpisodeID)
		if err != nil {
			return nil, err
		}
		if err := a.activity.store.taskEpisodeRepeatedOnsetError(
			episode.ID,
		); err != nil {
			return nil, fmt.Errorf(
				"shadow task evaluation excluded: %w",
				err,
			)
		}
		if err := taskEpisodeFrozenContextSafetyError(episode); err != nil {
			return nil, fmt.Errorf(
				"shadow task evaluation excluded: %w",
				err,
			)
		}
		switch episode.CurrentStatus {
		case TaskEpisodeStatusCompleted,
			TaskEpisodeStatusAbandoned,
			TaskEpisodeStatusExpired:
		default:
			return nil, ErrShadowTaskAwaitingObservedOutcome
		}
		readiness, err := BuildShadowTaskPairReadiness(
			a.activity.store,
			run.ID,
		)
		if err != nil {
			return nil, err
		}
		if episode.CurrentStatus == TaskEpisodeStatusExpired &&
			readiness.State != ShadowTaskPairStateReady &&
			readiness.State != ShadowTaskPairStateEvaluated {
			return nil, fmt.Errorf(
				"%w: %s",
				ErrShadowTaskAwaitingObservedOutcome,
				readiness.State,
			)
		}
		if taskWorkflowTextResult(run.CapabilityKey) &&
			readiness.State != ShadowTaskPairStateReady &&
			readiness.State != ShadowTaskPairStateEvaluated {
			switch readiness.State {
			case ShadowTaskPairStateAwaitingUserResult,
				ShadowTaskPairStateUserResultUnavailable:
				return nil, fmt.Errorf(
					"%w: %s",
					ErrShadowTaskAwaitingObservedOutcome,
					readiness.State,
				)
			case ShadowTaskPairStateShadowResultUnavailable:
				return nil, ErrShadowTaskShadowResultUnavailable
			case ShadowTaskPairStateShadowFailed:
				return nil, fmt.Errorf(
					"shadow task execution did not produce a pair: %s",
					strings.Join(readiness.Reasons, ", "),
				)
			default:
				return nil, fmt.Errorf(
					"shadow task pair is not ready: %s",
					readiness.State,
				)
			}
		}
		input, err := BuildShadowTaskEvaluationInput(a.activity.store, run.ID)
		if err != nil {
			return nil, err
		}
		comparatorPrompt, _, err := buildShadowTaskEvaluationPrompt(input)
		if err != nil {
			return nil, err
		}
		comparatorHash := shadowTaskComparatorHash(comparatorPrompt)
		existing, err := a.activity.store.ListShadowRunEvaluations(run.ID)
		if err != nil {
			return nil, err
		}
		for _, evaluation := range existing {
			if evaluation.ComparatorHash == comparatorHash &&
				evaluation.TaskEvaluation != nil {
				if _, ablationErr := a.activity.store.
					EvaluateWorkPatternProfileAblationForTask(
						episode,
						evaluation,
						time.Now().UnixMilli(),
					); ablationErr != nil {
					log.Printf(
						"[work-profile-ablation] evaluate existing task=%s evaluation=%s: %v",
						episode.ID,
						evaluation.ID,
						ablationErr,
					)
				}
				learning, _, learningErr := a.BuildAndPersistShadowTaskLearning(
					run.CapabilityKey,
					time.Now().UnixMilli(),
				)
				if learningErr != nil {
					return nil, learningErr
				}
				if _, _, profileErr := a.
					AppendWorkPatternProfileFromEvaluation(
						episode.ID,
						run.ID,
						evaluation.ID,
						time.Now().UnixMilli(),
					); profileErr != nil {
					log.Printf(
						"[work-profile-learning] existing evaluation=%s: %v",
						evaluation.ID,
						profileErr,
					)
				}
				return ShadowTaskEvaluationRuntimeResult{
					Evaluation: evaluation,
					Learning:   learning,
				}, nil
			}
		}

		a.mu.Lock()
		runner := a.runner
		a.mu.Unlock()
		if runner == nil {
			return nil, errors.New("agent runner unavailable")
		}
		baseCtx := ctx
		if baseCtx == nil {
			baseCtx = a.ctx
		}
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		evaluationCtx, cancel := context.WithTimeout(
			baseCtx,
			shadowTaskEvaluationTimeout,
		)
		defer cancel()
		startedAt := time.Now().UnixMilli()
		result, err := runShadowTaskEvaluation(
			evaluationCtx,
			runner,
			effectiveLightTaskModel(a),
			input,
		)
		completedAt := time.Now().UnixMilli()
		if err != nil {
			if result.ComparatorPrompt != "" {
				if _, attemptErr := recordTaskShadowModelAttempt(
					a.activity.store,
					TaskAgentAttemptPhaseEvaluation,
					ShadowTaskComparatorVersion,
					result.EvaluatorModel,
					input,
					result,
					result.EvaluatorRaw,
					result.ComparatorPrompt,
					err,
					startedAt,
					completedAt,
					episode.ID,
					run.ID,
					"",
				); attemptErr != nil {
					log.Printf(
						"[shadow-task-evaluation] record failed attempt run=%s: %v",
						run.ID,
						attemptErr,
					)
				}
			}
			return nil, err
		}
		persisted, added, err := PersistShadowTaskEvaluation(
			a.activity.store,
			input,
			result,
			completedAt,
		)
		if err != nil {
			if _, attemptErr := recordTaskShadowModelAttempt(
				a.activity.store,
				TaskAgentAttemptPhaseEvaluation,
				ShadowTaskComparatorVersion,
				result.EvaluatorModel,
				input,
				result,
				result.EvaluatorRaw,
				result.ComparatorPrompt,
				nil,
				startedAt,
				completedAt,
				episode.ID,
				run.ID,
				"",
			); attemptErr != nil {
				log.Printf(
					"[shadow-task-evaluation] record unlinked attempt run=%s: %v",
					run.ID,
					attemptErr,
				)
			}
			return nil, err
		}
		if _, attemptErr := recordTaskShadowModelAttempt(
			a.activity.store,
			TaskAgentAttemptPhaseEvaluation,
			ShadowTaskComparatorVersion,
			result.EvaluatorModel,
			input,
			result,
			result.EvaluatorRaw,
			result.ComparatorPrompt,
			nil,
			startedAt,
			completedAt,
			episode.ID,
			run.ID,
			persisted.ID,
		); attemptErr != nil {
			log.Printf(
				"[shadow-task-evaluation] record attempt run=%s evaluation=%s: %v",
				run.ID,
				persisted.ID,
				attemptErr,
			)
		}
		runtimeResult := ShadowTaskEvaluationRuntimeResult{
			Evaluation: persisted,
			Added:      added,
		}
		if _, ablationErr := a.activity.store.
			EvaluateWorkPatternProfileAblationForTask(
				episode,
				persisted,
				completedAt,
			); ablationErr != nil {
			log.Printf(
				"[work-profile-ablation] evaluate task=%s evaluation=%s: %v",
				episode.ID,
				persisted.ID,
				ablationErr,
			)
		}
		if added {
			learning, _, learningErr := a.BuildAndPersistShadowTaskLearning(
				run.CapabilityKey,
				time.Now().UnixMilli(),
			)
			if learningErr != nil {
				return nil, fmt.Errorf(
					"persist evaluation %s learning projection: %w",
					persisted.ID,
					learningErr,
				)
			}
			runtimeResult.Learning = learning
			if _, _, profileErr := a.
				AppendWorkPatternProfileFromEvaluation(
					episode.ID,
					run.ID,
					persisted.ID,
					completedAt,
				); profileErr != nil {
				log.Printf(
					"[work-profile-learning] evaluation=%s: %v",
					persisted.ID,
					profileErr,
				)
			}
			a.emitBrowserEvent("shadow_agent:task_evaluated", map[string]any{
				"shadowRunId":   run.ID,
				"evaluationId":  persisted.ID,
				"learningRunId": learning.ID,
			})
		}
		return runtimeResult, nil
	})
	if err != nil {
		return ShadowTaskEvaluationRuntimeResult{}, false, err
	}
	result, ok := value.(ShadowTaskEvaluationRuntimeResult)
	if !ok {
		return ShadowTaskEvaluationRuntimeResult{}, false, errors.New(
			"shadow task evaluation returned an invalid result",
		)
	}
	return result, result.Added, nil
}
