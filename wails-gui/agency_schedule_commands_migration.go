package main

import (
	"database/sql"
	"fmt"
)

func migrateAgencyScheduleCommands(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency schedule commands migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_schedule_command_receipts_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL,
			stage                 TEXT NOT NULL,
			status                TEXT NOT NULL,
			schedule_id           TEXT NOT NULL,
			command               TEXT NOT NULL,
			permission_state      TEXT NOT NULL,
			approval_receipt_id   TEXT NOT NULL DEFAULT '',
			scope_hash            TEXT NOT NULL DEFAULT '',
			before_state_json     TEXT NOT NULL,
			after_state_json      TEXT NOT NULL,
			error_stage           TEXT NOT NULL DEFAULT '',
			error_text            TEXT NOT NULL DEFAULT '',
			occurred_at           INTEGER NOT NULL,
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			UNIQUE (idempotency_key, stage),
			CHECK (stage IN ('started', 'terminal')),
			CHECK (status IN ('in_progress', 'succeeded', 'failed')),
			CHECK (
				(stage = 'started' AND status = 'in_progress')
				OR
				(stage = 'terminal' AND status IN ('succeeded', 'failed'))
			),
			CHECK (command IN ('pause', 'resume')),
			CHECK (permission_state IN (
				'pending', 'standing', 'direct', 'changed', 'unavailable'
			)),
			CHECK (occurred_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_schedule_commands_schedule_v1
			ON agency_schedule_command_receipts_v1(
				schedule_id, occurred_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_schedule_commands_recent_v1
			ON agency_schedule_command_receipts_v1(
				occurred_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_schedule_command_receipts_v1_no_update
		BEFORE UPDATE ON agency_schedule_command_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency schedule command receipts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_schedule_command_receipts_v1_no_delete
		BEFORE DELETE ON agency_schedule_command_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency schedule command receipts are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency schedule command schema: %w", err)
	}
	return nil
}
