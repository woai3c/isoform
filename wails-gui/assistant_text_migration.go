package main

import "database/sql"

// Field edits are user decisions, separate from inferred evidence. The ledger
// and canonical Self revision commit together; history remains append-only.
func migrateAssistantTextEdits(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS assistant_text_edits_v1 (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			field_id TEXT NOT NULL,
			value TEXT NOT NULL,
			learned_value TEXT NOT NULL,
			action TEXT NOT NULL CHECK(action IN ('edit', 'reset')),
			edited_at INTEGER NOT NULL CHECK(edited_at > 0)
		);
		CREATE INDEX IF NOT EXISTS assistant_text_edits_field_v1
			ON assistant_text_edits_v1(field_id, sequence DESC);
		CREATE TRIGGER IF NOT EXISTS assistant_text_edits_v1_no_update
		BEFORE UPDATE ON assistant_text_edits_v1 BEGIN
			SELECT RAISE(ABORT, 'Assistant edit provenance is immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS assistant_text_edits_v1_no_delete
		BEFORE DELETE ON assistant_text_edits_v1 BEGIN
			SELECT RAISE(ABORT, 'Assistant edit provenance is immutable');
		END;
	`)
	return err
}
