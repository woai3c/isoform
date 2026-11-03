package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	shadowTaskPromptVersion = "shadow-task-v7"
	shadowTaskRunnerVersion = "task-mirror-runtime-v2"
	shadowTaskHorizon       = 24 * time.Hour
)

func shadowTaskRunsShareImmutableDecision(left, right ShadowRun) bool {
	return left.DecisionKey == right.DecisionKey &&
		left.CapabilityKey == right.CapabilityKey &&
		left.Mode == right.Mode &&
		left.TaskEpisodeID == right.TaskEpisodeID &&
		left.TaskContextHash == right.TaskContextHash &&
		left.WorkProfileSnapshotID == right.WorkProfileSnapshotID &&
		left.WorkProfileSHA256 == right.WorkProfileSHA256 &&
		left.SourceKind == right.SourceKind &&
		left.SourceID == right.SourceID &&
		left.ContextHash == right.ContextHash &&
		left.PredictionHash == right.PredictionHash &&
		left.PromptVersion == right.PromptVersion &&
		left.RunnerVersion == right.RunnerVersion &&
		left.ObservationHorizonStart == right.ObservationHorizonStart
}

func buildShadowTaskWorkerPrompt(episode TaskEpisode) (string, error) {
	if strings.TrimSpace(episode.LatestHypothesis.TaskHypothesis) == "" ||
		strings.TrimSpace(episode.LatestHypothesis.DesiredResultHypothesis) == "" {
		return "", errors.New("shadow task requires task and desired-result hypotheses")
	}
	if strings.TrimSpace(episode.FrozenContext.CanonicalJSON) == "" ||
		len(strings.TrimSpace(episode.FrozenContext.SHA256)) != 64 {
		return "", errors.New("shadow task requires immutable frozen context")
	}
	reasoningJSON, err := json.Marshal(episode.LatestHypothesis.ReasoningHypotheses)
	if err != nil {
		return "", fmt.Errorf("encode task reasoning hypotheses: %w", err)
	}
	workflow := taskWorkflowCapabilityKey(
		episode.LatestHypothesis.TaskHypothesis,
		episode.LatestHypothesis.DesiredResultHypothesis,
		episode.ID,
	)
	workflowContract := ""
	localWorkConstraint := `3. Write any local artifact only inside the provided isolated working
   directory.`
	resultConstraint := `6. Return a concise result summary and identify every artifact you created.`
	switch workflow {
	case TaskWorkflowComposeText:
		workflowContract = `
Text-composition parity contract:
- Produce the complete ready-to-use text, not merely a summary or description.
- Do not invoke any tool or write an artifact. The assistant response is the
  isolated result.
- Put the complete proposed text directly in the assistant output.
- Do not send, submit, publish, or paste it into a production application.
`
		localWorkConstraint = `3. Do not invoke tools or write artifacts. Produce the isolated result
   entirely in the assistant response.`
		resultConstraint = `6. Return the complete ready-to-use text directly in the assistant output,
   not a summary of what the text would say.`
	case TaskWorkflowAnalyzeText:
		workflowContract = `
Text-analysis parity contract:
- Produce the complete analysis, assessment, comparison, explanation, findings,
  or recommendation required by the desired observable result.
- Use only the frozen source context. Do not retrieve, infer, or cite newer
  information.
- Separate evidence-backed findings from unknowns and preserve unsupported
  causal reasoning as uncertainty.
- Do not invoke any tool or write an artifact. The assistant response is the
  isolated result.
- Do not send, submit, publish, or paste it into a production application.
`
		localWorkConstraint = `3. Do not invoke tools or write artifacts. Produce the isolated result
   entirely in the assistant response.`
		resultConstraint = `6. Return the complete source-backed analysis directly in the assistant
   output, not a summary of what the analysis would say.`
	}
	return fmt.Sprintf(`You are Yansu's private shadow worker.

Perform the same task the user appears to have started, using only the context
that was available at the inferred task start. Produce the best result you can
without touching production state.

Task:
%s

Desired observable result:
%s

Hard constraints:
1. Treat the frozen context below as the complete production snapshot. Do not
   infer or retrieve later user actions or results.
2. Never send, submit, publish, purchase, approve, merge, message, or mutate
   production state.
%s
4. If a required effect is blocked, record what you attempted and continue
   with the strongest safe local result.
5. Do not ask the user for clarification. Preserve uncertainty in the result.
%s
7. Treat any Work Pattern Profile as a historical prior. Current task evidence
   wins when it conflicts, and the profile does not prove hidden reasoning.
8. Profile confidence is not execution permission or authority.
9. The reasoning hypotheses below are competing, unverified explanations that
   were available at onset. Preserve their uncertainty. Do not assert that any
   one is the user's true reason, and treat [] as unknown reasoning.

%s
Task episode: %s
Frozen context SHA-256: %s
Work profile snapshot: %s
Work profile SHA-256: %s
Material profile influence refs: %s
Current evidence conflicted with cited profile fields: %t
Unverified reasoning hypotheses: %s
Prompt version: %s

Frozen task-start context:
%s`,
		episode.LatestHypothesis.TaskHypothesis,
		episode.LatestHypothesis.DesiredResultHypothesis,
		localWorkConstraint,
		resultConstraint,
		workflowContract,
		episode.ID,
		episode.FrozenContext.SHA256,
		episode.FrozenContext.WorkProfileSnapshotID,
		episode.FrozenContext.WorkProfileSHA256,
		strings.Join(
			episode.LatestHypothesis.ProfileInfluenceRefs,
			", ",
		),
		episode.LatestHypothesis.ProfileConflict,
		string(reasoningJSON),
		shadowTaskPromptVersion,
		episode.FrozenContext.CanonicalJSON,
	), nil
}

