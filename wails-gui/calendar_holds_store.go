package main

import "database/sql"

// migrateCalendarHolds records only events created through the hold flow.
// The pending deadline is persisted so a delayed write can survive a restart.
func migrateCalendarHolds(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS holds (
		id TEXT PRIMARY KEY,
		event_identifier TEXT NOT NULL DEFAULT '',
		external_identifier TEXT NOT NULL DEFAULT '',
		calendar_id TEXT NOT NULL,
		title TEXT NOT NULL,
		start INTEGER NOT NULL,
		end INTEGER NOT NULL CHECK(end > start),
		source_kind TEXT NOT NULL CHECK(source_kind IN ('brief', 'task', 'promise', 'week', 'up_next')),
		source_id TEXT NOT NULL,
		subject_key TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL CHECK(state IN ('offered', 'checking', 'pending_write', 'held', 'removed', 'moved', 'gone', 'failed')),
		created_at INTEGER NOT NULL,
		write_after INTEGER NOT NULL DEFAULT 0,
		removed_at INTEGER NOT NULL DEFAULT 0
		, failure_reason TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

func migrateCalendarHoldRecovery(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(holds)`)
	if err != nil {
		return err
	}
	present := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "failure_reason" {
			present = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !present {
		_, err = tx.Exec(`ALTER TABLE holds ADD COLUMN failure_reason TEXT NOT NULL DEFAULT ''`)
	}
	return err
}

// At most one pending or held event may exist for a source before its start.
// The row remains for reconciliation after the active-offer marker is cleared.
func migrateCalendarHoldActiveSource(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(holds)`)
	if err != nil {
		return err
	}
	present := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "source_active" {
			present = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !present {
		if _, err := tx.Exec(`ALTER TABLE holds ADD COLUMN source_active INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS holds_active_source ON holds(subject_key) WHERE source_active = 1 AND state IN ('pending_write', 'checking', 'held', 'moved')`)
	return err
}
