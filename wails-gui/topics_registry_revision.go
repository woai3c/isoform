package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"wails-gui/storage"
)

// The writer pool keeps one connection, which can be replaced. This TEMP observer
// is incremental and disappears on close; it changes no persistent schema or data.
// New chronological capture sessions cannot withdraw an existing source.
// Everything present before collection, including late evidence, is watched. All other
// writes and commits from other connections remain conservative invalidators.
type topicActivityRevision struct {
	Generation, Source, External, Schema int64
}

func (s *topicsService) prepareRegistryActivityRevision(ctx context.Context) error {
	if s.app.activity == nil || s.app.activity.store == nil {
		return nil
	}
	s.registryRevisionMu.Lock()
	defer s.registryRevisionMu.Unlock()
	db := s.app.activity.store.db
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var schema int64
	if err := conn.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&schema); err != nil {
		return err
	}
	var observerExists bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_temp_master WHERE type='table' AND name='topic_registry_revision')`).Scan(&observerExists); err != nil {
		return err
	}
	if s.registryObservedDB != db || s.registryObservedSchema != schema || !observerExists {
		// A pool replacement retains the *sql.DB and main schema, but loses TEMP
		// state. Do not mark a partial installation complete if its context fails.
		s.registryObservedDB = nil
		s.registryObserverGeneration++
		if _, err = conn.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS topic_registry_revision (generation INTEGER NOT NULL, revision INTEGER NOT NULL, session_cutoff INTEGER NOT NULL, capture_after INTEGER NOT NULL);
            INSERT INTO topic_registry_revision SELECT 0,0,0,0 WHERE NOT EXISTS (SELECT 1 FROM topic_registry_revision)`); err != nil {
			return err
		}
		// Connection-local Source and data_version can repeat after a swap.
		// Carry a service generation in the row so neither view/source caches
		// nor an earlier publication proof can match the replacement observer.
		if _, err := conn.ExecContext(ctx, `UPDATE topic_registry_revision SET generation=?`, s.registryObserverGeneration); err != nil {
			return err
		}
		rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_table_list WHERE schema='main' AND type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
		for _, table := range tables {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				when := ""
				switch table {
				case "activity_events", "activity_segments", "activity_snapshots", "activity_ocr_frames", "activity_evidence_source_revisions", "activity_event_rewrite_revisions":
					// Revision-table upserts made by evidence triggers can be
					// INSERT or UPDATE, so apply the same session boundary.
					if op == "INSERT" || (op == "UPDATE" && (table == "activity_evidence_source_revisions" || table == "activity_event_rewrite_revisions")) {
						when = ` WHEN NOT EXISTS (SELECT 1 FROM activity_sessions WHERE id=NEW.session_id AND rowid>(SELECT session_cutoff FROM topic_registry_revision))`
					}
				}
				update := "revision=revision+1"
				if table == "activity_sessions" {
					switch op {
					case "INSERT":
						// BEFORE detects replacement as well as backfill. All
						// RHS expressions see the previous watermark.
						update = "revision=revision+CASE WHEN EXISTS (SELECT 1 FROM activity_sessions WHERE id=NEW.id) OR NEW.start_time<capture_after THEN 1 ELSE 0 END, capture_after=MAX(capture_after,NEW.end_time)"
					case "UPDATE":
						update += ", capture_after=MAX(capture_after,NEW.end_time)"
					}
				}
				statement := fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %s BEFORE %s ON main.%s%s BEGIN UPDATE topic_registry_revision SET %s; END`, quote(fmt.Sprintf("topic_registry_%s_%s", table, op)), op, quote(table), when, update)
				if _, err := conn.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		}
		s.registryObservedDB = db
		s.registryObservedSchema = schema
	}
	// Extend coverage incrementally before any new collection. This makes older cache proofs
	// stricter without changing their revision or invalidating good views.
	_, err = conn.ExecContext(ctx, `UPDATE topic_registry_revision SET session_cutoff=COALESCE((SELECT MAX(rowid) FROM activity_sessions),0), capture_after=MAX(capture_after,COALESCE((SELECT MAX(end_time) FROM activity_sessions WHERE rowid>session_cutoff),capture_after)) WHERE session_cutoff<COALESCE((SELECT MAX(rowid) FROM activity_sessions),0)`)
	return err
}

type topicRevisionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readTopicActivityRevision(ctx context.Context, db topicRevisionQueryer) (topicActivityRevision, error) {
	var stamp topicActivityRevision
	err := db.QueryRowContext(ctx, `SELECT generation, revision, data_version, schema_version FROM temp.topic_registry_revision, pragma_data_version, pragma_schema_version`).Scan(&stamp.Generation, &stamp.Source, &stamp.External, &stamp.Schema)
	return stamp, err
}

// Check the scoped revision on the very connection that excludes writers.
// A total_changes check made before acquiring that connection would still
// starve under continuous capture, even though none of its sources changed.
func lockTopicActivityRevision(ctx context.Context, db *sql.DB, expected topicActivityRevision) (func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		return nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK"); _ = conn.Close() })
	}
	current, err := readTopicActivityRevision(ctx, conn)
	if err == nil && current != expected {
		err = storage.ErrReadRevisionChanged
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}
