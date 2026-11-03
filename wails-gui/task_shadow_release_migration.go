package main

import (
	"database/sql"
	"fmt"
)

func migrateTaskShadowCapabilityRelease(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS task_shadow_capability_release_events_v1 (
			id                      TEXT PRIMARY KEY,
			event_key               TEXT NOT NULL UNIQUE,
			version                 TEXT NOT NULL,
			capability_key          TEXT NOT NULL,
			prompt_version          TEXT NOT NULL,
			campaign_version        TEXT NOT NULL,
			learning_policy_version TEXT NOT NULL,
			decision                TEXT NOT NULL CHECK (
				decision IN ('qualified', 'suspended')
			),
			evidence_hash           TEXT NOT NULL,
			previous_event_id       TEXT NOT NULL DEFAULT '',
			qualification_json      TEXT NOT NULL,
			evidence_manifest_json  TEXT NOT NULL,
			authority_effect        TEXT NOT NULL CHECK (authority_effect = 'none'),
			created_at              INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_release_capability_v1
			ON task_shadow_capability_release_events_v1(
				capability_key,
				prompt_version,
				created_at DESC
			);
		CREATE INDEX IF NOT EXISTS idx_task_shadow_release_decision_v1
			ON task_shadow_capability_release_events_v1(
				decision,
				created_at DESC
			);
		CREATE TRIGGER IF NOT EXISTS task_shadow_release_events_v1_no_update
		BEFORE UPDATE ON task_shadow_capability_release_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'task shadow capability release events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_shadow_release_events_v1_no_delete
		BEFORE DELETE ON task_shadow_capability_release_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'task shadow capability release events are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create task shadow capability release ledger: %w", err)
	}
	return nil
}
