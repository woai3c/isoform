package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	TaskAgentAttemptPhaseOnset      = "onset"
	TaskAgentAttemptPhaseLifecycle  = "lifecycle"
	TaskAgentAttemptPhaseEvaluation = "evaluation"
	TaskAgentAttemptPhaseLearning   = "learning"

	TaskAgentAttemptStatusStarted     = "started"
	TaskAgentAttemptStatusSucceeded   = "succeeded"
	TaskAgentAttemptStatusFailed      = "failed"
	TaskAgentAttemptStatusParseFailed = "parse_failed"

	taskAgentAttemptDefaultListLimit   = 100
	taskAgentAttemptMaxListLimit       = 500
	taskAgentAttemptDefaultEventLimit  = 10
	taskAgentAttemptMaxEventLimit      = 100
	taskAgentAttemptMaxErrorBytes      = 16 * 1024
	taskAgentAttemptMaxIdentifierBytes = 512
	taskAgentAttemptErrorSuffix        = "\n... <task agent error truncated> ..."
)

// TaskAgentAttemptInput is a complete immutable model invocation. A caller may
// persist a terminal status directly, or persist started and append a terminal
// event with CompleteTaskAgentAttempt. Supplying IdempotencyKey makes the write
// deterministic and safe to retry.
type TaskAgentAttemptInput struct {
	ID                  string `json:"id,omitempty"`
	IdempotencyKey      string `json:"idempotencyKey,omitempty"`
	Phase               string `json:"phase"`
	TaskEpisodeID       string `json:"taskEpisodeId,omitempty"`
	ShadowRunID         string `json:"shadowRunId,omitempty"`
	ShadowEvaluationID  string `json:"shadowEvaluationId,omitempty"`
	ModelName           string `json:"modelName"`
	ModelVersion        string `json:"modelVersion"`
	PromptVersion       string `json:"promptVersion"`
	PromptText          string `json:"promptText"`
	PromptHash          string `json:"promptHash,omitempty"`
	NormalizedInputJSON string `json:"normalizedInputJson"`
	InputHash           string `json:"inputHash,omitempty"`
	Status              string `json:"status"`
	RawOutput           string `json:"rawOutput,omitempty"`
	ParsedOutputJSON    string `json:"parsedOutputJson,omitempty"`
	Error               string `json:"error,omitempty"`
	StartedAt           int64  `json:"startedAt"`
	CompletedAt         int64  `json:"completedAt,omitempty"`
	CreatedAt           int64  `json:"createdAt,omitempty"`
}

type CompleteTaskAgentAttemptInput struct {
	Status           string `json:"status"`
	RawOutput        string `json:"rawOutput,omitempty"`
	ParsedOutputJSON string `json:"parsedOutputJson,omitempty"`
	Error            string `json:"error,omitempty"`
	CompletedAt      int64  `json:"completedAt"`
	CreatedAt        int64  `json:"createdAt,omitempty"`
}

