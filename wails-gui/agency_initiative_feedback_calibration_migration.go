package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativeFeedbackCalibration installs the append-only bridge
// between validated adherence evidence and historical planner cohorts.
func migrateAgencyInitiativeFeedbackCalibration(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative feedback calibration migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_feedback_calibration_samples_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			adherence_evaluation_id         TEXT NOT NULL UNIQUE REFERENCES agency_initiative_feedback_adherence_evaluations_v1(id) ON DELETE RESTRICT,
			adherence_evaluation_hash       TEXT NOT NULL,
			adherence_attempt_id            TEXT NOT NULL REFERENCES agency_initiative_feedback_adherence_attempts_v1(id) ON DELETE RESTRICT,
			adherence_attempt_hash          TEXT NOT NULL,
			application_id                  TEXT NOT NULL REFERENCES agency_initiative_feedback_applications_v1(id) ON DELETE RESTRICT,
			application_payload_hash        TEXT NOT NULL,
			feedback_id                     TEXT NOT NULL REFERENCES agency_initiative_corrections_v2(id) ON DELETE RESTRICT,
			feedback_payload_hash           TEXT NOT NULL,
			feedback_assessment             TEXT NOT NULL,
			statement_hash                  TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			initiative_creation_hash        TEXT NOT NULL,
			run_id                          TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			run_creation_hash               TEXT NOT NULL,
			worker_planning_run_id          TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			worker_planning_payload_hash    TEXT NOT NULL,
			worker_model                    TEXT NOT NULL,
			worker_planner_version          TEXT NOT NULL,
			worker_prompt_version           TEXT NOT NULL,
			source_work_kind                TEXT NOT NULL,
			outcome_id                      TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			outcome_payload_hash            TEXT NOT NULL,
			outcome_evaluation_id           TEXT NOT NULL REFERENCES agency_initiative_outcome_evaluations_v1(id) ON DELETE RESTRICT,
			outcome_evaluation_payload_hash TEXT NOT NULL,
			adherence_evaluator_model       TEXT NOT NULL,
			adherence_prompt_version        TEXT NOT NULL,
			plan_adherence                  TEXT NOT NULL CHECK (plan_adherence IN (
				'applied', 'partly_applied', 'not_applied', 'unknown'
			)),
			result_adherence                TEXT NOT NULL CHECK (result_adherence IN (
				'applied', 'partly_applied', 'not_applied', 'unknown'
			)),
			result_support                  TEXT NOT NULL CHECK (result_support IN (
				'verified', 'partial', 'contradicted', 'awaiting', 'unknown'
			)),
			semantic_evidence_status        TEXT NOT NULL CHECK (semantic_evidence_status IN (
				'available', 'insufficient'
			)),
			independent_result_evidence     INTEGER NOT NULL CHECK (independent_result_evidence IN (0, 1)),
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
			recorded_at                     INTEGER NOT NULL,
			created_at                      INTEGER NOT NULL,
			CHECK (recorded_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_calibration_cohort_v1
			ON agency_initiative_feedback_calibration_samples_v1(
				worker_prompt_version, worker_planner_version, worker_model,
				source_work_kind, recorded_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_calibration_pair_v1
			ON agency_initiative_feedback_calibration_samples_v1(
				application_id, outcome_id, recorded_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_calibration_feedback_v1
			ON agency_initiative_feedback_calibration_samples_v1(
				feedback_id, recorded_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_calibration_samples_v1_no_update
		BEFORE UPDATE ON agency_initiative_feedback_calibration_samples_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback calibration samples are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_calibration_samples_v1_no_delete
		BEFORE DELETE ON agency_initiative_feedback_calibration_samples_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback calibration samples are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative feedback calibration ledger: %w", err)
	}
	return nil
}