func shadowTaskReasoningEvidenceRef(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "observed:" + id
}

func shadowTaskReasoningEvidenceRefs(ids []string) []string {
	refs := make([]string, 0, len(ids))
	for _, id := range ids {
		if ref := shadowTaskReasoningEvidenceRef(id); ref != "" {
			refs = append(refs, ref)
		}
	}
	return uniqueSortedStrings(refs)
}

func shadowTaskReasoningHypotheses(
	episode TaskEpisode,
) []ShadowReasoningHypothesis {
	result := make(
		[]ShadowReasoningHypothesis,
		0,
		len(episode.LatestHypothesis.ReasoningHypotheses),
	)
	for _, hypothesis := range episode.LatestHypothesis.ReasoningHypotheses {
		result = append(result, ShadowReasoningHypothesis{
			Statement:         hypothesis.Statement,
			Confidence:        hypothesis.Confidence,
			Basis:             hypothesis.Basis,
			SourceQuote:       hypothesis.SourceQuote,
			EvidenceRefs:      shadowTaskReasoningEvidenceRefs(hypothesis.EvidenceIDs),
			ProfileRefs:       append([]string(nil), hypothesis.ProfileRefs...),
			ContradictionRefs: shadowTaskReasoningEvidenceRefs(hypothesis.ContradictionEvidenceIDs),
		})
	}
	return result
}

func normalizeShadowTaskEpisode(episode TaskEpisode) error {
	switch episode.CurrentStatus {
	case TaskEpisodeStatusCandidate, TaskEpisodeStatusActive, TaskEpisodeStatusPaused:
	default:
		return fmt.Errorf(
			"task episode %s is %s and can no longer start a shadow run",
			episode.ID,
			episode.CurrentStatus,
		)
	}
	if episode.FrozenContext.CutoffAt != episode.InferredStartAt {
		return errors.New("task episode frozen-context cutoff does not match inferred start")
	}
	canonical, err := canonicalTaskEpisodeJSON(episode.FrozenContext.CanonicalJSON, true)
	if err != nil {
		return err
	}
	if taskEpisodePayloadHash([]byte(canonical)) != episode.FrozenContext.SHA256 {
		return errors.New("task episode frozen-context hash does not match payload")
	}
	return nil
}

