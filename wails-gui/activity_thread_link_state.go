package main

import (
	"database/sql"
	"strings"
)

// User confirmation is independent of the model's reviewed membership flag.
// Reason provenance is shared once per thread, rather than copied per member.
func migrateActivityThreadLinkState(tx *sql.Tx) error {
	for _, column := range []string{
		`user_confirmed INTEGER NOT NULL DEFAULT 0`,
		`reason_basis TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.Exec(`ALTER TABLE activity_threads ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS activity_thread_confirmation_insert AFTER INSERT ON activity_thread_members BEGIN
            UPDATE activity_threads SET user_confirmed=0 WHERE id=NEW.thread_id;
        END`,
		`CREATE TRIGGER IF NOT EXISTS activity_thread_confirmation_delete AFTER DELETE ON activity_thread_members BEGIN
            UPDATE activity_threads SET user_confirmed=0 WHERE id=OLD.thread_id;
        END`,
		`CREATE TRIGGER IF NOT EXISTS activity_thread_confirmation_move AFTER UPDATE OF thread_id ON activity_thread_members WHEN OLD.thread_id != NEW.thread_id BEGIN
            UPDATE activity_threads SET user_confirmed=0 WHERE id IN (OLD.thread_id,NEW.thread_id);
        END`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
