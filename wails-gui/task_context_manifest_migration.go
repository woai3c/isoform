package main

import (
	"database/sql"
	"fmt"
)

func taskContextManifestColumnExists(
	tx *sql.Tx,
	table, column string,
) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
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
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read %s columns: %w", table, err)
	}
	return false, nil
}

func migrateTaskContextManifest(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate task context manifest: transaction is nil")
	}
	has, err := taskContextManifestColumnExists(
		tx,
		"task_episode_context_snapshots",
		"manifest_json",
	)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err := tx.Exec(`
		ALTER TABLE task_episode_context_snapshots
		ADD COLUMN manifest_json TEXT NOT NULL DEFAULT '[]'
	`); err != nil {
		return fmt.Errorf("add task context manifest: %w", err)
	}
	return nil
}