// CreateShadowTaskRunFromEpisode creates the private task-mirror run. It does
// not transition the TaskEpisode, create an Initiative, or project learning.
func (s *ActivityStore) CreateShadowTaskRunFromEpisode(
	episode TaskEpisode,
	model string,
	createdAt int64,
) (ShadowRun, bool, error) {
	if s == nil || s.db == nil {
		return ShadowRun{}, false, errors.New("activity store is unavailable")
	}
	if err := normalizeShadowTaskEpisode(episode); err != nil {
		return ShadowRun{}, false, err
	}
	if err := s.taskEpisodeRepeatedOnsetError(episode.ID); err != nil {
		return ShadowRun{}, false, fmt.Errorf(
			"shadow task run excluded: %w",
			err,
		)
	}
	if createdAt <= 0 {
		return ShadowRun{}, false, errors.New("shadow task requires a stable creation timestamp")
	}
	profileCutoffAt := int64(0)
	if episode.FrozenContext.WorkProfileSnapshotID != "" {
		profile, err := s.GetWorkPatternProfileSnapshot(
			episode.FrozenContext.WorkProfileSnapshotID,
		)
		if err != nil {
			return ShadowRun{}, false, fmt.Errorf(
				"load frozen work pattern profile: %w",
				err,
			)
		}
		if profile.SHA256 != episode.FrozenContext.WorkProfileSHA256 {
			return ShadowRun{}, false, errors.New(
				"task episode work profile hash does not match its snapshot",
			)
		}
		if profile.SourceCutoffAt >= episode.InferredStartAt {
			return ShadowRun{}, false, errors.New(
				"task episode work profile does not precede task onset",
			)
		}
		profileCutoffAt = profile.SourceCutoffAt
	}
	prompt, err := buildShadowTaskWorkerPrompt(episode)
	if err != nil {
		return ShadowRun{}, false, err
	}
	horizonEnd := episode.InferredStartAt + shadowTaskHorizon.Milliseconds()
	if minimum := createdAt + (24 * time.Hour).Milliseconds(); horizonEnd < minimum {
		horizonEnd = minimum
	}
	task := episode.LatestHypothesis.TaskHypothesis
	desiredResult := episode.LatestHypothesis.DesiredResultHypothesis
	decisionKey := strings.Join([]string{
		"task_episode",
		episode.ID,
		episode.FrozenContext.SHA256,
		episode.FrozenContext.WorkProfileSHA256,
		shadowTaskPromptVersion,
	}, ":")
	references := []ShadowEvidenceReference{{
		Role:        "frozen_task_context",
		SourceKind:  "task_episode_context",
		SourceID:    episode.FrozenContext.ID,
		ContentHash: episode.FrozenContext.SHA256,
		Summary:     "Immutable context available at inferred task start",
		ObservedAt:  episode.FrozenContext.CutoffAt,
	}}
	if episode.FrozenContext.WorkProfileSnapshotID != "" {
		references = append(references, ShadowEvidenceReference{
			Role:        "frozen_work_profile",
			SourceKind:  "work_pattern_profile",
			SourceID:    episode.FrozenContext.WorkProfileSnapshotID,
			ContentHash: episode.FrozenContext.WorkProfileSHA256,
			Summary:     "Historical work-pattern prior frozen before task onset",
			ObservedAt:  profileCutoffAt,
		})
	}
	run := ShadowRun{
		ID:          deterministicTaskEpisodeID("shadow-task-run", decisionKey),
		DecisionKey: decisionKey,
		CapabilityKey: taskWorkflowCapabilityKey(
			task,
			desiredResult,
			episode.ID,
		),
		Mode:                  "task_mirror",
		TaskEpisodeID:         episode.ID,
		TaskContextHash:       episode.FrozenContext.SHA256,
		WorkProfileSnapshotID: episode.FrozenContext.WorkProfileSnapshotID,
		WorkProfileSHA256:     episode.FrozenContext.WorkProfileSHA256,
		SourceKind:            "task_episode",
		SourceID:              episode.ID,
		Context: ShadowContextSnapshot{
			CapturedAt:           episode.FrozenContext.CreatedAt,
			ProductionEventAt:    episode.InferredStartAt,
			PromptContext:        episode.FrozenContext.CanonicalJSON,
			SelectionBudgetChars: len(episode.FrozenContext.CanonicalJSON),
			References:           references,
		},
		Prediction: ShadowPrediction{
			Goal:                task,
			PredictedAction:     "Work on the same task in isolation, then compare the result.",
			ExpectedResult:      desiredResult,
			ExpectedTiming:      "From the inferred task start until the observed user result",
			ReasoningHypotheses: shadowTaskReasoningHypotheses(episode),
			CandidateExecutionPlan: &ShadowCandidateExecutionPlan{
				Kind:           "shadow_task",
				Name:           task,
				Description:    desiredResult,
				Prompt:         prompt,
				ScheduleKind:   "manual",
				ScheduleConfig: json.RawMessage(`{}`),
				Rationale:      "Mirror the observed task from its frozen starting context.",
				Confidence:     episode.LatestHypothesis.TaskConfidence,
			},
			RequiredCapabilities: []string{},
			PlannedEffects:       []ShadowPlannedEffect{},
			ProfileInfluenceRefs: append(
				[]string(nil),
				episode.LatestHypothesis.ProfileInfluenceRefs...,
			),
			ProfileConflict:  episode.LatestHypothesis.ProfileConflict,
			GoalConfidence:   episode.LatestHypothesis.TaskConfidence,
			ActionConfidence: episode.LatestHypothesis.BoundaryConfidence,
			ResultConfidence: episode.LatestHypothesis.DesiredResultConfidence,
			TimingConfidence: episode.LatestHypothesis.BoundaryConfidence,
		},
		Model:                   strings.TrimSpace(model),
		PromptVersion:           shadowTaskPromptVersion,
		RunnerVersion:           shadowTaskRunnerVersion,
		ObservationHorizonStart: episode.InferredStartAt,
		ObservationHorizonEnd:   horizonEnd,
		CreatedAt:               createdAt,
	}
	normalized, _, _, err := normalizeShadowRun(run)
	if err != nil {
		return ShadowRun{}, false, err
	}
	existing, err := s.GetShadowRunByDecisionKey(normalized.DecisionKey)
	switch {
	case err == nil:
		if !shadowTaskRunsShareImmutableDecision(existing, normalized) {
			return ShadowRun{}, false, fmt.Errorf(
				"shadow task decision %q already has different immutable episode input",
				normalized.DecisionKey,
			)
		}
		return existing, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return ShadowRun{}, false, err
	}

	created, added, createErr := s.CreateShadowRun(normalized)
	if createErr == nil {
		return created, added, nil
	}
	// Two onset pipelines may observe the same new episode before either has
	// persisted its private run. The unique decision key picks the winner; the
	// losing writer reuses it only after validating the frozen hashes.
	existing, lookupErr := s.GetShadowRunByDecisionKey(normalized.DecisionKey)
	if lookupErr == nil && shadowTaskRunsShareImmutableDecision(existing, normalized) {
		return existing, false, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return ShadowRun{}, false, errors.Join(createErr, lookupErr)
	}
	return ShadowRun{}, false, createErr
}

