package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativeFeedbackAdherence installs append-only diagnostic
// ledgers. These records measure whether later work followed explicit result
// feedback; they are never context, authority, or execution permission.
func migrateAgencyInitiativeFeedbackAdherence(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative feedback adherence migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_feedback_adherence_attempts_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			application_id                  TEXT NOT NULL REFERENCES agency_initiative_feedback_applications_v1(id) ON DELETE RESTRICT,
			application_payload_hash        TEXT NOT NULL,
			feedback_id                     TEXT NOT NULL REFERENCES agency_initiative_corrections_v2(id) ON DELETE RESTRICT,
			feedback_payload_hash           TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id                          TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			worker_planning_run_id          TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			worker_planning_payload_hash    TEXT NOT NULL,
			outcome_id                      TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			outcome_payload_hash            TEXT NOT NULL,
			outcome_evaluation_id           TEXT NOT NULL REFERENCES agency_initiative_outcome_evaluations_v1(id) ON DELETE RESTRICT,
			outcome_evaluation_payload_hash TEXT NOT NULL,
			frozen_input_json               TEXT NOT NULL,
			frozen_input_hash               TEXT NOT NULL,
			allowlisted_evidence_json       TEXT NOT NULL,
			allowlisted_evidence_hash       TEXT NOT NULL,
			model                           TEXT NOT NULL,
			prompt_version                  TEXT NOT NULL,
			prompt                          TEXT NOT NULL,
			prompt_hash                     TEXT NOT NULL,
			raw_output                      TEXT NOT NULL DEFAULT '',
			raw_output_hash                 TEXT NOT NULL DEFAULT '',
			status                          TEXT NOT NULL CHECK (status IN (
				'model_error', 'invalid_output', 'evaluation_recorded'
			)),
			rejected_evidence_json          TEXT NOT NULL,
			error                           TEXT NOT NULL DEFAULT '',
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			attempted_at                    INTEGER NOT NULL,
			created_at                      INTEGER NOT NULL,
			UNIQUE (application_id, outcome_evaluation_id, version),
			CHECK (attempted_at > 0),
			CHECK (created_at > 0),
			CHECK ((raw_output = '') = (raw_output_hash = '')),
			CHECK ((status = 'evaluation_recorded' AND error = '') OR status <> 'evaluation_recorded')
		);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_attempts_application_v1
			ON agency_initiative_feedback_adherence_attempts_v1(application_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_attempts_outcome_v1
			ON agency_initiative_feedback_adherence_attempts_v1(outcome_evaluation_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_attempts_outcome_record_v1
			ON agency_initiative_feedback_adherence_attempts_v1(outcome_id, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_initiative_feedback_adherence_evaluations_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			attempt_id                      TEXT NOT NULL UNIQUE REFERENCES agency_initiative_feedback_adherence_attempts_v1(id) ON DELETE RESTRICT,
			application_id                  TEXT NOT NULL REFERENCES agency_initiative_feedback_applications_v1(id) ON DELETE RESTRICT,
			application_payload_hash        TEXT NOT NULL,
			feedback_id                     TEXT NOT NULL REFERENCES agency_initiative_corrections_v2(id) ON DELETE RESTRICT,
			statement_hash                  TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id                          TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			outcome_id                      TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			outcome_payload_hash            TEXT NOT NULL,
			outcome_evaluation_id           TEXT NOT NULL REFERENCES agency_initiative_outcome_evaluations_v1(id) ON DELETE RESTRICT,
			outcome_evaluation_payload_hash TEXT NOT NULL,
			output_version                  TEXT NOT NULL,
			plan_adherence                  TEXT NOT NULL CHECK (plan_adherence IN (
				'applied', 'partly_applied', 'not_applied', 'unknown'
			)),
			plan_confidence                 REAL NOT NULL CHECK (plan_confidence >= 0 AND plan_confidence <= 1),
			plan_reason                     TEXT NOT NULL,
			plan_evidence_json              TEXT NOT NULL,
			result_adherence                TEXT NOT NULL CHECK (result_adherence IN (
				'applied', 'partly_applied', 'not_applied', 'unknown'
			)),
			result_confidence               REAL NOT NULL CHECK (result_confidence >= 0 AND result_confidence <= 1),
			result_reason                   TEXT NOT NULL,
			result_evidence_json            TEXT NOT NULL,
			result_support                  TEXT NOT NULL CHECK (result_support IN (
				'verified', 'partial', 'contradicted', 'awaiting', 'unknown'
			)),
			result_support_confidence       REAL NOT NULL CHECK (result_support_confidence >= 0 AND result_support_confidence <= 1),
			result_support_reason           TEXT NOT NULL,
			result_support_evidence_json    TEXT NOT NULL,
			semantic_evidence_status        TEXT NOT NULL CHECK (semantic_evidence_status IN (
				'available', 'insufficient'
			)),
			semantic_matched_terms_json     TEXT NOT NULL,
			frozen_input_hash               TEXT NOT NULL,
			prompt_hash                     TEXT NOT NULL,
			raw_output_hash                 TEXT NOT NULL,
			reasoning_status                TEXT NOT NULL CHECK (reasoning_status = 'model_inferred_from_frozen_evidence'),
			eligibility_for_context         INTEGER NOT NULL CHECK (eligibility_for_context = 0),
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			created_at                      INTEGER NOT NULL,
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_evaluations_application_v1
			ON agency_initiative_feedback_adherence_evaluations_v1(application_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_evaluations_outcome_v1
			ON agency_initiative_feedback_adherence_evaluations_v1(outcome_evaluation_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_evaluations_outcome_record_v1
			ON agency_initiative_feedback_adherence_evaluations_v1(outcome_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_adherence_evaluations_calibration_v1
			ON agency_initiative_feedback_adherence_evaluations_v1(plan_adherence, result_adherence, result_support);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_adherence_attempts_v1_no_update
		BEFORE UPDATE ON agency_initiative_feedback_adherence_attempts_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback adherence attempts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_adherence_attempts_v1_no_delete
		BEFORE DELETE ON agency_initiative_feedback_adherence_attempts_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback adherence attempts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_adherence_evaluations_v1_no_update
		BEFORE UPDATE ON agency_initiative_feedback_adherence_evaluations_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback adherence evaluations are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_adherence_evaluations_v1_no_delete
		BEFORE DELETE ON agency_initiative_feedback_adherence_evaluations_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback adherence evaluations are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative feedback adherence ledgers: %w", err)
	}
	return nil
}
