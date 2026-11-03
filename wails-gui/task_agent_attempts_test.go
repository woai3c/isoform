package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func installTaskAgentAttemptMigration(t *testing.T, store *ActivityStore) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateTaskAgentAttempts(tx); err != nil {
		tx.Rollback()
		t.Fatalf("migrateTaskAgentAttempts: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit task agent attempts migration: %v", err)
	}
}

func taskAgentAttemptFixture(
	key, phase, status string,
	startedAt int64,
) TaskAgentAttemptInput {
	input := TaskAgentAttemptInput{
		IdempotencyKey:      key,
		Phase:               phase,
		TaskEpisodeID:       "episode-1",
		ShadowRunID:         "shadow-run-1",
		ShadowEvaluationID:  "evaluation-1",
		ModelName:           "gpt-5.6-sol",
		ModelVersion:        "2026-07-28",
		PromptVersion:       "task-onset-v1",
		PromptText:          "Infer the task without using future evidence.\n",
		NormalizedInputJSON: `{"z":2,"context":{"project":"Yansu"},"a":1}`,
		Status:              status,
		StartedAt:           startedAt,
		CreatedAt:           startedAt,
	}
	switch status {
	case TaskAgentAttemptStatusSucceeded:
		input.RawOutput = ` { "task": "Implement ledger" } `
		input.ParsedOutputJSON = `{"task":"Implement ledger","confidence":0.91}`
		input.CompletedAt = startedAt + 10
	case TaskAgentAttemptStatusFailed:
		input.RawOutput = "provider unavailable"
		input.Error = "provider unavailable"
		input.CompletedAt = startedAt + 20
	case TaskAgentAttemptStatusParseFailed:
		input.RawOutput = "not valid JSON"
		input.Error = "decode response: invalid character"
		input.CompletedAt = startedAt + 30
	}
	return input
}

func TestTaskAgentAttemptsSupportAllPhasesAndTerminalStatuses(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	phases := []string{
		TaskAgentAttemptPhaseOnset,
		TaskAgentAttemptPhaseLifecycle,
		TaskAgentAttemptPhaseEvaluation,
		TaskAgentAttemptPhaseLearning,
	}
	statuses := []string{
		TaskAgentAttemptStatusSucceeded,
		TaskAgentAttemptStatusFailed,
		TaskAgentAttemptStatusParseFailed,
	}
	for index, phase := range phases {
		status := statuses[index%len(statuses)]
		input := taskAgentAttemptFixture(
			"phase-"+phase,
			phase,
			status,
			int64(1_000+index*100),
		)
		attempt, created, err := store.RecordTaskAgentAttempt(input)
		if err != nil {
			t.Fatalf("add %s/%s attempt: %v", phase, status, err)
		}
		if !created || attempt.Phase != phase || attempt.Status != status {
			t.Fatalf("%s attempt=%+v created=%v", phase, attempt, created)
		}
		if attempt.ModelName != input.ModelName ||
			attempt.ModelVersion != input.ModelVersion ||
			attempt.PromptText != input.PromptText {
			t.Fatalf("exact model/prompt provenance changed: %+v", attempt)
		}
		if attempt.PromptHash != taskAgentAttemptHash([]byte(input.PromptText)) {
			t.Fatalf("prompt hash=%q", attempt.PromptHash)
		}
		wantInput := `{"a":1,"context":{"project":"Yansu"},"z":2}`
		if attempt.NormalizedInputJSON != wantInput ||
			attempt.InputHash != taskAgentAttemptHash([]byte(wantInput)) {
			t.Fatalf("normalized input=%q hash=%q", attempt.NormalizedInputJSON, attempt.InputHash)
		}
		if status == TaskAgentAttemptStatusSucceeded &&
			attempt.ParsedOutputJSON != `{"confidence":0.91,"task":"Implement ledger"}` {
			t.Fatalf("parsed output was not canonical: %q", attempt.ParsedOutputJSON)
		}
	}

	for _, status := range statuses {
		items, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
			Status: status,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("list %s attempts: %v", status, err)
		}
		if len(items) == 0 {
			t.Fatalf("terminal status %s was not persisted", status)
		}
	}
	filtered, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
		Phase:              TaskAgentAttemptPhaseEvaluation,
		Status:             TaskAgentAttemptStatusParseFailed,
		TaskEpisodeID:      "episode-1",
		ShadowRunID:        "shadow-run-1",
		ShadowEvaluationID: "evaluation-1",
		Limit:              10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Phase != TaskAgentAttemptPhaseEvaluation {
		t.Fatalf("combined attempt filter=%+v", filtered)
	}
}

func TestTaskAgentAttemptMigrationIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	installTaskAgentAttemptMigration(t, store)

	attempt, created, err := store.RecordTaskAgentAttempt(taskAgentAttemptFixture(
		"after-repeat-migration",
		TaskAgentAttemptPhaseOnset,
		TaskAgentAttemptStatusSucceeded,
		1_500,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !created || attempt.Status != TaskAgentAttemptStatusSucceeded {
		t.Fatalf("attempt=%+v created=%v", attempt, created)
	}
}

func TestTaskAgentAttemptStartedCompletionIsAppendOnlyAndIdempotent(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	input := taskAgentAttemptFixture(
		"started-then-complete",
		TaskAgentAttemptPhaseEvaluation,
		TaskAgentAttemptStatusStarted,
		2_000,
	)
	started, created, err := store.RecordTaskAgentAttempt(input)
	if err != nil {
		t.Fatal(err)
	}
	if !created || started.Status != TaskAgentAttemptStatusStarted ||
		started.LatestEventSequence != 1 {
		t.Fatalf("started attempt=%+v created=%v", started, created)
	}

	completion := CompleteTaskAgentAttemptInput{
		Status:           TaskAgentAttemptStatusSucceeded,
		RawOutput:        `{"match":true}`,
		ParsedOutputJSON: `{"match":true}`,
		CompletedAt:      2_500,
	}
	completed, appended, err := store.CompleteTaskAgentAttempt(started.ID, completion)
	if err != nil {
		t.Fatal(err)
	}
	if !appended || completed.Status != TaskAgentAttemptStatusSucceeded ||
		completed.LatestEventSequence != 2 || completed.CompletedAt != 2_500 {
		t.Fatalf("completed attempt=%+v appended=%v", completed, appended)
	}
	repeated, appended, err := store.CompleteTaskAgentAttempt(started.ID, completion)
	if err != nil {
		t.Fatal(err)
	}
	if appended || repeated.LatestEventID != completed.LatestEventID {
		t.Fatalf("completion retry appended=%v repeated=%+v", appended, repeated)
	}
	conflict := completion
	conflict.RawOutput = `{"match":false}`
	conflict.ParsedOutputJSON = `{"match":false}`
	if _, _, err := store.CompleteTaskAgentAttempt(started.ID, conflict); err == nil {
		t.Fatal("conflicting terminal completion was accepted")
	}
	events, err := store.ListTaskAgentAttemptEvents(started.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 ||
		events[0].Status != TaskAgentAttemptStatusStarted ||
		events[1].Status != TaskAgentAttemptStatusSucceeded {
		t.Fatalf("events=%+v", events)
	}
}

func TestTaskAgentAttemptDeterministicWriteAndConflict(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	input := taskAgentAttemptFixture(
		"deterministic-key",
		TaskAgentAttemptPhaseOnset,
		TaskAgentAttemptStatusSucceeded,
		3_000,
	)
	first, created, err := store.RecordTaskAgentAttempt(input)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first deterministic attempt was not created")
	}
	second, created, err := store.RecordTaskAgentAttempt(input)
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID || second.WriteHash != first.WriteHash {
		t.Fatalf("idempotent retry=%+v created=%v, first=%+v", second, created, first)
	}
	wantID := deterministicTaskAgentAttemptID("attempt", input.IdempotencyKey)
	if first.ID != wantID {
		t.Fatalf("attempt ID=%q, want deterministic %q", first.ID, wantID)
	}
	conflict := input
	conflict.PromptText = "A different immutable prompt"
	if _, _, err := store.RecordTaskAgentAttempt(conflict); err == nil ||
		!strings.Contains(err.Error(), "different immutable payload") {
		t.Fatalf("conflicting retry error=%v", err)
	}

	unkeyed := input
	unkeyed.ID = ""
	unkeyed.IdempotencyKey = ""
	unkeyed.StartedAt = 4_000
	unkeyed.CompletedAt = 4_010
	unkeyed.CreatedAt = 4_000
	randomFirst, _, err := store.RecordTaskAgentAttempt(unkeyed)
	if err != nil {
		t.Fatal(err)
	}
	randomSecond, _, err := store.RecordTaskAgentAttempt(unkeyed)
	if err != nil {
		t.Fatal(err)
	}
	if randomFirst.ID == randomSecond.ID {
		t.Fatal("unkeyed writes unexpectedly reused an attempt ID")
	}
}

func TestTaskAgentAttemptRowsAndEventsAreImmutable(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	attempt, _, err := store.RecordTaskAgentAttempt(taskAgentAttemptFixture(
		"immutable",
		TaskAgentAttemptPhaseLearning,
		TaskAgentAttemptStatusSucceeded,
		5_000,
	))
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListTaskAgentAttemptEvents(attempt.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "attempt update",
			sql:  `UPDATE task_agent_attempts SET model_name = 'changed' WHERE id = ?`,
			args: []any{attempt.ID},
		},
		{
			name: "attempt delete",
			sql:  `DELETE FROM task_agent_attempts WHERE id = ?`,
			args: []any{attempt.ID},
		},
		{
			name: "event update",
			sql:  `UPDATE task_agent_attempt_events SET raw_output = 'changed' WHERE id = ?`,
			args: []any{events[0].ID},
		},
		{
			name: "event delete",
			sql:  `DELETE FROM task_agent_attempt_events WHERE id = ?`,
			args: []any{events[0].ID},
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if _, err := store.db.Exec(check.sql, check.args...); err == nil ||
				!strings.Contains(err.Error(), "immutable") {
				t.Fatalf("tamper error=%v", err)
			}
		})
	}
	if _, err := store.db.Exec(`
		INSERT INTO task_agent_attempt_events (
			id, attempt_id, sequence, status, raw_output,
			parsed_output_json, error_text, payload_hash, occurred_at,
			completed_at, created_at
		) VALUES (?, ?, 2, 'failed', '', '', 'late', ?, 6000, 6000, 6000)`,
		"late-event",
		attempt.ID,
		strings.Repeat("a", 64),
	); err == nil || !strings.Contains(err.Error(), "already terminal") {
		t.Fatalf("post-terminal insert error=%v", err)
	}
}

