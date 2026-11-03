package main

import (
	"database/sql"
	"errors"
	"fmt"
)

func migratePurposeExtraction(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Purpose extraction migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS purpose_extraction_runs_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL UNIQUE,
			cutoff_at             INTEGER NOT NULL CHECK (cutoff_at > 0),
			input_version         TEXT NOT NULL,
			manifest_version      TEXT NOT NULL,
			prompt_version        TEXT NOT NULL,
			output_version        TEXT NOT NULL,
			model                 TEXT NOT NULL,
			status                TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'no_sources')),
			input_json            TEXT NOT NULL,
			input_hash            TEXT NOT NULL,
			manifest_json         TEXT NOT NULL,
			manifest_hash         TEXT NOT NULL,
			prompt                TEXT NOT NULL,
			prompt_hash           TEXT NOT NULL,
			raw_output            TEXT NOT NULL DEFAULT '',
			raw_output_hash       TEXT NOT NULL DEFAULT '',
			output_json           TEXT NOT NULL DEFAULT '',
			output_hash           TEXT NOT NULL DEFAULT '',
			error                 TEXT NOT NULL DEFAULT '',
			started_at            INTEGER NOT NULL CHECK (started_at > 0),
			completed_at          INTEGER NOT NULL CHECK (completed_at >= started_at),
			created_at            INTEGER NOT NULL CHECK (created_at >= completed_at),
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			CHECK ((status = 'succeeded' AND output_json <> '' AND error = '')
				OR (status = 'failed' AND error <> '')
				OR (status = 'no_sources' AND output_json = '' AND raw_output = '' AND error = ''))
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_extraction_runs_created_v1
			ON purpose_extraction_runs_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS purpose_extraction_hypotheses_v1 (
			id                         TEXT PRIMARY KEY,
			run_id                     TEXT NOT NULL REFERENCES purpose_extraction_runs_v1(id) ON DELETE RESTRICT,
			local_key                  TEXT NOT NULL,
			canonical_key              TEXT NOT NULL,
			duplicate_of_canonical_key TEXT NOT NULL DEFAULT '',
			kind                       TEXT NOT NULL CHECK (kind IN (
				'commitment', 'responsibility', 'goal', 'concern', 'opportunity'
			)),
			operation                  TEXT NOT NULL CHECK (operation IN (
				'support', 'update', 'wait', 'resolve', 'contradict', 'dismiss'
			)),
			title                      TEXT NOT NULL,
			desired_outcome            TEXT NOT NULL,
			owner                      TEXT NOT NULL CHECK (owner IN ('user', 'other', 'shared', 'unknown')),
			counterparty               TEXT NOT NULL DEFAULT '',
			scope                      TEXT NOT NULL DEFAULT '',
			due_kind                   TEXT NOT NULL CHECK (due_kind IN ('none', 'date', 'window', 'condition', 'unknown')),
			due_value                  TEXT NOT NULL DEFAULT '',
			waiting_condition          TEXT NOT NULL DEFAULT '',
			reasoning_status           TEXT NOT NULL CHECK (reasoning_status IN ('explicit', 'unknown')),
			rationale                  TEXT NOT NULL DEFAULT '',
			confidence                 REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
			evidence_refs_json         TEXT NOT NULL,
			payload_json               TEXT NOT NULL,
			payload_hash               TEXT NOT NULL,
			created_at                 INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, local_key)
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_hypotheses_run_v1
			ON purpose_extraction_hypotheses_v1(run_id, local_key);

		CREATE TABLE IF NOT EXISTS purpose_reconciliation_receipts_v1 (
			id                 TEXT PRIMARY KEY,
			idempotency_key    TEXT NOT NULL UNIQUE,
			run_id             TEXT NOT NULL REFERENCES purpose_extraction_runs_v1(id) ON DELETE RESTRICT,
			hypothesis_id      TEXT NOT NULL REFERENCES purpose_extraction_hypotheses_v1(id) ON DELETE RESTRICT,
			disposition        TEXT NOT NULL CHECK (disposition IN (
				'created', 'reinforced', 'revised', 'waiting', 'resolved',
				'contradicted', 'dismissed', 'deduplicated', 'no_change', 'rejected'
			)),
			open_loop_id       TEXT NOT NULL DEFAULT '',
			event_id           TEXT NOT NULL DEFAULT '',
			relation_id        TEXT NOT NULL DEFAULT '',
			reason             TEXT NOT NULL DEFAULT '',
			source_set_hash    TEXT NOT NULL,
			occurred_at        INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at         INTEGER NOT NULL CHECK (created_at >= occurred_at),
			payload_json       TEXT NOT NULL,
			payload_hash       TEXT NOT NULL,
			UNIQUE (hypothesis_id),
			CHECK ((disposition = 'rejected' AND reason <> '') OR disposition <> 'rejected')
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_receipts_run_v1
			ON purpose_reconciliation_receipts_v1(run_id, occurred_at, id);

		CREATE TRIGGER IF NOT EXISTS purpose_extraction_runs_v1_no_update
		BEFORE UPDATE ON purpose_extraction_runs_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose extraction runs are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_extraction_runs_v1_no_delete
		BEFORE DELETE ON purpose_extraction_runs_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose extraction runs are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_extraction_hypotheses_v1_no_update
		BEFORE UPDATE ON purpose_extraction_hypotheses_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose extraction hypotheses are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_extraction_hypotheses_v1_no_delete
		BEFORE DELETE ON purpose_extraction_hypotheses_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose extraction hypotheses are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_reconciliation_receipts_v1_no_update
		BEFORE UPDATE ON purpose_reconciliation_receipts_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose reconciliation receipts are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_reconciliation_receipts_v1_no_delete
		BEFORE DELETE ON purpose_reconciliation_receipts_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose reconciliation receipts are immutable'); END;
	`); err != nil {
		return fmt.Errorf("create Purpose extraction ledger: %w", err)
	}
	return nil
}
