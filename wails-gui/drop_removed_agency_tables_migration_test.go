package main

import (
	"database/sql"
	"strings"
	"testing"
)

// keptAgencyTables are the Agency and first-run tables that remaining code
// still reads or writes, together with the parents their foreign keys need.
var keptAgencyTables = []string{
	"agency_permission_decision_receipts_v1",
	"agency_origination_runs_v1",
	"agency_origination_candidates_v1",
	"agency_origination_permission_requests_v1",
	"first_run_journeys_v1",
	"first_outcome_runs_v1",
	"first_run_exclusions_v1",
	"agency_learning_corrections_v1",
}

// newPreDropActivityStore is a database migrated and logged the way builds
// before drop_removed_agency_tables left it.
func newPreDropActivityStore(t *testing.T) *ActivityStore {
	t.Helper()
	store := &ActivityStore{db: newTestDB(t), appliedMigrations: make(map[string]bool)}
	if err := store.ensureMigrationLog(); err != nil {
		t.Fatal(err)
	}
	for _, migration := range allActivityMigrations {
		if migration.Name == "drop_removed_agency_tables" {
			continue
		}
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migration.Fn(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration %q: %v", migration.Name, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO migration_log (name, applied_at) VALUES (?, 1)",
			migration.Name,
		); err != nil {
			tx.Rollback()
			t.Fatalf("record migration %q: %v", migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit migration %q: %v", migration.Name, err)
		}
	}
	return store
}

// seedRemovedAgencyTables stores one row in every kept table, rows in dropped
// tables that reference kept ones, and a dropped initiative chain with a
// RESTRICT and a CASCADE reference, so the drop meets real foreign keys and
// immutability triggers.
func seedRemovedAgencyTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO agency_origination_runs_v1 (
			id, idempotency_key, status, wake_reason, wake_at,
			source_input_json, source_manifest_json, planning_input_json,
			output_json, rejected_evidence_refs_json,
			rejected_candidate_keys_json, resource_policy_json,
			resource_usage_json, payload_json, payload_hash, created_at
		) VALUES (
			'origination-run-1', 'origination-run-1', 'permission_requested',
			'periodic', 1, '{}', '{}', '{}', '{}', '[]', '[]', '{}', '{}',
			'{}', 'hash', 1
		)`,
		`INSERT INTO agency_origination_candidates_v1 (
			id, run_id, local_key, kind, rank, selected, candidate_json,
			semantic_hash, disposition, authority_decision_json,
			duplication_check_json, payload_json, payload_hash, created_at
		) VALUES (
			'candidate-1', 'origination-run-1', 'candidate-1', 'handoff', 1, 1,
			'{}', 'semantic-hash', 'permission_requested', '{}', '{}', '{}',
			'hash', 1
		)`,
		`INSERT INTO agency_origination_permission_requests_v1 (
			id, idempotency_key, run_id, candidate_id, kind,
			permission_contract, request_json, payload_json, payload_hash,
			created_at
		) VALUES (
			'request-1', 'request-1', 'origination-run-1', 'candidate-1',
			'handoff', 'jit', '{}', '{}', 'hash', 1
		)`,
		`INSERT INTO agency_origination_permission_events_v1 (
			id, request_id, idempotency_key, status, occurred_at,
			payload_json, payload_hash, created_at
		) VALUES (
			'request-event-1', 'request-1', 'request-event-1', 'pending', 1,
			'{}', 'hash', 1
		)`,
		`INSERT INTO agency_permission_decision_receipts_v1 (
			id, idempotency_key, stage, status, request_id, kind, decision,
			request_source, request_snapshot_json, provenance_json,
			before_state_json, after_state_json, result_json, occurred_at,
			payload_json, payload_hash
		) VALUES (
			'receipt-1', 'decision-1', 'terminal', 'succeeded', 'schedule-1',
			'schedule', 'approve', 'schedule', '{}', '{}', '{}', '{}', '{}', 1,
			'{}', 'hash'
		)`,
		`INSERT INTO first_run_journeys_v1 (id, journey_key, version, created_at)
		VALUES ('journey-1', 'profile-1', 'first-run-v1', 1)`,
		`INSERT INTO first_run_journey_events_v1 (
			id, journey_id, idempotency_key, sequence, kind, payload_json,
			payload_hash, occurred_at, created_at
		) VALUES (
			'journey-event-1', 'journey-1', 'journey-event-1', 1, 'started',
			'{}', 'hash', 1, 1
		)`,
		`INSERT INTO first_outcome_runs_v1 (
			id, journey_id, idempotency_key, execution_mode, learning_eligible,
			capture_started_at, capture_ends_at, created_at
		) VALUES (
			'first-run-1', 'journey-1', 'first-run-1', 'guided_visible', 0, 1,
			2, 1
		)`,
		`INSERT INTO first_outcome_run_events_v1 (
			id, run_id, idempotency_key, sequence, kind, payload_json,
			payload_hash, occurred_at, created_at
		) VALUES (
			'first-run-event-1', 'first-run-1', 'first-run-event-1', 1,
			'started', '{}', 'hash', 1, 1
		)`,
		`INSERT INTO first_run_exclusions_v1 (
			id, journey_id, run_id, source_kind, source_id, reason, created_at
		) VALUES (
			'exclusion-1', 'journey-1', 'first-run-1', 'session', 'session-1',
			'guided_first_outcome', 2
		)`,
		`INSERT INTO agency_learning_corrections_v1 (
			id, idempotency_key, journey_id, capability_key,
			learning_revision_id, original_statement, correction,
			payload_json, payload_hash, created_at
		) VALUES (
			'correction-1', 'correction-1', 'journey-1', 'capability-1',
			'revision-1', 'You review pull requests on Fridays.',
			'Only release pull requests.', '{}', 'hash', 1
		)`,
		`INSERT INTO agency_initiatives_v2 (
			id, idempotency_key, name, purpose, desired_outcome, scope_json,
			why_now, authority_snapshot_json, budget_snapshot_json,
			creation_payload_json, creation_payload_hash, created_at
		) VALUES (
			'initiative-1', 'initiative-1', 'Release notes', 'Ship',
			'Notes drafted', '{}', 'Friday', '{}', '{}', '{}', 'hash', 1
		)`,
		`INSERT INTO agency_initiative_runs_v2 (
			id, initiative_id, idempotency_key, wake_condition_json,
			context_manifest_json, context_hash, authority_snapshot_json,
			budget_snapshot_json, max_attempts, retry_backoff_ms,
			creation_payload_json, creation_payload_hash, created_at
		) VALUES (
			'initiative-run-1', 'initiative-1', 'initiative-run-1', '{}', '{}',
			'context-hash', '{}', '{}', 1, 0, '{}', 'hash', 1
		)`,
		`INSERT INTO agency_initiative_run_leases_v2 (
			run_id, lease_token, worker_id, generation, acquired_at,
			renewed_at, expires_at
		) VALUES ('initiative-run-1', 'lease-1', 'worker-1', 1, 1, 1, 2)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed removed Agency tables: %v\n%s", err, statement)
		}
	}
}

