package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// migrateAgencyOperatingEnvelopeParallelInitiatives adds one bounded portfolio
// limit while preserving every historical v1-v3 payload and event byte-for-byte.
func migrateAgencyOperatingEnvelopeParallelInitiatives(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency parallel Initiative migration requires a transaction")
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
	if !strings.Contains(schema, "agency-operating-envelope-v4") {
		if _, err := tx.Exec(`
			DROP TABLE IF EXISTS agency_operating_envelope_events_v4_rebuild;
			DROP TABLE IF EXISTS agency_operating_envelopes_v4_rebuild;

			CREATE TABLE agency_operating_envelopes_v4_rebuild (
				id                           TEXT PRIMARY KEY,
				idempotency_key              TEXT NOT NULL UNIQUE,
				contract_version             TEXT NOT NULL,
				policy_version               TEXT NOT NULL,
				workspace_root               TEXT NOT NULL,
				workspace_root_hash          TEXT NOT NULL,
				mode                         TEXT NOT NULL,
				capabilities_json            TEXT NOT NULL,
				capabilities_hash            TEXT NOT NULL,
				purpose_scope_refs_json      TEXT NOT NULL DEFAULT '[]',
				purpose_scope_hash           TEXT NOT NULL DEFAULT '',
				max_concurrent_initiatives   INTEGER NOT NULL DEFAULT 1,
				max_risk_level               TEXT NOT NULL,
				max_effort_minutes           INTEGER NOT NULL,
				max_token_budget             INTEGER NOT NULL,
				max_monetary_minor_units     INTEGER NOT NULL,
				allow_irreversible           INTEGER NOT NULL DEFAULT 0,
				supersedes_id                TEXT NOT NULL DEFAULT '',
				issued_at                    INTEGER NOT NULL,
				expires_at                   INTEGER NOT NULL,
				payload_json                 TEXT NOT NULL,
				payload_hash                 TEXT NOT NULL,
				created_at                   INTEGER NOT NULL,
				CHECK (
					contract_version = 'agency-operating-envelope-v1'
					AND policy_version = 'agency-operating-envelope-policy-v1'
					AND mode IN ('review_only', 'local_draft')
					AND purpose_scope_refs_json = '[]'
					AND purpose_scope_hash = ''
					AND max_concurrent_initiatives = 1
					OR contract_version = 'agency-operating-envelope-v2'
					AND policy_version = 'agency-operating-envelope-policy-v2'
					AND mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
					AND purpose_scope_refs_json = '[]'
					AND purpose_scope_hash = ''
					AND max_concurrent_initiatives = 1
					OR contract_version = 'agency-operating-envelope-v3'
					AND policy_version = 'agency-operating-envelope-policy-v3'
					AND mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
					AND purpose_scope_refs_json != '[]'
					AND length(purpose_scope_hash) = 64
					AND max_concurrent_initiatives = 1
					OR contract_version = 'agency-operating-envelope-v4'
					AND policy_version = 'agency-operating-envelope-policy-v4'
					AND mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
					AND purpose_scope_refs_json != '[]'
					AND length(purpose_scope_hash) = 64
					AND max_concurrent_initiatives BETWEEN 1 AND 3
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

			CREATE TABLE agency_operating_envelope_events_v4_rebuild (
				id                    TEXT PRIMARY KEY,
				envelope_id           TEXT NOT NULL REFERENCES agency_operating_envelopes_v4_rebuild(id),
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

			INSERT INTO agency_operating_envelopes_v4_rebuild (
				id, idempotency_key, contract_version, policy_version,
				workspace_root, workspace_root_hash, mode, capabilities_json,
				capabilities_hash, purpose_scope_refs_json, purpose_scope_hash,
				max_concurrent_initiatives, max_risk_level, max_effort_minutes,
				max_token_budget, max_monetary_minor_units, allow_irreversible,
				supersedes_id, issued_at, expires_at, payload_json, payload_hash,
				created_at
			)
			SELECT
				id, idempotency_key, contract_version, policy_version,
				workspace_root, workspace_root_hash, mode, capabilities_json,
				capabilities_hash, purpose_scope_refs_json, purpose_scope_hash,
				1, max_risk_level, max_effort_minutes, max_token_budget,
				max_monetary_minor_units, allow_irreversible, supersedes_id,
				issued_at, expires_at, payload_json, payload_hash, created_at
			FROM agency_operating_envelopes_v1;

			INSERT INTO agency_operating_envelope_events_v4_rebuild
			SELECT * FROM agency_operating_envelope_events_v1;

			DROP TABLE agency_operating_envelope_events_v1;
			DROP TABLE agency_operating_envelopes_v1;
			ALTER TABLE agency_operating_envelopes_v4_rebuild
				RENAME TO agency_operating_envelopes_v1;
			ALTER TABLE agency_operating_envelope_events_v4_rebuild
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
			AND NEW.purpose_scope_refs_json = '[]'
			AND NEW.purpose_scope_hash = ''
			AND NEW.max_concurrent_initiatives = 1
			OR NEW.contract_version = 'agency-operating-envelope-v2'
			AND NEW.policy_version = 'agency-operating-envelope-policy-v2'
			AND NEW.mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
			AND NEW.purpose_scope_refs_json = '[]'
			AND NEW.purpose_scope_hash = ''
			AND NEW.max_concurrent_initiatives = 1
			OR NEW.contract_version = 'agency-operating-envelope-v3'
			AND NEW.policy_version = 'agency-operating-envelope-policy-v3'
			AND NEW.mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
			AND NEW.purpose_scope_refs_json != '[]'
			AND length(NEW.purpose_scope_hash) = 64
			AND NEW.max_concurrent_initiatives = 1
			OR NEW.contract_version = 'agency-operating-envelope-v4'
			AND NEW.policy_version = 'agency-operating-envelope-policy-v4'
			AND NEW.mode IN ('review_only', 'local_draft', 'local_and_gmail_draft')
			AND NEW.purpose_scope_refs_json != '[]'
			AND length(NEW.purpose_scope_hash) = 64
			AND NEW.max_concurrent_initiatives BETWEEN 1 AND 3
		)
			OR NEW.max_risk_level != 'low'
			OR NEW.max_effort_minutes != 30
			OR NEW.max_token_budget != 20000
			OR NEW.max_monetary_minor_units != 0
			OR NEW.allow_irreversible != 0
			OR NEW.expires_at <= NEW.issued_at
			OR NEW.expires_at - NEW.issued_at > 2592000000
		BEGIN
			SELECT RAISE(ABORT, 'agency operating envelope violates the fixed versioned parallel-work policy');
		END;
	`)
	return err
}
