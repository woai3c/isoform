package main

import (
	"database/sql"
	"fmt"
)

func migrateTaskShadowObserverExactInput(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate task shadow observer exact input: transaction is nil")
	}
	if err := addTaskShadowObserverCoverageColumn(
		tx,
		"task_shadow_observer_traces_v1",
		"input_identity_hash",
		"TEXT NOT NULL DEFAULT '' CHECK (input_identity_hash = '' OR length(input_identity_hash) = 64)",
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_task_shadow_observer_traces_v1_exact_input
			ON task_shadow_observer_traces_v1(
				input_identity_hash, created_at DESC, id DESC
			)
			WHERE input_identity_hash <> '';
	`); err != nil {
		return fmt.Errorf("create task shadow observer exact-input index: %w", err)
	}
	return nil
}
