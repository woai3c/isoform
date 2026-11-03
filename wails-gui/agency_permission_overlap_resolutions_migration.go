package main

import (
	"database/sql"
	"fmt"
)

// migrateAgencyPermissionOverlapResolutions installs the append-only task
// relationship boundary. A resolution changes presentation and later decision
// provenance, never permission or authority by itself.
func migrateAgencyPermissionOverlapResolutions(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency permission overlap resolutions migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_permission_overlap_resolutions_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL UNIQUE,
			version               TEXT NOT NULL,
			group_id              TEXT NOT NULL UNIQUE,
			choice                TEXT NOT NULL,
			selected_request_id   TEXT NOT NULL DEFAULT '',
			request_ids_json      TEXT NOT NULL,
			group_snapshot_json   TEXT NOT NULL,
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			created_at            INTEGER NOT NULL,
			CHECK (version = 'agency-permission-overlap-resolution-v1'),
			CHECK (choice IN ('use_one_plan', 'keep_separate')),
			CHECK (
				(choice = 'use_one_plan' AND selected_request_id != '')
				OR
				(choice = 'keep_separate' AND selected_request_id = '')
			),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_permission_overlap_resolutions_recent_v1
			ON agency_permission_overlap_resolutions_v1(
				created_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_permission_overlap_resolutions_v1_no_update
		BEFORE UPDATE ON agency_permission_overlap_resolutions_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency permission overlap resolutions are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_permission_overlap_resolutions_v1_no_delete
		BEFORE DELETE ON agency_permission_overlap_resolutions_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency permission overlap resolutions are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency permission overlap resolution schema: %w", err)
	}
	return nil
}
