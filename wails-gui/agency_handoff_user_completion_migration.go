package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// migrateAgencyOriginationPermissionCompletedByUser expands the immutable
// event enum while preserving every existing row. The dedicated terminal state
// is intentionally not a grant, rejection, expiry, or cancellation.
func migrateAgencyOriginationPermissionCompletedByUser(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency permission user completion migration requires a transaction")
	}
	var schema string
	err := tx.QueryRow(`
		SELECT sql
		FROM sqlite_master
		WHERE type = 'table'
			AND name = 'agency_origination_permission_events_v1'`,
	).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("agency origination permission events must exist before user completion migration")
	}
	if err != nil {
		return fmt.Errorf("inspect agency permission event schema: %w", err)
	}
	if strings.Contains(strings.ToLower(schema), "'completed_by_user'") {
		return nil
	}
	if _, err := tx.Exec(`
		ALTER TABLE agency_origination_permission_events_v1
			RENAME TO agency_origination_permission_events_v1_legacy;
		DROP TRIGGER IF EXISTS agency_origination_permission_events_v1_no_update;
		DROP TRIGGER IF EXISTS agency_origination_permission_events_v1_no_delete;
		DROP INDEX IF EXISTS idx_agency_permission_events_request_v1;

		CREATE TABLE agency_origination_permission_events_v1 (
			id                TEXT PRIMARY KEY,
			request_id        TEXT NOT NULL REFERENCES agency_origination_permission_requests_v1(id),
			idempotency_key   TEXT NOT NULL UNIQUE,
			status            TEXT NOT NULL,
			reason            TEXT NOT NULL DEFAULT '',
			occurred_at       INTEGER NOT NULL,
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			created_at        INTEGER NOT NULL,
			CHECK (status IN (
				'pending', 'granted', 'rejected', 'cancelled', 'expired',
				'completed_by_user'
			)),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX idx_agency_permission_events_request_v1
			ON agency_origination_permission_events_v1(
				request_id, occurred_at ASC, id ASC
			);
		CREATE TRIGGER agency_origination_permission_events_v1_no_update
		BEFORE UPDATE ON agency_origination_permission_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination permission events are immutable');
		END;
		CREATE TRIGGER agency_origination_permission_events_v1_no_delete
		BEFORE DELETE ON agency_origination_permission_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination permission events are immutable');
		END;

		INSERT INTO agency_origination_permission_events_v1 (
			id, request_id, idempotency_key, status, reason, occurred_at,
			payload_json, payload_hash, created_at
		)
		SELECT
			id, request_id, idempotency_key, status, reason, occurred_at,
			payload_json, payload_hash, created_at
		FROM agency_origination_permission_events_v1_legacy;
		DROP TABLE agency_origination_permission_events_v1_legacy;
	`); err != nil {
		return fmt.Errorf("expand agency permission event status: %w", err)
	}
	return nil
}

// migrateAgencyHandoffUserCompletions installs the append-only receipt used
// when retained user-result evidence makes a pending one-time Handoff obsolete.
func migrateAgencyHandoffUserCompletions(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency handoff user completion migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_handoff_user_completion_receipts_v1 (
			id                         TEXT PRIMARY KEY,
			idempotency_key            TEXT NOT NULL,
			version                    TEXT NOT NULL,
			stage                      TEXT NOT NULL,
			status                     TEXT NOT NULL,
			request_id                 TEXT NOT NULL,
			request_payload_hash       TEXT NOT NULL,
			task_episode_id            TEXT NOT NULL,
			completion_transition_id   TEXT NOT NULL,
			result_evidence_ids_json   TEXT NOT NULL,
			source_snapshot_json       TEXT NOT NULL,
			before_state_json          TEXT NOT NULL,
			after_state_json           TEXT NOT NULL,
			effects_json               TEXT NOT NULL,
			error_stage                TEXT NOT NULL DEFAULT '',
			error_text                 TEXT NOT NULL DEFAULT '',
			occurred_at                INTEGER NOT NULL,
			payload_json               TEXT NOT NULL,
			payload_hash               TEXT NOT NULL,
			UNIQUE (idempotency_key, stage),
			CHECK (version = 'agency-handoff-user-completion-v1'),
			CHECK (stage IN ('started', 'terminal')),
			CHECK (status IN ('in_progress', 'succeeded', 'failed')),
			CHECK (
				(stage = 'started' AND status = 'in_progress')
				OR
				(stage = 'terminal' AND status IN ('succeeded', 'failed'))
			),
			CHECK (request_id != ''),
			CHECK (request_payload_hash != ''),
			CHECK (task_episode_id != ''),
			CHECK (completion_transition_id != ''),
			CHECK (occurred_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_handoff_user_completion_request_v1
			ON agency_handoff_user_completion_receipts_v1(
				request_id, occurred_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_handoff_user_completion_episode_v1
			ON agency_handoff_user_completion_receipts_v1(
				task_episode_id, occurred_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_handoff_user_completion_recent_v1
			ON agency_handoff_user_completion_receipts_v1(
				occurred_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_handoff_user_completion_receipts_v1_no_update
		BEFORE UPDATE ON agency_handoff_user_completion_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency handoff user completion receipts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_handoff_user_completion_receipts_v1_no_delete
		BEFORE DELETE ON agency_handoff_user_completion_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency handoff user completion receipts are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency handoff user completion schema: %w", err)
	}
	return nil
}
