package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativeFeedbackApplications installs the immutable proof that
// one explicit result correction reached one later worker planning run before
// execution. The ledger is diagnostic evidence, never execution permission.
func migrateAgencyInitiativeFeedbackApplications(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative feedback application migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_feedback_applications_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			feedback_id                     TEXT NOT NULL REFERENCES agency_initiative_corrections_v2(id) ON DELETE RESTRICT,
			feedback_payload_hash           TEXT NOT NULL,
			statement_hash                  TEXT NOT NULL,
			assessment                      TEXT NOT NULL CHECK (assessment IN (
				'useful', 'partly_useful', 'not_useful', 'incorrect'
			)),
			source_outcome_id               TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			source_evaluation_id            TEXT NOT NULL DEFAULT '',
			source_evaluation_payload_hash  TEXT NOT NULL DEFAULT '',
			evidence_ref                    TEXT NOT NULL,
			source_planning_run_id          TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			source_planning_payload_hash    TEXT NOT NULL,
			source_work_kind                TEXT NOT NULL CHECK (source_work_kind IN (
				'origination_candidate', 'continuation_plan'
			)),
			source_work_semantic_hash       TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			initiative_creation_hash        TEXT NOT NULL,
			run_id                          TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			run_creation_hash               TEXT NOT NULL,
			worker_planning_run_id          TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			worker_planning_payload_hash    TEXT NOT NULL,
			worker_plan_hash                TEXT NOT NULL,
			application_status              TEXT NOT NULL CHECK (application_status IN (
				'planned', 'no_useful_work'
			)),
			reasoning_status                TEXT NOT NULL CHECK (reasoning_status = 'unknown'),
			task_context_version            TEXT NOT NULL,
			feedback_contract_version       TEXT NOT NULL,
			manifest_version                TEXT NOT NULL,
			source_planner_version          TEXT NOT NULL,
			source_prompt_version           TEXT NOT NULL,
			worker_planner_version          TEXT NOT NULL,
			worker_prompt_version           TEXT NOT NULL,
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			applied_at                      INTEGER NOT NULL,
			created_at                      INTEGER NOT NULL,
			UNIQUE (worker_planning_run_id, feedback_id),
			CHECK (applied_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_applications_worker_v1
			ON agency_initiative_feedback_applications_v1(worker_planning_run_id, created_at ASC, id ASC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_applications_feedback_v1
			ON agency_initiative_feedback_applications_v1(feedback_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_feedback_applications_run_v1
			ON agency_initiative_feedback_applications_v1(run_id, created_at DESC, id DESC);
		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_applications_v1_no_update
		BEFORE UPDATE ON agency_initiative_feedback_applications_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback applications are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_feedback_applications_v1_no_delete
		BEFORE DELETE ON agency_initiative_feedback_applications_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative feedback applications are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative feedback application ledger: %w", err)
	}
	return nil
}