func TestTaskAgentAttemptValidationRejectsMalformedInput(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	base := taskAgentAttemptFixture(
		"malformed",
		TaskAgentAttemptPhaseOnset,
		TaskAgentAttemptStatusSucceeded,
		7_000,
	)
	tests := []struct {
		name   string
		mutate func(*TaskAgentAttemptInput)
	}{
		{
			name: "unknown phase",
			mutate: func(input *TaskAgentAttemptInput) {
				input.Phase = "planning"
			},
		},
		{
			name: "unknown status",
			mutate: func(input *TaskAgentAttemptInput) {
				input.Status = "complete"
			},
		},
		{
			name: "malformed input JSON",
			mutate: func(input *TaskAgentAttemptInput) {
				input.NormalizedInputJSON = `{"broken":`
			},
		},
		{
			name: "multiple input JSON values",
			mutate: func(input *TaskAgentAttemptInput) {
				input.NormalizedInputJSON = `{} {}`
			},
		},
		{
			name: "prompt hash mismatch",
			mutate: func(input *TaskAgentAttemptInput) {
				input.PromptHash = strings.Repeat("0", 64)
			},
		},
		{
			name: "input hash mismatch",
			mutate: func(input *TaskAgentAttemptInput) {
				input.InputHash = strings.Repeat("0", 64)
			},
		},
		{
			name: "malformed parsed JSON",
			mutate: func(input *TaskAgentAttemptInput) {
				input.ParsedOutputJSON = `{"broken":`
			},
		},
		{
			name: "completion before start",
			mutate: func(input *TaskAgentAttemptInput) {
				input.CompletedAt = input.StartedAt - 1
			},
		},
		{
			name: "oversized linkage",
			mutate: func(input *TaskAgentAttemptInput) {
				input.TaskEpisodeID = strings.Repeat("x", taskAgentAttemptMaxIdentifierBytes+1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.IdempotencyKey += "-" + strings.ReplaceAll(test.name, " ", "-")
			test.mutate(&input)
			if _, _, err := store.RecordTaskAgentAttempt(input); err == nil {
				t.Fatal("malformed attempt was accepted")
			}
		})
	}

	started := taskAgentAttemptFixture(
		"invalid-started-output",
		TaskAgentAttemptPhaseLifecycle,
		TaskAgentAttemptStatusStarted,
		8_000,
	)
	started.RawOutput = "premature"
	if _, _, err := store.RecordTaskAgentAttempt(started); err == nil {
		t.Fatal("started attempt with output was accepted")
	}
	if err := migrateTaskAgentAttempts(nil); err == nil {
		t.Fatal("nil migration transaction was accepted")
	}
}

func TestTaskAgentAttemptErrorAndListBounds(t *testing.T) {
	store := newTestStore(t)
	installTaskAgentAttemptMigration(t, store)
	for index := 0; index < taskAgentAttemptMaxListLimit+5; index++ {
		input := taskAgentAttemptFixture(
			fmt.Sprintf("bounded-%03d", index),
			TaskAgentAttemptPhaseLifecycle,
			TaskAgentAttemptStatusFailed,
			int64(10_000+index),
		)
		input.Error = strings.Repeat("é", taskAgentAttemptMaxErrorBytes)
		attempt, _, err := store.RecordTaskAgentAttempt(input)
		if err != nil {
			t.Fatalf("add bounded attempt %d: %v", index, err)
		}
		if len(attempt.Error) > taskAgentAttemptMaxErrorBytes ||
			!strings.HasPrefix(attempt.Error, "é") ||
			!strings.HasSuffix(attempt.Error, taskAgentAttemptErrorSuffix) {
			t.Fatalf("error was not safely bounded: bytes=%d", len(attempt.Error))
		}
	}
	items, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
		Phase: TaskAgentAttemptPhaseLifecycle,
		Limit: taskAgentAttemptMaxListLimit + 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != taskAgentAttemptMaxListLimit {
		t.Fatalf("bounded list returned %d items, want %d", len(items), taskAgentAttemptMaxListLimit)
	}
	two, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 2 {
		t.Fatalf("limit=2 returned %d attempts", len(two))
	}
	if normalizeTaskAgentAttemptListLimit(0) != taskAgentAttemptDefaultListLimit ||
		normalizeTaskAgentAttemptListLimit(taskAgentAttemptMaxListLimit+1) != taskAgentAttemptMaxListLimit ||
		normalizeTaskAgentAttemptEventLimit(0) != taskAgentAttemptDefaultEventLimit ||
		normalizeTaskAgentAttemptEventLimit(taskAgentAttemptMaxEventLimit+1) != taskAgentAttemptMaxEventLimit {
		t.Fatal("task agent attempt limits are not safely bounded")
	}
	if _, err := store.GetTaskAgentAttempt(strings.Repeat("x", taskAgentAttemptMaxIdentifierBytes+1)); err == nil {
		t.Fatal("oversized get identifier was accepted")
	}
	if _, found, err := store.GetTaskAgentAttemptByIdempotencyKey("missing"); err != nil || found {
		t.Fatalf("missing key found=%v err=%v", found, err)
	}
	if _, err := store.GetTaskAgentAttempt("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing attempt error=%v, want sql.ErrNoRows", err)
	}
}
