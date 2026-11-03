package main

import (
	"testing"
)

func TestMigrateFirstRunCreatesImmutableLedgersIdempotently(t *testing.T) {
	db := newTestDB(t)
	for pass := 0; pass < 2; pass++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateFirstRun(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migrateFirstRun pass %d: %v", pass+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit first run migration pass %d: %v", pass+1, err)
		}
	}

	tables := []string{
		"first_run_journeys_v1",
		"first_run_journey_events_v1",
		"first_outcome_runs_v1",
		"first_outcome_run_events_v1",
		"first_outcome_artifacts_v1",
		"first_run_receipts_v1",
		"first_run_exclusions_v1",
		"agency_learning_corrections_v1",
	}
	for _, table := range tables {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %q count=%d, want 1", table, count)
		}
	}

	triggers := []string{
		"first_run_journeys_v1",
		"first_run_journey_events_v1",
		"first_outcome_runs_v1",
		"first_outcome_run_events_v1",
		"first_outcome_artifacts_v1",
		"first_run_receipts_v1",
		"first_run_exclusions_v1",
		"agency_learning_corrections_v1",
	}
	for _, table := range triggers {
		for _, operation := range []string{"update", "delete"} {
			name := table + "_no_" + operation
			var count int
			if err := db.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`,
				name,
			).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("trigger %q count=%d, want 1", name, count)
			}
		}
	}

	if err := migrateFirstRun(nil); err == nil {
		t.Fatal("nil migration transaction was accepted")
	}
}

func TestMigrateFirstRunPreservesGuidedRunConstraints(t *testing.T) {
	db := newTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateFirstRun(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		INSERT INTO first_run_journeys_v1 (id, journey_key, version, created_at)
		VALUES ('journey-1', 'profile-1', 'first-run-v1', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO first_outcome_runs_v1 (
			id, journey_id, idempotency_key, execution_mode, learning_eligible,
			capture_started_at, capture_ends_at, created_at
		) VALUES ('run-1', 'journey-1', 'run-key-1', 'unattended', 1, 1, 2, 1)`); err == nil {
		t.Fatal("first outcome run accepted non-guided, learning-eligible values")
	}

	if _, err := db.Exec(`
		INSERT INTO first_outcome_runs_v1 (
			id, journey_id, idempotency_key, execution_mode, learning_eligible,
			capture_started_at, capture_ends_at, created_at
		) VALUES ('run-1', 'journey-1', 'run-key-1', 'guided_visible', 0, 1, 2, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`UPDATE first_outcome_runs_v1 SET capture_ends_at = 3 WHERE id = 'run-1'`,
	); err == nil {
		t.Fatal("immutable first outcome run accepted UPDATE")
	}
	if _, err := db.Exec(
		`DELETE FROM first_outcome_runs_v1 WHERE id = 'run-1'`,
	); err == nil {
		t.Fatal("immutable first outcome run accepted DELETE")
	}
}
