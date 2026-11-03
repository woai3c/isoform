package main

import (
	"database/sql"
	"fmt"
)

func migrateAgencyInitiativeReAdmissions(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate agency Initiative re-admissions: transaction is nil")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_initiative_re_admissions_v1 (
			id                               TEXT PRIMARY KEY,
			idempotency_key                  TEXT NOT NULL UNIQUE,
			policy_version                   TEXT NOT NULL,
			prior_initiative_id              TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			prior_initiative_payload_hash    TEXT NOT NULL,
			prior_planning_run_id            TEXT NOT NULL REFERENCES agency_planning_runs_v2(id) ON DELETE RESTRICT,
			prior_planning_payload_hash      TEXT NOT NULL,
			prior_envelope_id                TEXT NOT NULL REFERENCES agency_operating_envelopes_v1(id) ON DELETE RESTRICT,
			prior_envelope_event_id          TEXT NOT NULL DEFAULT '',
			successor_envelope_id            TEXT NOT NULL REFERENCES agency_operating_envelopes_v1(id) ON DELETE RESTRICT,
			successor_envelope_payload_hash  TEXT NOT NULL,
			loss_kind                        TEXT NOT NULL CHECK (
				loss_kind IN ('revoked', 'expired', 'replaced')
			),
			requested_at                     INTEGER NOT NULL,
			payload_json                     TEXT NOT NULL,
			payload_hash                     TEXT NOT NULL,
			created_at                       INTEGER NOT NULL,
			UNIQUE (prior_initiative_id, successor_envelope_id, policy_version),
			CHECK (prior_envelope_id <> successor_envelope_id),
			CHECK (requested_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_re_admissions_prior_v1
			ON agency_initiative_re_admissions_v1(
				prior_initiative_id, requested_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_re_admissions_successor_envelope_v1
			ON agency_initiative_re_admissions_v1(
				successor_envelope_id, requested_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS agency_initiative_re_admission_events_v1 (
			id                       TEXT PRIMARY KEY,
			re_admission_id          TEXT NOT NULL REFERENCES agency_initiative_re_admissions_v1(id) ON DELETE RESTRICT,
			idempotency_key          TEXT NOT NULL UNIQUE,
			sequence                 INTEGER NOT NULL,
			from_status              TEXT NOT NULL DEFAULT '',
			to_status                TEXT NOT NULL CHECK (
				to_status IN (
					'pending', 'evaluating', 'no_longer_useful',
					'duplicate_suppressed', 'conflict_suppressed',
					'authority_denied', 'resource_deferred',
					'fresh_initiative_created', 'failed'
				)
			),
			reason                   TEXT NOT NULL DEFAULT '',
			origination_run_id       TEXT NOT NULL DEFAULT '',
			origination_candidate_id TEXT NOT NULL DEFAULT '',
			successor_initiative_id  TEXT NOT NULL DEFAULT '',
			context_hash             TEXT NOT NULL DEFAULT '',
			self_revision_id         TEXT NOT NULL DEFAULT '',
			detail_json              TEXT NOT NULL,
			payload_json             TEXT NOT NULL,
			payload_hash             TEXT NOT NULL,
			occurred_at              INTEGER NOT NULL,
			created_at               INTEGER NOT NULL,
			UNIQUE (re_admission_id, sequence),
			CHECK (sequence > 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0),
			CHECK (
				(to_status = 'fresh_initiative_created' AND successor_initiative_id <> '')
				OR
				(to_status <> 'fresh_initiative_created' AND successor_initiative_id = '')
			)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_re_admission_events_current_v1
			ON agency_initiative_re_admission_events_v1(
				re_admission_id, sequence DESC
			);
		CREATE INDEX IF NOT EXISTS idx_agency_re_admission_events_status_v1
			ON agency_initiative_re_admission_events_v1(
				to_status, occurred_at, re_admission_id
			);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_agency_re_admission_successor_once_v1
			ON agency_initiative_re_admission_events_v1(re_admission_id)
			WHERE successor_initiative_id <> '';

		CREATE TRIGGER IF NOT EXISTS agency_initiative_re_admissions_v1_no_update
		BEFORE UPDATE ON agency_initiative_re_admissions_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative re-admission roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_re_admissions_v1_no_delete
		BEFORE DELETE ON agency_initiative_re_admissions_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative re-admission roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_re_admission_events_v1_no_update
		BEFORE UPDATE ON agency_initiative_re_admission_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative re-admission events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_re_admission_events_v1_no_delete
		BEFORE DELETE ON agency_initiative_re_admission_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency Initiative re-admission events are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("migrate agency Initiative re-admissions: %w", err)
	}
	return nil
}
