package main

import (
	"database/sql"
	"errors"
	"fmt"
)

func migrateAgencyInitiativePlannerRollbackReviewProvenance(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner rollback review provenance migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_planner_rollback_review_provenance_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL CHECK (version = 'agency-initiative-planner-rollback-review-provenance-v1'),
			rollout_event_id                TEXT NOT NULL UNIQUE,
			rollout_event_payload_hash      TEXT NOT NULL,
			activation_event_id             TEXT NOT NULL,
			activation_event_payload_hash   TEXT NOT NULL,
			monitoring_review_id            TEXT NOT NULL UNIQUE,
			monitoring_review_payload_hash  TEXT NOT NULL,
			monitoring_manifest_hash        TEXT NOT NULL,
			review_rollout_head_event_id    TEXT NOT NULL,
			review_rollout_head_event_hash  TEXT NOT NULL,
			review_selected_prompt_version  TEXT NOT NULL,
			review_selected_planner_version TEXT NOT NULL,
			decision                        TEXT NOT NULL CHECK (decision = 'rollback_requested'),
			provenance_only                 INTEGER NOT NULL CHECK (provenance_only = 1),
			causality_claimed               INTEGER NOT NULL CHECK (causality_claimed = 0),
			rollback_authorization_effect   TEXT NOT NULL CHECK (rollback_authorization_effect = 'none'),
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
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			linked_at                       INTEGER NOT NULL CHECK (linked_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_rollback_review_provenance_time_v1
			ON agency_initiative_planner_rollback_review_provenance_v1(linked_at ASC, id ASC);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_rollback_review_provenance_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_rollback_review_provenance_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner rollback review provenance is immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_rollback_review_provenance_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_rollback_review_provenance_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner rollback review provenance is immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative planner rollback review provenance ledger: %w", err)
	}
	return nil
}
