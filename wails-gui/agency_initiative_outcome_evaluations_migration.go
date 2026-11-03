package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativeOutcomeEvaluations installs an immutable evaluation
// ledger beside execution-reported Initiative outcomes. Existing outcome rows
// remain receipts; evaluation rows are the only source of reusable outcome
// context.
func migrateAgencyInitiativeOutcomeEvaluations(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("agency initiative outcome evaluation migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_outcome_evaluations_v1 (
			id                          TEXT PRIMARY KEY,
			outcome_id                  TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			initiative_id               TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id                      TEXT NOT NULL DEFAULT '',
			idempotency_key             TEXT NOT NULL UNIQUE,
			status                      TEXT NOT NULL CHECK (status IN (
				'verified', 'partial', 'unknown', 'contradicted', 'awaiting_observation'
			)),
			evaluator_kind              TEXT NOT NULL,
			evaluator_version           TEXT NOT NULL,
			deterministic_projection_json TEXT NOT NULL,
			evidence_json               TEXT NOT NULL,
			verification_json           TEXT NOT NULL,
			eligibility_for_context     INTEGER NOT NULL CHECK (eligibility_for_context IN (0, 1)),
			summary                     TEXT NOT NULL,
			payload_json                TEXT NOT NULL,
			payload_hash                TEXT NOT NULL,
			created_at                  INTEGER NOT NULL,
			CHECK (created_at > 0),
			CHECK (eligibility_for_context = 0 OR status = 'verified')
		);
		CREATE INDEX IF NOT EXISTS idx_agency_outcome_evaluations_outcome_v1
			ON agency_initiative_outcome_evaluations_v1(outcome_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_outcome_evaluations_context_v1
			ON agency_initiative_outcome_evaluations_v1(eligibility_for_context, created_at DESC, id DESC);
		CREATE TRIGGER IF NOT EXISTS agency_initiative_outcome_evaluations_v1_no_update
		BEFORE UPDATE ON agency_initiative_outcome_evaluations_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative outcome evaluations are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_outcome_evaluations_v1_no_delete
		BEFORE DELETE ON agency_initiative_outcome_evaluations_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative outcome evaluations are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create agency initiative outcome evaluation ledger: %w", err)
	}
	return nil
}
