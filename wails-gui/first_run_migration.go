package main

import (
	"database/sql"
	"fmt"
)

// migrateFirstRun installs the immutable ledgers for the protected first
// experience of Agency. Product state is derived from sequenced events rather
// than a client-controlled mutable status row.
func migrateFirstRun(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate first run: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS first_run_journeys_v1 (
			id          TEXT PRIMARY KEY,
			journey_key TEXT NOT NULL UNIQUE,
			version     TEXT NOT NULL,
			created_at  INTEGER NOT NULL CHECK (created_at > 0)
		);

		CREATE TABLE IF NOT EXISTS first_run_journey_events_v1 (
			id              TEXT PRIMARY KEY,
			journey_id      TEXT NOT NULL REFERENCES first_run_journeys_v1(id) ON DELETE RESTRICT,
			idempotency_key TEXT NOT NULL UNIQUE,
			sequence        INTEGER NOT NULL CHECK (sequence > 0),
			kind            TEXT NOT NULL CHECK (
				kind IN (
					'started',
					'permission_checked',
					'first_outcome_started',
					'first_outcome_ready',
					'first_outcome_seen',
					'first_outcome_failed',
					'learning_corrected',
					'skipped',
					'day_one_seen',
					'graduated'
				)
			),
			payload_json    TEXT NOT NULL,
			payload_hash    TEXT NOT NULL,
			occurred_at     INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at      INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (journey_id, sequence)
		);
		CREATE INDEX IF NOT EXISTS idx_first_run_journey_events_v1_journey
			ON first_run_journey_events_v1(journey_id, sequence ASC);
		CREATE INDEX IF NOT EXISTS idx_first_run_journey_events_v1_kind
			ON first_run_journey_events_v1(kind, occurred_at DESC);

		CREATE TABLE IF NOT EXISTS first_outcome_runs_v1 (
			id                TEXT PRIMARY KEY,
			journey_id        TEXT NOT NULL REFERENCES first_run_journeys_v1(id) ON DELETE RESTRICT,
			idempotency_key   TEXT NOT NULL UNIQUE,
			execution_mode    TEXT NOT NULL CHECK (execution_mode = 'guided_visible'),
			learning_eligible INTEGER NOT NULL CHECK (learning_eligible = 0),
			capture_started_at INTEGER NOT NULL CHECK (capture_started_at > 0),
			capture_ends_at   INTEGER NOT NULL CHECK (capture_ends_at >= capture_started_at),
			created_at        INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_first_outcome_runs_v1_journey
			ON first_outcome_runs_v1(journey_id, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS first_outcome_run_events_v1 (
			id              TEXT PRIMARY KEY,
			run_id          TEXT NOT NULL REFERENCES first_outcome_runs_v1(id) ON DELETE RESTRICT,
			idempotency_key TEXT NOT NULL UNIQUE,
			sequence        INTEGER NOT NULL CHECK (sequence > 0),
			kind            TEXT NOT NULL CHECK (
				kind IN (
					'started',
					'capturing',
					'analysis_started',
					'completed',
					'failed',
					'cancelled'
				)
			),
			payload_json    TEXT NOT NULL,
			payload_hash    TEXT NOT NULL,
			occurred_at     INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at      INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, sequence)
		);
		CREATE INDEX IF NOT EXISTS idx_first_outcome_run_events_v1_run
			ON first_outcome_run_events_v1(run_id, sequence ASC);

		CREATE TABLE IF NOT EXISTS first_outcome_artifacts_v1 (
			id                  TEXT PRIMARY KEY,
			run_id              TEXT NOT NULL UNIQUE REFERENCES first_outcome_runs_v1(id) ON DELETE RESTRICT,
			title               TEXT NOT NULL,
			task                TEXT NOT NULL,
			progress            TEXT NOT NULL,
			prepared_next_step  TEXT NOT NULL,
			open_loops_json     TEXT NOT NULL,
			uncertainties_json  TEXT NOT NULL,
			evidence_summary    TEXT NOT NULL,
			source_sessions_json TEXT NOT NULL,
			app_names_json      TEXT NOT NULL,
			context_hash        TEXT NOT NULL,
			prompt_version      TEXT NOT NULL,
			model               TEXT NOT NULL,
			prompt_hash         TEXT NOT NULL,
			raw_output_hash     TEXT NOT NULL,
			payload_json        TEXT NOT NULL,
			payload_hash        TEXT NOT NULL,
			created_at          INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_first_outcome_artifacts_v1_created
			ON first_outcome_artifacts_v1(created_at DESC);

		CREATE TABLE IF NOT EXISTS first_run_receipts_v1 (
			id                TEXT PRIMARY KEY,
			journey_id        TEXT NOT NULL REFERENCES first_run_journeys_v1(id) ON DELETE RESTRICT,
			run_id            TEXT NOT NULL DEFAULT '',
			artifact_id       TEXT NOT NULL DEFAULT '',
			idempotency_key   TEXT NOT NULL UNIQUE,
			kind              TEXT NOT NULL CHECK (
				kind IN (
					'first_outcome_ready',
					'first_outcome_failed',
					'learning_correction'
				)
			),
			title             TEXT NOT NULL,
			summary           TEXT NOT NULL,
			outcome           TEXT NOT NULL DEFAULT '',
			safety_mode       TEXT NOT NULL,
			learning_excluded INTEGER NOT NULL CHECK (learning_excluded IN (0, 1)),
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			occurred_at       INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at        INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_first_run_receipts_v1_journey
			ON first_run_receipts_v1(journey_id, occurred_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_first_run_receipts_v1_kind
			ON first_run_receipts_v1(kind, occurred_at DESC);

		CREATE TABLE IF NOT EXISTS first_run_exclusions_v1 (
			id          TEXT PRIMARY KEY,
			journey_id  TEXT NOT NULL REFERENCES first_run_journeys_v1(id) ON DELETE RESTRICT,
			run_id      TEXT NOT NULL REFERENCES first_outcome_runs_v1(id) ON DELETE RESTRICT,
			source_kind TEXT NOT NULL,
			source_id   TEXT NOT NULL,
			reason      TEXT NOT NULL,
			created_at  INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (source_kind, source_id)
		);
		CREATE INDEX IF NOT EXISTS idx_first_run_exclusions_v1_run
			ON first_run_exclusions_v1(run_id, created_at ASC);

		CREATE TABLE IF NOT EXISTS agency_learning_corrections_v1 (
			id                   TEXT PRIMARY KEY,
			idempotency_key      TEXT NOT NULL UNIQUE,
			journey_id           TEXT NOT NULL DEFAULT '',
			capability_key       TEXT NOT NULL,
			learning_revision_id TEXT NOT NULL,
			original_statement   TEXT NOT NULL,
			correction           TEXT NOT NULL,
			payload_json         TEXT NOT NULL,
			payload_hash         TEXT NOT NULL,
			created_at           INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_learning_corrections_v1_capability
			ON agency_learning_corrections_v1(capability_key, created_at DESC);

		CREATE TRIGGER IF NOT EXISTS first_run_journeys_v1_no_update
		BEFORE UPDATE ON first_run_journeys_v1
		BEGIN SELECT RAISE(ABORT, 'first run journeys are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_journeys_v1_no_delete
		BEFORE DELETE ON first_run_journeys_v1
		BEGIN SELECT RAISE(ABORT, 'first run journeys are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_journey_events_v1_no_update
		BEFORE UPDATE ON first_run_journey_events_v1
		BEGIN SELECT RAISE(ABORT, 'first run journey events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_journey_events_v1_no_delete
		BEFORE DELETE ON first_run_journey_events_v1
		BEGIN SELECT RAISE(ABORT, 'first run journey events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_runs_v1_no_update
		BEFORE UPDATE ON first_outcome_runs_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome runs are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_runs_v1_no_delete
		BEFORE DELETE ON first_outcome_runs_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome runs are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_run_events_v1_no_update
		BEFORE UPDATE ON first_outcome_run_events_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome run events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_run_events_v1_no_delete
		BEFORE DELETE ON first_outcome_run_events_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome run events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_artifacts_v1_no_update
		BEFORE UPDATE ON first_outcome_artifacts_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome artifacts are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_artifacts_v1_no_delete
		BEFORE DELETE ON first_outcome_artifacts_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome artifacts are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_receipts_v1_no_update
		BEFORE UPDATE ON first_run_receipts_v1
		BEGIN SELECT RAISE(ABORT, 'first run receipts are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_receipts_v1_no_delete
		BEFORE DELETE ON first_run_receipts_v1
		BEGIN SELECT RAISE(ABORT, 'first run receipts are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_exclusions_v1_no_update
		BEFORE UPDATE ON first_run_exclusions_v1
		BEGIN SELECT RAISE(ABORT, 'first run exclusions are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_run_exclusions_v1_no_delete
		BEFORE DELETE ON first_run_exclusions_v1
		BEGIN SELECT RAISE(ABORT, 'first run exclusions are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS agency_learning_corrections_v1_no_update
		BEFORE UPDATE ON agency_learning_corrections_v1
		BEGIN SELECT RAISE(ABORT, 'agency learning corrections are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS agency_learning_corrections_v1_no_delete
		BEFORE DELETE ON agency_learning_corrections_v1
		BEGIN SELECT RAISE(ABORT, 'agency learning corrections are immutable'); END;
	`); err != nil {
		return fmt.Errorf("create first run ledgers: %w", err)
	}
	return nil
}