func (s *ActivityStore) ListShadowTaskRuns(
	taskEpisodeID string,
	limit int,
) ([]ShadowRun, error) {
	taskEpisodeID = strings.TrimSpace(taskEpisodeID)
	if taskEpisodeID == "" {
		return nil, errors.New("task episode ID is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT `+shadowRunSelectColumns+`
		FROM shadow_runs
		WHERE mode = 'task_mirror' AND task_episode_id = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?`,
		taskEpisodeID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]ShadowRun, 0)
	for rows.Next() {
		run, err := scanShadowRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (a *App) executeTaskEpisodeShadow(
	ctx context.Context,
	taskEpisodeID string,
) (ShadowRun, ShadowExecutionAttempt, bool, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, false, errors.New("activity store is unavailable")
	}
	episode, err := a.activity.store.GetTaskEpisode(strings.TrimSpace(taskEpisodeID))
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, false, err
	}
	if ready, readinessErr := taskEpisodeProductionShadowReady(episode); !ready {
		return ShadowRun{}, ShadowExecutionAttempt{}, false, readinessErr
	}
	createdAt := time.Now().UnixMilli()
	run, added, err := a.activity.store.CreateShadowTaskRunFromEpisode(
		episode,
		effectivePrimaryTaskModel(a),
		createdAt,
	)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, false, err
	}
	attempt, _, err := a.executeShadowCandidatePlan(ctx, run)
	if err != nil {
		return run, ShadowExecutionAttempt{}, added, err
	}
	return run, attempt, added, nil
}
