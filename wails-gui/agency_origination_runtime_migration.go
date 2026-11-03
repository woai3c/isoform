package main

import (
	"database/sql"
	"fmt"
)

// migrateAgencyOriginationRuntime installs the durable boundary between the
// pure origination planner and product state. All records are append-only:
// planner confidence can be audited, but it cannot mutate authority or execute
// work.
func migrateAgencyOriginationRuntime(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("agency origination runtime migration requires a transaction")
	}
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_ambient_authority_grants_v1 (
			id                         TEXT PRIMARY KEY,
			idempotency_key            TEXT NOT NULL UNIQUE,
			capability                 TEXT NOT NULL,
			scope_hash                 TEXT NOT NULL DEFAULT '',
			max_risk_level             TEXT NOT NULL,
			max_effort_minutes         INTEGER NOT NULL,
			max_token_budget           INTEGER NOT NULL,
			max_monetary_minor_units   INTEGER NOT NULL,
			allow_irreversible         INTEGER NOT NULL DEFAULT 0,
			issued_at                  INTEGER NOT NULL,
			expires_at                 INTEGER NOT NULL,
			payload_json               TEXT NOT NULL,
			payload_hash               TEXT NOT NULL,
			created_at                 INTEGER NOT NULL,
			CHECK (max_risk_level IN ('low', 'medium', 'high', 'critical')),
			CHECK (max_effort_minutes >= 0),
			CHECK (max_token_budget >= 0),
			CHECK (max_monetary_minor_units >= 0),
			CHECK (allow_irreversible IN (0, 1)),
			CHECK (issued_at > 0),
			CHECK (expires_at > issued_at)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_ambient_authority_capability_v1
			ON agency_ambient_authority_grants_v1(
				capability, issued_at DESC, expires_at DESC, id
			);

		CREATE TABLE IF NOT EXISTS agency_ambient_authority_events_v1 (
			id                TEXT PRIMARY KEY,
			grant_id          TEXT NOT NULL REFERENCES agency_ambient_authority_grants_v1(id),
			idempotency_key   TEXT NOT NULL UNIQUE,
			event_type        TEXT NOT NULL,
			reason            TEXT NOT NULL DEFAULT '',
			occurred_at       INTEGER NOT NULL,
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			created_at        INTEGER NOT NULL,
			CHECK (event_type IN ('issued', 'revoked')),
			CHECK (occurred_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_ambient_authority_events_grant_v1
			ON agency_ambient_authority_events_v1(
				grant_id, occurred_at ASC, id ASC
			);

		CREATE TABLE IF NOT EXISTS agency_origination_runs_v1 (
			id                            TEXT PRIMARY KEY,
			idempotency_key               TEXT NOT NULL UNIQUE,
			status                        TEXT NOT NULL,
			wake_reason                   TEXT NOT NULL,
			wake_at                       INTEGER NOT NULL,
			wake_detail                   TEXT NOT NULL DEFAULT '',
			source_input_json             TEXT NOT NULL,
			source_manifest_json          TEXT NOT NULL,
			planning_input_json           TEXT NOT NULL,
			context_hash                  TEXT NOT NULL DEFAULT '',
			self_revision_id              TEXT NOT NULL DEFAULT '',
			runtime_version               TEXT NOT NULL DEFAULT '',
			planner_version               TEXT NOT NULL DEFAULT '',
			prompt_version                TEXT NOT NULL DEFAULT '',
			input_version                 TEXT NOT NULL DEFAULT '',
			output_version                TEXT NOT NULL DEFAULT '',
			prompt                        TEXT NOT NULL DEFAULT '',
			prompt_hash                   TEXT NOT NULL DEFAULT '',
			model                         TEXT NOT NULL DEFAULT '',
			raw_output                    TEXT NOT NULL DEFAULT '',
			output_json                   TEXT NOT NULL,
			rejected_evidence_refs_json   TEXT NOT NULL,
			rejected_candidate_keys_json  TEXT NOT NULL,
			error_stage                   TEXT NOT NULL DEFAULT '',
			error_text                    TEXT NOT NULL DEFAULT '',
			selected_candidate_key        TEXT NOT NULL DEFAULT '',
			initiative_id                 TEXT NOT NULL DEFAULT '',
			initiative_run_id             TEXT NOT NULL DEFAULT '',
			permission_request_id         TEXT NOT NULL DEFAULT '',
			resource_policy_json          TEXT NOT NULL,
			resource_usage_json           TEXT NOT NULL,
			payload_json                  TEXT NOT NULL,
			payload_hash                  TEXT NOT NULL,
			created_at                    INTEGER NOT NULL,
			CHECK (status IN (
				'no_useful_work',
				'initiative_created',
				'permission_requested',
				'authority_denied',
				'duplicate_suppressed',
				'conflict_suppressed',
				'resource_deferred',
				'planner_error',
				'parse_error',
				'validation_error',
				'source_error',
				'adapter_error'
			)),
			CHECK (wake_reason IN ('periodic', 'meaningful_state_change')),
			CHECK (wake_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_origination_runs_created_v1
			ON agency_origination_runs_v1(created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_origination_runs_status_v1
			ON agency_origination_runs_v1(status, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_origination_candidates_v1 (
			id                       TEXT PRIMARY KEY,
			run_id                   TEXT NOT NULL REFERENCES agency_origination_runs_v1(id),
			local_key                TEXT NOT NULL,
			kind                     TEXT NOT NULL,
			rank                     INTEGER NOT NULL,
			selected                 INTEGER NOT NULL DEFAULT 0,
			candidate_json           TEXT NOT NULL,
			semantic_hash            TEXT NOT NULL,
			disposition              TEXT NOT NULL,
			reason                   TEXT NOT NULL DEFAULT '',
			authority_decision_json  TEXT NOT NULL,
			duplication_check_json   TEXT NOT NULL,
			initiative_id            TEXT NOT NULL DEFAULT '',
			initiative_run_id        TEXT NOT NULL DEFAULT '',
			permission_request_id    TEXT NOT NULL DEFAULT '',
			payload_json             TEXT NOT NULL,
			payload_hash             TEXT NOT NULL,
			created_at               INTEGER NOT NULL,
			UNIQUE (run_id, local_key),
			CHECK (kind IN ('initiative', 'handoff', 'schedule', 'invalid')),
			CHECK (rank > 0),
			CHECK (selected IN (0, 1)),
			CHECK (disposition IN (
				'not_selected',
				'initiative_created',
				'permission_requested',
				'authority_denied',
				'duplicate_suppressed',
				'conflict_suppressed',
				'resource_deferred',
				'planner_rejected',
				'adapter_error'
			)),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_origination_candidates_run_v1
			ON agency_origination_candidates_v1(run_id, rank ASC, id ASC);
		CREATE INDEX IF NOT EXISTS idx_agency_origination_candidates_semantic_v1
			ON agency_origination_candidates_v1(
				semantic_hash, created_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS agency_origination_permission_requests_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL UNIQUE,
			run_id                TEXT NOT NULL REFERENCES agency_origination_runs_v1(id),
			candidate_id          TEXT NOT NULL REFERENCES agency_origination_candidates_v1(id),
			kind                  TEXT NOT NULL,
			permission_contract   TEXT NOT NULL,
			recurrence            TEXT NOT NULL DEFAULT '',
			request_json          TEXT NOT NULL,
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			created_at            INTEGER NOT NULL,
			CHECK (kind IN ('handoff', 'schedule')),
			CHECK (
				(kind = 'handoff' AND permission_contract = 'jit' AND recurrence = '')
				OR
				(kind = 'schedule' AND permission_contract = 'standing' AND recurrence != '')
			),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_permission_requests_created_v1
			ON agency_origination_permission_requests_v1(
				created_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS agency_origination_permission_events_v1 (
			id                TEXT PRIMARY KEY,
			request_id        TEXT NOT NULL REFERENCES agency_origination_permission_requests_v1(id),
			idempotency_key   TEXT NOT NULL UNIQUE,
			status            TEXT NOT NULL,
			reason            TEXT NOT NULL DEFAULT '',
			occurred_at       INTEGER NOT NULL,
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			created_at        INTEGER NOT NULL,
			CHECK (status IN ('pending', 'granted', 'rejected', 'cancelled', 'expired')),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_permission_events_request_v1
			ON agency_origination_permission_events_v1(
				request_id, occurred_at ASC, id ASC
			);

		CREATE TRIGGER IF NOT EXISTS agency_ambient_authority_grants_v1_no_update
		BEFORE UPDATE ON agency_ambient_authority_grants_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency ambient authority grants are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_ambient_authority_grants_v1_no_delete
		BEFORE DELETE ON agency_ambient_authority_grants_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency ambient authority grants are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_ambient_authority_events_v1_no_update
		BEFORE UPDATE ON agency_ambient_authority_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency ambient authority events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_ambient_authority_events_v1_no_delete
		BEFORE DELETE ON agency_ambient_authority_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency ambient authority events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_runs_v1_no_update
		BEFORE UPDATE ON agency_origination_runs_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_runs_v1_no_delete
		BEFORE DELETE ON agency_origination_runs_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_candidates_v1_no_update
		BEFORE UPDATE ON agency_origination_candidates_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination candidates are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_candidates_v1_no_delete
		BEFORE DELETE ON agency_origination_candidates_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination candidates are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_permission_requests_v1_no_update
		BEFORE UPDATE ON agency_origination_permission_requests_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination permission requests are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_permission_requests_v1_no_delete
		BEFORE DELETE ON agency_origination_permission_requests_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination permission requests are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_permission_events_v1_no_update
		BEFORE UPDATE ON agency_origination_permission_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination permission events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_origination_permission_events_v1_no_delete
		BEFORE DELETE ON agency_origination_permission_events_v1
		BEGIN
			SELECT RAISE(ABORT, 'agency origination permission events are immutable');
		END;
	`)
	if err != nil {
		return fmt.Errorf("create agency origination runtime schema: %w", err)
	}
	for _, column := range []struct {
		table string
		name  string
		sql   string
	}{
		{
			table: "agency_origination_runs_v1",
			name:  "runtime_version",
			sql: `ALTER TABLE agency_origination_runs_v1
				ADD COLUMN runtime_version TEXT NOT NULL DEFAULT ''`,
		},
		{
			table: "agency_origination_runs_v1",
			name:  "initiative_run_id",
			sql: `ALTER TABLE agency_origination_runs_v1
				ADD COLUMN initiative_run_id TEXT NOT NULL DEFAULT ''`,
		},
		{
			table: "agency_origination_candidates_v1",
			name:  "initiative_run_id",
			sql: `ALTER TABLE agency_origination_candidates_v1
				ADD COLUMN initiative_run_id TEXT NOT NULL DEFAULT ''`,
		},
	} {
		exists, err := agencyOriginationRuntimeColumnExists(
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
		if _, err := tx.Exec(column.sql); err != nil {
			return fmt.Errorf(
				"add %s.%s: %w",
				column.table,
				column.name,
				err,
			)
		}
	}
	return nil
}

func agencyOriginationRuntimeColumnExists(
	tx *sql.Tx,
	table, column string,
) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue any
			primaryKey   int
		)
		if err := rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
