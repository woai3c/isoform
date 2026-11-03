package main

import (
	"database/sql"
	"errors"
	"fmt"
)

func migrateAgencyInitiativePlannerRollout(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner rollout migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_planner_rollout_events_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			version                         TEXT NOT NULL CHECK (version = 'agency-initiative-planner-rollout-event-v1'),
			action                          TEXT NOT NULL CHECK (action IN ('activate_candidate', 'rollback_to_baseline')),
			target_prompt_version           TEXT NOT NULL,
			target_planner_version          TEXT NOT NULL,
			previous_prompt_version         TEXT NOT NULL,
			previous_planner_version        TEXT NOT NULL,
			previous_event_id               TEXT NOT NULL,
			previous_event_payload_hash     TEXT NOT NULL,
			release_decision_id             TEXT NOT NULL,
			release_decision_payload_hash   TEXT NOT NULL,
			experiment_id                   TEXT NOT NULL,
			experiment_payload_hash         TEXT NOT NULL,
			evidence_manifest_hash          TEXT NOT NULL,
			activation_event_id             TEXT NOT NULL,
			activation_event_payload_hash   TEXT NOT NULL,
			reviewer_kind                   TEXT NOT NULL CHECK (reviewer_kind = 'human_developer'),
			review_source                   TEXT NOT NULL CHECK (review_source = 'native_agency_runtime_devtools'),
			rationale                       TEXT NOT NULL CHECK (length(trim(rationale)) > 0 AND length(rationale) <= 4000),
			confirmed_release_and_contract  INTEGER NOT NULL CHECK (confirmed_release_and_contract = 1),
			confirmed_future_planning_only  INTEGER NOT NULL CHECK (confirmed_future_planning_only = 1),
			confirmed_no_authority          INTEGER NOT NULL CHECK (confirmed_no_authority = 1),
			automatic_activation            INTEGER NOT NULL CHECK (automatic_activation = 0),
			planner_selection_effect        TEXT NOT NULL CHECK (planner_selection_effect = 'future_planning_only'),
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
			occurred_at                     INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at                      INTEGER NOT NULL CHECK (created_at > 0),
			CHECK (
				(previous_event_id = '' AND previous_event_payload_hash = '') OR
				(previous_event_id <> '' AND previous_event_payload_hash <> '')
			),
			CHECK (
				(action = 'activate_candidate' AND activation_event_id = '' AND activation_event_payload_hash = '') OR
				(action = 'rollback_to_baseline' AND activation_event_id <> '' AND activation_event_payload_hash <> '')
			),
			UNIQUE (previous_event_id)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planner_rollout_events_created_v1
			ON agency_initiative_planner_rollout_events_v1(occurred_at DESC, id DESC);

		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_rollout_events_v1_no_update
		BEFORE UPDATE ON agency_initiative_planner_rollout_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner rollout events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_planner_rollout_events_v1_no_delete
		BEFORE DELETE ON agency_initiative_planner_rollout_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'Agency Initiative planner rollout events are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Agency Initiative planner rollout ledger: %w", err)
	}
	return migrateAgencyInitiativePlannerRunProvenanceColumns(tx)
}

func migrateAgencyInitiativePlannerRunProvenanceColumns(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Agency Initiative planner provenance migration requires transaction")
	}
	planningColumns := []struct {
		name string
		sql  string
	}{
		{
			name: "planner_rollout_event_id",
			sql:  `ALTER TABLE agency_planning_runs_v2 ADD COLUMN planner_rollout_event_id TEXT NOT NULL DEFAULT ''`,
		},
		{
			name: "planner_rollout_event_payload_hash",
			sql:  `ALTER TABLE agency_planning_runs_v2 ADD COLUMN planner_rollout_event_payload_hash TEXT NOT NULL DEFAULT ''`,
		},
		{
			name: "planner_release_decision_id",
			sql:  `ALTER TABLE agency_planning_runs_v2 ADD COLUMN planner_release_decision_id TEXT NOT NULL DEFAULT ''`,
		},
		{
			name: "planner_release_decision_payload_hash",
			sql:  `ALTER TABLE agency_planning_runs_v2 ADD COLUMN planner_release_decision_payload_hash TEXT NOT NULL DEFAULT ''`,
		},
		{
			name: "rendered_prompt_hash",
			sql:  `ALTER TABLE agency_planning_runs_v2 ADD COLUMN rendered_prompt_hash TEXT NOT NULL DEFAULT ''`,
		},
	}
	for _, column := range planningColumns {
		exists, err := agencyInitiativePlannerRolloutColumnExists(
			tx,
			"agency_planning_runs_v2",
			column.name,
		)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(column.sql); err != nil {
			return fmt.Errorf(
				"add Agency Initiative planner provenance column %s: %w",
				column.name,
				err,
			)
		}
	}
	return nil
}

func agencyInitiativePlannerRolloutColumnExists(
	tx *sql.Tx,
	table string,
	column string,
) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue any
			primaryKey   int
		)
		if err := rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			return false, fmt.Errorf("scan %s columns: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
