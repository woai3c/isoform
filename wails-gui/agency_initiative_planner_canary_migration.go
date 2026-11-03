package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativePlannerCanary installs the append-only, no-effect
// planner shadow experiment ledgers.
func migrateAgencyInitiativePlannerCanary(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner canary migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_planner_canary_experiments_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			calibration_projection_version  TEXT NOT NULL,
			cohort_key                      TEXT NOT NULL,
			worker_prompt_version           TEXT NOT NULL,
			worker_planner_version          TEXT NOT NULL,
			worker_model                    TEXT NOT NULL,
			source_work_kind                TEXT NOT NULL,
			sample_manifest_json            TEXT NOT NULL,
			sample_manifest_hash            TEXT NOT NULL,
			sample_count                    INTEGER NOT NULL CHECK (sample_count > 0),
			distinct_outcome_count          INTEGER NOT NULL CHECK (distinct_outcome_count > 0),
			distinct_correction_count       INTEGER NOT NULL CHECK (distinct_correction_count > 0),
			independent_result_count        INTEGER NOT NULL CHECK (independent_result_count >= 0),
			minimum_samples                 INTEGER NOT NULL CHECK (minimum_samples > 0),
			minimum_distinct_outcomes       INTEGER NOT NULL CHECK (minimum_distinct_outcomes > 0),
			minimum_distinct_corrections    INTEGER NOT NULL CHECK (minimum_distinct_corrections > 0),
			minimum_independent_results     INTEGER NOT NULL CHECK (minimum_independent_results > 0),
			baseline_prompt_version         TEXT NOT NULL,
			baseline_planner_version        TEXT NOT NULL,
			candidate_prompt_version        TEXT NOT NULL,
			candidate_planner_version       TEXT NOT NULL,
			automatic_selection             INTEGER NOT NULL CHECK (automatic_selection = 0),
			eligibility_for_context         INTEGER NOT NULL CHECK (eligibility_for_context = 0),
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			continuation_effect             TEXT NOT NULL CHECK (continuation_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			admitted_at                     INTEGER NOT NULL CHECK (admitted_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_canary_experiments_cohort_v1
			ON agency_initiative_planner_canary_experiments_v1(
				cohort_key, candidate_prompt_version, admitted_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS agency_initiative_planner_canary_cases_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			experiment_id                   TEXT NOT NULL REFERENCES agency_initiative_planner_canary_experiments_v1(id) ON DELETE RESTRICT,
			experiment_payload_hash         TEXT NOT NULL,
			calibration_sample_id           TEXT NOT NULL REFERENCES agency_initiative_feedback_calibration_samples_v1(id) ON DELETE RESTRICT,
			calibration_sample_payload_hash TEXT NOT NULL,
			case_ordinal                    INTEGER NOT NULL CHECK (case_ordinal >= 0),
			application_id                  TEXT NOT NULL,
			application_payload_hash        TEXT NOT NULL,
			feedback_id                     TEXT NOT NULL,
			feedback_payload_hash           TEXT NOT NULL,
			statement_hash                  TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL,
			initiative_creation_hash        TEXT NOT NULL,
			run_id                          TEXT NOT NULL,
			run_creation_hash               TEXT NOT NULL,
			worker_planning_run_id          TEXT NOT NULL,
			worker_planning_payload_hash    TEXT NOT NULL,
			outcome_id                      TEXT NOT NULL,
			outcome_payload_hash            TEXT NOT NULL,
			outcome_evaluation_id           TEXT NOT NULL,
			outcome_evaluation_payload_hash TEXT NOT NULL,
			literal_terms_json              TEXT NOT NULL,
			literal_terms_hash              TEXT NOT NULL,
			baseline_prompt_version         TEXT NOT NULL,
			baseline_planner_version        TEXT NOT NULL,
			baseline_prompt                 TEXT NOT NULL,
			baseline_prompt_hash            TEXT NOT NULL,
			candidate_prompt_version        TEXT NOT NULL,
			candidate_planner_version       TEXT NOT NULL,
			candidate_prompt                TEXT NOT NULL,
			candidate_prompt_hash           TEXT NOT NULL,
			eligibility_for_context         INTEGER NOT NULL CHECK (eligibility_for_context = 0),
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			continuation_effect             TEXT NOT NULL CHECK (continuation_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			frozen_at                       INTEGER NOT NULL CHECK (frozen_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (experiment_id, calibration_sample_id),
			UNIQUE (experiment_id, case_ordinal)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_canary_cases_experiment_v1
			ON agency_initiative_planner_canary_cases_v1(
				experiment_id, case_ordinal ASC, id ASC
			);

		CREATE TABLE IF NOT EXISTS agency_initiative_planner_canary_attempts_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			experiment_id                   TEXT NOT NULL REFERENCES agency_initiative_planner_canary_experiments_v1(id) ON DELETE RESTRICT,
			experiment_payload_hash         TEXT NOT NULL,
			case_id                         TEXT NOT NULL REFERENCES agency_initiative_planner_canary_cases_v1(id) ON DELETE RESTRICT,
			case_payload_hash               TEXT NOT NULL,
			calibration_sample_id           TEXT NOT NULL REFERENCES agency_initiative_feedback_calibration_samples_v1(id) ON DELETE RESTRICT,
			calibration_sample_payload_hash TEXT NOT NULL,
			variant                         TEXT NOT NULL CHECK (variant IN ('baseline', 'candidate')),
			model                           TEXT NOT NULL,
			prompt_version                  TEXT NOT NULL,
			planner_version                 TEXT NOT NULL,
			prompt                          TEXT NOT NULL,
			prompt_hash                     TEXT NOT NULL,
			raw_output                      TEXT NOT NULL,
			raw_output_hash                 TEXT NOT NULL,
			status                          TEXT NOT NULL CHECK (status IN ('valid_output', 'invalid_output', 'model_error')),
			decision                        TEXT NOT NULL,
			plan_json                       TEXT NOT NULL,
			plan_hash                       TEXT NOT NULL,
			matched_terms_json              TEXT NOT NULL,
			matched_terms_hash              TEXT NOT NULL,
			missing_terms_json              TEXT NOT NULL,
			missing_terms_hash              TEXT NOT NULL,
			required_capabilities_json      TEXT NOT NULL,
			required_capabilities_hash      TEXT NOT NULL,
			observation_mode                TEXT NOT NULL,
			observation_source              TEXT NOT NULL,
			validation_error                TEXT NOT NULL,
			execution_attempted             INTEGER NOT NULL CHECK (execution_attempted = 0),
			eligibility_for_context         INTEGER NOT NULL CHECK (eligibility_for_context = 0),
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			continuation_effect             TEXT NOT NULL CHECK (continuation_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			attempted_at                    INTEGER NOT NULL CHECK (attempted_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (case_id, variant)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_canary_attempts_experiment_v1
			ON agency_initiative_planner_canary_attempts_v1(
				experiment_id, case_id, variant, attempted_at ASC
			);

		CREATE TABLE IF NOT EXISTS agency_initiative_planner_canary_comparisons_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			experiment_id                   TEXT NOT NULL REFERENCES agency_initiative_planner_canary_experiments_v1(id) ON DELETE RESTRICT,
			experiment_payload_hash         TEXT NOT NULL,
			case_id                         TEXT NOT NULL UNIQUE REFERENCES agency_initiative_planner_canary_cases_v1(id) ON DELETE RESTRICT,
			case_payload_hash               TEXT NOT NULL,
			calibration_sample_id           TEXT NOT NULL REFERENCES agency_initiative_feedback_calibration_samples_v1(id) ON DELETE RESTRICT,
			calibration_sample_payload_hash TEXT NOT NULL,
			baseline_attempt_id             TEXT NOT NULL UNIQUE REFERENCES agency_initiative_planner_canary_attempts_v1(id) ON DELETE RESTRICT,
			baseline_attempt_payload_hash   TEXT NOT NULL,
			candidate_attempt_id            TEXT NOT NULL UNIQUE REFERENCES agency_initiative_planner_canary_attempts_v1(id) ON DELETE RESTRICT,
			candidate_attempt_payload_hash  TEXT NOT NULL,
			validity_relation               TEXT NOT NULL CHECK (validity_relation IN (
				'both_valid', 'baseline_only_valid', 'candidate_only_valid', 'neither_valid'
			)),
			decision_relation               TEXT NOT NULL CHECK (decision_relation IN ('same', 'different', 'not_comparable')),
			literal_coverage_relation       TEXT NOT NULL CHECK (literal_coverage_relation IN (
				'candidate_more', 'baseline_more', 'equal', 'not_comparable'
			)),
			capability_scope_relation       TEXT NOT NULL CHECK (capability_scope_relation IN (
				'same', 'candidate_broader', 'candidate_narrower', 'different', 'not_comparable'
			)),
			observation_contract_relation   TEXT NOT NULL CHECK (observation_contract_relation IN ('same', 'different', 'not_comparable')),
			baseline_matched_term_count     INTEGER NOT NULL CHECK (baseline_matched_term_count >= 0),
			candidate_matched_term_count    INTEGER NOT NULL CHECK (candidate_matched_term_count >= 0),
			automatic_selection             INTEGER NOT NULL CHECK (automatic_selection = 0),
			eligibility_for_context         INTEGER NOT NULL CHECK (eligibility_for_context = 0),
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			continuation_effect             TEXT NOT NULL CHECK (continuation_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			compared_at                     INTEGER NOT NULL CHECK (compared_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_canary_comparisons_experiment_v1
			ON agency_initiative_planner_canary_comparisons_v1(
				experiment_id, compared_at ASC, id ASC
			);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_experiments_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_canary_experiments_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary experiments are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_experiments_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_canary_experiments_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary experiments are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_cases_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_canary_cases_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary cases are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_cases_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_canary_cases_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary cases are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_attempts_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_canary_attempts_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary attempts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_attempts_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_canary_attempts_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary attempts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_comparisons_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_canary_comparisons_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary comparisons are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_canary_comparisons_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_canary_comparisons_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner canary comparisons are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative planner canary ledgers: %w", err)
	}
	return nil
}
