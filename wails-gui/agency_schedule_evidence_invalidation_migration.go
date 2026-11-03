package main

import (
	"database/sql"
	"fmt"
)

func migrateAgencyScheduleEvidenceInvalidations(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency schedule evidence-invalidation migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_schedule_evidence_invalidation_receipts_v1 (
			id                       TEXT PRIMARY KEY,
			idempotency_key          TEXT NOT NULL,
			version                  TEXT NOT NULL,
			stage                    TEXT NOT NULL,
			status                   TEXT NOT NULL,
			request_id               TEXT NOT NULL,
			request_payload_hash     TEXT NOT NULL,
			reason_code              TEXT NOT NULL,
			source_snapshot_json     TEXT NOT NULL,
			before_state_json        TEXT NOT NULL,
			after_state_json         TEXT NOT NULL,
			effects_json             TEXT NOT NULL,
			error_stage              TEXT NOT NULL DEFAULT '',
			error_text               TEXT NOT NULL DEFAULT '',
			occurred_at              INTEGER NOT NULL,
			payload_json             TEXT NOT NULL,
			payload_hash             TEXT NOT NULL,
			UNIQUE (idempotency_key, stage),
			CHECK (version = 'agency-schedule-evidence-invalidation-v1'),
			CHECK (stage IN ('started', 'terminal')),
			CHECK (status IN ('in_progress', 'succeeded', 'failed')),
			CHECK (
				(stage = 'started' AND status = 'in_progress')
				OR
				(stage = 'terminal' AND status IN ('succeeded', 'failed'))
			),
			CHECK (request_id != ''),
			CHECK (request_payload_hash != ''),
			CHECK (reason_code IN (
				'repeated_task_start', 'proposal_expired', 'historical_generation'
			)),
			CHECK (occurred_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_schedule_evidence_invalidation_request_v1
			ON agency_schedule_evidence_invalidation_receipts_v1(
				request_id, occurred_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_schedule_evidence_invalidation_recent_v1
			ON agency_schedule_evidence_invalidation_receipts_v1(
				occurred_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_schedule_evidence_invalidation_receipts_v1_no_update
		BEFORE UPDATE ON agency_schedule_evidence_invalidation_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency schedule evidence-invalidation receipts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_schedule_evidence_invalidation_receipts_v1_no_delete
		BEFORE DELETE ON agency_schedule_evidence_invalidation_receipts_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency schedule evidence-invalidation receipts are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency schedule evidence-invalidation schema: %w", err)
	}
	return nil
}
