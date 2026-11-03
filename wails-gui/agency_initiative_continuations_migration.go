package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrateAgencyInitiativeContinuations installs an immutable relation ledger
// between one terminal execution report, its latest independent evaluation,
// and the separate decision about what the Initiative should do next.
func migrateAgencyInitiativeContinuations(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("agency initiative continuation migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_continuations_v1 (
			id                              TEXT PRIMARY KEY,
			idempotency_key                 TEXT NOT NULL UNIQUE,
			policy_version                  TEXT NOT NULL,
			initiative_id                   TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			initiative_payload_hash         TEXT NOT NULL,
			prior_run_id                    TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			prior_run_payload_hash          TEXT NOT NULL,
			outcome_id                      TEXT NOT NULL REFERENCES agency_initiative_outcomes_v2(id) ON DELETE RESTRICT,
			outcome_payload_hash            TEXT NOT NULL,
			outcome_evaluation_id           TEXT NOT NULL REFERENCES agency_initiative_outcome_evaluations_v1(id) ON DELETE RESTRICT,
			outcome_evaluation_payload_hash TEXT NOT NULL,
			requested_at                    INTEGER NOT NULL,
			payload_json                    TEXT NOT NULL,
			payload_hash                    TEXT NOT NULL,
			created_at                      INTEGER NOT NULL,
			UNIQUE (outcome_evaluation_id, policy_version),
			CHECK (requested_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_continuations_initiative_v1
			ON agency_initiative_continuations_v1(
				initiative_id, requested_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_continuations_evaluation_v1
			ON agency_initiative_continuations_v1(
				outcome_evaluation_id, policy_version
			);

		CREATE TABLE IF NOT EXISTS agency_initiative_continuation_events_v1 (
			id                 TEXT PRIMARY KEY,
			continuation_id    TEXT NOT NULL REFERENCES agency_initiative_continuations_v1(id) ON DELETE RESTRICT,
			idempotency_key    TEXT NOT NULL UNIQUE,
			sequence           INTEGER NOT NULL,
			from_status        TEXT NOT NULL DEFAULT '',
			to_status          TEXT NOT NULL CHECK (to_status IN (
				'pending', 'evaluating', 'waiting', 'continue_scheduled',
				'completed', 'failed'
			)),
			reason             TEXT NOT NULL DEFAULT '',
			planning_run_id    TEXT NOT NULL DEFAULT '',
			planning_payload_hash TEXT NOT NULL DEFAULT '',
			successor_run_id   TEXT NOT NULL DEFAULT '',
			successor_run_payload_hash TEXT NOT NULL DEFAULT '',
			context_hash       TEXT NOT NULL DEFAULT '',
			self_revision_id   TEXT NOT NULL DEFAULT '',
			wait_condition     TEXT NOT NULL DEFAULT '',
			model              TEXT NOT NULL DEFAULT '',
			prompt_version     TEXT NOT NULL DEFAULT '',
			prompt_hash        TEXT NOT NULL DEFAULT '',
			raw_output_hash    TEXT NOT NULL DEFAULT '',
			detail_json        TEXT NOT NULL,
			payload_json       TEXT NOT NULL,
			payload_hash       TEXT NOT NULL,
			occurred_at        INTEGER NOT NULL,
			created_at         INTEGER NOT NULL,
			UNIQUE (continuation_id, sequence),
			CHECK (sequence > 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0),
			CHECK (
				(to_status = 'continue_scheduled' AND planning_run_id <> ''
					AND planning_payload_hash <> '' AND successor_run_id <> ''
					AND successor_run_payload_hash <> '')
				OR
				(to_status <> 'continue_scheduled' AND planning_run_id = ''
					AND planning_payload_hash = '' AND successor_run_id = ''
					AND successor_run_payload_hash = '')
			),
			CHECK (
				(to_status = 'waiting' AND wait_condition <> '')
				OR
				(to_status <> 'waiting' AND wait_condition = '')
			)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_continuation_events_current_v1
			ON agency_initiative_continuation_events_v1(
				continuation_id, sequence DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_continuation_events_status_v1
			ON agency_initiative_continuation_events_v1(
				to_status, occurred_at, continuation_id
			);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_agency_continuation_successor_once_v1
			ON agency_initiative_continuation_events_v1(continuation_id)
			WHERE successor_run_id <> '';

		CREATE TRIGGER IF NOT EXISTS agency_initiative_continuations_v1_no_update
		BEFORE UPDATE ON agency_initiative_continuations_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative continuation roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_continuations_v1_no_delete
		BEFORE DELETE ON agency_initiative_continuations_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative continuation roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_continuation_events_v1_no_update
		BEFORE UPDATE ON agency_initiative_continuation_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative continuation events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_continuation_events_v1_no_delete
		BEFORE DELETE ON agency_initiative_continuation_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative continuation events are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create agency Initiative continuation ledger: %w", err)
	}
	return nil
}
