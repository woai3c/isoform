package main

import "testing"

func TestMigrateWorkPatternProfileCorrectionRevisionsUpgradesHistoricalSchema(
	t *testing.T,
) {
	db := newTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE work_pattern_profile_snapshots_v1 (
			id TEXT PRIMARY KEY
		);
		INSERT INTO work_pattern_profile_snapshots_v1 (id) VALUES ('profile-1');
		CREATE TABLE work_pattern_profile_corrections_v1 (
			id TEXT PRIMARY KEY,
			idempotency_key TEXT NOT NULL UNIQUE,
			profile_snapshot_id TEXT NOT NULL
				REFERENCES work_pattern_profile_snapshots_v1(id)
				ON DELETE RESTRICT,
			scope_kind TEXT NOT NULL,
			scope_key TEXT NOT NULL,
			original_statement TEXT NOT NULL,
			correction TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX idx_work_pattern_profile_corrections_scope_v1
			ON work_pattern_profile_corrections_v1(
				scope_kind,
				scope_key,
				created_at DESC
			);
		CREATE TRIGGER work_pattern_profile_corrections_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_corrections_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile corrections are immutable');
		END;
		CREATE TRIGGER work_pattern_profile_corrections_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_corrections_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile corrections are immutable');
		END;
		INSERT INTO work_pattern_profile_corrections_v1 (
			id, idempotency_key, profile_snapshot_id, scope_kind, scope_key,
			original_statement, correction, created_at
		) VALUES
			('correction-2', 'key-2', 'profile-1', 'global', 'all',
			 'second original', 'second correction', 20),
			('correction-1', 'key-1', 'profile-1', 'global', 'all',
			 'first original', 'first correction', 10);
	`); err != nil {
		t.Fatal(err)
	}

	for pass := 0; pass < 2; pass++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateWorkPatternProfileCorrectionRevisions(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration pass %d: %v", pass+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit pass %d: %v", pass+1, err)
		}
	}

	rows, err := db.Query(`
		SELECT id, revision
		FROM work_pattern_profile_corrections_v1
		ORDER BY revision ASC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make([]string, 0, 2)
	for rows.Next() {
		var id string
		var revision int
		if err := rows.Scan(&id, &revision); err != nil {
			t.Fatal(err)
		}
		got = append(got, id+":"+string(rune('0'+revision)))
	}
	if len(got) != 2 || got[0] != "correction-1:1" ||
		got[1] != "correction-2:2" {
		t.Fatalf("revisions=%v", got)
	}
	if _, err := db.Exec(`
		UPDATE work_pattern_profile_corrections_v1
		SET correction = 'rewritten'
		WHERE id = 'correction-1'`); err == nil {
		t.Fatal("upgraded correction ledger accepted UPDATE")
	}
	if _, err := db.Exec(`
		INSERT INTO work_pattern_profile_corrections_v1 (
			id, idempotency_key, profile_snapshot_id, scope_kind, scope_key,
			revision, original_statement, correction, created_at
		) VALUES (
			'duplicate-revision', 'duplicate-key', 'profile-1', 'global', 'all',
			2, 'duplicate', 'duplicate', 30
		)`); err == nil {
		t.Fatal("upgraded correction ledger accepted duplicate revision")
	}
}
