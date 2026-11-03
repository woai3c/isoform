package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativePlannerReleaseDecisions installs the append-only
// human judgment ledger over complete planner-canary evidence. A decision is
// deliberately incapable of selecting or activating a production planner.
func migrateAgencyInitiativePlannerReleaseDecisions(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner release decision migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_planner_release_decisions_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL,
			experiment_id                   TEXT NOT NULL REFERENCES agency_initiative_planner_canary_experiments_v1(id) ON DELETE RESTRICT,
			experiment_payload_hash         TEXT NOT NULL,
			cohort_key                      TEXT NOT NULL,
			baseline_prompt_version         TEXT NOT NULL,
			baseline_planner_version        TEXT NOT NULL,
			candidate_prompt_version        TEXT NOT NULL,
			candidate_planner_version       TEXT NOT NULL,
			evidence_manifest_version       TEXT NOT NULL,
			evidence_manifest_json          TEXT NOT NULL,
			evidence_manifest_hash          TEXT NOT NULL,
			case_count                      INTEGER NOT NULL CHECK (case_count > 0),
			attempt_count                   INTEGER NOT NULL CHECK (attempt_count = case_count * 2),
			comparison_count                INTEGER NOT NULL CHECK (comparison_count = case_count),
			decision                        TEXT NOT NULL CHECK (decision IN ('hold', 'reject', 'approve_for_rollout')),
			rationale                       TEXT NOT NULL CHECK (length(trim(rationale)) > 0 AND length(rationale) <= 4000),
			previous_decision_id            TEXT NOT NULL,
			previous_decision_payload_hash  TEXT NOT NULL,
			reviewer_kind                   TEXT NOT NULL CHECK (reviewer_kind = 'human_developer'),
			review_source                   TEXT NOT NULL CHECK (review_source = 'native_agency_runtime_devtools'),
			evidence_reviewed               INTEGER NOT NULL CHECK (evidence_reviewed = 1),
			confirmed_no_automatic_selection INTEGER NOT NULL CHECK (confirmed_no_automatic_selection = 1),
			confirmed_no_activation         INTEGER NOT NULL CHECK (confirmed_no_activation = 1),
			automatic_selection             INTEGER NOT NULL CHECK (automatic_selection = 0),
			planner_selection_effect        TEXT NOT NULL CHECK (planner_selection_effect = 'none'),
			production_activation_effect    TEXT NOT NULL CHECK (production_activation_effect = 'none'),
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
			decided_at                      INTEGER NOT NULL CHECK (decided_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0),
			CHECK (
				(previous_decision_id = '' AND previous_decision_payload_hash = '') OR
				(previous_decision_id <> '' AND previous_decision_payload_hash <> '')
			),
			UNIQUE (experiment_id, previous_decision_id)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_release_decisions_experiment_v1
			ON agency_initiative_planner_release_decisions_v1(
				experiment_id, decided_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_release_decisions_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_release_decisions_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner release decisions are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_release_decisions_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_release_decisions_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner release decisions are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative planner release decision ledger: %w", err)
	}
	return nil
}
