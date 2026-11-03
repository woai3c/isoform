package main

import (
	"database/sql"
	"fmt"
	"strings"
)

func addTaskShadowObserverCoverageColumn(
	tx *sql.Tx,
	table, name, definition string,
) error {
	if _, err := tx.Exec(
		"ALTER TABLE " + table + " ADD COLUMN " + name + " " + definition,
	); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("add %s.%s: %w", table, name, err)
	}
	return nil
}

func migrateTaskShadowObserverCoverage(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate task shadow observer coverage: transaction is nil")
	}
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{
			table:      "activity_pipeline_runs",
			name:       "shadow_observer_expected",
			definition: "INTEGER NOT NULL DEFAULT 0 CHECK (shadow_observer_expected IN (0, 1))",
		},
		{
			table:      "activity_pipeline_runs",
			name:       "shadow_observer_admission",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "activity_pipeline_runs",
			name:       "shadow_observer_dispatch_status",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "activity_pipeline_runs",
			name:       "shadow_observer_dispatch_reason",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "activity_pipeline_runs",
			name:       "shadow_observer_trace_id",
			definition: "TEXT NOT NULL DEFAULT ''",
		},
		{
			table:      "task_shadow_observer_traces_v1",
			name:       "source_pipeline_run_id",
			definition: "INTEGER NOT NULL DEFAULT 0 CHECK (source_pipeline_run_id >= 0)",
		},
	} {
		if err := addTaskShadowObserverCoverageColumn(
			tx,
			column.table,
			column.name,
			column.definition,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_activity_pipeline_runs_shadow_observer_trace
			ON activity_pipeline_runs(shadow_observer_trace_id)
			WHERE shadow_observer_trace_id <> '';
		CREATE INDEX IF NOT EXISTS idx_activity_pipeline_runs_shadow_observer_expected
			ON activity_pipeline_runs(shadow_observer_expected, started_at DESC, id DESC);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_task_shadow_observer_traces_v1_source_pipeline
			ON task_shadow_observer_traces_v1(source_pipeline_run_id)
			WHERE source_pipeline_run_id > 0;
	`); err != nil {
		return fmt.Errorf("create task shadow observer coverage indexes: %w", err)
	}
	return nil
}
