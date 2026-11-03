package main

import (
	"database/sql"
	"fmt"
)

// migrateSelfDream installs immutable analysis, decision, evaluation, lineage,
// rollout, and replay ledgers. Canonical Self remains in self_model_entries.
func migrateSelfDream(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate Self Dream: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS self_dream_runs_v1 (
			id                TEXT PRIMARY KEY,
			idempotency_key   TEXT NOT NULL UNIQUE,
			trigger_kind      TEXT NOT NULL CHECK (
				trigger_kind IN ('scheduled', 'manual', 'correction', 'startup')
			),
			policy_version    TEXT NOT NULL,
			assembler_version TEXT NOT NULL,
			source_cutoff_at  INTEGER NOT NULL CHECK (source_cutoff_at > 0),
			base_self_json    TEXT NOT NULL,
			base_self_hash    TEXT NOT NULL,
			context_json      TEXT NOT NULL,
			context_hash      TEXT NOT NULL,
			manifest_json     TEXT NOT NULL,
			input_hash        TEXT NOT NULL,
			prompt_version    TEXT NOT NULL,
			prompt            TEXT NOT NULL,
			prompt_hash       TEXT NOT NULL,
			requested_model   TEXT NOT NULL DEFAULT '',
			created_at        INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_runs_v1_input
			ON self_dream_runs_v1(policy_version, input_hash, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_self_dream_runs_v1_created
			ON self_dream_runs_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_dream_events_v1 (
			id          TEXT PRIMARY KEY,
			run_id      TEXT NOT NULL REFERENCES self_dream_runs_v1(id) ON DELETE RESTRICT,
			event_kind  TEXT NOT NULL CHECK (
				event_kind IN (
					'created', 'analyzing', 'attempt_completed',
					'evaluated', 'completed', 'failed', 'promotion_skipped'
				)
			),
			detail_json TEXT NOT NULL DEFAULT '{}',
			created_at  INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_events_v1_run
			ON self_dream_events_v1(run_id, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_dream_attempts_v1 (
			id                TEXT PRIMARY KEY,
			run_id            TEXT NOT NULL REFERENCES self_dream_runs_v1(id) ON DELETE RESTRICT,
			attempt_kind      TEXT NOT NULL CHECK (attempt_kind = 'production_shadow'),
			status            TEXT NOT NULL CHECK (
				status IN ('completed', 'failed', 'deterministic_only')
			),
			safety_mode       TEXT NOT NULL CHECK (safety_mode = 'local_no_tools'),
			learning_eligible INTEGER NOT NULL CHECK (learning_eligible = 0),
			product_mutation  INTEGER NOT NULL CHECK (product_mutation = 0),
			model             TEXT NOT NULL DEFAULT '',
			prompt_hash       TEXT NOT NULL,
			raw_output        TEXT NOT NULL DEFAULT '',
			raw_output_hash   TEXT NOT NULL,
			error             TEXT NOT NULL DEFAULT '',
			duration_ms       INTEGER NOT NULL CHECK (duration_ms >= 0),
			created_at        INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, attempt_kind)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_attempts_v1_run
			ON self_dream_attempts_v1(run_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS self_dream_operations_v1 (
			id                  TEXT PRIMARY KEY,
			run_id              TEXT NOT NULL REFERENCES self_dream_runs_v1(id) ON DELETE RESTRICT,
			attempt_id          TEXT NOT NULL REFERENCES self_dream_attempts_v1(id) ON DELETE RESTRICT,
			ordinal             INTEGER NOT NULL CHECK (ordinal >= 0),
			operation_kind      TEXT NOT NULL CHECK (
				operation_kind IN (
					'merge', 'split', 'contextualize',
					'contradict', 'supersede', 'no_change'
				)
			),
			target_entry_ids_json TEXT NOT NULL,
			self_kind           TEXT NOT NULL,
			scope_kind          TEXT NOT NULL,
			scope_key           TEXT NOT NULL DEFAULT '',
			statement           TEXT NOT NULL DEFAULT '',
			split_statements_json TEXT NOT NULL DEFAULT '[]',
			reasoning_status    TEXT NOT NULL,
			rationale           TEXT NOT NULL DEFAULT '',
			uncertainty         TEXT NOT NULL DEFAULT '',
			confidence          REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
			evidence_refs_json  TEXT NOT NULL,
			source              TEXT NOT NULL CHECK (source IN ('deterministic', 'model')),
			validation_status   TEXT NOT NULL CHECK (
				validation_status IN (
					'qualified', 'review_required', 'rejected', 'no_change'
				)
			),
			validation_reason   TEXT NOT NULL DEFAULT '',
			auto_eligible       INTEGER NOT NULL CHECK (auto_eligible IN (0, 1)),
			operation_hash      TEXT NOT NULL,
			created_at          INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, ordinal),
			UNIQUE (run_id, operation_hash)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_operations_v1_run
			ON self_dream_operations_v1(run_id, ordinal);
		CREATE INDEX IF NOT EXISTS idx_self_dream_operations_v1_review
			ON self_dream_operations_v1(validation_status, created_at DESC);

		CREATE TABLE IF NOT EXISTS self_dream_evaluations_v1 (
			id                    TEXT PRIMARY KEY,
			run_id                TEXT NOT NULL REFERENCES self_dream_runs_v1(id) ON DELETE RESTRICT,
			evaluation_version    TEXT NOT NULL,
			neutral_json          TEXT NOT NULL,
			dream_json            TEXT NOT NULL,
			improvement           REAL NOT NULL,
			coverage_loss         REAL NOT NULL CHECK (coverage_loss >= 0 AND coverage_loss <= 1),
			contradiction_risk    REAL NOT NULL CHECK (contradiction_risk >= 0 AND contradiction_risk <= 1),
			unsupported_reasoning INTEGER NOT NULL CHECK (unsupported_reasoning >= 0),
			real_evidence         INTEGER NOT NULL CHECK (real_evidence IN (0, 1)),
			created_at            INTEGER NOT NULL CHECK (created_at > 0),
			UNIQUE (run_id, evaluation_version)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_evaluations_v1_created
			ON self_dream_evaluations_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_dream_decisions_v1 (
			id              TEXT PRIMARY KEY,
			operation_id    TEXT NOT NULL REFERENCES self_dream_operations_v1(id) ON DELETE RESTRICT,
			idempotency_key TEXT NOT NULL UNIQUE,
			actor_kind      TEXT NOT NULL CHECK (actor_kind IN ('user', 'rollout_gate')),
			decision_kind   TEXT NOT NULL CHECK (
				decision_kind IN (
					'accept', 'keep_separate', 'not_enough_evidence',
					'correct', 'auto_promote'
				)
			),
			note            TEXT NOT NULL DEFAULT '',
			created_at      INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_decisions_v1_operation
			ON self_dream_decisions_v1(operation_id, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_dream_compilations_v1 (
			id                    TEXT PRIMARY KEY,
			operation_id          TEXT NOT NULL UNIQUE REFERENCES self_dream_operations_v1(id) ON DELETE RESTRICT,
			decision_id           TEXT NOT NULL REFERENCES self_dream_decisions_v1(id) ON DELETE RESTRICT,
			compilation_kind      TEXT NOT NULL CHECK (
				compilation_kind IN (
					'merge', 'split', 'contextualize',
					'contradict', 'supersede'
				)
			),
			source_entry_ids_json TEXT NOT NULL,
			output_entry_ids_json TEXT NOT NULL,
			created_at             INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_compilations_v1_created
			ON self_dream_compilations_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_dream_rollout_events_v1 (
			id           TEXT PRIMARY KEY,
			enabled      INTEGER NOT NULL CHECK (enabled IN (0, 1)),
			reason       TEXT NOT NULL,
			metrics_json TEXT NOT NULL,
			created_at   INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_rollout_events_v1_created
			ON self_dream_rollout_events_v1(created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS self_dream_replays_v1 (
			id                      TEXT PRIMARY KEY,
			run_id                  TEXT NOT NULL REFERENCES self_dream_runs_v1(id) ON DELETE RESTRICT,
			status                  TEXT NOT NULL CHECK (status IN ('completed', 'failed')),
			safety_mode             TEXT NOT NULL CHECK (safety_mode = 'local_no_tools'),
			learning_eligible       INTEGER NOT NULL CHECK (learning_eligible = 0),
			product_mutation        INTEGER NOT NULL CHECK (product_mutation = 0),
			context_hash            TEXT NOT NULL,
			context_matches_source  INTEGER NOT NULL CHECK (context_matches_source IN (0, 1)),
			prompt_version          TEXT NOT NULL,
			model                   TEXT NOT NULL,
			prompt_hash             TEXT NOT NULL,
			raw_output              TEXT NOT NULL DEFAULT '',
			raw_output_hash         TEXT NOT NULL,
			operations_json         TEXT NOT NULL,
			error                   TEXT NOT NULL DEFAULT '',
			duration_ms             INTEGER NOT NULL CHECK (duration_ms >= 0),
			created_at              INTEGER NOT NULL CHECK (created_at > 0)
		);
		CREATE INDEX IF NOT EXISTS idx_self_dream_replays_v1_run
			ON self_dream_replays_v1(run_id, created_at DESC, id DESC);
	`); err != nil {
		return fmt.Errorf("create Self Dream ledgers: %w", err)
	}

	for _, table := range []string{
		"self_dream_runs_v1",
		"self_dream_events_v1",
		"self_dream_attempts_v1",
		"self_dream_operations_v1",
		"self_dream_evaluations_v1",
		"self_dream_decisions_v1",
		"self_dream_compilations_v1",
		"self_dream_rollout_events_v1",
		"self_dream_replays_v1",
	} {
		if _, err := tx.Exec(fmt.Sprintf(`
			CREATE TRIGGER IF NOT EXISTS %s_no_update
			BEFORE UPDATE ON %s
			BEGIN SELECT RAISE(ABORT, '%s is immutable'); END;
			CREATE TRIGGER IF NOT EXISTS %s_no_delete
			BEFORE DELETE ON %s
			BEGIN SELECT RAISE(ABORT, '%s is immutable'); END;
		`, table, table, table, table, table, table)); err != nil {
			return fmt.Errorf("protect %s: %w", table, err)
		}
	}
	return nil
}
