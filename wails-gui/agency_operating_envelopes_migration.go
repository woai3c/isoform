package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func migrateAgencyOperatingEnvelopes(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency operating envelope migration requires a transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_operating_envelopes_v1 (
			id                         TEXT PRIMARY KEY,
			idempotency_key            TEXT NOT NULL UNIQUE,
			contract_version           TEXT NOT NULL,
			policy_version             TEXT NOT NULL,
			workspace_root             TEXT NOT NULL,
			workspace_root_hash        TEXT NOT NULL,
			mode                       TEXT NOT NULL,
			capabilities_json          TEXT NOT NULL,
			capabilities_hash          TEXT NOT NULL,
			max_risk_level             TEXT NOT NULL,
			max_effort_minutes         INTEGER NOT NULL,
			max_token_budget           INTEGER NOT NULL,
			max_monetary_minor_units   INTEGER NOT NULL,
			allow_irreversible         INTEGER NOT NULL DEFAULT 0,
			supersedes_id              TEXT NOT NULL DEFAULT '',
			issued_at                  INTEGER NOT NULL,
			expires_at                 INTEGER NOT NULL,
			payload_json               TEXT NOT NULL,
			payload_hash               TEXT NOT NULL,
			created_at                 INTEGER NOT NULL,
			CHECK (mode IN ('review_only', 'local_draft')),
			CHECK (max_risk_level = 'low'),
			CHECK (contract_version = 'agency-operating-envelope-v1'),
			CHECK (policy_version = 'agency-operating-envelope-policy-v1'),
			CHECK (max_effort_minutes = 30),
			CHECK (max_token_budget = 20000),
			CHECK (max_monetary_minor_units = 0),
			CHECK (allow_irreversible = 0),
			CHECK (issued_at > 0),
			CHECK (expires_at > issued_at),
			CHECK (expires_at - issued_at <= 2592000000),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_operating_envelopes_issued_v1
			ON agency_operating_envelopes_v1(issued_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_operating_envelope_events_v1 (
			id                    TEXT PRIMARY KEY,
			envelope_id           TEXT NOT NULL REFERENCES agency_operating_envelopes_v1(id),
			idempotency_key       TEXT NOT NULL UNIQUE,
			event_type            TEXT NOT NULL,
			reason                TEXT NOT NULL DEFAULT '',
			related_envelope_id   TEXT NOT NULL DEFAULT '',
			occurred_at           INTEGER NOT NULL,
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			created_at            INTEGER NOT NULL,
			CHECK (event_type IN ('issued', 'revoked', 'replaced')),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_operating_envelope_events_v1
			ON agency_operating_envelope_events_v1(
				envelope_id, occurred_at ASC, id ASC
			);

		CREATE TRIGGER IF NOT EXISTS agency_operating_envelopes_v1_no_update
		BEFORE UPDATE ON agency_operating_envelopes_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelopes are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelopes_v1_no_delete
		BEFORE DELETE ON agency_operating_envelopes_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelopes are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelope_events_v1_no_update
		BEFORE UPDATE ON agency_operating_envelope_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelope_events_v1_no_delete
		BEFORE DELETE ON agency_operating_envelope_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope events are immutable');
		END;
	`); err != nil {
		return err
	}
	return migrateAgencyOperatingEnvelopePolicyGuard(tx)
}

// The additive trigger also protects databases where the original table was
// created before exact v1 limits were encoded as CHECK constraints.
func migrateAgencyOperatingEnvelopePolicyGuard(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency operating envelope policy guard requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelopes_v1_policy_guard
		BEFORE INSERT ON agency_operating_envelopes_v1
		WHEN NEW.contract_version != 'agency-operating-envelope-v1'
			OR NEW.policy_version != 'agency-operating-envelope-policy-v1'
			OR NEW.mode NOT IN ('review_only', 'local_draft')
			OR NEW.max_risk_level != 'low'
			OR NEW.max_effort_minutes != 30
			OR NEW.max_token_budget != 20000
			OR NEW.max_monetary_minor_units != 0
			OR NEW.allow_irreversible != 0
			OR NEW.expires_at <= NEW.issued_at
			OR NEW.expires_at - NEW.issued_at > 2592000000
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope violates the fixed v1 policy');
		END;
	`)
	return err
}

// migrateAgencyOperatingEnvelopeGmailDraftBoundary widens the immutable
// envelope table through a transactional rebuild. Existing v1 rows and event
// history are copied byte-for-byte; only newly issued v2 envelopes can include
// the exact Gmail draft capability.
func migrateAgencyOperatingEnvelopeGmailDraftBoundary(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency Gmail draft boundary migration requires a transaction")
	}
	var schema string
	err := tx.QueryRow(`
		SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'agency_operating_envelopes_v1'
	`).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("agency operating envelope table is unavailable")
	}
	if err != nil {
		return err
	}
	if !strings.Contains(schema, "local_and_gmail_draft") {
		if _, err := tx.Exec(`
			DROP TABLE IF EXISTS agency_operating_envelope_events_v2_rebuild;
			DROP TABLE IF EXISTS agency_operating_envelopes_v2_rebuild;

			CREATE TABLE agency_operating_envelopes_v2_rebuild (
				id                         TEXT PRIMARY KEY,
				idempotency_key            TEXT NOT NULL UNIQUE,
				contract_version           TEXT NOT NULL,
				policy_version             TEXT NOT NULL,
				workspace_root             TEXT NOT NULL,
				workspace_root_hash        TEXT NOT NULL,
				mode                       TEXT NOT NULL,
				capabilities_json          TEXT NOT NULL,
				capabilities_hash          TEXT NOT NULL,
				max_risk_level             TEXT NOT NULL,
				max_effort_minutes         INTEGER NOT NULL,
				max_token_budget           INTEGER NOT NULL,
				max_monetary_minor_units   INTEGER NOT NULL,
				allow_irreversible         INTEGER NOT NULL DEFAULT 0,
				supersedes_id              TEXT NOT NULL DEFAULT '',
				issued_at                  INTEGER NOT NULL,
				expires_at                 INTEGER NOT NULL,
				payload_json               TEXT NOT NULL,
				payload_hash               TEXT NOT NULL,
				created_at                 INTEGER NOT NULL,
				CHECK (
					contract_version = 'agency-operating-envelope-v1'
					AND policy_version = 'agency-operating-envelope-policy-v1'
					AND mode IN ('review_only', 'local_draft')
					OR contract_version = 'agency-operating-envelope-v2'
					AND policy_version = 'agency-operating-envelope-policy-v2'
					AND mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
				),
				CHECK (max_risk_level = 'low'),
				CHECK (max_effort_minutes = 30),
				CHECK (max_token_budget = 20000),
				CHECK (max_monetary_minor_units = 0),
				CHECK (allow_irreversible = 0),
				CHECK (issued_at > 0),
				CHECK (expires_at > issued_at),
				CHECK (expires_at - issued_at <= 2592000000),
				CHECK (created_at > 0)
			);

			CREATE TABLE agency_operating_envelope_events_v2_rebuild (
				id                    TEXT PRIMARY KEY,
				envelope_id           TEXT NOT NULL REFERENCES agency_operating_envelopes_v2_rebuild(id),
				idempotency_key       TEXT NOT NULL UNIQUE,
				event_type            TEXT NOT NULL,
				reason                TEXT NOT NULL DEFAULT '',
				related_envelope_id   TEXT NOT NULL DEFAULT '',
				occurred_at           INTEGER NOT NULL,
				payload_json          TEXT NOT NULL,
				payload_hash          TEXT NOT NULL,
				created_at            INTEGER NOT NULL,
				CHECK (event_type IN ('issued', 'revoked', 'replaced')),
				CHECK (occurred_at > 0),
				CHECK (created_at > 0)
			);

			INSERT INTO agency_operating_envelopes_v2_rebuild
			SELECT * FROM agency_operating_envelopes_v1;
			INSERT INTO agency_operating_envelope_events_v2_rebuild
			SELECT * FROM agency_operating_envelope_events_v1;

			DROP TABLE agency_operating_envelope_events_v1;
			DROP TABLE agency_operating_envelopes_v1;
			ALTER TABLE agency_operating_envelopes_v2_rebuild
				RENAME TO agency_operating_envelopes_v1;
			ALTER TABLE agency_operating_envelope_events_v2_rebuild
				RENAME TO agency_operating_envelope_events_v1;
		`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_agency_operating_envelopes_issued_v1
			ON agency_operating_envelopes_v1(issued_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_operating_envelope_events_v1
			ON agency_operating_envelope_events_v1(
				envelope_id, occurred_at ASC, id ASC
			);

		CREATE TRIGGER IF NOT EXISTS agency_operating_envelopes_v1_no_update
		BEFORE UPDATE ON agency_operating_envelopes_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelopes are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelopes_v1_no_delete
		BEFORE DELETE ON agency_operating_envelopes_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelopes are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelope_events_v1_no_update
		BEFORE UPDATE ON agency_operating_envelope_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_operating_envelope_events_v1_no_delete
		BEFORE DELETE ON agency_operating_envelope_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope events are immutable');
		END;

		DROP TRIGGER IF EXISTS agency_operating_envelopes_v1_policy_guard;
		CREATE TRIGGER agency_operating_envelopes_v1_policy_guard
		BEFORE INSERT ON agency_operating_envelopes_v1
		WHEN NOT (
			NEW.contract_version = 'agency-operating-envelope-v1'
			AND NEW.policy_version = 'agency-operating-envelope-policy-v1'
			AND NEW.mode IN ('review_only', 'local_draft')
			OR NEW.contract_version = 'agency-operating-envelope-v2'
			AND NEW.policy_version = 'agency-operating-envelope-policy-v2'
			AND NEW.mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
		)
			OR NEW.max_risk_level != 'low'
			OR NEW.max_effort_minutes != 30
			OR NEW.max_token_budget != 20000
			OR NEW.max_monetary_minor_units != 0
			OR NEW.allow_irreversible != 0
			OR NEW.expires_at <= NEW.issued_at
			OR NEW.expires_at - NEW.issued_at > 2592000000
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope violates the fixed versioned policy');
		END;
	`)
	return err
}
