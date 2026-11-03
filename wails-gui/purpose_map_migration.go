package main

import (
	"database/sql"
	"errors"
	"fmt"
)

// migratePurposeMap installs the immutable operational-purpose ledger. Roots,
// lifecycle events, evidence links, relations, and relation events are all
// append-only; current state is always derived from validated history.
func migratePurposeMap(tx *sql.Tx) error {
	if tx == nil {
		return errors.New("Purpose Map migration requires transaction")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS purpose_open_loops_v1 (
			id               TEXT PRIMARY KEY,
			idempotency_key  TEXT NOT NULL UNIQUE,
			canonical_key    TEXT NOT NULL,
			kind             TEXT NOT NULL CHECK (kind IN (
				'commitment', 'responsibility', 'goal', 'concern', 'opportunity'
			)),
			contract_version TEXT NOT NULL CHECK (contract_version = 'purpose-open-loop-v1'),
			created_at       INTEGER NOT NULL CHECK (created_at > 0),
			payload_json     TEXT NOT NULL,
			payload_hash     TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loops_created_v1
			ON purpose_open_loops_v1(created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loops_canonical_v1
			ON purpose_open_loops_v1(canonical_key, created_at, id);

		CREATE TABLE IF NOT EXISTS purpose_open_loop_events_v1 (
			id                  TEXT PRIMARY KEY,
			open_loop_id        TEXT NOT NULL REFERENCES purpose_open_loops_v1(id) ON DELETE RESTRICT,
			idempotency_key     TEXT NOT NULL UNIQUE,
			sequence            INTEGER NOT NULL CHECK (sequence > 0),
			event_type          TEXT NOT NULL CHECK (event_type IN (
				'discovered', 'reinforced', 'revised', 'waiting', 'resolved',
				'reopened', 'contradicted', 'dismissed', 'superseded'
			)),
			expected_event_id   TEXT NOT NULL DEFAULT '',
			predecessor_event_id TEXT NOT NULL DEFAULT '',
			title               TEXT NOT NULL,
			desired_outcome     TEXT NOT NULL,
			owner               TEXT NOT NULL CHECK (owner IN ('user', 'other', 'shared', 'unknown')),
			counterparty        TEXT NOT NULL DEFAULT '',
			scope               TEXT NOT NULL DEFAULT '',
			due_kind            TEXT NOT NULL CHECK (due_kind IN ('none', 'date', 'window', 'condition', 'unknown')),
			due_value           TEXT NOT NULL DEFAULT '',
			waiting_condition   TEXT NOT NULL DEFAULT '',
			lifecycle_state     TEXT NOT NULL CHECK (lifecycle_state IN (
				'candidate', 'active', 'waiting', 'resolved', 'contradicted',
				'dismissed', 'superseded'
			)),
			support_state       TEXT NOT NULL CHECK (support_state IN (
				'hypothesis', 'supported', 'contradicted', 'unknown'
			)),
			confidence          REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
			rationale           TEXT NOT NULL DEFAULT '',
			reasoning_status    TEXT NOT NULL CHECK (reasoning_status IN ('explicit', 'unknown')),
			evidence_set_hash   TEXT NOT NULL,
			occurred_at         INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at          INTEGER NOT NULL CHECK (created_at > 0),
			payload_json        TEXT NOT NULL,
			payload_hash        TEXT NOT NULL,
			UNIQUE (open_loop_id, sequence),
			CHECK (
				(sequence = 1 AND event_type = 'discovered' AND expected_event_id = '' AND predecessor_event_id = '')
				OR
				(sequence > 1 AND event_type <> 'discovered' AND expected_event_id <> '' AND predecessor_event_id <> '')
			),
			CHECK (
				(reasoning_status = 'explicit' AND rationale <> '')
				OR
				(reasoning_status = 'unknown' AND rationale = '')
			),
			CHECK (
				(due_kind IN ('none', 'unknown') AND due_value = '')
				OR
				(due_kind IN ('date', 'window', 'condition') AND due_value <> '')
			),
			CHECK (
				(lifecycle_state = 'waiting' AND waiting_condition <> '')
				OR
				(lifecycle_state <> 'waiting')
			)
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loop_events_current_v1
			ON purpose_open_loop_events_v1(open_loop_id, sequence DESC);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loop_events_occurred_v1
			ON purpose_open_loop_events_v1(occurred_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS purpose_open_loop_evidence_v1 (
			id                   TEXT PRIMARY KEY,
			event_id             TEXT NOT NULL REFERENCES purpose_open_loop_events_v1(id) ON DELETE RESTRICT,
			source_kind          TEXT NOT NULL,
			source_id            TEXT NOT NULL,
			source_version       TEXT NOT NULL,
			source_observed_at   INTEGER NOT NULL CHECK (source_observed_at > 0),
			source_hash          TEXT NOT NULL,
			evidence_class       TEXT NOT NULL CHECK (evidence_class IN (
				'explicit_user', 'observed_user', 'evaluated_projection',
				'verified_external', 'verified_outcome', 'correction'
			)),
			stance               TEXT NOT NULL CHECK (stance IN (
				'support', 'update', 'wait', 'resolve', 'contradict', 'dismiss'
			)),
			independence_key     TEXT NOT NULL,
			excerpt_hash         TEXT NOT NULL DEFAULT '',
			privacy_decision     TEXT NOT NULL CHECK (privacy_decision IN ('include', 'redact')),
			created_at           INTEGER NOT NULL CHECK (created_at > 0),
			payload_json         TEXT NOT NULL,
			payload_hash         TEXT NOT NULL,
			UNIQUE (event_id, source_kind, source_id, source_version, stance)
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loop_evidence_event_v1
			ON purpose_open_loop_evidence_v1(event_id, id);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loop_evidence_source_v1
			ON purpose_open_loop_evidence_v1(source_kind, source_id, source_version);

		CREATE TABLE IF NOT EXISTS purpose_open_loop_relations_v1 (
			id               TEXT PRIMARY KEY,
			idempotency_key  TEXT NOT NULL UNIQUE,
			subject_id       TEXT NOT NULL REFERENCES purpose_open_loops_v1(id) ON DELETE RESTRICT,
			object_id        TEXT NOT NULL REFERENCES purpose_open_loops_v1(id) ON DELETE RESTRICT,
			relation_type    TEXT NOT NULL CHECK (relation_type IN (
				'duplicate_of', 'supersedes', 'depends_on', 'blocks',
				'related_to'
			)),
			contract_version TEXT NOT NULL CHECK (contract_version = 'purpose-open-loop-relation-v1'),
			created_at       INTEGER NOT NULL CHECK (created_at > 0),
			payload_json     TEXT NOT NULL,
			payload_hash     TEXT NOT NULL,
			CHECK (subject_id <> object_id),
			UNIQUE (subject_id, object_id, relation_type)
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loop_relations_subject_v1
			ON purpose_open_loop_relations_v1(subject_id, relation_type, object_id);
		CREATE INDEX IF NOT EXISTS idx_purpose_open_loop_relations_object_v1
			ON purpose_open_loop_relations_v1(object_id, relation_type, subject_id);

		CREATE TABLE IF NOT EXISTS purpose_open_loop_relation_events_v1 (
			id                 TEXT PRIMARY KEY,
			relation_id        TEXT NOT NULL REFERENCES purpose_open_loop_relations_v1(id) ON DELETE RESTRICT,
			idempotency_key    TEXT NOT NULL UNIQUE,
			sequence           INTEGER NOT NULL CHECK (sequence > 0),
			event_type         TEXT NOT NULL CHECK (event_type IN ('asserted', 'retracted')),
			expected_event_id  TEXT NOT NULL DEFAULT '',
			evidence_event_id  TEXT NOT NULL REFERENCES purpose_open_loop_events_v1(id) ON DELETE RESTRICT,
			reason             TEXT NOT NULL DEFAULT '',
			occurred_at        INTEGER NOT NULL CHECK (occurred_at > 0),
			created_at         INTEGER NOT NULL CHECK (created_at > 0),
			payload_json       TEXT NOT NULL,
			payload_hash       TEXT NOT NULL,
			UNIQUE (relation_id, sequence),
			CHECK (
				(sequence = 1 AND event_type = 'asserted' AND expected_event_id = '')
				OR
				(sequence > 1 AND expected_event_id <> '')
			),
			CHECK (event_type <> 'retracted' OR reason <> '')
		);
		CREATE INDEX IF NOT EXISTS idx_purpose_relation_events_current_v1
			ON purpose_open_loop_relation_events_v1(relation_id, sequence DESC);

		CREATE TRIGGER IF NOT EXISTS purpose_open_loops_v1_no_update
		BEFORE UPDATE ON purpose_open_loops_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose roots are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loops_v1_no_delete
		BEFORE DELETE ON purpose_open_loops_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose roots are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_events_v1_no_update
		BEFORE UPDATE ON purpose_open_loop_events_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_events_v1_no_delete
		BEFORE DELETE ON purpose_open_loop_events_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_evidence_v1_no_update
		BEFORE UPDATE ON purpose_open_loop_evidence_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose evidence is immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_evidence_v1_no_delete
		BEFORE DELETE ON purpose_open_loop_evidence_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose evidence is immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_relations_v1_no_update
		BEFORE UPDATE ON purpose_open_loop_relations_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose relations are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_relations_v1_no_delete
		BEFORE DELETE ON purpose_open_loop_relations_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose relations are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_relation_events_v1_no_update
		BEFORE UPDATE ON purpose_open_loop_relation_events_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose relation events are immutable'); END;
		CREATE TRIGGER IF NOT EXISTS purpose_open_loop_relation_events_v1_no_delete
		BEFORE DELETE ON purpose_open_loop_relation_events_v1
		BEGIN SELECT RAISE(ABORT, 'Purpose relation events are immutable'); END;
	`); err != nil {
		return fmt.Errorf("create Purpose Map ledger: %w", err)
	}
	return nil
}
