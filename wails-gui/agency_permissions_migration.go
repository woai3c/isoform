package main

import (
	"database/sql"
	"fmt"
)

// migrateAgencyPermissionDecisions installs the append-only audit boundary for
// explicit Agency permission decisions. It is intentionally standalone: the
// migration does not require the origination tables because handoff and
// schedule requests can predate the Agency planner.
func migrateAgencyPermissionDecisions(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency permission decisions migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_permission_decision_receipts_v1 (
			id                     TEXT PRIMARY KEY,
			idempotency_key        TEXT NOT NULL,
			stage                  TEXT NOT NULL,
			status                 TEXT NOT NULL,
			request_id             TEXT NOT NULL,
			kind                   TEXT NOT NULL,
			decision               TEXT NOT NULL,
			request_source         TEXT NOT NULL,
			request_snapshot_json  TEXT NOT NULL,
			provenance_json        TEXT NOT NULL,
			before_state_json      TEXT NOT NULL,
			after_state_json       TEXT NOT NULL,
			result_json            TEXT NOT NULL,
			error_stage            TEXT NOT NULL DEFAULT '',
			error_text             TEXT NOT NULL DEFAULT '',
			occurred_at            INTEGER NOT NULL,
			payload_json           TEXT NOT NULL,
			payload_hash           TEXT NOT NULL,
			UNIQUE (idempotency_key, stage),
			CHECK (stage IN ('started', 'terminal')),
			CHECK (status IN ('in_progress', 'succeeded', 'failed')),
			CHECK (
				(stage = 'started' AND status = 'in_progress')
				OR
				(stage = 'terminal' AND status IN ('succeeded', 'failed'))
			),
			CHECK (kind IN ('handoff', 'schedule')),
			CHECK (decision IN ('approve', 'decline')),
			CHECK (request_source IN (
				'handoff',
				'schedule',
				'agency_origination',
				'unresolved'
			)),
			CHECK (occurred_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_permission_receipts_request_v1
			ON agency_permission_decision_receipts_v1(
				request_id, occurred_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_permission_receipts_recent_v1
			ON agency_permission_decision_receipts_v1(
				occurred_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_permission_decision_receipts_v1_no_update
		BEFORE UPDATE ON agency_permission_decision_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency permission decision receipts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_permission_decision_receipts_v1_no_delete
		BEFORE DELETE ON agency_permission_decision_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency permission decision receipts are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency permission decision schema: %w", err)
	}
	return nil
}
