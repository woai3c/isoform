package main

import (
	"database/sql"
	"errors"
	"fmt"
)

func migrateAgencyInitiativePlannerRolloutMonitoring(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner rollout monitoring migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_planner_rollout_observations_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL CHECK (version = 'agency-initiative-planner-rollout-observation-v1'),
			subject_key                     TEXT NOT NULL,
			source_snapshot_hash            TEXT NOT NULL,
			epoch_kind                      TEXT NOT NULL CHECK (epoch_kind IN ('compiled_root', 'activation', 'rollback')),
			epoch_id                        TEXT NOT NULL,
			prompt_version                  TEXT NOT NULL,
			planner_version                 TEXT NOT NULL,
			rendered_prompt_hash            TEXT NOT NULL,
			planning_run_id                 TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			planning_run_payload_hash       TEXT NOT NULL,
			rollout_event_id                TEXT NOT NULL,
			rollout_event_payload_hash      TEXT NOT NULL,
			release_decision_id             TEXT NOT NULL,
			release_decision_payload_hash   TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			initiative_creation_hash        TEXT NOT NULL,
			run_id                          TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			run_creation_hash               TEXT NOT NULL,
			outcome_id                      TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			outcome_payload_hash            TEXT NOT NULL,
			outcome_evaluation_id           TEXT NOT NULL REFERENCES agency_initiative_outcome_evaluations_v1(id) ON DELETE RESTRICT,
			outcome_evaluation_payload_hash TEXT NOT NULL,
			generation_json                 TEXT NOT NULL,
			generation_hash                 TEXT NOT NULL,
			planner_validity                TEXT NOT NULL CHECK (planner_validity = 'valid'),
			execution_containment           TEXT NOT NULL CHECK (execution_containment IN ('contained', 'blocked_or_failed', 'violation_detected', 'unknown')),
			result_support                  TEXT NOT NULL CHECK (result_support IN ('verified', 'partial', 'unknown', 'contradicted', 'awaiting_observation')),
			user_correction_status          TEXT NOT NULL CHECK (user_correction_status IN ('none', 'recorded')),
			correction_manifest_json        TEXT NOT NULL,
			correction_manifest_hash        TEXT NOT NULL,
			correction_adherence_status     TEXT NOT NULL CHECK (correction_adherence_status IN ('not_applicable', 'awaiting', 'partially_recorded', 'recorded')),
			adherence_manifest_json         TEXT NOT NULL,
			adherence_manifest_hash         TEXT NOT NULL,
			planner_selection_effect        TEXT NOT NULL CHECK (planner_selection_effect = 'none'),
			initiative_creation_effect      TEXT NOT NULL CHECK (initiative_creation_effect = 'none'),
			context_effect                  TEXT NOT NULL CHECK (context_effect = 'none'),
			memory_effect                   TEXT NOT NULL CHECK (memory_effect = 'none'),
			knowledge_effect                TEXT NOT NULL CHECK (knowledge_effect = 'none'),
			self_effect                     TEXT NOT NULL CHECK (self_effect = 'none'),
			purpose_effect                  TEXT NOT NULL CHECK (purpose_effect = 'none'),
			authority_effect                TEXT NOT NULL CHECK (authority_effect = 'none'),
			lifecycle_effect                TEXT NOT NULL CHECK (lifecycle_effect = 'none'),
			continuation_effect             TEXT NOT NULL CHECK (continuation_effect = 'none'),
			execution_permission_effect     TEXT NOT NULL CHECK (execution_permission_effect = 'none'),
			external_action_effect          TEXT NOT NULL CHECK (external_action_effect = 'none'),
			automatic_rollback              INTEGER NOT NULL CHECK (automatic_rollback = 0),
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			observed_at                     INTEGER NOT NULL CHECK (observed_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0),
			CHECK (
				(epoch_kind = 'compiled_root' AND epoch_id = 'compiled-root' AND rollout_event_id = '' AND rollout_event_payload_hash = '' AND release_decision_id = '' AND release_decision_payload_hash = '') OR
				(epoch_kind IN ('activation', 'rollback') AND epoch_id = rollout_event_id AND rollout_event_id <> '' AND rollout_event_payload_hash <> '' AND release_decision_id <> '' AND release_decision_payload_hash <> '')
			)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_rollout_observations_epoch_v1
			ON agency_initiative_planner_rollout_observations_v1(epoch_id, observed_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_rollout_observations_subject_v1
			ON agency_initiative_planner_rollout_observations_v1(subject_key, observed_at DESC, id DESC);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_rollout_observations_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_rollout_observations_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner rollout observations are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_rollout_observations_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_rollout_observations_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner rollout observations are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative planner rollout monitoring ledger: %w", err)
	}
	return nil
}