type TaskAgentAttemptEvent struct {
	ID               string `json:"id"`
	TaskAttemptID    string `json:"taskAttemptId"`
	Sequence         int    `json:"sequence"`
	Status           string `json:"status"`
	RawOutput        string `json:"rawOutput,omitempty"`
	ParsedOutputJSON string `json:"parsedOutputJson,omitempty"`
	Error            string `json:"error,omitempty"`
	PayloadHash      string `json:"payloadHash"`
	OccurredAt       int64  `json:"occurredAt"`
	CompletedAt      int64  `json:"completedAt,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
}

type TaskAgentAttempt struct {
	ID                  string `json:"id"`
	IdempotencyKey      string `json:"idempotencyKey"`
	WriteHash           string `json:"writeHash"`
	Phase               string `json:"phase"`
	TaskEpisodeID       string `json:"taskEpisodeId,omitempty"`
	ShadowRunID         string `json:"shadowRunId,omitempty"`
	ShadowEvaluationID  string `json:"shadowEvaluationId,omitempty"`
	ModelName           string `json:"modelName"`
	ModelVersion        string `json:"modelVersion"`
	PromptVersion       string `json:"promptVersion"`
	PromptText          string `json:"promptText"`
	PromptHash          string `json:"promptHash"`
	NormalizedInputJSON string `json:"normalizedInputJson"`
	InputHash           string `json:"inputHash"`
	Status              string `json:"status"`
	RawOutput           string `json:"rawOutput,omitempty"`
	ParsedOutputJSON    string `json:"parsedOutputJson,omitempty"`
	Error               string `json:"error,omitempty"`
	StartedAt           int64  `json:"startedAt"`
	CompletedAt         int64  `json:"completedAt,omitempty"`
	CreatedAt           int64  `json:"createdAt"`
	LatestEventID       string `json:"latestEventId"`
	LatestEventSequence int    `json:"latestEventSequence"`
}

type TaskAgentAttemptFilter struct {
	Phase              string `json:"phase,omitempty"`
	Status             string `json:"status,omitempty"`
	TaskEpisodeID      string `json:"taskEpisodeId,omitempty"`
	ShadowRunID        string `json:"shadowRunId,omitempty"`
	ShadowEvaluationID string `json:"shadowEvaluationId,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

type taskAgentAttemptRootPayload struct {
	ID                  string `json:"id"`
	IdempotencyKey      string `json:"idempotencyKey"`
	Phase               string `json:"phase"`
	TaskEpisodeID       string `json:"taskEpisodeId,omitempty"`
	ShadowRunID         string `json:"shadowRunId,omitempty"`
	ShadowEvaluationID  string `json:"shadowEvaluationId,omitempty"`
	ModelName           string `json:"modelName"`
	ModelVersion        string `json:"modelVersion"`
	PromptVersion       string `json:"promptVersion"`
	PromptText          string `json:"promptText"`
	PromptHash          string `json:"promptHash"`
	NormalizedInputJSON string `json:"normalizedInputJson"`
	InputHash           string `json:"inputHash"`
	StartedAt           int64  `json:"startedAt"`
	CreatedAt           int64  `json:"createdAt"`
	InitialEventHash    string `json:"initialEventHash"`
}

type taskAgentAttemptEventPayload struct {
	Status           string `json:"status"`
	RawOutput        string `json:"rawOutput,omitempty"`
	ParsedOutputJSON string `json:"parsedOutputJson,omitempty"`
	Error            string `json:"error,omitempty"`
	OccurredAt       int64  `json:"occurredAt"`
	CompletedAt      int64  `json:"completedAt,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
}

func normalizeTaskAgentAttemptListLimit(limit int) int {
	switch {
	case limit <= 0:
		return taskAgentAttemptDefaultListLimit
	case limit > taskAgentAttemptMaxListLimit:
		return taskAgentAttemptMaxListLimit
	default:
		return limit
	}
}

func normalizeTaskAgentAttemptEventLimit(limit int) int {
	switch {
	case limit <= 0:
		return taskAgentAttemptDefaultEventLimit
	case limit > taskAgentAttemptMaxEventLimit:
		return taskAgentAttemptMaxEventLimit
	default:
		return limit
	}
}

func taskAgentAttemptHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func deterministicTaskAgentAttemptID(kind, key string) string {
	return uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte("task-agent-attempt\x00"+kind+"\x00"+key),
	).String()
}

func canonicalTaskAgentAttemptJSON(raw, field string, required bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		if required {
			return "", fmt.Errorf("%s requires JSON", field)
		}
		return "", nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode %s: %w", field, err)
	}
	if value == nil && required {
		return "", fmt.Errorf("%s cannot be null", field)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", fmt.Errorf("%s contains multiple JSON values", field)
		}
		return "", fmt.Errorf("decode trailing %s: %w", field, err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical %s: %w", field, err)
	}
	return string(canonical), nil
}

func normalizeTaskAgentAttemptPhase(value string, allowEmpty bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" && allowEmpty {
		return "", nil
	}
	switch value {
	case TaskAgentAttemptPhaseOnset,
		TaskAgentAttemptPhaseLifecycle,
		TaskAgentAttemptPhaseEvaluation,
		TaskAgentAttemptPhaseLearning:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported task agent attempt phase %q", value)
	}
}

func normalizeTaskAgentAttemptStatus(value string, allowEmpty bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" && allowEmpty {
		return "", nil
	}
	switch value {
	case TaskAgentAttemptStatusStarted,
		TaskAgentAttemptStatusSucceeded,
		TaskAgentAttemptStatusFailed,
		TaskAgentAttemptStatusParseFailed:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported task agent attempt status %q", value)
	}
}

func validateTaskAgentAttemptIdentifier(value, field string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && required {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > taskAgentAttemptMaxIdentifierBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", field, taskAgentAttemptMaxIdentifierBytes)
	}
	return value, nil
}

func truncateTaskAgentAttemptError(value string) string {
	if len(value) <= taskAgentAttemptMaxErrorBytes {
		return value
	}
	budget := taskAgentAttemptMaxErrorBytes - len(taskAgentAttemptErrorSuffix)
	value = value[:budget]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + taskAgentAttemptErrorSuffix
}

func taskAgentAttemptEventFromPayload(
	attemptID string,
	sequence int,
	payload taskAgentAttemptEventPayload,
) (TaskAgentAttemptEvent, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return TaskAgentAttemptEvent{}, fmt.Errorf("encode task agent attempt event: %w", err)
	}
	hash := taskAgentAttemptHash(encoded)
	return TaskAgentAttemptEvent{
		ID:               deterministicTaskAgentAttemptID("event", attemptID+"\x00"+hash),
		TaskAttemptID:    attemptID,
		Sequence:         sequence,
		Status:           payload.Status,
		RawOutput:        payload.RawOutput,
		ParsedOutputJSON: payload.ParsedOutputJSON,
		Error:            payload.Error,
		PayloadHash:      hash,
		OccurredAt:       payload.OccurredAt,
		CompletedAt:      payload.CompletedAt,
		CreatedAt:        payload.CreatedAt,
	}, nil
}

func normalizeTaskAgentAttemptEvent(
	status, rawOutput, parsedOutputJSON, errorText string,
	startedAt, completedAt, createdAt int64,
) (taskAgentAttemptEventPayload, error) {
	var err error
	status, err = normalizeTaskAgentAttemptStatus(status, false)
	if err != nil {
		return taskAgentAttemptEventPayload{}, err
	}
	errorText = truncateTaskAgentAttemptError(errorText)
	switch status {
	case TaskAgentAttemptStatusStarted:
		if rawOutput != "" || strings.TrimSpace(parsedOutputJSON) != "" || errorText != "" {
			return taskAgentAttemptEventPayload{}, errors.New("started task agent attempt cannot contain output or error")
		}
		if completedAt != 0 {
			return taskAgentAttemptEventPayload{}, errors.New("started task agent attempt cannot have a completion timestamp")
		}
		if createdAt == 0 {
			createdAt = startedAt
		}
		if createdAt < startedAt {
			return taskAgentAttemptEventPayload{}, errors.New("task agent attempt event is created before it starts")
		}
		return taskAgentAttemptEventPayload{
			Status:     status,
			OccurredAt: startedAt,
			CreatedAt:  createdAt,
		}, nil
	case TaskAgentAttemptStatusSucceeded:
		parsedOutputJSON, err = canonicalTaskAgentAttemptJSON(
			parsedOutputJSON,
			"task agent parsed output",
			true,
		)
		if err != nil {
			return taskAgentAttemptEventPayload{}, err
		}
		if errorText != "" {
			return taskAgentAttemptEventPayload{}, errors.New("succeeded task agent attempt cannot contain an error")
		}
	case TaskAgentAttemptStatusFailed:
		if errorText == "" {
			return taskAgentAttemptEventPayload{}, errors.New("failed task agent attempt requires an error")
		}
		if strings.TrimSpace(parsedOutputJSON) != "" {
			return taskAgentAttemptEventPayload{}, errors.New("failed task agent attempt cannot contain parsed output")
		}
	case TaskAgentAttemptStatusParseFailed:
		if errorText == "" {
			return taskAgentAttemptEventPayload{}, errors.New("parse-failed task agent attempt requires an error")
		}
		if strings.TrimSpace(parsedOutputJSON) != "" {
			return taskAgentAttemptEventPayload{}, errors.New("parse-failed task agent attempt cannot contain parsed output")
		}
	}
	if completedAt <= 0 {
		return taskAgentAttemptEventPayload{}, errors.New("terminal task agent attempt requires a completion timestamp")
	}
	if completedAt < startedAt {
		return taskAgentAttemptEventPayload{}, errors.New("task agent attempt completes before it starts")
	}
	if createdAt == 0 {
		createdAt = completedAt
	}
	if createdAt < completedAt {
		return taskAgentAttemptEventPayload{}, errors.New("task agent attempt event is created before completion")
	}
	return taskAgentAttemptEventPayload{
		Status:           status,
		RawOutput:        rawOutput,
		ParsedOutputJSON: parsedOutputJSON,
		Error:            errorText,
		OccurredAt:       completedAt,
		CompletedAt:      completedAt,
		CreatedAt:        createdAt,
	}, nil
}

func normalizeTaskAgentAttemptInput(
	input TaskAgentAttemptInput,
) (TaskAgentAttemptInput, TaskAgentAttemptEvent, string, error) {
	var err error
	input.Phase, err = normalizeTaskAgentAttemptPhase(input.Phase, false)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	for value, target := range map[string]*string{
		"task episode ID":      &input.TaskEpisodeID,
		"shadow run ID":        &input.ShadowRunID,
		"shadow evaluation ID": &input.ShadowEvaluationID,
	} {
		*target, err = validateTaskAgentAttemptIdentifier(*target, value, false)
		if err != nil {
			return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
		}
	}
	if strings.TrimSpace(input.ModelName) == "" ||
		strings.TrimSpace(input.ModelVersion) == "" ||
		strings.TrimSpace(input.PromptVersion) == "" {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", errors.New(
			"task agent attempt requires exact model name, model version, and prompt version",
		)
	}
	if input.PromptText == "" {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", errors.New(
			"task agent attempt requires the exact prompt text",
		)
	}
	input.PromptHash = strings.ToLower(strings.TrimSpace(input.PromptHash))
	expectedPromptHash := taskAgentAttemptHash([]byte(input.PromptText))
	if input.PromptHash != "" && input.PromptHash != expectedPromptHash {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", errors.New(
			"task agent attempt prompt hash does not match",
		)
	}
	input.PromptHash = expectedPromptHash
	input.NormalizedInputJSON, err = canonicalTaskAgentAttemptJSON(
		input.NormalizedInputJSON,
		"task agent normalized input",
		true,
	)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	input.InputHash = strings.ToLower(strings.TrimSpace(input.InputHash))
	expectedInputHash := taskAgentAttemptHash([]byte(input.NormalizedInputJSON))
	if input.InputHash != "" && input.InputHash != expectedInputHash {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", errors.New(
			"task agent attempt input hash does not match",
		)
	}
	input.InputHash = expectedInputHash
	if input.StartedAt <= 0 {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", errors.New(
			"task agent attempt requires a stable start timestamp",
		)
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = input.StartedAt
	}
	if input.CreatedAt < input.StartedAt {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", errors.New(
			"task agent attempt is created before it starts",
		)
	}
	input.IdempotencyKey, err = validateTaskAgentAttemptIdentifier(
		input.IdempotencyKey,
		"task agent attempt idempotency key",
		false,
	)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	input.ID, err = validateTaskAgentAttemptIdentifier(
		input.ID,
		"task agent attempt ID",
		false,
	)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	if input.IdempotencyKey != "" {
		if input.ID == "" {
			input.ID = deterministicTaskAgentAttemptID("attempt", input.IdempotencyKey)
		}
	} else {
		if input.ID == "" {
			input.ID = uuid.NewString()
		}
		input.IdempotencyKey = "attempt:" + input.ID
	}

	normalizedStatus, err := normalizeTaskAgentAttemptStatus(input.Status, false)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	eventCreatedAt := input.CreatedAt
	if normalizedStatus != TaskAgentAttemptStatusStarted &&
		eventCreatedAt < input.CompletedAt {
		eventCreatedAt = input.CompletedAt
	}
	eventPayload, err := normalizeTaskAgentAttemptEvent(
		normalizedStatus,
		input.RawOutput,
		input.ParsedOutputJSON,
		input.Error,
		input.StartedAt,
		input.CompletedAt,
		eventCreatedAt,
	)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	input.Status = eventPayload.Status
	input.RawOutput = eventPayload.RawOutput
	input.ParsedOutputJSON = eventPayload.ParsedOutputJSON
	input.Error = eventPayload.Error
	input.CompletedAt = eventPayload.CompletedAt
	event, err := taskAgentAttemptEventFromPayload(input.ID, 1, eventPayload)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", err
	}
	rootPayload := taskAgentAttemptRootPayload{
		ID:                  input.ID,
		IdempotencyKey:      input.IdempotencyKey,
		Phase:               input.Phase,
		TaskEpisodeID:       input.TaskEpisodeID,
		ShadowRunID:         input.ShadowRunID,
		ShadowEvaluationID:  input.ShadowEvaluationID,
		ModelName:           input.ModelName,
		ModelVersion:        input.ModelVersion,
		PromptVersion:       input.PromptVersion,
		PromptText:          input.PromptText,
		PromptHash:          input.PromptHash,
		NormalizedInputJSON: input.NormalizedInputJSON,
		InputHash:           input.InputHash,
		StartedAt:           input.StartedAt,
		CreatedAt:           input.CreatedAt,
		InitialEventHash:    event.PayloadHash,
	}
	rootJSON, err := json.Marshal(rootPayload)
	if err != nil {
		return TaskAgentAttemptInput{}, TaskAgentAttemptEvent{}, "", fmt.Errorf(
			"encode task agent attempt payload: %w",
			err,
		)
	}
	return input, event, taskAgentAttemptHash(rootJSON), nil
}

func insertTaskAgentAttemptEvent(
	tx *sql.Tx,
	event TaskAgentAttemptEvent,
) error {
	if _, err := tx.Exec(`
		INSERT INTO task_agent_attempt_events (
			id, attempt_id, sequence, status, raw_output,
			parsed_output_json, error_text, payload_hash, occurred_at,
			completed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.TaskAttemptID,
		event.Sequence,
		event.Status,
		event.RawOutput,
		event.ParsedOutputJSON,
		event.Error,
		event.PayloadHash,
		event.OccurredAt,
		event.CompletedAt,
		event.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert task agent attempt event: %w", err)
	}
	return nil
}

// RecordTaskAgentAttempt atomically stores the immutable request and initial
// status event. Reusing an idempotency key succeeds only when the normalized
// immutable creation payload is identical. PromptHash and InputHash may be
// omitted; this method computes them, while supplied hashes are verified.
func (s *ActivityStore) RecordTaskAgentAttempt(
	input TaskAgentAttemptInput,
) (TaskAgentAttempt, bool, error) {
	if s == nil || s.db == nil {
		return TaskAgentAttempt{}, false, errors.New("activity store is unavailable")
	}
	normalized, event, writeHash, err := normalizeTaskAgentAttemptInput(input)
	if err != nil {
		return TaskAgentAttempt{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskAgentAttempt{}, false, fmt.Errorf("begin task agent attempt: %w", err)
	}
	defer tx.Rollback()

	var existingID, existingHash string
	err = tx.QueryRow(`
		SELECT id, write_hash
		FROM task_agent_attempts
		WHERE idempotency_key = ?`,
		normalized.IdempotencyKey,
	).Scan(&existingID, &existingHash)
	switch {
	case err == nil:
		if existingHash != writeHash {
			return TaskAgentAttempt{}, false, fmt.Errorf(
				"task agent attempt idempotency key %q has a different immutable payload",
				normalized.IdempotencyKey,
			)
		}
		if err := tx.Rollback(); err != nil {
			return TaskAgentAttempt{}, false, err
		}
		existing, getErr := s.GetTaskAgentAttempt(existingID)
		return existing, false, getErr
	case !errors.Is(err, sql.ErrNoRows):
		return TaskAgentAttempt{}, false, err
	}

	if _, err := tx.Exec(`
		INSERT INTO task_agent_attempts (
			id, idempotency_key, write_hash, phase, task_episode_id,
			shadow_run_id, shadow_evaluation_id, model_name, model_version,
			prompt_version, prompt_text, prompt_hash, normalized_input_json,
			input_hash, started_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		normalized.ID,
		normalized.IdempotencyKey,
		writeHash,
		normalized.Phase,
		normalized.TaskEpisodeID,
		normalized.ShadowRunID,
		normalized.ShadowEvaluationID,
		normalized.ModelName,
		normalized.ModelVersion,
		normalized.PromptVersion,
		normalized.PromptText,
		normalized.PromptHash,
		normalized.NormalizedInputJSON,
		normalized.InputHash,
		normalized.StartedAt,
		normalized.CreatedAt,
	); err != nil {
		insertErr := err
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return TaskAgentAttempt{}, false, fmt.Errorf(
				"insert task agent attempt: %v; rollback: %w",
				insertErr,
				rollbackErr,
			)
		}
		existing, found, getErr := s.GetTaskAgentAttemptByIdempotencyKey(
			normalized.IdempotencyKey,
		)
		switch {
		case getErr != nil:
			return TaskAgentAttempt{}, false, fmt.Errorf(
				"insert task agent attempt: %v; read concurrent attempt: %w",
				insertErr,
				getErr,
			)
		case found && existing.WriteHash == writeHash:
			return existing, false, nil
		case found:
			return TaskAgentAttempt{}, false, fmt.Errorf(
				"task agent attempt idempotency key %q has a different immutable payload",
				normalized.IdempotencyKey,
			)
		default:
			return TaskAgentAttempt{}, false, fmt.Errorf("insert task agent attempt: %w", insertErr)
		}
	}
	if err := insertTaskAgentAttemptEvent(tx, event); err != nil {
		return TaskAgentAttempt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TaskAgentAttempt{}, false, fmt.Errorf("commit task agent attempt: %w", err)
	}
	attempt, err := s.GetTaskAgentAttempt(normalized.ID)
	return attempt, true, err
}

const taskAgentAttemptSelectColumns = `
	a.id, a.idempotency_key, a.write_hash, a.phase, a.task_episode_id,
	a.shadow_run_id, a.shadow_evaluation_id, a.model_name, a.model_version,
	a.prompt_version, a.prompt_text, a.prompt_hash, a.normalized_input_json,
	a.input_hash, a.started_at, a.created_at,
	event.id, event.sequence, event.status, event.raw_output,
	event.parsed_output_json, event.error_text, event.completed_at`

const taskAgentAttemptLatestEventJoin = `
	JOIN task_agent_attempt_events event
		ON event.attempt_id = a.id
		AND event.sequence = (
			SELECT MAX(candidate.sequence)
			FROM task_agent_attempt_events candidate
			WHERE candidate.attempt_id = a.id
		)`

func scanTaskAgentAttempt(
	scanner interface{ Scan(dest ...any) error },
) (TaskAgentAttempt, error) {
	var item TaskAgentAttempt
	if err := scanner.Scan(
		&item.ID,
		&item.IdempotencyKey,
		&item.WriteHash,
		&item.Phase,
		&item.TaskEpisodeID,
		&item.ShadowRunID,
		&item.ShadowEvaluationID,
		&item.ModelName,
		&item.ModelVersion,
		&item.PromptVersion,
		&item.PromptText,
		&item.PromptHash,
		&item.NormalizedInputJSON,
		&item.InputHash,
		&item.StartedAt,
		&item.CreatedAt,
		&item.LatestEventID,
		&item.LatestEventSequence,
		&item.Status,
		&item.RawOutput,
		&item.ParsedOutputJSON,
		&item.Error,
		&item.CompletedAt,
	); err != nil {
		return TaskAgentAttempt{}, err
	}
	return item, nil
}

func (s *ActivityStore) GetTaskAgentAttempt(id string) (TaskAgentAttempt, error) {
	if s == nil || s.db == nil {
		return TaskAgentAttempt{}, errors.New("activity store is unavailable")
	}
	var err error
	id, err = validateTaskAgentAttemptIdentifier(id, "task agent attempt ID", true)
	if err != nil {
		return TaskAgentAttempt{}, err
	}
	return scanTaskAgentAttempt(s.db.QueryRow(`
		SELECT `+taskAgentAttemptSelectColumns+`
		FROM task_agent_attempts a
		`+taskAgentAttemptLatestEventJoin+`
		WHERE a.id = ?`,
		id,
	))
}

func (s *ActivityStore) GetTaskAgentAttemptByIdempotencyKey(
	key string,
) (TaskAgentAttempt, bool, error) {
	if s == nil || s.db == nil {
		return TaskAgentAttempt{}, false, errors.New("activity store is unavailable")
	}
	var err error
	key, err = validateTaskAgentAttemptIdentifier(
		key,
		"task agent attempt idempotency key",
		true,
	)
	if err != nil {
		return TaskAgentAttempt{}, false, err
	}
	item, err := scanTaskAgentAttempt(s.db.QueryRow(`
		SELECT `+taskAgentAttemptSelectColumns+`
		FROM task_agent_attempts a
		`+taskAgentAttemptLatestEventJoin+`
		WHERE a.idempotency_key = ?`,
		key,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskAgentAttempt{}, false, nil
	}
	return item, err == nil, err
}

func normalizeTaskAgentAttemptFilter(
	filter TaskAgentAttemptFilter,
) (TaskAgentAttemptFilter, error) {
	var err error
	filter.Phase, err = normalizeTaskAgentAttemptPhase(filter.Phase, true)
	if err != nil {
		return TaskAgentAttemptFilter{}, err
	}
	filter.Status, err = normalizeTaskAgentAttemptStatus(filter.Status, true)
	if err != nil {
		return TaskAgentAttemptFilter{}, err
	}
	for value, target := range map[string]*string{
		"task episode ID":      &filter.TaskEpisodeID,
		"shadow run ID":        &filter.ShadowRunID,
		"shadow evaluation ID": &filter.ShadowEvaluationID,
	} {
		*target, err = validateTaskAgentAttemptIdentifier(*target, value, false)
		if err != nil {
			return TaskAgentAttemptFilter{}, err
		}
	}
	filter.Limit = normalizeTaskAgentAttemptListLimit(filter.Limit)
	return filter, nil
}

// ListTaskAgentAttempts is always bounded. Empty filters return the newest
// attempts across all phases, while linkage and latest-status filters can be
// combined without exposing an unbounded scan.
func (s *ActivityStore) ListTaskAgentAttempts(
	filter TaskAgentAttemptFilter,
) ([]TaskAgentAttempt, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	filter, err := normalizeTaskAgentAttemptFilter(filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT `+taskAgentAttemptSelectColumns+`
		FROM task_agent_attempts a
		`+taskAgentAttemptLatestEventJoin+`
		WHERE (? = '' OR a.phase = ?)
			AND (? = '' OR event.status = ?)
			AND (? = '' OR a.task_episode_id = ?)
			AND (? = '' OR a.shadow_run_id = ?)
			AND (? = '' OR a.shadow_evaluation_id = ?)
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT ?`,
		filter.Phase,
		filter.Phase,
		filter.Status,
		filter.Status,
		filter.TaskEpisodeID,
		filter.TaskEpisodeID,
		filter.ShadowRunID,
		filter.ShadowRunID,
		filter.ShadowEvaluationID,
		filter.ShadowEvaluationID,
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list task agent attempts: %w", err)
	}
	defer rows.Close()
	items := make([]TaskAgentAttempt, 0, filter.Limit)
	for rows.Next() {
		item, err := scanTaskAgentAttempt(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanTaskAgentAttemptEvent(
	scanner interface{ Scan(dest ...any) error },
) (TaskAgentAttemptEvent, error) {
	var item TaskAgentAttemptEvent
	if err := scanner.Scan(
		&item.ID,
		&item.TaskAttemptID,
		&item.Sequence,
		&item.Status,
		&item.RawOutput,
		&item.ParsedOutputJSON,
		&item.Error,
		&item.PayloadHash,
		&item.OccurredAt,
		&item.CompletedAt,
		&item.CreatedAt,
	); err != nil {
		return TaskAgentAttemptEvent{}, err
	}
	return item, nil
}

func (s *ActivityStore) ListTaskAgentAttemptEvents(
	attemptID string,
	limit int,
) ([]TaskAgentAttemptEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	var err error
	attemptID, err = validateTaskAgentAttemptIdentifier(
		attemptID,
		"task agent attempt ID",
		true,
	)
	if err != nil {
		return nil, err
	}
	limit = normalizeTaskAgentAttemptEventLimit(limit)
	rows, err := s.db.Query(`
		SELECT id, attempt_id, sequence, status, raw_output,
			parsed_output_json, error_text, payload_hash, occurred_at,
			completed_at, created_at
		FROM task_agent_attempt_events
		WHERE attempt_id = ?
		ORDER BY sequence ASC
		LIMIT ?`,
		attemptID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list task agent attempt events: %w", err)
	}
	defer rows.Close()
	items := make([]TaskAgentAttemptEvent, 0, limit)
	for rows.Next() {
		item, err := scanTaskAgentAttemptEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CompleteTaskAgentAttempt appends the sole terminal event to a started
// attempt. Repeating an identical completion is idempotent; a conflicting
// completion is rejected without changing the original evidence.
func (s *ActivityStore) CompleteTaskAgentAttempt(
	attemptID string,
	input CompleteTaskAgentAttemptInput,
) (TaskAgentAttempt, bool, error) {
	if s == nil || s.db == nil {
		return TaskAgentAttempt{}, false, errors.New("activity store is unavailable")
	}
	var err error
	attemptID, err = validateTaskAgentAttemptIdentifier(
		attemptID,
		"task agent attempt ID",
		true,
	)
	if err != nil {
		return TaskAgentAttempt{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskAgentAttempt{}, false, fmt.Errorf("begin task agent completion: %w", err)
	}
	defer tx.Rollback()

	var startedAt int64
	if err := tx.QueryRow(
		`SELECT started_at FROM task_agent_attempts WHERE id = ?`,
		attemptID,
	).Scan(&startedAt); err != nil {
		return TaskAgentAttempt{}, false, err
	}
	status, err := normalizeTaskAgentAttemptStatus(input.Status, false)
	if err != nil {
		return TaskAgentAttempt{}, false, err
	}
	if status == TaskAgentAttemptStatusStarted {
		return TaskAgentAttempt{}, false, errors.New(
			"task agent completion requires a terminal status",
		)
	}
	eventPayload, err := normalizeTaskAgentAttemptEvent(
		status,
		input.RawOutput,
		input.ParsedOutputJSON,
		input.Error,
		startedAt,
		input.CompletedAt,
		input.CreatedAt,
	)
	if err != nil {
		return TaskAgentAttempt{}, false, err
	}
	event, err := taskAgentAttemptEventFromPayload(attemptID, 2, eventPayload)
	if err != nil {
		return TaskAgentAttempt{}, false, err
	}

	var latestSequence int
	var latestHash string
	if err := tx.QueryRow(`
		SELECT sequence, payload_hash
		FROM task_agent_attempt_events
		WHERE attempt_id = ?
		ORDER BY sequence DESC
		LIMIT 1`,
		attemptID,
	).Scan(&latestSequence, &latestHash); err != nil {
		return TaskAgentAttempt{}, false, err
	}
	if latestSequence > 1 {
		if latestHash != event.PayloadHash {
			return TaskAgentAttempt{}, false, errors.New(
				"task agent attempt already has a different terminal event",
			)
		}
		if err := tx.Rollback(); err != nil {
			return TaskAgentAttempt{}, false, err
		}
		existing, getErr := s.GetTaskAgentAttempt(attemptID)
		return existing, false, getErr
	}
	if err := insertTaskAgentAttemptEvent(tx, event); err != nil {
		insertErr := err
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return TaskAgentAttempt{}, false, fmt.Errorf(
				"append task agent completion: %v; rollback: %w",
				insertErr,
				rollbackErr,
			)
		}
		existing, getErr := s.GetTaskAgentAttempt(attemptID)
		switch {
		case getErr != nil:
			return TaskAgentAttempt{}, false, fmt.Errorf(
				"append task agent completion: %v; read concurrent completion: %w",
				insertErr,
				getErr,
			)
		case existing.LatestEventSequence > 1 && existing.LatestEventID == event.ID:
			return existing, false, nil
		case existing.LatestEventSequence > 1:
			return TaskAgentAttempt{}, false, errors.New(
				"task agent attempt already has a different terminal event",
			)
		default:
			return TaskAgentAttempt{}, false, insertErr
		}
	}
	if err := tx.Commit(); err != nil {
		return TaskAgentAttempt{}, false, fmt.Errorf("commit task agent completion: %w", err)
	}
	attempt, err := s.GetTaskAgentAttempt(attemptID)
	return attempt, true, err
}
