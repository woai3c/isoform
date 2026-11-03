package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	workPatternProfileAblationPolicyVersion = "work-pattern-ablation-v3"

	workPatternProfileAblationObservedReferenceVersion = "work-pattern-observed-reference-v1"
	workPatternProfileAblationEvidenceActionResult     = "action_and_result"
	workPatternProfileAblationEvidenceOnsetResult      = "onset_and_result"
	workPatternProfileAblationEvidenceResultOnly       = "result_only"
	workPatternProfileAblationEvidenceInsufficient     = "insufficient"
)

type WorkPatternProfileAblationRun struct {
	ID                string            `json:"id"`
	IdempotencyKey    string            `json:"idempotencyKey"`
	SourceKind        string            `json:"sourceKind"`
	SourceID          string            `json:"sourceId"`
	WindowStart       int64             `json:"windowStart"`
	CutoffAt          int64             `json:"cutoffAt"`
	ProfileSnapshotID string            `json:"profileSnapshotId"`
	ProfileSHA256     string            `json:"profileSha256"`
	PrimaryMode       string            `json:"primaryMode"`
	NeutralAttemptID  string            `json:"neutralAttemptId,omitempty"`
	ProfileAttemptID  string            `json:"profileAttemptId,omitempty"`
	NeutralStatus     string            `json:"neutralStatus"`
	ProfileStatus     string            `json:"profileStatus"`
	NeutralResponse   TaskOnsetResponse `json:"neutralResponse"`
	ProfileResponse   TaskOnsetResponse `json:"profileResponse"`
	Model             string            `json:"model"`
	PromptVersion     string            `json:"promptVersion"`
	StartedAt         int64             `json:"startedAt"`
	CompletedAt       int64             `json:"completedAt"`
	CreatedAt         int64             `json:"createdAt"`
}

type RecordWorkPatternProfileAblationRunInput struct {
	IdempotencyKey    string
	SourceKind        string
	SourceID          string
	WindowStart       int64
	CutoffAt          int64
	ProfileSnapshotID string
	ProfileSHA256     string
	PrimaryMode       string
	NeutralAttemptID  string
	ProfileAttemptID  string
	NeutralStatus     string
	ProfileStatus     string
	NeutralResponse   TaskOnsetResponse
	ProfileResponse   TaskOnsetResponse
	Model             string
	PromptVersion     string
	StartedAt         int64
	CompletedAt       int64
	CreatedAt         int64
}

type WorkPatternProfileAblationEvaluation struct {
	ID                     string                            `json:"id"`
	AblationRunID          string                            `json:"ablationRunId"`
	TaskEpisodeID          string                            `json:"taskEpisodeId"`
	ShadowEvaluationID     string                            `json:"shadowEvaluationId,omitempty"`
	PolicyVersion          string                            `json:"policyVersion"`
	NeutralScore           *float64                          `json:"neutralScore,omitempty"`
	ProfileScore           *float64                          `json:"profileScore,omitempty"`
	NeutralBoundaryErrorMS *int64                            `json:"neutralBoundaryErrorMs,omitempty"`
	ProfileBoundaryErrorMS *int64                            `json:"profileBoundaryErrorMs,omitempty"`
	Winner                 string                            `json:"winner"`
	EvidenceJSON           string                            `json:"evidenceJson"`
	Metrics                WorkPatternProfileAblationMetrics `json:"metrics"`
	CreatedAt              int64                             `json:"createdAt"`
}

type WorkPatternProfileAblationBranchMetrics struct {
	PredictionCount        int      `json:"predictionCount"`
	MatchedPredictionCount int      `json:"matchedPredictionCount"`
	OnsetPrecision         float64  `json:"onsetPrecision"`
	OnsetRecall            float64  `json:"onsetRecall"`
	TaskMatch              float64  `json:"taskMatch"`
	ActionMatch            *float64 `json:"actionMatch,omitempty"`
	ResultMatch            *float64 `json:"resultMatch,omitempty"`
	BoundaryErrorMS        *int64   `json:"boundaryErrorMs,omitempty"`
	SplitErrorCount        int      `json:"splitErrorCount"`
	MergeErrorCount        int      `json:"mergeErrorCount"`
	CalibrationError       *float64 `json:"calibrationError,omitempty"`
	CompositeScore         float64  `json:"compositeScore"`
}

type WorkPatternProfileAblationMetrics struct {
	Version                  string                                  `json:"version"`
	ObservedReferenceVersion string                                  `json:"observedReferenceVersion,omitempty"`
	ObservedEvidenceStrength string                                  `json:"observedEvidenceStrength,omitempty"`
	ObservedEvidenceSHA256   string                                  `json:"observedEvidenceSha256,omitempty"`
	ObservedEvidence         []WorkPatternProfileAblationEvidence    `json:"observedEvidence,omitempty"`
	ObservedTaskHash         string                                  `json:"observedTaskHash"`
	ObservedResultHash       string                                  `json:"observedResultHash"`
	Neutral                  WorkPatternProfileAblationBranchMetrics `json:"neutral"`
	Profile                  WorkPatternProfileAblationBranchMetrics `json:"profile"`
	MergeScope               string                                  `json:"mergeScope"`
}

type WorkPatternProfileAblationEvidence struct {
	ID               string `json:"id"`
	Role             string `json:"role"`
	SourceKind       string `json:"sourceKind"`
	ObservedAt       int64  `json:"observedAt"`
	ContentHash      string `json:"contentHash"`
	SemanticTextHash string `json:"semanticTextHash"`
}

