package main

import (
	"database/sql"
	"fmt"
)

// migrateFirstOutcomeDebugReplays installs an append-only operator ledger for
// replaying a First Outcome prompt. Replays are diagnostic evidence only and
// cannot become product history, learning, or authority.
func migrateFirstOutcomeDebugReplays(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate first outcome debug replays: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS first_outcome_debug_replays_v1 (
			id                       TEXT PRIMARY KEY,
			run_id                   TEXT NOT NULL REFERENCES first_outcome_runs_v1(id) ON DELETE RESTRICT,
			replay_version           TEXT NOT NULL,
			status                   TEXT NOT NULL CHECK (status IN ('completed', 'failed')),
			safety_mode              TEXT NOT NULL CHECK (safety_mode = 'local_no_tools'),
			learning_eligible        INTEGER NOT NULL CHECK (learning_eligible = 0),
			product_mutation         INTEGER NOT NULL CHECK (product_mutation = 0),
			source_sessions_json     TEXT NOT NULL,
			context_hash             TEXT NOT NULL,
			original_context_hash    TEXT NOT NULL DEFAULT '',
			context_matches_original INTEGER NOT NULL CHECK (context_matches_original IN (0, 1)),
			prompt_version           TEXT NOT NULL,
			model                    TEXT NOT NULL,
			prompt_hash              TEXT NOT NULL,
			raw_output_hash          TEXT NOT NULL,
			content_json             TEXT NOT NULL,
			error                    TEXT NOT NULL DEFAULT '',
			duration_ms              INTEGER NOT NULL CHECK (duration_ms >= 0),
			created_at               INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_first_outcome_debug_replays_v1_run
			ON first_outcome_debug_replays_v1(run_id, created_at DESC, id DESC);

		CREATE TRIGGER IF NOT EXISTS first_outcome_debug_replays_v1_no_update
		BEFORE UPDATE ON first_outcome_debug_replays_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome debug replays are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS first_outcome_debug_replays_v1_no_delete
		BEFORE DELETE ON first_outcome_debug_replays_v1
		BEGIN SELECT RAISE(ABORT, 'first outcome debug replays are immutable'); END;
	`); err != nil {
		return fmt.Errorf("create first outcome debug replay ledger: %w", err)
	}
	return nil
}
