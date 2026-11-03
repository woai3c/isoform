package main

import (
	"database/sql"
	"fmt"
)

func migrateAgencyActivationGate(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency activation gate migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_origination_gate_receipts_v1 (
			id                           TEXT PRIMARY KEY,
			idempotency_key              TEXT NOT NULL UNIQUE,
			policy_version               TEXT NOT NULL,
			state                        TEXT NOT NULL,
			decision                     TEXT NOT NULL,
			reason                       TEXT NOT NULL,
			wake_reason                  TEXT NOT NULL,
			active_envelope_id           TEXT NOT NULL DEFAULT '',
			latest_envelope_id           TEXT NOT NULL DEFAULT '',
			allowed_candidate_kinds_json TEXT NOT NULL,
			capacity_route               TEXT NOT NULL DEFAULT '',
			state_fingerprint            TEXT NOT NULL,
			state_json                   TEXT NOT NULL,
			payload_json                 TEXT NOT NULL,
			payload_hash                 TEXT NOT NULL,
			observed_at                  INTEGER NOT NULL,
			created_at                   INTEGER NOT NULL,
			CHECK (policy_version = 'agency-activation-gate-v1'),
			CHECK (state IN ('ready', 'activation_required', 'invalid')),
			CHECK (decision IN ('allow', 'suppress', 'deny')),
			CHECK (wake_reason IN ('periodic', 'meaningful_state_change')),
			CHECK (capacity_route IN ('', 'background_understanding')),
			CHECK (observed_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_origination_gate_receipts_created_v1
			ON agency_origination_gate_receipts_v1(
				created_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_origination_gate_receipts_state_v1
			ON agency_origination_gate_receipts_v1(
				state, decision, created_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_origination_gate_receipts_v1_no_update
		BEFORE UPDATE ON agency_origination_gate_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination gate receipts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_gate_receipts_v1_no_delete
		BEFORE DELETE ON agency_origination_gate_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination gate receipts are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency activation gate schema: %w", err)
	}
	return nil
}