// assertRemovedAgencyTablesDropped checks that the removed tables are gone
// with their indexes and triggers, and that the kept tables still hold their
// row and still refuse changes.
func assertRemovedAgencyTablesDropped(t *testing.T, store *ActivityStore) {
	t.Helper()
	for _, table := range removedAgencyTables {
		var count int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE name = ? OR tbl_name = ?`,
			table, table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s left %d schema objects", table, count)
		}
	}
	for _, table := range keptAgencyTables {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count kept %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("kept %s rows=%d, want 1", table, count)
		}
		for _, trigger := range []string{table + "_no_update", table + "_no_delete"} {
			ok, err := store.hasTrigger(trigger)
			if err != nil || !ok {
				t.Errorf("trigger %s present=%t err=%v", trigger, ok, err)
			}
		}
		if _, err := store.db.Exec("DELETE FROM " + table); err == nil {
			t.Errorf("kept %s accepted DELETE", table)
		}
	}

	rows, err := store.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	violations := rows.Next()
	rows.Close()
	if violations {
		t.Error("foreign_key_check reported violations")
	}
	var integrity string
	if err := store.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Errorf("integrity_check=%q", integrity)
	}

	// ALTER TABLE ... RENAME re-reads every trigger and view in the schema,
	// so it fails if one still names a dropped table.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`ALTER TABLE first_run_exclusions_v1 RENAME TO first_run_exclusions_v1_check`,
	); err != nil {
		t.Errorf("schema no longer renames cleanly: %v", err)
	}
}

func TestRemovedAgencyTablesCoverEveryUnusedTableChildrenFirst(t *testing.T) {
	store := newPreDropActivityStore(t)
	position := make(map[string]int, len(removedAgencyTables))
	for index, table := range removedAgencyTables {
		if _, ok := position[table]; ok {
			t.Fatalf("%s is listed twice", table)
		}
		position[table] = index
	}
	kept := make(map[string]bool, len(keptAgencyTables))
	for _, table := range keptAgencyTables {
		if _, ok := position[table]; ok {
			t.Fatalf("%s is both kept and dropped", table)
		}
		kept[table] = true
	}

	rows, err := store.db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table'
			AND (name LIKE 'agency\_%' ESCAPE '\'
				OR name LIKE 'first\_run\_%' ESCAPE '\'
				OR name LIKE 'first\_outcome\_%' ESCAPE '\'
				OR name LIKE 'purpose\_agency\_%' ESCAPE '\')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		found++
		if _, dropped := position[name]; !dropped && !kept[name] {
			t.Errorf("%s is neither kept nor dropped", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if want := len(removedAgencyTables) + len(keptAgencyTables); found != want {
		t.Errorf("Agency and first-run tables=%d, want %d", found, want)
	}

	references, err := store.db.Query(`
		SELECT m.name, f."table"
		FROM sqlite_master m, pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer references.Close()
	for references.Next() {
		var child, parent string
		if err := references.Scan(&child, &parent); err != nil {
			t.Fatal(err)
		}
		parentAt, parentDropped := position[parent]
		if !parentDropped {
			continue
		}
		childAt, childDropped := position[child]
		switch {
		case !childDropped:
			t.Errorf("%s still references dropped %s", child, parent)
		case childAt > parentAt:
			t.Errorf("%s is dropped after its parent %s", child, parent)
		}
	}
	if err := references.Err(); err != nil {
		t.Fatal(err)
	}
	references.Close()

	objects, err := store.db.Query(`
		SELECT tbl_name, name, sql FROM sqlite_master
		WHERE type IN ('trigger', 'view')`)
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	for objects.Next() {
		var table, name, definition string
		if err := objects.Scan(&table, &name, &definition); err != nil {
			t.Fatal(err)
		}
		if _, dropped := position[table]; dropped {
			continue
		}
		for _, removed := range removedAgencyTables {
			if strings.Contains(definition, removed) {
				t.Errorf("%s on %s names dropped %s", name, table, removed)
			}
		}
	}
	if err := objects.Err(); err != nil {
		t.Fatal(err)
	}
	objects.Close()
}

func TestDropRemovedAgencyTablesUpgradesAnOldDatabase(t *testing.T) {
	store := newPreDropActivityStore(t)
	for _, table := range append(append([]string{}, removedAgencyTables...), keptAgencyTables...) {
		ok, err := store.hasTable(table)
		if err != nil || !ok {
			t.Fatalf("old database table %s present=%t err=%v", table, ok, err)
		}
	}
	seedRemovedAgencyTables(t, store.db)

	store.runPendingMigrations()
	if !store.appliedMigrations["drop_removed_agency_tables"] {
		t.Fatal("drop_removed_agency_tables was not applied")
	}
	assertRemovedAgencyTablesDropped(t, store)

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateDropRemovedAgencyTables(tx); err != nil {
		tx.Rollback()
		t.Fatalf("second drop: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertRemovedAgencyTablesDropped(t, store)

	if err := migrateDropRemovedAgencyTables(nil); err == nil {
		t.Fatal("nil migration transaction was accepted")
	}
}

func TestDropRemovedAgencyTablesReplaysWhenTheLogIsLost(t *testing.T) {
	store := newPreDropActivityStore(t)
	seedRemovedAgencyTables(t, store.db)
	store.runPendingMigrations()

	// Bootstrap cannot recognize the Agency migrations whose tables were
	// dropped, so they replay and the drop runs after them again.
	if _, err := store.db.Exec("DELETE FROM migration_log"); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	for _, migration := range allActivityMigrations {
		if !store.appliedMigrations[migration.Name] {
			t.Errorf("migration %q not applied after replay", migration.Name)
		}
	}
	assertRemovedAgencyTablesDropped(t, store)
}

func TestNewActivityStoreDropsRemovedAgencyTables(t *testing.T) {
	store := newTestStore(t)
	for _, table := range removedAgencyTables {
		if ok, err := store.hasTable(table); err != nil || ok {
			t.Errorf("new database table %s present=%t err=%v", table, ok, err)
		}
	}
	for _, table := range keptAgencyTables {
		if ok, err := store.hasTable(table); err != nil || !ok {
			t.Errorf("new database table %s present=%t err=%v", table, ok, err)
		}
	}
}
