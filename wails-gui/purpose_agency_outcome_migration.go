package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func migratePurposeAgencyOutcomeReceipts(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Purpose Agency outcome receipt migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS purpose_agency_outcome_receipts_v1 (
			id                          TEXT PRIMARY KEY,
			idempotency_key             TEXT NOT NULL UNIQUE,
			evaluation_id               TEXT NOT NULL UNIQUE REFERENCES agency_initiative_outcome_evaluations_v1(id) ON DELETE RESTRICT,
			outcome_id                  TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			initiative_id               TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id                      TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			planning_run_id             TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			status                      TEXT NOT NULL CHECK (status IN ('resolved', 'not_applicable', 'no_match', 'ambiguous', 'stale', 'ineligible', 'provenance_failed')),
			purpose_open_loop_id        TEXT NOT NULL DEFAULT '',
			purpose_event_id            TEXT NOT NULL DEFAULT '',
			purpose_ref                 TEXT NOT NULL DEFAULT '',
			resolution_event_id         TEXT NOT NULL DEFAULT '',
			desired_outcome_hash        TEXT NOT NULL,
			evaluation_payload_hash     TEXT NOT NULL,
			planning_payload_hash       TEXT NOT NULL,
			reason                      TEXT NOT NULL,
			evidence_json               TEXT NOT NULL,
			payload_json                TEXT NOT NULL,
			payload_hash                TEXT NOT NULL,
			created_at                  INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_agency_outcome_receipts_created_v1
			ON purpose_agency_outcome_receipts_v1(created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_purpose_agency_outcome_receipts_purpose_v1
			ON purpose_agency_outcome_receipts_v1(purpose_open_loop_id, created_at DESC, id DESC);
		CREATE TRIGGER IF NOT EXISTS purpose_agency_outcome_receipts_v1_no_update
		BEFORE UPDATE ON purpose_agency_outcome_receipts_v1
		BEGIN SELECT RAISE(ABORT, 'purpose Agency outcome receipts are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_agency_outcome_receipts_v1_no_delete
		BEFORE DELETE ON purpose_agency_outcome_receipts_v1
		BEGIN SELECT RAISE(ABORT, 'purpose Agency outcome receipts are immutable'); END;
	`)
	if err != nil {
		return fmt.Errorf("migrate Purpose Agency outcome receipts: %w", err)
	}
	return nil
}

func migratePurposeAgencyOutcomeReceiptProvenanceStatus(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Purpose Agency outcome receipt status migration requires a transaction")
	}
	var schema string
	err := tx.QueryRow(`
		SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'purpose_agency_outcome_receipts_v1'
	`).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return migratePurposeAgencyOutcomeReceipts(tx)
	}
	if err != nil {
		return fmt.Errorf("inspect Purpose Agency outcome receipt status schema: %w", err)
	}
	if strings.Contains(strings.ToLower(schema), "'provenance_failed'") {
		return nil
	}
	if _, err := tx.Exec(`
		ALTER TABLE purpose_agency_outcome_receipts_v1
			RENAME TO purpose_agency_outcome_receipts_v1_legacy;
		DROP TRIGGER IF EXISTS purpose_agency_outcome_receipts_v1_no_update;
		DROP TRIGGER IF EXISTS purpose_agency_outcome_receipts_v1_no_delete;
		DROP INDEX IF EXISTS idx_purpose_agency_outcome_receipts_created_v1;
		DROP INDEX IF EXISTS idx_purpose_agency_outcome_receipts_purpose_v1;
	`); err != nil {
		return fmt.Errorf("prepare Purpose Agency outcome receipt status migration: %w", err)
	}
	if err := migratePurposeAgencyOutcomeReceipts(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO purpose_agency_outcome_receipts_v1 (
			id, idempotency_key, evaluation_id, outcome_id, initiative_id,
			run_id, planning_run_id, status, purpose_open_loop_id,
			purpose_event_id, purpose_ref, resolution_event_id,
			desired_outcome_hash, evaluation_payload_hash,
			planning_payload_hash, reason, evidence_json, payload_json,
			payload_hash, created_at
		)
		SELECT
			id, idempotency_key, evaluation_id, outcome_id, initiative_id,
			run_id, planning_run_id, status, purpose_open_loop_id,
			purpose_event_id, purpose_ref, resolution_event_id,
			desired_outcome_hash, evaluation_payload_hash,
			planning_payload_hash, reason, evidence_json, payload_json,
			payload_hash, created_at
		FROM purpose_agency_outcome_receipts_v1_legacy;
		DROP TABLE purpose_agency_outcome_receipts_v1_legacy;
	`); err != nil {
		return fmt.Errorf("copy Purpose Agency outcome receipts into expanded status schema: %w", err)
	}
	return nil
}
