package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"yansu-agent/agent"
)

type taskOnsetAblationAttempt struct {
	Response    TaskOnsetResponse
	AttemptID   string
	Status      string
	StartedAt   int64
	CompletedAt int64
	RunErr      error
	RecordErr   error
}

type taskOnsetAblationResult struct {
	Response            TaskOnsetResponse
	CompletedAt         int64
	AttemptIDs          []string
	ProductionAttemptID string
	Diagnostics         []string
	AblationRun         WorkPatternProfileAblationRun
	PrimaryMode         string
	ProfileUsed         bool
	ProductionErr       error
}

func executeTaskOnsetAblationAttempt(
	ctx context.Context,
	store *ActivityStore,
	runner agent.AgentRunner,
	model string,
	input TaskOnsetInput,
) taskOnsetAblationAttempt {
	startedAt := time.Now().UnixMilli()
	response, raw, prompt, runErr := runTaskOnsetDetection(
		ctx,
		runner,
		model,
		input,
	)
	completedAt := time.Now().UnixMilli()
	result := taskOnsetAblationAttempt{
		Response:    response,
		Status:      taskShadowAttemptStatus(runErr),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		RunErr:      runErr,
	}
	if strings.TrimSpace(prompt) == "" {
		return result
	}
	attemptInput, err := prepareTaskOnsetPromptInput(input)
	if err != nil {
		result.RecordErr = fmt.Errorf(
			"prepare exact task onset attempt input: %w",
			err,
		)
		return result
	}
	attempt, recordErr := recordTaskShadowModelAttempt(
		store,
		TaskAgentAttemptPhaseOnset,
		taskOnsetPromptVersion,
		model,
		attemptInput,
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
	result.AttemptID = attempt.ID
	result.RecordErr = recordErr
	return result
}

func appendTaskOnsetAblationAttempt(
	result *taskOnsetAblationResult,
	label string,
	attempt taskOnsetAblationAttempt,
) {
	if result == nil {
		return
	}
	if attempt.AttemptID != "" {
		result.AttemptIDs = append(result.AttemptIDs, attempt.AttemptID)
	}
	if attempt.RecordErr != nil {
		result.Diagnostics = append(
			result.Diagnostics,
			fmt.Sprintf("%s attempt receipt: %v", label, attempt.RecordErr),
		)
	}
}

func taskOnsetAblationPrimaryResponse(
	run WorkPatternProfileAblationRun,
) (TaskOnsetResponse, string, error) {
	switch run.PrimaryMode {
	case WorkPatternInfluenceProfile:
		if run.ProfileStatus != TaskAgentAttemptStatusSucceeded {
			return TaskOnsetResponse{}, run.ProfileAttemptID, errors.New(
				"profile-aware onset attempt did not succeed",
			)
		}
		return run.ProfileResponse, run.ProfileAttemptID, nil
	default:
		if run.NeutralStatus != TaskAgentAttemptStatusSucceeded {
			return TaskOnsetResponse{}, run.NeutralAttemptID, errors.New(
				"neutral onset attempt did not succeed",
			)
		}
		return run.NeutralResponse, run.NeutralAttemptID, nil
	}
}

// runTaskOnsetWithWorkPatternAblation keeps the existing task-capture path on
// one configured branch. The paired branch is diagnostic-only: it cannot
// create a TaskEpisode, ShadowRun, initiative, action, or authority decision.
func runTaskOnsetWithWorkPatternAblation(
	ctx context.Context,
	store *ActivityStore,
	runner agent.AgentRunner,
	model string,
	input TaskOnsetInput,
	profile WorkPatternProfileContext,
	profileInfluenceEnabled bool,
) taskOnsetAblationResult {
	result := taskOnsetAblationResult{
		PrimaryMode: WorkPatternInfluenceNeutral,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	neutralInput := input
	neutralInput.WorkProfile = nil

	if !workPatternProfileEvidenceQualified(profile) {
		attempt := executeTaskOnsetAblationAttempt(
			ctx,
			store,
			runner,
			model,
			neutralInput,
		)
		result.Response = attempt.Response
		result.CompletedAt = attempt.CompletedAt
		result.ProductionAttemptID = attempt.AttemptID
		result.ProductionErr = attempt.RunErr
		appendTaskOnsetAblationAttempt(&result, "neutral", attempt)
		return result
	}

	profile = profileWorkPatternProfileContext(profile)
	profileInput := input
	profileInput.WorkProfile = &profile
	if profileInfluenceEnabled {
		result.PrimaryMode = WorkPatternInfluenceProfile
		result.ProfileUsed = true
	}

	ablationKey := "work-pattern-ablation:" + taskAgentAttemptHash(
		[]byte(workPatternAblationKey(
			input,
			profile,
			model,
			result.PrimaryMode,
		)),
	)
	existing, err := store.GetWorkPatternProfileAblationRunByKey(ablationKey)
	if err == nil {
		result.AblationRun = existing
		result.CompletedAt = existing.CompletedAt
		result.AttemptIDs = uniqueSortedStrings([]string{
			existing.NeutralAttemptID,
			existing.ProfileAttemptID,
		})
		result.Response, result.ProductionAttemptID, result.ProductionErr =
			taskOnsetAblationPrimaryResponse(existing)
		return result
	}
	if !errors.Is(err, sql.ErrNoRows) {
		result.ProductionErr = fmt.Errorf("load onset ablation: %w", err)
		return result
	}

	var neutralAttempt, profileAttempt taskOnsetAblationAttempt
	if result.PrimaryMode == WorkPatternInfluenceProfile {
		profileAttempt = executeTaskOnsetAblationAttempt(
			ctx,
			store,
			runner,
			model,
			profileInput,
		)
		appendTaskOnsetAblationAttempt(&result, "profile", profileAttempt)
		neutralAttempt = executeTaskOnsetAblationAttempt(
			ctx,
			store,
			runner,
			model,
			neutralInput,
		)
		appendTaskOnsetAblationAttempt(&result, "neutral", neutralAttempt)
		result.Response = profileAttempt.Response
		result.CompletedAt = profileAttempt.CompletedAt
		result.ProductionAttemptID = profileAttempt.AttemptID
		result.ProductionErr = profileAttempt.RunErr
	} else {
		neutralAttempt = executeTaskOnsetAblationAttempt(
			ctx,
			store,
			runner,
			model,
			neutralInput,
		)
		appendTaskOnsetAblationAttempt(&result, "neutral", neutralAttempt)
		profileAttempt = executeTaskOnsetAblationAttempt(
			ctx,
			store,
			runner,
			model,
			profileInput,
		)
		appendTaskOnsetAblationAttempt(&result, "profile", profileAttempt)
		result.Response = neutralAttempt.Response
		result.CompletedAt = neutralAttempt.CompletedAt
		result.ProductionAttemptID = neutralAttempt.AttemptID
		result.ProductionErr = neutralAttempt.RunErr
	}

	startedAt := neutralAttempt.StartedAt
	if startedAt == 0 ||
		(profileAttempt.StartedAt > 0 && profileAttempt.StartedAt < startedAt) {
		startedAt = profileAttempt.StartedAt
	}
	completedAt := max(neutralAttempt.CompletedAt, profileAttempt.CompletedAt)
	if completedAt == 0 {
		completedAt = result.CompletedAt
	}
	run, _, recordErr := store.RecordWorkPatternProfileAblationRun(
		RecordWorkPatternProfileAblationRunInput{
			IdempotencyKey:    ablationKey,
			SourceKind:        input.SourceKind,
			SourceID:          input.SourceID,
			WindowStart:       input.WindowStart,
			CutoffAt:          input.CutoffAt,
			ProfileSnapshotID: profile.SnapshotID,
			ProfileSHA256:     profile.SnapshotSHA256,
			PrimaryMode:       result.PrimaryMode,
			NeutralAttemptID:  neutralAttempt.AttemptID,
			ProfileAttemptID:  profileAttempt.AttemptID,
			NeutralStatus:     neutralAttempt.Status,
			ProfileStatus:     profileAttempt.Status,
			NeutralResponse:   neutralAttempt.Response,
			ProfileResponse:   profileAttempt.Response,
			Model:             model,
			PromptVersion:     taskOnsetPromptVersion,
			StartedAt:         startedAt,
			CompletedAt:       completedAt,
			CreatedAt:         completedAt,
		},
	)
	if recordErr != nil {
		result.Diagnostics = append(
			result.Diagnostics,
			"ablation receipt: "+recordErr.Error(),
		)
	} else {
		result.AblationRun = run
	}
	if result.PrimaryMode == WorkPatternInfluenceNeutral &&
		profileAttempt.RunErr != nil {
		result.Diagnostics = append(
			result.Diagnostics,
			"profile diagnostic: "+profileAttempt.RunErr.Error(),
		)
	}
	if result.PrimaryMode == WorkPatternInfluenceProfile &&
		neutralAttempt.RunErr != nil {
		result.Diagnostics = append(
			result.Diagnostics,
			"neutral diagnostic: "+neutralAttempt.RunErr.Error(),
		)
	}
	result.AttemptIDs = uniqueSortedStrings(result.AttemptIDs)
	return result
}
