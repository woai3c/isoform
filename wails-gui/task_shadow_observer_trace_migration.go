package main

import (
	"database/sql"
	"fmt"
)

func migrateTaskShadowObserverTraces(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate task shadow observer traces: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS task_shadow_observer_traces_v1 (
			id                              TEXT PRIMARY KEY,
			contract_version                TEXT NOT NULL,
			runtime_instance_id             TEXT NOT NULL,
			group_id                        TEXT NOT NULL,
			session_ids_json                TEXT NOT NULL,
			session_manifest_hash           TEXT NOT NULL,
			source                          TEXT NOT NULL CHECK (source = 'activity_triager'),
			flow                            TEXT NOT NULL CHECK (
				flow IN ('full_onset', 'lifecycle_continuation')
			),
			admission                       TEXT NOT NULL CHECK (
				admission IN ('eligible', 'prefiltered', 'cooldown')
			),
			admission_reason                TEXT NOT NULL DEFAULT '',
			window_start                    INTEGER NOT NULL,
			window_end                      INTEGER NOT NULL,
			session_count                   INTEGER NOT NULL,
			declared_event_count            INTEGER NOT NULL,
			retained_event_count            INTEGER NOT NULL,
			retained_event_manifest_hash    TEXT NOT NULL,
			shadow_enabled_at_admission     INTEGER NOT NULL CHECK (
				shadow_enabled_at_admission IN (0, 1)
			),
			source_pipeline_run_id          INTEGER NOT NULL DEFAULT 0 CHECK (
				source_pipeline_run_id >= 0
			),
			root_hash                       TEXT NOT NULL,
			created_at                      INTEGER NOT NULL,
			CHECK (length(id) > 0 AND length(id) <= 512),
			CHECK (length(contract_version) > 0 AND length(contract_version) <= 128),
			CHECK (length(runtime_instance_id) > 0 AND length(runtime_instance_id) <= 512),
			CHECK (length(group_id) > 0 AND length(group_id) <= 512),
			CHECK (json_valid(session_ids_json) AND json_type(session_ids_json) = 'array'),
			CHECK (length(session_manifest_hash) = 64),
			CHECK (length(admission_reason) <= 4096),
			CHECK (window_start > 0 AND window_end >= window_start),
			CHECK (session_count > 0 AND session_count <= 256),
			CHECK (declared_event_count >= 0),
			CHECK (retained_event_count >= 0),
			CHECK (length(retained_event_manifest_hash) = 64),
			CHECK (length(root_hash) = 64),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_observer_traces_v1_recent
			ON task_shadow_observer_traces_v1(created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_observer_traces_v1_group
			ON task_shadow_observer_traces_v1(group_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_observer_traces_v1_admission
			ON task_shadow_observer_traces_v1(admission, created_at DESC, id DESC);
		CREATE TABLE IF NOT EXISTS task_shadow_observer_trace_events_v1 (
			id                  TEXT PRIMARY KEY,
			trace_id            TEXT NOT NULL REFERENCES task_shadow_observer_traces_v1(id) ON DELETE RESTRICT,
			sequence            INTEGER NOT NULL,
			status              TEXT NOT NULL CHECK (
				status IN (
					'started',
					'completed',
					'completed_with_errors',
					'skipped',
					'cancelled',
					'failed'
				)
			),
			payload_json        TEXT NOT NULL,
			payload_hash        TEXT NOT NULL,
			previous_hash       TEXT NOT NULL,
			occurred_at         INTEGER NOT NULL,
			created_at          INTEGER NOT NULL,
			UNIQUE (trace_id, sequence),
			CHECK (sequence > 0),
			CHECK (json_valid(payload_json) AND json_type(payload_json) = 'object'),
			CHECK (length(payload_hash) = 64),
			CHECK (length(previous_hash) = 64),
			CHECK (occurred_at > 0),
			CHECK (created_at >= occurred_at)
		);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_observer_trace_events_v1_latest
			ON task_shadow_observer_trace_events_v1(trace_id, sequence DESC);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_observer_trace_events_v1_status
			ON task_shadow_observer_trace_events_v1(status, occurred_at DESC, id DESC);

		CREATE TRIGGER IF NOT EXISTS task_shadow_observer_trace_events_v1_validate_insert
		BEFORE INSERT ON task_shadow_observer_trace_events_v1
		BEGIN
			SELECT CASE
				WHEN NEW.sequence != COALESCE((
					SELECT MAX(existing.sequence) + 1
					FROM task_shadow_observer_trace_events_v1 existing
					WHERE existing.trace_id = NEW.trace_id
				), 1)
				THEN RAISE(ABORT, 'task shadow observer event sequence is not append-only')
			END;
			SELECT CASE
				WHEN EXISTS (
					SELECT 1
					FROM task_shadow_observer_trace_events_v1 existing
					WHERE existing.trace_id = NEW.trace_id
						AND existing.status IN (
							'completed',
							'completed_with_errors',
							'skipped',
							'cancelled',
							'failed'
						)
				)
				THEN RAISE(ABORT, 'task shadow observer trace is already terminal')
			END;
			SELECT CASE
				WHEN NEW.sequence = 1 AND NEW.status NOT IN ('started', 'skipped')
				THEN RAISE(ABORT, 'task shadow observer first event must start or skip')
			END;
			SELECT CASE
				WHEN NEW.sequence > 1 AND NEW.status = 'started'
				THEN RAISE(ABORT, 'task shadow observer trace cannot restart')
			END;
			SELECT CASE
				WHEN NEW.sequence > 1 AND NOT EXISTS (
					SELECT 1
					FROM task_shadow_observer_trace_events_v1 existing
					WHERE existing.trace_id = NEW.trace_id
						AND existing.sequence = 1
						AND existing.status = 'started'
				)
				THEN RAISE(ABORT, 'task shadow observer terminal event requires started')
			END;
			SELECT CASE
				WHEN NEW.sequence = 1 AND NEW.previous_hash != (
					SELECT root_hash
					FROM task_shadow_observer_traces_v1
					WHERE id = NEW.trace_id
				)
				THEN RAISE(ABORT, 'task shadow observer root hash link mismatch')
			END;
			SELECT CASE
				WHEN NEW.sequence > 1 AND NEW.previous_hash != (
					SELECT payload_hash
					FROM task_shadow_observer_trace_events_v1
					WHERE trace_id = NEW.trace_id
						AND sequence = NEW.sequence - 1
				)
				THEN RAISE(ABORT, 'task shadow observer event hash link mismatch')
			END;
		END;

		CREATE TRIGGER IF NOT EXISTS task_shadow_observer_traces_v1_no_update
		BEFORE UPDATE ON task_shadow_observer_traces_v1
		BEGIN
			SELECT RAISE(ABORT, 'task shadow observer traces are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_shadow_observer_traces_v1_no_delete
		BEFORE DELETE ON task_shadow_observer_traces_v1
		BEGIN
			SELECT RAISE(ABORT, 'task shadow observer traces are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_shadow_observer_trace_events_v1_no_update
		BEFORE UPDATE ON task_shadow_observer_trace_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'task shadow observer trace events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_shadow_observer_trace_events_v1_no_delete
		BEFORE DELETE ON task_shadow_observer_trace_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'task shadow observer trace events are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create task shadow observer trace ledger: %w", err)
	}
	return nil
}