type workPatternProfileAblationObservedReference struct {
	Version               string
	EvidenceStrength      string
	EvidenceSHA256        string
	Evidence              []WorkPatternProfileAblationEvidence
	BehaviorText          string
	BehaviorSHA256        string
	ResultText            string
	ResultSHA256          string
	StartedAt             int64
	CompletedAt           int64
	PredictionCompletedAt int64
}

func workPatternIndependentObservedSource(sourceKind string) bool {
	switch strings.TrimSpace(sourceKind) {
	case "activity_event", "terminal_event", "accessibility_event", "browser_event":
		return true
	default:
		return false
	}
}

func workPatternAblationKey(
	input TaskOnsetInput,
	profile WorkPatternProfileContext,
	model string,
	primaryMode string,
) string {
	return strings.Join([]string{
		workPatternProfileAblationPolicyVersion,
		input.SourceKind,
		input.SourceID,
		fmt.Sprint(input.WindowStart),
		fmt.Sprint(input.CutoffAt),
		profile.SnapshotID,
		profile.SnapshotSHA256,
		taskOnsetPromptVersion,
		strings.TrimSpace(model),
		normalizeWorkPatternInfluenceMode(primaryMode),
	}, "\x00")
}

func canonicalWorkPatternOnsetResponse(
	value TaskOnsetResponse,
) (string, error) {
	if value.Episodes == nil {
		value.Episodes = []TaskOnsetPrediction{}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func scanWorkPatternProfileAblationRun(
	scanner interface{ Scan(dest ...any) error },
) (WorkPatternProfileAblationRun, error) {
	var (
		result                   WorkPatternProfileAblationRun
		neutralJSON, profileJSON string
	)
	if err := scanner.Scan(
		&result.ID,
		&result.IdempotencyKey,
		&result.SourceKind,
		&result.SourceID,
		&result.WindowStart,
		&result.CutoffAt,
		&result.ProfileSnapshotID,
		&result.ProfileSHA256,
		&result.PrimaryMode,
		&result.NeutralAttemptID,
		&result.ProfileAttemptID,
		&result.NeutralStatus,
		&result.ProfileStatus,
		&neutralJSON,
		&profileJSON,
		&result.Model,
		&result.PromptVersion,
		&result.StartedAt,
		&result.CompletedAt,
		&result.CreatedAt,
	); err != nil {
		return WorkPatternProfileAblationRun{}, err
	}
	if err := json.Unmarshal(
		[]byte(neutralJSON),
		&result.NeutralResponse,
	); err != nil {
		return WorkPatternProfileAblationRun{}, err
	}
	if err := json.Unmarshal(
		[]byte(profileJSON),
		&result.ProfileResponse,
	); err != nil {
		return WorkPatternProfileAblationRun{}, err
	}
	return result, nil
}

const workPatternAblationRunSelectColumns = `
	id, idempotency_key, source_kind, source_id, window_start, cutoff_at,
	profile_snapshot_id, profile_sha256, primary_mode, neutral_attempt_id,
	profile_attempt_id, neutral_status, profile_status, neutral_response_json,
	profile_response_json, model, prompt_version, started_at, completed_at,
	created_at`

func (s *ActivityStore) GetWorkPatternProfileAblationRunByKey(
	idempotencyKey string,
) (WorkPatternProfileAblationRun, error) {
	return scanWorkPatternProfileAblationRun(s.db.QueryRow(`
		SELECT `+workPatternAblationRunSelectColumns+`
		FROM work_pattern_profile_ablation_runs_v1
		WHERE idempotency_key = ?`,
		strings.TrimSpace(idempotencyKey),
	))
}

func (s *ActivityStore) GetWorkPatternProfileAblationRun(
	id string,
) (WorkPatternProfileAblationRun, error) {
	return scanWorkPatternProfileAblationRun(s.db.QueryRow(`
		SELECT `+workPatternAblationRunSelectColumns+`
		FROM work_pattern_profile_ablation_runs_v1
		WHERE id = ?`,
		strings.TrimSpace(id),
	))
}

func workPatternProfileAblationAttemptInput(
	store *ActivityStore,
	attemptID string,
	run WorkPatternProfileAblationRun,
) (TaskOnsetInput, error) {
	attempt, err := store.GetTaskAgentAttempt(strings.TrimSpace(attemptID))
	if err != nil {
		return TaskOnsetInput{}, err
	}
	if attempt.Phase != TaskAgentAttemptPhaseOnset ||
		attempt.Status != TaskAgentAttemptStatusSucceeded ||
		attempt.PromptVersion != taskOnsetPromptVersion ||
		attempt.ModelName != run.Model ||
		attempt.PromptHash != taskAgentAttemptHash([]byte(attempt.PromptText)) ||
		attempt.InputHash !=
			taskAgentAttemptHash([]byte(attempt.NormalizedInputJSON)) {
		return TaskOnsetInput{}, errors.New(
			"work pattern ablation attempt provenance is invalid",
		)
	}
	canonical, err := canonicalTaskAgentAttemptJSON(
		attempt.NormalizedInputJSON,
		"work pattern ablation attempt input",
		true,
	)
	if err != nil || canonical != attempt.NormalizedInputJSON {
		return TaskOnsetInput{}, errors.New(
			"work pattern ablation attempt input is not canonical",
		)
	}
	var input TaskOnsetInput
	if err := json.Unmarshal([]byte(attempt.NormalizedInputJSON), &input); err != nil {
		return TaskOnsetInput{}, fmt.Errorf(
			"decode work pattern ablation attempt input: %w",
			err,
		)
	}
	if input.SourceKind != run.SourceKind || input.SourceID != run.SourceID ||
		input.WindowStart != run.WindowStart || input.CutoffAt != run.CutoffAt {
		return TaskOnsetInput{}, errors.New(
			"work pattern ablation attempt does not match its source window",
		)
	}
	return input, nil
}

func currentWorkPatternProfileAblationRun(
	store *ActivityStore,
	run WorkPatternProfileAblationRun,
) (bool, error) {
	if store == nil || store.db == nil {
		return false, errors.New("activity store is unavailable")
	}
	if run.ProfileStatus != TaskAgentAttemptStatusSucceeded ||
		run.NeutralStatus != TaskAgentAttemptStatusSucceeded ||
		run.ProfileAttemptID == "" || run.NeutralAttemptID == "" ||
		run.PromptVersion != taskOnsetPromptVersion {
		return false, nil
	}
	profileInput, err := workPatternProfileAblationAttemptInput(
		store,
		run.ProfileAttemptID,
		run,
	)
	if err != nil {
		return false, err
	}
	neutralInput, err := workPatternProfileAblationAttemptInput(
		store,
		run.NeutralAttemptID,
		run,
	)
	if err != nil {
		return false, err
	}
	if profileInput.WorkProfile == nil || neutralInput.WorkProfile != nil ||
		profileInput.WorkProfile.SnapshotID != run.ProfileSnapshotID ||
		profileInput.WorkProfile.SnapshotSHA256 != run.ProfileSHA256 {
		return false, errors.New(
			"work pattern ablation pair has invalid profile assignment",
		)
	}
	profile := *profileInput.WorkProfile
	profileInput.WorkProfile = nil
	profileBaseJSON, err := json.Marshal(profileInput)
	if err != nil {
		return false, err
	}
	neutralJSON, err := json.Marshal(neutralInput)
	if err != nil {
		return false, err
	}
	if string(profileBaseJSON) != string(neutralJSON) {
		return false, errors.New(
			"work pattern ablation branches used different production evidence",
		)
	}
	expectedKey := "work-pattern-ablation:" + taskAgentAttemptHash(
		[]byte(workPatternAblationKey(
			profileInput,
			profile,
			run.Model,
			run.PrimaryMode,
		)),
	)
	if run.IdempotencyKey != expectedKey {
		return false, nil
	}
	if run.ID != deterministicTaskEpisodeID(
		"work-pattern-profile-ablation",
		expectedKey,
	) {
		return false, errors.New(
			"current work pattern ablation run identity is invalid",
		)
	}
	return true, nil
}

func (s *ActivityStore) RecordWorkPatternProfileAblationRun(
	input RecordWorkPatternProfileAblationRunInput,
) (WorkPatternProfileAblationRun, bool, error) {
	if s == nil || s.db == nil {
		return WorkPatternProfileAblationRun{}, false, errors.New(
			"activity store is unavailable",
		)
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.ProfileSnapshotID = strings.TrimSpace(input.ProfileSnapshotID)
	input.ProfileSHA256 = strings.ToLower(strings.TrimSpace(input.ProfileSHA256))
	input.PrimaryMode = normalizeWorkPatternInfluenceMode(input.PrimaryMode)
	input.NeutralAttemptID = strings.TrimSpace(input.NeutralAttemptID)
	input.ProfileAttemptID = strings.TrimSpace(input.ProfileAttemptID)
	input.NeutralStatus = strings.TrimSpace(input.NeutralStatus)
	input.ProfileStatus = strings.TrimSpace(input.ProfileStatus)
	input.Model = strings.TrimSpace(input.Model)
	input.PromptVersion = strings.TrimSpace(input.PromptVersion)
	if input.IdempotencyKey == "" || input.SourceKind == "" ||
		input.SourceID == "" || input.WindowStart <= 0 ||
		input.CutoffAt < input.WindowStart ||
		input.ProfileSnapshotID == "" || len(input.ProfileSHA256) != 64 ||
		input.NeutralStatus == "" || input.ProfileStatus == "" ||
		input.Model == "" || input.PromptVersion == "" ||
		input.StartedAt <= 0 || input.CompletedAt < input.StartedAt {
		return WorkPatternProfileAblationRun{}, false, errors.New(
			"work pattern ablation run is incomplete",
		)
	}
	if input.CreatedAt <= 0 {
		input.CreatedAt = input.CompletedAt
	}
	snapshot, err := s.GetWorkPatternProfileSnapshot(input.ProfileSnapshotID)
	if err != nil {
		return WorkPatternProfileAblationRun{}, false, err
	}
	if snapshot.SHA256 != input.ProfileSHA256 ||
		snapshot.SourceCutoffAt >= input.WindowStart {
		return WorkPatternProfileAblationRun{}, false, errors.New(
			"work pattern ablation profile is not a valid pre-window snapshot",
		)
	}
	neutralJSON, err := canonicalWorkPatternOnsetResponse(input.NeutralResponse)
	if err != nil {
		return WorkPatternProfileAblationRun{}, false, err
	}
	profileJSON, err := canonicalWorkPatternOnsetResponse(input.ProfileResponse)
	if err != nil {
		return WorkPatternProfileAblationRun{}, false, err
	}
	result := WorkPatternProfileAblationRun{
		ID: deterministicTaskEpisodeID(
			"work-pattern-profile-ablation",
			input.IdempotencyKey,
		),
		IdempotencyKey:    input.IdempotencyKey,
		SourceKind:        input.SourceKind,
		SourceID:          input.SourceID,
		WindowStart:       input.WindowStart,
		CutoffAt:          input.CutoffAt,
		ProfileSnapshotID: input.ProfileSnapshotID,
		ProfileSHA256:     input.ProfileSHA256,
		PrimaryMode:       input.PrimaryMode,
		NeutralAttemptID:  input.NeutralAttemptID,
		ProfileAttemptID:  input.ProfileAttemptID,
		NeutralStatus:     input.NeutralStatus,
		ProfileStatus:     input.ProfileStatus,
		NeutralResponse:   input.NeutralResponse,
		ProfileResponse:   input.ProfileResponse,
		Model:             input.Model,
		PromptVersion:     input.PromptVersion,
		StartedAt:         input.StartedAt,
		CompletedAt:       input.CompletedAt,
		CreatedAt:         input.CreatedAt,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return WorkPatternProfileAblationRun{}, false, err
	}
	defer tx.Rollback()
	existing, err := scanWorkPatternProfileAblationRun(tx.QueryRow(`
		SELECT `+workPatternAblationRunSelectColumns+`
		FROM work_pattern_profile_ablation_runs_v1
		WHERE idempotency_key = ?`,
		input.IdempotencyKey,
	))
	if err == nil {
		existingNeutral, _ := canonicalWorkPatternOnsetResponse(
			existing.NeutralResponse,
		)
		existingProfile, _ := canonicalWorkPatternOnsetResponse(
			existing.ProfileResponse,
		)
		if existing.ProfileSnapshotID != result.ProfileSnapshotID ||
			existing.ProfileSHA256 != result.ProfileSHA256 ||
			existing.PrimaryMode != result.PrimaryMode ||
			existingNeutral != neutralJSON ||
			existingProfile != profileJSON {
			return WorkPatternProfileAblationRun{}, false, errors.New(
				"work pattern ablation idempotency key has a different payload",
			)
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkPatternProfileAblationRun{}, false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO work_pattern_profile_ablation_runs_v1 (
			id, idempotency_key, source_kind, source_id, window_start,
			cutoff_at, profile_snapshot_id, profile_sha256, primary_mode,
			neutral_attempt_id, profile_attempt_id, neutral_status,
			profile_status, neutral_response_json, profile_response_json,
			model, prompt_version, started_at, completed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		result.ID,
		result.IdempotencyKey,
		result.SourceKind,
		result.SourceID,
		result.WindowStart,
		result.CutoffAt,
		result.ProfileSnapshotID,
		result.ProfileSHA256,
		result.PrimaryMode,
		result.NeutralAttemptID,
		result.ProfileAttemptID,
		result.NeutralStatus,
		result.ProfileStatus,
		neutralJSON,
		profileJSON,
		result.Model,
		result.PromptVersion,
		result.StartedAt,
		result.CompletedAt,
		result.CreatedAt,
	); err != nil {
		return WorkPatternProfileAblationRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return WorkPatternProfileAblationRun{}, false, err
	}
	return result, true, nil
}

func (s *ActivityStore) ListWorkPatternProfileAblationRuns(
	limit int,
) ([]WorkPatternProfileAblationRun, error) {
	if limit <= 0 {
		limit = workPatternProfileDefaultList
	}
	if limit > workPatternProfileMaximumList {
		limit = workPatternProfileMaximumList
	}
	rows, err := s.db.Query(`
		SELECT `+workPatternAblationRunSelectColumns+`
		FROM work_pattern_profile_ablation_runs_v1
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkPatternProfileAblationRun, 0, limit)
	for rows.Next() {
		item, err := scanWorkPatternProfileAblationRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func workPatternTextSimilarity(left, right string) float64 {
	leftWords := selfModelWords(left)
	rightWords := selfModelWords(right)
	if len(leftWords) == 0 || len(rightWords) == 0 {
		return 0
	}
	intersection := 0
	union := make(map[string]struct{}, len(leftWords)+len(rightWords))
	for word := range leftWords {
		union[word] = struct{}{}
	}
	for word := range rightWords {
		union[word] = struct{}{}
		if _, ok := leftWords[word]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(max(len(union), 1))
}

func workPatternPredictionMetrics(
	response TaskOnsetResponse,
	observed ShadowTaskEvaluationWork,
) (WorkPatternProfileAblationBranchMetrics, bool) {
	result := WorkPatternProfileAblationBranchMetrics{
		PredictionCount: len(response.Episodes),
	}
	observedText := strings.Join([]string{
		observed.Task,
		observed.DesiredResult,
	}, " ")
	if len(selfModelWords(observedText)) == 0 {
		return result, false
	}
	bestIndex := -1
	bestTaskScore := float64(-1)
	const onsetMatchThreshold = 0.20
	for index, episode := range response.Episodes {
		taskScore := workPatternTextSimilarity(
			episode.Task+" "+episode.DesiredResult,
			observedText,
		)
		if taskScore >= onsetMatchThreshold {
			result.MatchedPredictionCount++
		}
		if taskScore > bestTaskScore {
			bestTaskScore = taskScore
			bestIndex = index
		}
	}
	if result.PredictionCount > 0 {
		result.OnsetPrecision = float64(result.MatchedPredictionCount) /
			float64(result.PredictionCount)
	}
	if result.MatchedPredictionCount > 0 {
		result.OnsetRecall = 1
		result.SplitErrorCount = max(
			result.MatchedPredictionCount-1,
			0,
		)
	}
	result.TaskMatch = math.Max(bestTaskScore, 0)
	if bestIndex >= 0 {
		best := response.Episodes[bestIndex]
		if observed.StartedAt > 0 && best.InferredStartedAt > 0 {
			boundary := best.InferredStartedAt - observed.StartedAt
			if boundary < 0 {
				boundary = -boundary
			}
			result.BoundaryErrorMS = &boundary
		}
		if actions := strings.Join(observed.Actions, " "); actions != "" {
			score := workPatternTextSimilarity(best.Task, actions)
			result.ActionMatch = &score
		}
		if observedResult := strings.TrimSpace(strings.Join([]string{
			observed.DesiredResult,
			observed.Result,
		}, " ")); observedResult != "" {
			score := workPatternTextSimilarity(
				best.DesiredResult,
				observedResult,
			)
			result.ResultMatch = &score
		}
		calibration := math.Abs(
			workPatternClamp(best.TaskConfidence) - result.TaskMatch,
		)
		result.CalibrationError = &calibration
	}
	weightedTotal := result.TaskMatch * 0.40
	weight := 0.40
	weightedTotal += result.OnsetPrecision*0.15 + result.OnsetRecall*0.15
	weight += 0.30
	if result.ActionMatch != nil {
		weightedTotal += *result.ActionMatch * 0.10
		weight += 0.10
	}
	if result.ResultMatch != nil {
		weightedTotal += *result.ResultMatch * 0.15
		weight += 0.15
	}
	if result.BoundaryErrorMS != nil {
		minuteMS := float64(time.Minute.Milliseconds())
		boundaryScore := 1 /
			(1 + float64(*result.BoundaryErrorMS)/minuteMS)
		weightedTotal += boundaryScore * 0.05
		weight += 0.05
	}
	if weight > 0 {
		result.CompositeScore = workPatternClamp(weightedTotal / weight)
	}
	return result, true
}

func buildWorkPatternProfileAblationObservedReference(
	store *ActivityStore,
	episode TaskEpisode,
	evaluation ShadowRunEvaluation,
	ablationRun WorkPatternProfileAblationRun,
) (workPatternProfileAblationObservedReference, error) {
	result := workPatternProfileAblationObservedReference{
		Version:          workPatternProfileAblationObservedReferenceVersion,
		EvidenceStrength: workPatternProfileAblationEvidenceInsufficient,
		Evidence:         []WorkPatternProfileAblationEvidence{},
	}
	if store == nil || store.db == nil {
		return result, errors.New("activity store is unavailable")
	}
	if ablationRun.ID == "" || ablationRun.CompletedAt <= 0 ||
		ablationRun.ProfileSnapshotID !=
			episode.FrozenContext.WorkProfileSnapshotID ||
		ablationRun.WindowStart > episode.InferredStartAt ||
		ablationRun.CutoffAt < episode.InferredStartAt {
		return result, errors.New(
			"ablation observed reference requires the exact paired prediction run",
		)
	}
	result.PredictionCompletedAt = ablationRun.CompletedAt
	input := evaluation.TaskEvaluationInput
	if input == nil || strings.TrimSpace(input.ShadowRunID) == "" ||
		input.TaskEpisodeID != episode.ID {
		return result, errors.New(
			"ablation observed reference requires a linked Shadow evaluation",
		)
	}
	run, err := store.GetShadowRun(input.ShadowRunID)
	if err != nil {
		return result, err
	}
	if run.TaskEpisodeID != episode.ID {
		return result, errors.New(
			"ablation Shadow run does not match the observed Task Episode",
		)
	}
	if evaluation.CreatedAt <= 0 {
		return result, errors.New(
			"ablation observed reference requires a stable evaluation cutoff",
		)
	}
	detail, err := getShadowLearningInfluenceEpisodeDetailAt(
		store,
		episode.ID,
		evaluation.CreatedAt,
	)
	if err != nil {
		return result, err
	}
	selection := selectTaskEpisodeMaterializedResult(
		detail.Evidence,
		run.CapabilityKey,
	)
	if selection.invalidReason != "" {
		return result, fmt.Errorf(
			"ablation observed result is invalid: %s",
			selection.invalidReason,
		)
	}
	if selection.result == nil || len(selection.evidenceIDs) != 1 {
		return result, errors.New(
			"ablation observed reference has no unique materialized user result",
		)
	}
	selectedResultEvidence := make(map[string]TaskEpisodeEvidence)
	for _, evidence := range detail.Evidence {
		for _, selectedID := range selection.evidenceIDs {
			if evidence.ID == selectedID {
				selectedResultEvidence[selectedID] = evidence
			}
		}
	}
	selectedEvidence, ok := selectedResultEvidence[selection.evidenceIDs[0]]
	if !ok || !workPatternIndependentObservedSource(selectedEvidence.SourceKind) ||
		selectedEvidence.ObservedAt <= ablationRun.CompletedAt {
		return result, errors.New(
			"ablation observed result does not postdate the paired prediction",
		)
	}
	result.ResultText = strings.TrimSpace(selection.result.Content)
	if result.ResultText == "" ||
		result.ResultText != strings.TrimSpace(input.Observed.Result) {
		return result, errors.New(
			"ablation observed result does not match the immutable comparison",
		)
	}
	result.ResultSHA256 = workPatternHash([]byte(result.ResultText))
	selectedResults := make(map[string]struct{}, len(selection.evidenceIDs))
	for _, id := range selection.evidenceIDs {
		selectedResults[id] = struct{}{}
	}

	onset := make([]string, 0)
	actions := make([]string, 0)
	actionStartedAt := int64(0)
	onsetStartedAt := int64(0)
	for _, evidence := range detail.Evidence {
		summary := strings.TrimSpace(evidence.Summary)
		include := false
		switch evidence.Role {
		case TaskEpisodeEvidenceOnset:
			if summary != "" &&
				workPatternIndependentObservedSource(evidence.SourceKind) &&
				evidence.ObservedAt >= ablationRun.WindowStart &&
				evidence.ObservedAt <= ablationRun.CutoffAt {
				onset = append(onset, summary)
				if onsetStartedAt == 0 || evidence.ObservedAt < onsetStartedAt {
					onsetStartedAt = evidence.ObservedAt
				}
				include = true
			}
		case TaskEpisodeEvidenceAction:
			if summary != "" &&
				workPatternIndependentObservedSource(evidence.SourceKind) &&
				evidence.ObservedAt > ablationRun.CompletedAt &&
				evidence.ObservedAt <= selectedEvidence.ObservedAt {
				actions = append(actions, summary)
				if actionStartedAt == 0 || evidence.ObservedAt < actionStartedAt {
					actionStartedAt = evidence.ObservedAt
				}
				include = true
			}
		case TaskEpisodeEvidenceResult:
			if _, ok := selectedResults[evidence.ID]; ok {
				summary = result.ResultText
				result.CompletedAt = evidence.ObservedAt
				include = true
			}
		}
		if include {
			result.Evidence = append(
				result.Evidence,
				WorkPatternProfileAblationEvidence{
					ID:               evidence.ID,
					Role:             evidence.Role,
					SourceKind:       evidence.SourceKind,
					ObservedAt:       evidence.ObservedAt,
					ContentHash:      evidence.ContentHash,
					SemanticTextHash: workPatternHash([]byte(summary)),
				},
			)
		}
	}
	sort.SliceStable(result.Evidence, func(left, right int) bool {
		if result.Evidence[left].ObservedAt != result.Evidence[right].ObservedAt {
			return result.Evidence[left].ObservedAt <
				result.Evidence[right].ObservedAt
		}
		return result.Evidence[left].ID < result.Evidence[right].ID
	})
	if len(actions) > 0 {
		result.BehaviorText = joinShadowTaskEvaluationLines(actions)
		result.StartedAt = actionStartedAt
		result.EvidenceStrength =
			workPatternProfileAblationEvidenceActionResult
	} else if len(onset) > 0 {
		result.BehaviorText = joinShadowTaskEvaluationLines(onset)
		result.StartedAt = onsetStartedAt
		result.EvidenceStrength =
			workPatternProfileAblationEvidenceOnsetResult
	} else {
		result.EvidenceStrength =
			workPatternProfileAblationEvidenceResultOnly
	}
	result.BehaviorSHA256 = workPatternHash([]byte(result.BehaviorText))
	evidenceJSON, err := json.Marshal(struct {
		Version               string                               `json:"version"`
		EvidenceStrength      string                               `json:"evidenceStrength"`
		Evidence              []WorkPatternProfileAblationEvidence `json:"evidence"`
		BehaviorSHA256        string                               `json:"behaviorSha256"`
		ResultSHA256          string                               `json:"resultSha256"`
		StartedAt             int64                                `json:"startedAt"`
		CompletedAt           int64                                `json:"completedAt"`
		PredictionCompletedAt int64                                `json:"predictionCompletedAt"`
	}{
		Version:               result.Version,
		EvidenceStrength:      result.EvidenceStrength,
		Evidence:              result.Evidence,
		BehaviorSHA256:        result.BehaviorSHA256,
		ResultSHA256:          result.ResultSHA256,
		StartedAt:             result.StartedAt,
		CompletedAt:           result.CompletedAt,
		PredictionCompletedAt: result.PredictionCompletedAt,
	})
	if err != nil {
		return result, err
	}
	result.EvidenceSHA256 = workPatternHash(evidenceJSON)
	return result, nil
}

func workPatternEvidencePredictionMetrics(
	response TaskOnsetResponse,
	observed workPatternProfileAblationObservedReference,
) (WorkPatternProfileAblationBranchMetrics, bool) {
	result := WorkPatternProfileAblationBranchMetrics{
		PredictionCount: len(response.Episodes),
	}
	if observed.EvidenceStrength !=
		workPatternProfileAblationEvidenceActionResult &&
		observed.EvidenceStrength !=
			workPatternProfileAblationEvidenceOnsetResult {
		return result, false
	}
	behavior := strings.TrimSpace(observed.BehaviorText)
	userResult := strings.TrimSpace(observed.ResultText)
	if behavior == "" || userResult == "" {
		return result, false
	}
	if len(response.Episodes) == 0 {
		return result, true
	}
	bestIndex := -1
	bestComposite := float64(-1)
	bestSemantic := float64(0)
	bestAction := float64(0)
	bestResult := float64(0)
	bestBoundaryScore := float64(0)
	var bestBoundary *int64
	const evidenceMatchThreshold = 0.20
	for index, episode := range response.Episodes {
		actionMatch := workPatternTextSimilarity(episode.Task, behavior)
		resultMatch := workPatternTextSimilarity(
			episode.DesiredResult,
			userResult,
		)
		semantic := actionMatch*0.50 + resultMatch*0.50
		if semantic >= evidenceMatchThreshold {
			result.MatchedPredictionCount++
		}
		boundaryScore := float64(0)
		var boundary *int64
		if observed.StartedAt > 0 && episode.InferredStartedAt > 0 {
			value := episode.InferredStartedAt - observed.StartedAt
			if value < 0 {
				value = -value
			}
			boundary = &value
			boundaryScore = 1 /
				(1 + float64(value)/float64(time.Minute.Milliseconds()))
		}
		weighted := actionMatch*0.45 + resultMatch*0.45
		weight := 0.90
		if boundary != nil {
			weighted += boundaryScore * 0.10
			weight += 0.10
		}
		composite := workPatternClamp(weighted / weight)
		if composite > bestComposite {
			bestIndex = index
			bestComposite = composite
			bestSemantic = semantic
			bestAction = actionMatch
			bestResult = resultMatch
			bestBoundary = boundary
			bestBoundaryScore = boundaryScore
		}
	}
	if result.PredictionCount > 0 {
		if result.MatchedPredictionCount > 0 {
			// One observed task can match at most one prediction. Additional
			// predictions are false positives even when several are similar.
			result.OnsetPrecision = 1 / float64(result.PredictionCount)
		}
	}
	if result.MatchedPredictionCount > 0 {
		result.OnsetRecall = 1
		result.SplitErrorCount = max(result.MatchedPredictionCount-1, 0)
	}
	if bestIndex >= 0 {
		best := response.Episodes[bestIndex]
		result.TaskMatch = bestSemantic
		result.ActionMatch = &bestAction
		result.ResultMatch = &bestResult
		result.BoundaryErrorMS = bestBoundary
		calibration := math.Abs(
			workPatternClamp(best.TaskConfidence) - bestSemantic,
		)
		result.CalibrationError = &calibration
		weighted := bestAction*0.45 + bestResult*0.45
		weight := 0.90
		if bestBoundary != nil {
			weighted += bestBoundaryScore * 0.10
			weight += 0.10
		}
		baseScore := workPatternClamp(weighted / weight)
		result.CompositeScore = workPatternClamp(
			baseScore * result.OnsetPrecision,
		)
	}
	return result, true
}

func scanWorkPatternProfileAblationEvaluation(
	scanner interface{ Scan(dest ...any) error },
) (WorkPatternProfileAblationEvaluation, error) {
	var (
		result                           WorkPatternProfileAblationEvaluation
		neutralScore, profileScore       sql.NullFloat64
		neutralBoundary, profileBoundary sql.NullInt64
	)
	if err := scanner.Scan(
		&result.ID,
		&result.AblationRunID,
		&result.TaskEpisodeID,
		&result.ShadowEvaluationID,
		&result.PolicyVersion,
		&neutralScore,
		&profileScore,
		&neutralBoundary,
		&profileBoundary,
		&result.Winner,
		&result.EvidenceJSON,
		&result.CreatedAt,
	); err != nil {
		return WorkPatternProfileAblationEvaluation{}, err
	}
	if neutralScore.Valid {
		result.NeutralScore = &neutralScore.Float64
	}
	if profileScore.Valid {
		result.ProfileScore = &profileScore.Float64
	}
	if neutralBoundary.Valid {
		result.NeutralBoundaryErrorMS = &neutralBoundary.Int64
	}
	if profileBoundary.Valid {
		result.ProfileBoundaryErrorMS = &profileBoundary.Int64
	}
	if err := json.Unmarshal([]byte(result.EvidenceJSON), &result.Metrics); err != nil {
		return WorkPatternProfileAblationEvaluation{}, fmt.Errorf(
			"decode work pattern ablation metrics: %w",
			err,
		)
	}
	return result, nil
}

func workPatternAblationWinner(
	neutralScore, profileScore *float64,
	neutralBoundary, profileBoundary *int64,
) string {
	if neutralScore == nil || profileScore == nil {
		return "unknown"
	}
	const meaningfulDelta = 0.02
	if *profileScore > *neutralScore+meaningfulDelta {
		return "profile"
	}
	if *neutralScore > *profileScore+meaningfulDelta {
		return "neutral"
	}
	if neutralBoundary != nil && profileBoundary != nil {
		if *profileBoundary < *neutralBoundary {
			return "profile"
		}
		if *neutralBoundary < *profileBoundary {
			return "neutral"
		}
	}
	return "tie"
}

func workPatternProfileAblationMetricsForEvidence(
	run WorkPatternProfileAblationRun,
	observed workPatternProfileAblationObservedReference,
) (
	WorkPatternProfileAblationMetrics,
	*float64,
	*float64,
	*int64,
	*int64,
	string,
) {
	neutralMetrics, neutralKnown := workPatternEvidencePredictionMetrics(
		run.NeutralResponse,
		observed,
	)
	profileMetrics, profileKnown := workPatternEvidencePredictionMetrics(
		run.ProfileResponse,
		observed,
	)
	var neutralScore, profileScore *float64
	var neutralBoundary, profileBoundary *int64
	if neutralKnown {
		value := neutralMetrics.CompositeScore
		neutralScore = &value
		neutralBoundary = neutralMetrics.BoundaryErrorMS
	}
	if profileKnown {
		value := profileMetrics.CompositeScore
		profileScore = &value
		profileBoundary = profileMetrics.BoundaryErrorMS
	}
	winner := workPatternAblationWinner(
		neutralScore,
		profileScore,
		neutralBoundary,
		profileBoundary,
	)
	metrics := WorkPatternProfileAblationMetrics{
		Version:                  workPatternProfileAblationPolicyVersion,
		ObservedReferenceVersion: observed.Version,
		ObservedEvidenceStrength: observed.EvidenceStrength,
		ObservedEvidenceSHA256:   observed.EvidenceSHA256,
		ObservedEvidence: append(
			[]WorkPatternProfileAblationEvidence(nil),
			observed.Evidence...,
		),
		ObservedTaskHash:   observed.BehaviorSHA256,
		ObservedResultHash: observed.ResultSHA256,
		Neutral:            neutralMetrics,
		Profile:            profileMetrics,
		MergeScope:         "independent_observed_evidence",
	}
	return metrics, neutralScore, profileScore, neutralBoundary,
		profileBoundary, winner
}

func (s *ActivityStore) EvaluateWorkPatternProfileAblationForTask(
	episode TaskEpisode,
	evaluation ShadowRunEvaluation,
	createdAt int64,
) ([]WorkPatternProfileAblationEvaluation, error) {
	if episode.FrozenContext.WorkProfileSnapshotID == "" ||
		evaluation.TaskEvaluationInput == nil {
		return []WorkPatternProfileAblationEvaluation{}, nil
	}
	if err := s.taskEpisodeRepeatedOnsetError(episode.ID); err != nil {
		return nil, fmt.Errorf(
			"profile ablation excluded: %w",
			err,
		)
	}
	rows, err := s.db.Query(`
		SELECT `+workPatternAblationRunSelectColumns+`
		FROM work_pattern_profile_ablation_runs_v1
		WHERE profile_snapshot_id = ?
			AND window_start <= ?
			AND cutoff_at >= ?
		ORDER BY created_at ASC, rowid ASC`,
		episode.FrozenContext.WorkProfileSnapshotID,
		episode.InferredStartAt,
		episode.InferredStartAt,
	)
	if err != nil {
		return nil, err
	}
	runs := make([]WorkPatternProfileAblationRun, 0)
	for rows.Next() {
		run, err := scanWorkPatternProfileAblationRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	result := make([]WorkPatternProfileAblationEvaluation, 0, len(runs))
	for _, run := range runs {
		current, err := currentWorkPatternProfileAblationRun(s, run)
		if err != nil {
			return result, err
		}
		if !current {
			continue
		}
		observed, err := buildWorkPatternProfileAblationObservedReference(
			s,
			episode,
			evaluation,
			run,
		)
		if err != nil {
			return result, err
		}
		metrics, neutralScore, profileScore, neutralBoundary,
			profileBoundary, winner :=
			workPatternProfileAblationMetricsForEvidence(
				run,
				observed,
			)
		evidencePayload, err := json.Marshal(metrics)
		if err != nil {
			return result, err
		}
		idKey := strings.Join([]string{
			run.ID,
			episode.ID,
			evaluation.ID,
			workPatternProfileAblationPolicyVersion,
		}, "\x00")
		item := WorkPatternProfileAblationEvaluation{
			ID: deterministicTaskEpisodeID(
				"work-pattern-profile-ablation-evaluation",
				idKey,
			),
			AblationRunID:          run.ID,
			TaskEpisodeID:          episode.ID,
			ShadowEvaluationID:     evaluation.ID,
			PolicyVersion:          workPatternProfileAblationPolicyVersion,
			NeutralScore:           neutralScore,
			ProfileScore:           profileScore,
			NeutralBoundaryErrorMS: neutralBoundary,
			ProfileBoundaryErrorMS: profileBoundary,
			Winner:                 winner,
			EvidenceJSON:           string(evidencePayload),
			Metrics:                metrics,
			CreatedAt:              createdAt,
		}
		_, err = s.db.Exec(`
			INSERT OR IGNORE INTO work_pattern_profile_ablation_evaluations_v1 (
				id, ablation_run_id, task_episode_id, shadow_evaluation_id,
				policy_version, neutral_score, profile_score,
				neutral_boundary_error_ms, profile_boundary_error_ms,
				winner, evidence_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.AblationRunID,
			item.TaskEpisodeID,
			item.ShadowEvaluationID,
			item.PolicyVersion,
			item.NeutralScore,
			item.ProfileScore,
			item.NeutralBoundaryErrorMS,
			item.ProfileBoundaryErrorMS,
			item.Winner,
			item.EvidenceJSON,
			item.CreatedAt,
		)
		if err != nil {
			return result, err
		}
		persisted, err := scanWorkPatternProfileAblationEvaluation(
			s.db.QueryRow(`
				SELECT
					id, ablation_run_id, task_episode_id, shadow_evaluation_id,
					policy_version, neutral_score, profile_score,
					neutral_boundary_error_ms, profile_boundary_error_ms,
					winner, evidence_json, created_at
				FROM work_pattern_profile_ablation_evaluations_v1
				WHERE id = ?`,
				item.ID,
			),
		)
		if err != nil {
			return result, err
		}
		result = append(result, persisted)
	}
	return result, nil
}

func (s *ActivityStore) ListWorkPatternProfileAblationEvaluations(
	limit int,
) ([]WorkPatternProfileAblationEvaluation, error) {
	if limit <= 0 {
		limit = workPatternProfileDefaultList
	}
	if limit > workPatternProfileMaximumList {
		limit = workPatternProfileMaximumList
	}
	rows, err := s.db.Query(`
		SELECT
			id, ablation_run_id, task_episode_id, shadow_evaluation_id,
			policy_version, neutral_score, profile_score,
			neutral_boundary_error_ms, profile_boundary_error_ms,
			winner, evidence_json, created_at
		FROM work_pattern_profile_ablation_evaluations_v1
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkPatternProfileAblationEvaluation, 0, limit)
	for rows.Next() {
		item, err := scanWorkPatternProfileAblationEvaluation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].CreatedAt != result[j].CreatedAt {
			return result[i].CreatedAt > result[j].CreatedAt
		}
		return result[i].ID < result[j].ID
	})
	return result, rows.Err()
}

func workPatternAblationImprovement(
	evaluations []WorkPatternProfileAblationEvaluation,
) (float64, int) {
	total := 0.0
	count := 0
	for _, item := range evaluations {
		if item.NeutralScore == nil || item.ProfileScore == nil {
			continue
		}
		total += *item.ProfileScore - *item.NeutralScore
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return math.Round((total/float64(count))*10_000) / 10_000, count
}
