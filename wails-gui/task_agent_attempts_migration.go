package main

import (
	"database/sql"
	"fmt"
)

// migrateTaskAgentAttempts creates the immutable model-attempt ledger used by
// task onset, lifecycle, evaluation, and learning. The attempt root freezes the
// exact request. Status and response data live in append-only events so a
// started attempt can later receive one terminal event without mutation.
func migrateTaskAgentAttempts(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate task agent attempts: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS task_agent_attempts (
			id                     TEXT PRIMARY KEY,
			idempotency_key        TEXT NOT NULL UNIQUE,
			write_hash             TEXT NOT NULL,
			phase                  TEXT NOT NULL CHECK (
				phase IN ('onset', 'lifecycle', 'evaluation', 'learning')
			),
			task_episode_id        TEXT NOT NULL DEFAULT '',
			shadow_run_id          TEXT NOT NULL DEFAULT '',
			shadow_evaluation_id   TEXT NOT NULL DEFAULT '',
			model_name             TEXT NOT NULL,
			model_version          TEXT NOT NULL,
			prompt_version         TEXT NOT NULL,
			prompt_text            TEXT NOT NULL,
			prompt_hash            TEXT NOT NULL,
			normalized_input_json  TEXT NOT NULL,
			input_hash             TEXT NOT NULL,
			started_at             INTEGER NOT NULL,
			created_at             INTEGER NOT NULL,
			CHECK (length(id) > 0),
			CHECK (length(idempotency_key) > 0),
			CHECK (length(write_hash) = 64),
			CHECK (length(prompt_hash) = 64),
			CHECK (length(input_hash) = 64),
			CHECK (length(model_name) > 0),
			CHECK (length(model_version) > 0),
			CHECK (length(prompt_version) > 0),
			CHECK (length(prompt_text) > 0),
			CHECK (json_valid(normalized_input_json)),
			CHECK (started_at > 0),
			CHECK (created_at >= started_at)
		);
		CREATE INDEX IF NOT EXISTS idx_task_agent_attempts_episode
			ON task_agent_attempts(task_episode_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_task_agent_attempts_run
			ON task_agent_attempts(shadow_run_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_task_agent_attempts_evaluation
			ON task_agent_attempts(shadow_evaluation_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_task_agent_attempts_phase
			ON task_agent_attempts(phase, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS task_agent_attempt_events (
			id                  TEXT PRIMARY KEY,
			attempt_id          TEXT NOT NULL REFERENCES task_agent_attempts(id) ON DELETE RESTRICT,
			sequence            INTEGER NOT NULL,
			status              TEXT NOT NULL CHECK (
				status IN ('started', 'succeeded', 'failed', 'parse_failed')
			),
			raw_output          TEXT NOT NULL DEFAULT '',
			parsed_output_json  TEXT NOT NULL DEFAULT '',
			error_text          TEXT NOT NULL DEFAULT '',
			payload_hash        TEXT NOT NULL,
			occurred_at         INTEGER NOT NULL,
			completed_at        INTEGER NOT NULL DEFAULT 0,
			created_at          INTEGER NOT NULL,
			UNIQUE (attempt_id, sequence),
			CHECK (sequence > 0),
			CHECK (length(payload_hash) = 64),
			CHECK (length(error_text) <= 16384),
			CHECK (
				parsed_output_json = '' OR
				json_valid(parsed_output_json)
			),
			CHECK (occurred_at > 0),
			CHECK (created_at >= occurred_at),
			CHECK (
				(status = 'started' AND completed_at = 0 AND occurred_at > 0) OR
				(status != 'started' AND completed_at = occurred_at)
			),
			CHECK (
				(
					status = 'started' AND
					raw_output = '' AND
					parsed_output_json = '' AND
					error_text = ''
				) OR
				(
					status = 'succeeded' AND
					parsed_output_json != '' AND
					error_text = ''
				) OR
				(
					status IN ('failed', 'parse_failed') AND
					parsed_output_json = '' AND
					error_text != ''
				)
			)
		);
		CREATE INDEX IF NOT EXISTS idx_task_agent_attempt_events_latest
			ON task_agent_attempt_events(attempt_id, sequence DESC);
		CREATE INDEX IF NOT EXISTS idx_task_agent_attempt_events_status
			ON task_agent_attempt_events(status, occurred_at DESC, id DESC);

		CREATE TRIGGER IF NOT EXISTS task_agent_attempt_events_validate_insert
		BEFORE INSERT ON task_agent_attempt_events
		BEGIN
			SELECT CASE
				WHEN NEW.sequence != COALESCE((
					SELECT MAX(existing.sequence) + 1
					FROM task_agent_attempt_events existing
					WHERE existing.attempt_id = NEW.attempt_id
				), 1)
				THEN RAISE(ABORT, 'task agent attempt event sequence is not append-only')
			END;
			SELECT CASE
				WHEN EXISTS (
					SELECT 1
					FROM task_agent_attempt_events existing
					WHERE existing.attempt_id = NEW.attempt_id
						AND existing.status IN ('succeeded', 'failed', 'parse_failed')
				)
				THEN RAISE(ABORT, 'task agent attempt is already terminal')
			END;
			SELECT CASE
				WHEN NEW.sequence > 1 AND NEW.status = 'started'
				THEN RAISE(ABORT, 'task agent attempt cannot restart')
			END;
			SELECT CASE
				WHEN NEW.sequence > 1 AND NOT EXISTS (
					SELECT 1
					FROM task_agent_attempt_events existing
					WHERE existing.attempt_id = NEW.attempt_id
						AND existing.sequence = 1
						AND existing.status = 'started'
				)
				THEN RAISE(ABORT, 'task agent terminal event requires a started event')
			END;
		END;

		CREATE TRIGGER IF NOT EXISTS task_agent_attempts_no_update
		BEFORE UPDATE ON task_agent_attempts
		BEGIN
			SELECT RAISE(ABORT, 'task agent attempts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_agent_attempts_no_delete
		BEFORE DELETE ON task_agent_attempts
		BEGIN
			SELECT RAISE(ABORT, 'task agent attempts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_agent_attempt_events_no_update
		BEFORE UPDATE ON task_agent_attempt_events
		BEGIN
			SELECT RAISE(ABORT, 'task agent attempt events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_agent_attempt_events_no_delete
		BEFORE DELETE ON task_agent_attempt_events
		BEGIN
			SELECT RAISE(ABORT, 'task agent attempt events are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create task agent attempt ledger: %w", err)
	}
	return nil
}
