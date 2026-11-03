package main

import (
	"database/sql"
	"fmt"
)

func migrateAgencyChat(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency chat migration requires a transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_chat_messages_v1 (
			id             TEXT PRIMARY KEY,
			request_id     TEXT NOT NULL,
			context_kind   TEXT NOT NULL,
			context_id     TEXT NOT NULL,
			sequence       INTEGER NOT NULL,
			author         TEXT NOT NULL,
			content        TEXT NOT NULL,
			payload_hash   TEXT NOT NULL,
			created_at     INTEGER NOT NULL,
			UNIQUE (request_id, sequence),
			CHECK (context_kind IN (
				'initiative', 'watching', 'permission', 'self', 'history'
			)),
			CHECK (sequence IN (1, 2)),
			CHECK (author IN ('user', 'yansu')),
			CHECK (
				(sequence = 1 AND author = 'user')
				OR (sequence = 2 AND author = 'yansu')
			),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_chat_messages_context_v1
			ON agency_chat_messages_v1(
				context_kind, context_id, created_at ASC, id ASC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_chat_messages_recent_v1
			ON agency_chat_messages_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_chat_runs_v1 (
			id             TEXT PRIMARY KEY,
			request_id     TEXT NOT NULL UNIQUE,
			context_kind   TEXT NOT NULL,
			context_id     TEXT NOT NULL,
			model          TEXT NOT NULL,
			prompt_version TEXT NOT NULL,
			prompt         TEXT NOT NULL,
			prompt_hash    TEXT NOT NULL,
			raw_output     TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL,
			error_text     TEXT NOT NULL DEFAULT '',
			payload_hash   TEXT NOT NULL,
			started_at     INTEGER NOT NULL,
			completed_at   INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			CHECK (status IN ('succeeded', 'failed')),
			CHECK (started_at > 0),
			CHECK (completed_at >= started_at),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_chat_runs_context_v1
			ON agency_chat_runs_v1(
				context_kind, context_id, created_at DESC, id DESC
			);

		CREATE TRIGGER IF NOT EXISTS agency_chat_messages_v1_no_update
		BEFORE UPDATE ON agency_chat_messages_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency chat messages are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_chat_messages_v1_no_delete
		BEFORE DELETE ON agency_chat_messages_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency chat messages are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_chat_runs_v1_no_update
		BEFORE UPDATE ON agency_chat_runs_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency chat runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_chat_runs_v1_no_delete
		BEFORE DELETE ON agency_chat_runs_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency chat runs are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create agency chat ledger: %w", err)
	}
	return nil
}
