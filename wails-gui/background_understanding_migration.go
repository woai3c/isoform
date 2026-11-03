package main

import (
	"database/sql"
	"fmt"
)

// migrateBackgroundUnderstanding installs the privacy-grant, mission, source
// receipt, and materialization ledgers. Product state is derived from immutable
// roots and append-only events so scope changes and revocations remain auditable.
func migrateBackgroundUnderstanding(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf(
			"background understanding migration requires a transaction",
		)
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS background_understanding_setting_events_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL UNIQUE,
			enabled               INTEGER NOT NULL CHECK (enabled IN (0, 1)),
			daily_run_limit       INTEGER NOT NULL CHECK (
				daily_run_limit >= 1 AND daily_run_limit <= 24
			),
			min_interval_minutes  INTEGER NOT NULL CHECK (
				min_interval_minutes >= 5 AND min_interval_minutes <= 1440
			),
			reason                TEXT NOT NULL DEFAULT '',
			created_at            INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_background_settings_created_v1
			ON background_understanding_setting_events_v1(
				created_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS background_source_grants_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL UNIQUE,
			source                TEXT NOT NULL CHECK (source IN ('gmail')),
			scope_label           TEXT NOT NULL,
			source_query          TEXT NOT NULL,
			lookback_days         INTEGER NOT NULL CHECK (
				lookback_days >= 1 AND lookback_days <= 365
			),
			max_items_per_run     INTEGER NOT NULL CHECK (
				max_items_per_run >= 1 AND max_items_per_run <= 50
			),
			allowed_operations_json TEXT NOT NULL,
			policy_version        TEXT NOT NULL,
			issued_at             INTEGER NOT NULL CHECK (issued_at > 0),
			expires_at            INTEGER NOT NULL DEFAULT 0 CHECK (
				expires_at = 0 OR expires_at > issued_at
			),
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			created_at            INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_background_source_grants_v1
			ON background_source_grants_v1(
				source, issued_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS background_source_grant_events_v1 (
			id                    TEXT PRIMARY KEY,
			grant_id              TEXT NOT NULL REFERENCES background_source_grants_v1(id)
				ON DELETE RESTRICT,
			idempotency_key       TEXT NOT NULL UNIQUE,
			event_type            TEXT NOT NULL CHECK (
				event_type IN ('issued', 'revoked')
			),
			reason                TEXT NOT NULL DEFAULT '',
			occurred_at           INTEGER NOT NULL CHECK (occurred_at > 0),
			payload_json          TEXT NOT NULL,
			payload_hash          TEXT NOT NULL,
			created_at            INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_background_source_grant_events_v1
			ON background_source_grant_events_v1(
				grant_id, occurred_at ASC, id ASC
			);

		CREATE TABLE IF NOT EXISTS background_understanding_runs_v1 (
			id                    TEXT PRIMARY KEY,
			idempotency_key       TEXT NOT NULL UNIQUE,
			trigger_kind          TEXT NOT NULL CHECK (
				trigger_kind IN ('automatic', 'manual')
			),
			trigger_reason        TEXT NOT NULL,
			mission_kind          TEXT NOT NULL CHECK (
				mission_kind IN ('retained_activity', 'gmail')
			),
			source                TEXT NOT NULL CHECK (
				source IN ('local_activity', 'gmail')
			),
			grant_id              TEXT NOT NULL DEFAULT '',
			policy_version        TEXT NOT NULL,
			source_scope_json     TEXT NOT NULL,
			started_at            INTEGER NOT NULL CHECK (started_at > 0),
			created_at            INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_background_runs_created_v1
			ON background_understanding_runs_v1(
				created_at DESC, id DESC
			);
		CREATE INDEX IF NOT EXISTS idx_background_runs_mission_v1
			ON background_understanding_runs_v1(
				mission_kind, created_at DESC, id DESC
			);

		CREATE TABLE IF NOT EXISTS background_understanding_run_events_v1 (
			id                    TEXT PRIMARY KEY,
			run_id                TEXT NOT NULL REFERENCES background_understanding_runs_v1(id)
				ON DELETE RESTRICT,
			sequence              INTEGER NOT NULL CHECK (sequence >= 1),
			state                 TEXT NOT NULL CHECK (
				state IN (
					'selected', 'running', 'completed',
					'skipped', 'failed', 'cancelled'
				)
			),
			detail_json           TEXT NOT NULL,
			detail_hash           TEXT NOT NULL,
			occurred_at           INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at            INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, sequence)
		);
		CREATE INDEX IF NOT EXISTS idx_background_run_events_v1
			ON background_understanding_run_events_v1(
				run_id, sequence ASC
			);

		CREATE TABLE IF NOT EXISTS background_understanding_source_items_v1 (
			id                    TEXT PRIMARY KEY,
			run_id                TEXT NOT NULL REFERENCES background_understanding_runs_v1(id)
				ON DELETE RESTRICT,
			source                TEXT NOT NULL CHECK (
				source IN ('local_activity', 'gmail')
			),
			source_item_kind      TEXT NOT NULL,
			external_id_hash      TEXT NOT NULL,
			thread_id_hash        TEXT NOT NULL DEFAULT '',
			content_hash          TEXT NOT NULL,
			attribution_kind      TEXT NOT NULL CHECK (
				attribution_kind IN ('self', 'other', 'unknown', 'aggregate')
			),
			observed_at           INTEGER NOT NULL DEFAULT 0 CHECK (
				observed_at >= 0
			),
			metadata_json         TEXT NOT NULL,
			created_at            INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, external_id_hash, content_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_background_source_items_v1
			ON background_understanding_source_items_v1(
				source, external_id_hash, content_hash, created_at DESC
			);

		CREATE TABLE IF NOT EXISTS background_understanding_materializations_v1 (
			id                    TEXT PRIMARY KEY,
			run_id                TEXT NOT NULL REFERENCES background_understanding_runs_v1(id)
				ON DELETE RESTRICT,
			artifact_kind         TEXT NOT NULL CHECK (
				artifact_kind IN (
					'memory', 'knowledge', 'work_pattern_profile'
				)
			),
			artifact_id           TEXT NOT NULL,
			action                TEXT NOT NULL CHECK (
				action IN ('created', 'updated', 'existing', 'skipped')
			),
			summary               TEXT NOT NULL DEFAULT '',
			source_receipt_hash   TEXT NOT NULL DEFAULT '',
			created_at            INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, artifact_kind, artifact_id, action)
		);
		CREATE INDEX IF NOT EXISTS idx_background_materializations_v1
			ON background_understanding_materializations_v1(
				run_id, created_at ASC, id ASC
			);
	`); err != nil {
		return fmt.Errorf("create background understanding ledgers: %w", err)
	}

	for _, table := range []string{
		"background_understanding_setting_events_v1",
		"background_source_grants_v1",
		"background_source_grant_events_v1",
		"background_understanding_runs_v1",
		"background_understanding_run_events_v1",
		"background_understanding_source_items_v1",
		"background_understanding_materializations_v1",
	} {
		if _, err := tx.Exec(fmt.Sprintf(`
			CREATE TRIGGER IF NOT EXISTS %s_no_update
			BEFORE UPDATE ON %s
			BEGIN SELECT RAISE(ABORT, '%s is immutable'); END;
			CREATE TRIGGER IF NOT EXISTS %s_no_delete
			BEFORE DELETE ON %s
			BEGIN SELECT RAISE(ABORT, '%s is immutable'); END;
		`,
			table, table, table,
			table, table, table,
		)); err != nil {
			return fmt.Errorf(
				"create background understanding immutability triggers for %s: %w",
				table,
				err,
			)
		}
	}
	return nil
}
