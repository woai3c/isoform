package main

import (
	"database/sql"
	"testing"
)

func TestCalendarHoldsMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHolds(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHolds(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO holds (id, calendar_id, title, start, end, source_kind, source_id, state, created_at, write_after) VALUES ('h', 'c', 'Plan', 10, 20, 'task', 't', 'pending_write', 1, 5)`)
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var deadline int64
	if err := db.QueryRow(`SELECT state, write_after FROM holds WHERE id = 'h'`).Scan(&state, &deadline); err != nil || state != "pending_write" || deadline != 5 {
		t.Fatalf("state=%q deadline=%d err=%v", state, deadline, err)
	}
	if _, err := db.Exec(`UPDATE holds SET state = 'unknown' WHERE id = 'h'`); err == nil {
		t.Fatal("accepted unknown state")
	}
	if _, err := db.Exec(`UPDATE holds SET end = start WHERE id = 'h'`); err == nil {
		t.Fatal("accepted empty time range")
	}
}

func TestCalendarHoldRecoveryMigrationPreservesExistingRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE holds (id TEXT PRIMARY KEY); INSERT INTO holds (id) VALUES ('existing')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHoldRecovery(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateCalendarHoldRecovery(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := db.QueryRow(`SELECT failure_reason FROM holds WHERE id = 'existing'`).Scan(&reason); err != nil || reason != "" {
		t.Fatalf("reason=%q err=%v", reason, err)
	}
}
