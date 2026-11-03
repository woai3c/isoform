package main

import (
	"database/sql"
	"fmt"
)

// migrateAgencyActivationGatePolicyV4 preserves historical receipts while
// admitting the immutable periodic private-work policy generation.
func migrateAgencyActivationGatePolicyV4(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("Agency activation gate v4 migration requires a transaction")
	}
	_, err := tx.Exec(`
		DROP TRIGGER IF EXISTS agency_origination_gate_receipts_v1_no_update;
		DROP TRIGGER IF EXISTS agency_origination_gate_receipts_v1_no_delete;
		DROP INDEX IF EXISTS idx_agency_origination_gate_receipts_created_v1;
		DROP INDEX IF EXISTS idx_agency_origination_gate_receipts_state_v1;

		ALTER TABLE agency_origination_gate_receipts_v1
			RENAME TO agency_origination_gate_receipts_v1_before_policy_v4;

		CREATE TABLE agency_origination_gate_receipts_v1 (
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
			CHECK (policy_version IN (
				'agency-activation-gate-v1',
				'agency-activation-gate-v2',
				'agency-activation-gate-v3',
				'agency-activation-gate-v4'
			)),
			CHECK (state IN ('ready', 'activation_required', 'invalid')),
			CHECK (decision IN ('allow', 'suppress', 'deny')),
			CHECK (wake_reason IN ('periodic', 'meaningful_state_change')),
			CHECK (capacity_route IN ('', 'background_understanding')),
			CHECK (observed_at > 0),
			CHECK (created_at > 0)
		);

		INSERT INTO agency_origination_gate_receipts_v1 (
			id, idempotency_key, policy_version, state, decision, reason,
			wake_reason, active_envelope_id, latest_envelope_id,
			allowed_candidate_kinds_json, capacity_route, state_fingerprint,
			state_json, payload_json, payload_hash, observed_at, created_at
		)
		SELECT
			id, idempotency_key, policy_version, state, decision, reason,
			wake_reason, active_envelope_id, latest_envelope_id,
			allowed_candidate_kinds_json, capacity_route, state_fingerprint,
			state_json, payload_json, payload_hash, observed_at, created_at
		FROM agency_origination_gate_receipts_v1_before_policy_v4;

		DROP TABLE agency_origination_gate_receipts_v1_before_policy_v4;

		CREATE INDEX idx_agency_origination_gate_receipts_created_v1
			ON agency_origination_gate_receipts_v1(
				created_at DESC, id DESC
			);
		CREATE INDEX idx_agency_origination_gate_receipts_state_v1
			ON agency_origination_gate_receipts_v1(
				state, decision, created_at DESC, id DESC
			);
		CREATE TRIGGER agency_origination_gate_receipts_v1_no_update
		BEFORE UPDATE ON agency_origination_gate_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination gate receipts are immutable');
		END;
		CREATE TRIGGER agency_origination_gate_receipts_v1_no_delete
		BEFORE DELETE ON agency_origination_gate_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination gate receipts are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("migrate Agency activation gate policy v4: %w", err)
	}
	return nil
}
