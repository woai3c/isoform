package main

import (
	"database/sql"
	"fmt"
)

func migrateWorkPatternProfileSessionCache(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf(
			"migrate work pattern profile session cache: transaction is nil",
		)
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS work_pattern_profile_session_cache_v1 (
			session_id           TEXT NOT NULL,
			policy_version       TEXT NOT NULL,
			session_start        INTEGER NOT NULL,
			session_end          INTEGER NOT NULL,
			source_max_event_id  INTEGER NOT NULL,
			source_event_count   INTEGER NOT NULL,
			source_timestamp_sum INTEGER NOT NULL,
			source_data_bytes    INTEGER NOT NULL,
			events_json          TEXT NOT NULL,
			events_sha256        TEXT NOT NULL,
			generated_at         INTEGER NOT NULL,
			PRIMARY KEY (session_id, policy_version)
		);
		CREATE INDEX IF NOT EXISTS
			idx_work_pattern_profile_session_cache_retention_v1
			ON work_pattern_profile_session_cache_v1(
				session_end ASC,
				policy_version
			);
	`); err != nil {
		return fmt.Errorf(
			"create work pattern profile session cache: %w",
			err,
		)
	}
	return nil
}

func migrateWorkPatternProfiles(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate work pattern profiles: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS work_pattern_profile_snapshots_v1 (
			id                  TEXT PRIMARY KEY,
			policy_version      TEXT NOT NULL,
			source_window_start INTEGER NOT NULL,
			source_cutoff_at    INTEGER NOT NULL,
			source_max_event_id INTEGER NOT NULL DEFAULT 0,
			source_hash         TEXT NOT NULL,
			status              TEXT NOT NULL CHECK (
				status IN ('insufficient', 'qualified')
			),
			sample_count        INTEGER NOT NULL,
			session_count       INTEGER NOT NULL,
			confidence          REAL NOT NULL CHECK (
				confidence >= 0 AND confidence <= 1
			),
			canonical_json      TEXT NOT NULL,
			sha256              TEXT NOT NULL,
			created_at          INTEGER NOT NULL,
			UNIQUE (
				policy_version,
				source_window_start,
				source_cutoff_at,
				source_hash
			)
		);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_snapshots_cutoff_v1
			ON work_pattern_profile_snapshots_v1(
				source_cutoff_at DESC,
				created_at DESC
			);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_snapshots_status_v1
			ON work_pattern_profile_snapshots_v1(
				status,
				source_cutoff_at DESC
			);

		CREATE TABLE IF NOT EXISTS work_pattern_profile_evidence_v1 (
			id                  TEXT PRIMARY KEY,
			profile_snapshot_id TEXT NOT NULL
				REFERENCES work_pattern_profile_snapshots_v1(id)
				ON DELETE RESTRICT,
			source_kind         TEXT NOT NULL,
			source_id           TEXT NOT NULL,
			observed_at         INTEGER NOT NULL,
			content_hash        TEXT NOT NULL DEFAULT '',
			included            INTEGER NOT NULL CHECK (included IN (0, 1)),
			reason              TEXT NOT NULL,
			created_at          INTEGER NOT NULL,
			UNIQUE (
				profile_snapshot_id,
				source_kind,
				source_id,
				content_hash,
				reason
			)
		);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_evidence_snapshot_v1
			ON work_pattern_profile_evidence_v1(
				profile_snapshot_id,
				observed_at ASC,
				id ASC
			);

		CREATE TABLE IF NOT EXISTS work_pattern_profile_corrections_v1 (
			id                 TEXT PRIMARY KEY,
			idempotency_key    TEXT NOT NULL UNIQUE,
			profile_snapshot_id TEXT NOT NULL
				REFERENCES work_pattern_profile_snapshots_v1(id)
				ON DELETE RESTRICT,
			scope_kind         TEXT NOT NULL,
			scope_key          TEXT NOT NULL,
			revision           INTEGER NOT NULL CHECK (revision > 0),
			original_statement TEXT NOT NULL,
			correction         TEXT NOT NULL,
			created_at         INTEGER NOT NULL,
			UNIQUE (scope_kind, scope_key, revision)
		);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_corrections_scope_v1
			ON work_pattern_profile_corrections_v1(
				scope_kind,
				scope_key,
				created_at DESC
			);

		CREATE TABLE IF NOT EXISTS work_pattern_profile_ablation_runs_v1 (
			id                 TEXT PRIMARY KEY,
			idempotency_key    TEXT NOT NULL UNIQUE,
			source_kind        TEXT NOT NULL,
			source_id          TEXT NOT NULL,
			window_start       INTEGER NOT NULL,
			cutoff_at          INTEGER NOT NULL,
			profile_snapshot_id TEXT NOT NULL
				REFERENCES work_pattern_profile_snapshots_v1(id)
				ON DELETE RESTRICT,
			profile_sha256     TEXT NOT NULL,
			primary_mode       TEXT NOT NULL CHECK (
				primary_mode IN ('neutral', 'profile')
			),
			neutral_attempt_id TEXT NOT NULL DEFAULT '',
			profile_attempt_id TEXT NOT NULL DEFAULT '',
			neutral_status     TEXT NOT NULL,
			profile_status     TEXT NOT NULL,
			neutral_response_json TEXT NOT NULL DEFAULT '{"episodes":[]}',
			profile_response_json TEXT NOT NULL DEFAULT '{"episodes":[]}',
			model              TEXT NOT NULL,
			prompt_version     TEXT NOT NULL,
			started_at         INTEGER NOT NULL,
			completed_at       INTEGER NOT NULL,
			created_at         INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_ablation_source_v1
			ON work_pattern_profile_ablation_runs_v1(
				source_kind,
				source_id,
				created_at DESC
			);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_ablation_profile_v1
			ON work_pattern_profile_ablation_runs_v1(
				profile_snapshot_id,
				created_at DESC
			);

		CREATE TABLE IF NOT EXISTS work_pattern_profile_ablation_evaluations_v1 (
			id                   TEXT PRIMARY KEY,
			ablation_run_id      TEXT NOT NULL
				REFERENCES work_pattern_profile_ablation_runs_v1(id)
				ON DELETE RESTRICT,
			task_episode_id      TEXT NOT NULL
				REFERENCES task_episodes(id)
				ON DELETE RESTRICT,
			shadow_evaluation_id TEXT NOT NULL DEFAULT '',
			policy_version       TEXT NOT NULL,
			neutral_score        REAL,
			profile_score        REAL,
			neutral_boundary_error_ms INTEGER,
			profile_boundary_error_ms INTEGER,
			winner               TEXT NOT NULL CHECK (
				winner IN ('neutral', 'profile', 'tie', 'unknown')
			),
			evidence_json        TEXT NOT NULL,
			created_at           INTEGER NOT NULL,
			UNIQUE (
				ablation_run_id,
				task_episode_id,
				shadow_evaluation_id,
				policy_version
			)
		);
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_ablation_eval_v1
			ON work_pattern_profile_ablation_evaluations_v1(
				ablation_run_id,
				created_at ASC
			);

		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_snapshots_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_snapshots_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile snapshots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_snapshots_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_snapshots_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile snapshots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_evidence_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_evidence_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile evidence is immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_evidence_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_evidence_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile evidence is immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_corrections_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_corrections_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile corrections are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_corrections_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_corrections_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile corrections are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_ablation_runs_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_ablation_runs_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile ablation runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_ablation_runs_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_ablation_runs_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile ablation runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_ablation_eval_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_ablation_evaluations_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile ablation evaluations are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_ablation_eval_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_ablation_evaluations_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile ablation evaluations are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create work pattern profile ledgers: %w", err)
	}

	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{
			table:      "task_episode_context_snapshots",
			name:       "work_profile_snapshot_id",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "task_episode_context_snapshots",
			name:       "work_profile_sha256",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "shadow_runs",
			name:       "work_profile_snapshot_id",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "shadow_runs",
			name:       "work_profile_sha256",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
	}
	for _, column := range columns {
		exists, err := taskContextManifestColumnExists(
			tx,
			column.table,
			column.name,
		)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN %s %s`,
			column.table,
			column.name,
			column.definition,
		)); err != nil {
			return fmt.Errorf(
				"add %s.%s: %w",
				column.table,
				column.name,
				err,
			)
		}
	}
	if _, err := tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_task_episode_context_work_profile_v1
			ON task_episode_context_snapshots(
				work_profile_snapshot_id,
				cutoff_at DESC
			);
		CREATE INDEX IF NOT EXISTS idx_shadow_runs_work_profile_v1
			ON shadow_runs(
				work_profile_snapshot_id,
				created_at DESC
			);
	`); err != nil {
		return fmt.Errorf("index frozen work pattern profiles: %w", err)
	}
	return nil
}

// migrateWorkPatternProfileCorrectionRevisions upgrades the short-lived
// pre-revision schema that may have been created by an earlier dev build. The
// correction ledger remains append-only; existing rows are copied in their
// original order and assigned deterministic per-scope revision numbers.
func migrateWorkPatternProfileCorrectionRevisions(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf(
			"migrate work pattern profile correction revisions: transaction is nil",
		)
	}
	exists, err := taskContextManifestColumnExists(
		tx,
		"work_pattern_profile_corrections_v1",
		"revision",
	)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(`
			DROP TRIGGER IF EXISTS work_pattern_profile_corrections_no_update_v1;
			DROP TRIGGER IF EXISTS work_pattern_profile_corrections_no_delete_v1;
			DROP INDEX IF EXISTS idx_work_pattern_profile_corrections_scope_v1;

			ALTER TABLE work_pattern_profile_corrections_v1
				RENAME TO work_pattern_profile_corrections_pre_revision_v1;

			CREATE TABLE work_pattern_profile_corrections_v1 (
				id                  TEXT PRIMARY KEY,
				idempotency_key     TEXT NOT NULL UNIQUE,
				profile_snapshot_id TEXT NOT NULL
					REFERENCES work_pattern_profile_snapshots_v1(id)
					ON DELETE RESTRICT,
				scope_kind          TEXT NOT NULL,
				scope_key           TEXT NOT NULL,
				revision            INTEGER NOT NULL CHECK (revision > 0),
				original_statement  TEXT NOT NULL,
				correction          TEXT NOT NULL,
				created_at          INTEGER NOT NULL,
				UNIQUE (scope_kind, scope_key, revision)
			);

			INSERT INTO work_pattern_profile_corrections_v1 (
				id, idempotency_key, profile_snapshot_id, scope_kind,
				scope_key, revision, original_statement, correction, created_at
			)
			SELECT
				id,
				idempotency_key,
				profile_snapshot_id,
				scope_kind,
				scope_key,
				ROW_NUMBER() OVER (
					PARTITION BY scope_kind, scope_key
					ORDER BY created_at ASC, id ASC
				),
				original_statement,
				correction,
				created_at
			FROM work_pattern_profile_corrections_pre_revision_v1;

			DROP TABLE work_pattern_profile_corrections_pre_revision_v1;
		`); err != nil {
			return fmt.Errorf(
				"upgrade work pattern profile correction revisions: %w",
				err,
			)
		}
	}
	if _, err := tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_work_pattern_profile_corrections_scope_v1
			ON work_pattern_profile_corrections_v1(
				scope_kind,
				scope_key,
				revision DESC
			);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_work_pattern_profile_corrections_revision_v1
			ON work_pattern_profile_corrections_v1(
				scope_kind,
				scope_key,
				revision
			);
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_corrections_no_update_v1
		BEFORE UPDATE ON work_pattern_profile_corrections_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile corrections are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS work_pattern_profile_corrections_no_delete_v1
		BEFORE DELETE ON work_pattern_profile_corrections_v1
		BEGIN
			SELECT RAISE(ABORT, 'work pattern profile corrections are immutable');
		END;
	`); err != nil {
		return fmt.Errorf(
			"finalize work pattern profile correction revisions: %w",
			err,
		)
	}
	return nil
}
