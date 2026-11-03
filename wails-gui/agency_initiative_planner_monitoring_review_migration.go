package main

import (
	"database/sql"
	"errors"
	"fmt"
)

func migrateAgencyInitiativePlannerMonitoringReviews(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner monitoring review migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_planner_monitoring_reviews_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL CHECK (version = 'agency-initiative-planner-monitoring-review-v1'),
			manifest_version                TEXT NOT NULL CHECK (manifest_version = 'agency-initiative-planner-monitoring-review-manifest-v1'),
			monitoring_projection_version   TEXT NOT NULL,
			rollout_projection_version      TEXT NOT NULL,
			manifest_json                   TEXT NOT NULL,
			manifest_hash                   TEXT NOT NULL,
			source_fingerprint              TEXT NOT NULL,
			observation_revision_count      INTEGER NOT NULL CHECK (observation_revision_count >= 0),
			current_observation_count       INTEGER NOT NULL CHECK (current_observation_count > 0),
			exclusion_count                 INTEGER NOT NULL CHECK (exclusion_count >= 0),
			rollout_state                   TEXT NOT NULL,
			rollout_head_event_id           TEXT NOT NULL,
			rollout_head_event_payload_hash TEXT NOT NULL,
			selected_prompt_version         TEXT NOT NULL,
			selected_planner_version        TEXT NOT NULL,
			decision                        TEXT NOT NULL CHECK (decision IN ('continue_observing', 'investigate', 'rollback_requested')),
			rationale                       TEXT NOT NULL,
			previous_review_id              TEXT NOT NULL,
			previous_review_payload_hash    TEXT NOT NULL,
			reviewer_kind                   TEXT NOT NULL CHECK (reviewer_kind = 'human_developer'),
			review_source                   TEXT NOT NULL CHECK (review_source = 'native_agency_runtime_devtools'),
			evidence_reviewed               INTEGER NOT NULL CHECK (evidence_reviewed = 1),
			confirmed_no_causal_inference   INTEGER NOT NULL CHECK (confirmed_no_causal_inference = 1),
			confirmed_no_automatic_action   INTEGER NOT NULL CHECK (confirmed_no_automatic_action = 1),
			automatic_decision              INTEGER NOT NULL CHECK (automatic_decision = 0),
			causality_claimed               INTEGER NOT NULL CHECK (causality_claimed = 0),
			planner_selection_effect        TEXT NOT NULL CHECK (planner_selection_effect = 'none'),
			production_rollback_effect      TEXT NOT NULL CHECK (production_rollback_effect = 'none'),
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
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			reviewed_at                     INTEGER NOT NULL CHECK (reviewed_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0),
			CHECK (
				(rollout_head_event_id = '' AND rollout_head_event_payload_hash = '') OR
				(rollout_head_event_id <> '' AND rollout_head_event_payload_hash <> '')
			),
			CHECK (
				(previous_review_id = '' AND previous_review_payload_hash = '') OR
				(previous_review_id <> '' AND previous_review_payload_hash <> '')
			)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_monitoring_reviews_time_v1
			ON agency_initiative_planner_monitoring_reviews_v1(reviewed_at ASC, id ASC);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_monitoring_reviews_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_monitoring_reviews_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner monitoring reviews are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_monitoring_reviews_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_monitoring_reviews_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner monitoring reviews are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative planner monitoring review ledger: %w", err)
	}
	return nil
}
