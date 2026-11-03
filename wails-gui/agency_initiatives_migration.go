package main

import (
	"database/sql"
	"fmt"
)

// migrateAgencyInitiativesV2 creates the production Agency runtime without
// changing or importing the proposal-shaped legacy initiatives table.
//
// Durable product history is append-only. The lease table is deliberately the
// only mutable table: it is a coordination projection protected by immutable
// lease events and run-state events.
func migrateAgencyInitiativesV2(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate agency initiatives v2: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS agency_planning_runs_v2 (
			id                        TEXT PRIMARY KEY,
			idempotency_key           TEXT NOT NULL UNIQUE,
			initiative_id             TEXT NOT NULL DEFAULT '',
			wake_reason               TEXT NOT NULL,
			wake_at                   INTEGER NOT NULL,
			decision                  TEXT NOT NULL CHECK (
				decision IN ('selected', 'no_useful_work')
			),
			context_manifest_json     TEXT NOT NULL,
			context_hash              TEXT NOT NULL,
			self_revision_id          TEXT NOT NULL DEFAULT '',
			candidate_work_json       TEXT NOT NULL,
			selected_candidate_json   TEXT NOT NULL,
			expected_usefulness_json  TEXT NOT NULL,
			risk_json                 TEXT NOT NULL,
			cost_json                 TEXT NOT NULL,
			dependencies_json         TEXT NOT NULL,
			duplication_check_json    TEXT NOT NULL,
			capability_manifest_json  TEXT NOT NULL,
			authority_snapshot_json   TEXT NOT NULL,
			budget_snapshot_json      TEXT NOT NULL,
			model                     TEXT NOT NULL,
			prompt                    TEXT NOT NULL,
			prompt_version            TEXT NOT NULL,
			planner_version           TEXT NOT NULL,
			raw_output                TEXT NOT NULL,
			payload_json              TEXT NOT NULL,
			planner_rollout_event_id  TEXT NOT NULL DEFAULT '',
			planner_rollout_event_payload_hash TEXT NOT NULL DEFAULT '',
			planner_release_decision_id TEXT NOT NULL DEFAULT '',
			planner_release_decision_payload_hash TEXT NOT NULL DEFAULT '',
			rendered_prompt_hash      TEXT NOT NULL DEFAULT '',
			payload_hash              TEXT NOT NULL,
			created_at                INTEGER NOT NULL,
			CHECK (wake_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_planning_runs_v2_initiative
			ON agency_planning_runs_v2(initiative_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_planning_runs_v2_decision
			ON agency_planning_runs_v2(decision, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_initiatives_v2 (
			id                          TEXT PRIMARY KEY,
			idempotency_key             TEXT NOT NULL UNIQUE,
			name                        TEXT NOT NULL,
			purpose                     TEXT NOT NULL,
			desired_outcome             TEXT NOT NULL,
			scope_json                  TEXT NOT NULL,
			why_now                     TEXT NOT NULL,
			originating_planning_run_id TEXT NOT NULL DEFAULT '',
			self_revision_id            TEXT NOT NULL DEFAULT '',
			authority_snapshot_json     TEXT NOT NULL,
			budget_snapshot_json        TEXT NOT NULL,
			creation_payload_json       TEXT NOT NULL,
			creation_payload_hash       TEXT NOT NULL,
			created_at                  INTEGER NOT NULL,
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiatives_v2_created
			ON agency_initiatives_v2(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_initiative_state_events_v2 (
			id                TEXT PRIMARY KEY,
			initiative_id     TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			idempotency_key   TEXT NOT NULL UNIQUE,
			sequence          INTEGER NOT NULL,
			from_state        TEXT NOT NULL DEFAULT '',
			to_state          TEXT NOT NULL CHECK (
				to_state IN (
					'planning', 'active', 'waiting', 'paused',
					'completed', 'stopped', 'failed'
				)
			),
			reason            TEXT NOT NULL DEFAULT '',
			working_summary   TEXT NOT NULL DEFAULT '',
			next_action       TEXT NOT NULL DEFAULT '',
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			occurred_at       INTEGER NOT NULL,
			created_at        INTEGER NOT NULL,
			UNIQUE (initiative_id, sequence),
			CHECK (sequence > 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_state_events_v2_current
			ON agency_initiative_state_events_v2(initiative_id, sequence DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_state_events_v2_state
			ON agency_initiative_state_events_v2(to_state, occurred_at DESC);

		CREATE TABLE IF NOT EXISTS agency_initiative_runs_v2 (
			id                       TEXT PRIMARY KEY,
			initiative_id            TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			planning_run_id          TEXT NOT NULL DEFAULT '',
			idempotency_key          TEXT NOT NULL UNIQUE,
			wake_condition_json      TEXT NOT NULL,
			context_manifest_json    TEXT NOT NULL,
			context_hash             TEXT NOT NULL,
			self_revision_id         TEXT NOT NULL DEFAULT '',
			authority_snapshot_json  TEXT NOT NULL,
			budget_snapshot_json     TEXT NOT NULL,
			max_attempts             INTEGER NOT NULL,
			retry_backoff_ms         INTEGER NOT NULL,
			creation_payload_json    TEXT NOT NULL,
			creation_payload_hash    TEXT NOT NULL,
			created_at               INTEGER NOT NULL,
			CHECK (max_attempts > 0),
			CHECK (retry_backoff_ms >= 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_runs_v2_initiative
			ON agency_initiative_runs_v2(initiative_id, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_initiative_run_events_v2 (
			id                TEXT PRIMARY KEY,
			run_id            TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			idempotency_key   TEXT NOT NULL UNIQUE,
			sequence          INTEGER NOT NULL,
			from_state        TEXT NOT NULL DEFAULT '',
			to_state          TEXT NOT NULL CHECK (
				to_state IN (
					'pending', 'leased', 'running', 'waiting',
					'retry_scheduled', 'succeeded', 'failed', 'cancelled'
				)
			),
			reason            TEXT NOT NULL DEFAULT '',
			available_at      INTEGER NOT NULL,
			retry_number      INTEGER NOT NULL DEFAULT 0,
			recovery_number   INTEGER NOT NULL DEFAULT 0,
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			occurred_at       INTEGER NOT NULL,
			created_at        INTEGER NOT NULL,
			UNIQUE (run_id, sequence),
			CHECK (sequence > 0),
			CHECK (available_at >= 0),
			CHECK (retry_number >= 0),
			CHECK (recovery_number >= 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_run_events_v2_current
			ON agency_initiative_run_events_v2(run_id, sequence DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_run_events_v2_pending
			ON agency_initiative_run_events_v2(to_state, available_at, run_id);

		CREATE TABLE IF NOT EXISTS agency_initiative_run_leases_v2 (
			run_id        TEXT PRIMARY KEY REFERENCES agency_initiative_runs_v2(id) ON DELETE CASCADE,
			lease_token   TEXT NOT NULL UNIQUE,
			worker_id     TEXT NOT NULL,
			generation    INTEGER NOT NULL,
			acquired_at   INTEGER NOT NULL,
			renewed_at    INTEGER NOT NULL,
			expires_at    INTEGER NOT NULL,
			CHECK (generation > 0),
			CHECK (acquired_at > 0),
			CHECK (renewed_at >= acquired_at),
			CHECK (expires_at > renewed_at)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_run_leases_v2_expiry
			ON agency_initiative_run_leases_v2(expires_at, run_id);

		CREATE TABLE IF NOT EXISTS agency_initiative_lease_events_v2 (
			id                TEXT PRIMARY KEY,
			run_id            TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			idempotency_key   TEXT NOT NULL UNIQUE,
			lease_token       TEXT NOT NULL,
			worker_id         TEXT NOT NULL,
			generation        INTEGER NOT NULL,
			event_type        TEXT NOT NULL CHECK (
				event_type IN ('acquired', 'renewed', 'released', 'expired_recovered')
			),
			expires_at        INTEGER NOT NULL,
			detail_json       TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			occurred_at       INTEGER NOT NULL,
			created_at        INTEGER NOT NULL,
			CHECK (generation > 0),
			CHECK (expires_at > 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_lease_events_v2_run
			ON agency_initiative_lease_events_v2(run_id, occurred_at ASC, id ASC);

		CREATE TABLE IF NOT EXISTS agency_initiative_actions_v2 (
			id                    TEXT PRIMARY KEY,
			initiative_id         TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id                TEXT NOT NULL REFERENCES agency_initiative_runs_v2(id) ON DELETE RESTRICT,
			idempotency_key       TEXT NOT NULL UNIQUE,
			sequence              INTEGER NOT NULL,
			capability_key        TEXT NOT NULL,
			capability_json       TEXT NOT NULL,
			policy_decision       TEXT NOT NULL CHECK (
				policy_decision IN ('allow', 'require_approval', 'deny')
			),
			policy_version        TEXT NOT NULL,
			policy_evaluation_json TEXT NOT NULL,
			authority_grant_id    TEXT NOT NULL DEFAULT '',
			status                TEXT NOT NULL CHECK (
				status IN ('attempted', 'succeeded', 'failed', 'blocked')
			),
			request_json          TEXT NOT NULL,
			result_json           TEXT NOT NULL,
			before_evidence_json  TEXT NOT NULL,
			after_evidence_json   TEXT NOT NULL,
			tool_trace_json       TEXT NOT NULL,
			error_text            TEXT NOT NULL DEFAULT '',
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			occurred_at           INTEGER NOT NULL,
			created_at            INTEGER NOT NULL,
			UNIQUE (run_id, sequence),
			CHECK (sequence > 0),
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_actions_v2_run
			ON agency_initiative_actions_v2(run_id, sequence ASC);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_actions_v2_initiative
			ON agency_initiative_actions_v2(initiative_id, occurred_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_initiative_artifacts_v2 (
			id                TEXT PRIMARY KEY,
			initiative_id     TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id            TEXT NOT NULL DEFAULT '',
			action_id         TEXT NOT NULL DEFAULT '',
			idempotency_key   TEXT NOT NULL UNIQUE,
			kind              TEXT NOT NULL,
			name              TEXT NOT NULL,
			uri               TEXT NOT NULL DEFAULT '',
			media_type        TEXT NOT NULL DEFAULT '',
			content_hash      TEXT NOT NULL,
			metadata_json     TEXT NOT NULL,
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			created_at        INTEGER NOT NULL,
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_artifacts_v2_initiative
			ON agency_initiative_artifacts_v2(initiative_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_artifacts_v2_run
			ON agency_initiative_artifacts_v2(run_id, created_at ASC, id ASC);

		CREATE TABLE IF NOT EXISTS agency_initiative_outcomes_v2 (
			id                TEXT PRIMARY KEY,
			initiative_id     TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id            TEXT NOT NULL DEFAULT '',
			idempotency_key   TEXT NOT NULL UNIQUE,
			kind              TEXT NOT NULL,
			summary           TEXT NOT NULL,
			evidence_json     TEXT NOT NULL,
			metrics_json      TEXT NOT NULL,
			payload_json      TEXT NOT NULL,
			payload_hash      TEXT NOT NULL,
			observed_at       INTEGER NOT NULL,
			created_at        INTEGER NOT NULL,
			CHECK (observed_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_outcomes_v2_initiative
			ON agency_initiative_outcomes_v2(initiative_id, observed_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS agency_initiative_corrections_v2 (
			id                       TEXT PRIMARY KEY,
			initiative_id            TEXT NOT NULL REFERENCES agency_initiatives_v2(id) ON DELETE RESTRICT,
			run_id                   TEXT NOT NULL DEFAULT '',
			idempotency_key          TEXT NOT NULL UNIQUE,
			source_kind              TEXT NOT NULL,
			source_id                TEXT NOT NULL,
			correction_type          TEXT NOT NULL CHECK (
				correction_type IN (
					'redirect', 'constraint', 'factual', 'preference',
					'outcome_review', 'stop_reason'
				)
			),
			statement                TEXT NOT NULL,
			evidence_json            TEXT NOT NULL,
			prior_planning_run_id    TEXT NOT NULL DEFAULT '',
			replanning_run_id        TEXT NOT NULL DEFAULT '',
			self_revision_id         TEXT NOT NULL DEFAULT '',
			payload_json             TEXT NOT NULL,
			payload_hash             TEXT NOT NULL,
			occurred_at              INTEGER NOT NULL,
			created_at               INTEGER NOT NULL,
			CHECK (occurred_at > 0),
			CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_agency_initiative_corrections_v2_initiative
			ON agency_initiative_corrections_v2(initiative_id, occurred_at DESC, id DESC);

		CREATE TRIGGER IF NOT EXISTS agency_planning_runs_v2_no_update
		BEFORE UPDATE ON agency_planning_runs_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency planning runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_planning_runs_v2_no_delete
		BEFORE DELETE ON agency_planning_runs_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency planning runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiatives_v2_no_update
		BEFORE UPDATE ON agency_initiatives_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiatives_v2_no_delete
		BEFORE DELETE ON agency_initiatives_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative roots are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_state_events_v2_no_update
		BEFORE UPDATE ON agency_initiative_state_events_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative state events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_state_events_v2_no_delete
		BEFORE DELETE ON agency_initiative_state_events_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative state events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_runs_v2_no_update
		BEFORE UPDATE ON agency_initiative_runs_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_runs_v2_no_delete
		BEFORE DELETE ON agency_initiative_runs_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative runs are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_run_events_v2_no_update
		BEFORE UPDATE ON agency_initiative_run_events_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative run events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_run_events_v2_no_delete
		BEFORE DELETE ON agency_initiative_run_events_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative run events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_lease_events_v2_no_update
		BEFORE UPDATE ON agency_initiative_lease_events_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative lease events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_lease_events_v2_no_delete
		BEFORE DELETE ON agency_initiative_lease_events_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative lease events are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_actions_v2_no_update
		BEFORE UPDATE ON agency_initiative_actions_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative actions are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_actions_v2_no_delete
		BEFORE DELETE ON agency_initiative_actions_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative actions are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_artifacts_v2_no_update
		BEFORE UPDATE ON agency_initiative_artifacts_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative artifacts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_artifacts_v2_no_delete
		BEFORE DELETE ON agency_initiative_artifacts_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative artifacts are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_outcomes_v2_no_update
		BEFORE UPDATE ON agency_initiative_outcomes_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative outcomes are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_outcomes_v2_no_delete
		BEFORE DELETE ON agency_initiative_outcomes_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative outcomes are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_corrections_v2_no_update
		BEFORE UPDATE ON agency_initiative_corrections_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative corrections are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS agency_initiative_corrections_v2_no_delete
		BEFORE DELETE ON agency_initiative_corrections_v2
		BEGIN
			SELECT RAISE(ABORT, 'agency initiative corrections are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("create agency initiatives v2 ledger: %w", err)
	}
	return nil
}
