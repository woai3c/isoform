package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateSelfUserDirectives installs the immutable direct-user reasoning
// ledger. Each lifecycle event references the exact canonical Self revision
// produced by that user operation.
func migrateSelfUserDirectives(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Self directive migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS self_user_directives_v1 (
			id               TEXT PRIMARY KEY,
			idempotency_key  TEXT NOT NULL UNIQUE,
			entry_key        TEXT NOT NULL UNIQUE,
			kind             TEXT NOT NULL CHECK (kind IN ('principle', 'boundary')),
			contract_version TEXT NOT NULL CHECK (contract_version = 'self-user-directive-v1'),
			created_at       INTEGER NOT NULL CHECK (created_at > 0),
			payload_json     TEXT NOT NULL,
			payload_hash     TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_self_user_directives_created_v1
			ON self_user_directives_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_user_directive_events_v1 (
			id                          TEXT PRIMARY KEY,
			directive_id                TEXT NOT NULL REFERENCES self_user_directives_v1(id) ON DELETE RESTRICT,
			idempotency_key             TEXT NOT NULL UNIQUE,
			sequence                    INTEGER NOT NULL,
			event_type                  TEXT NOT NULL CHECK (event_type IN ('created', 'revised', 'retracted')),
			expected_self_entry_id      TEXT NOT NULL DEFAULT '',
			self_entry_id               TEXT NOT NULL REFERENCES self_model_entries(id) ON DELETE RESTRICT,
			self_source_hash            TEXT NOT NULL,
			statement                   TEXT NOT NULL,
			scope                       TEXT NOT NULL DEFAULT '',
			denied_capabilities_json    TEXT NOT NULL,
			retraction_reason           TEXT NOT NULL DEFAULT '',
			occurred_at                 INTEGER NOT NULL,
			created_at                  INTEGER NOT NULL,
			payload_json                TEXT NOT NULL,
			payload_hash                TEXT NOT NULL,
			UNIQUE (directive_id, sequence),
			CHECK (sequence > 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0),
			CHECK (
				(event_type = 'created' AND sequence = 1 AND expected_self_entry_id = '')
				OR
				(event_type IN ('revised', 'retracted') AND sequence > 1 AND expected_self_entry_id <> '')
			),
			CHECK (
				(event_type = 'retracted' AND retraction_reason <> '')
				OR
				(event_type <> 'retracted' AND retraction_reason = '')
			)
		);
		CREATE INDEX IF NOT EXISTS idx_self_user_directive_events_current_v1
			ON self_user_directive_events_v1(directive_id, sequence DESC);
		CREATE INDEX IF NOT EXISTS idx_self_user_directive_events_self_v1
			ON self_user_directive_events_v1(self_entry_id);

		CREATE TRIGGER IF NOT EXISTS self_user_directives_v1_no_update
		BEFORE UPDATE ON self_user_directives_v1
		BEGIN
			SELECT RAISE(ABORT, 'Self directive roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS self_user_directives_v1_no_delete
		BEFORE DELETE ON self_user_directives_v1
		BEGIN
			SELECT RAISE(ABORT, 'Self directive roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS self_user_directive_events_v1_no_update
		BEFORE UPDATE ON self_user_directive_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'Self directive events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS self_user_directive_events_v1_no_delete
		BEFORE DELETE ON self_user_directive_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'Self directive events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS self_model_entries_no_delete
		BEFORE DELETE ON self_model_entries
		BEGIN
			SELECT RAISE(ABORT, 'self model entries are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create Self directive ledger: %w", err)
	}
	return nil
}
