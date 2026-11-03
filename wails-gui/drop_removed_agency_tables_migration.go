package main

import (
	"database/sql"
	"fmt"
)

// removedAgencyTables are the Agency and first-run tables that nothing has read
// or written since the Agency was removed on 2026-10-13. Every child comes
// before its parents: with foreign keys on, DROP TABLE first deletes all rows,
// and that delete fails while a child row still references them. Dropping a
// table also drops its indexes and immutability triggers, and the implicit
// delete does not fire those triggers.
//
// These stay because remaining code reads or writes them:
// agency_permission_decision_receipts_v1 (turning on a leftover Agency schedule
// proposal), agency_origination_permission_requests_v1 with its parents
// agency_origination_runs_v1 and agency_origination_candidates_v1 (the plan of
// leftover Agency one-time work), and first_run_exclusions_v1 with its parents
// first_run_journeys_v1 and first_outcome_runs_v1, plus
// agency_learning_corrections_v1 (learning exclusions and corrections).
var removedAgencyTables = []string{
	// Ledgers nothing references.
	"agency_chat_messages_v1",
	"agency_chat_runs_v1",
	"agency_handoff_evidence_invalidation_receipts_v1",
	"agency_handoff_source_end_receipts_v1",
	"agency_handoff_user_completion_receipts_v1",
	"agency_schedule_command_receipts_v1",
	"agency_schedule_evidence_invalidation_receipts_v1",
	"agency_permission_overlap_resolutions_v1",
	"agency_origination_gate_receipts_v1",
	"agency_origination_permission_events_v1",
	"agency_ambient_authority_events_v1",
	"agency_ambient_authority_grants_v1",

	// First-run history under the kept journeys and guided runs.
	"first_run_journey_events_v1",
	"first_run_receipts_v1",
	"first_outcome_run_events_v1",
	"first_outcome_artifacts_v1",
	"first_outcome_debug_replays_v1",

	// Re-admission and operating envelopes.
	"agency_initiative_re_admission_events_v1",
	"agency_initiative_re_admissions_v1",
	"agency_operating_envelope_events_v1",
	"agency_operating_envelopes_v1",

	// Planner canary, release and rollout.
	"agency_initiative_planner_canary_comparisons_v1",
	"agency_initiative_planner_canary_attempts_v1",
	"agency_initiative_planner_canary_cases_v1",
	"agency_initiative_planner_release_decisions_v1",
	"agency_initiative_planner_canary_experiments_v1",
	"agency_initiative_planner_monitoring_reviews_v1",
	"agency_initiative_planner_rollback_review_provenance_v1",
	"agency_initiative_planner_rollout_events_v1",
	"agency_initiative_planner_rollout_observations_v1",

	// Outcomes, feedback and continuations.
	"purpose_agency_outcome_receipts_v1",
	"agency_initiative_continuation_events_v1",
	"agency_initiative_continuations_v1",
	"agency_initiative_feedback_calibration_samples_v1",
	"agency_initiative_feedback_adherence_evaluations_v1",
	"agency_initiative_feedback_adherence_attempts_v1",
	"agency_initiative_feedback_applications_v1",
	"agency_initiative_outcome_evaluations_v1",
	"agency_initiative_outcomes_v2",

	// Initiatives, their runs and planning runs.
	"agency_initiative_actions_v2",
	"agency_initiative_artifacts_v2",
	"agency_initiative_corrections_v2",
	"agency_initiative_state_events_v2",
	"agency_initiative_lease_events_v2",
	"agency_initiative_run_events_v2",
	"agency_initiative_run_leases_v2",
	"agency_initiative_runs_v2",
	"agency_planning_runs_v2",
	"agency_initiatives_v2",
}

// migrateDropRemovedAgencyTables drops the tables of the removed Agency. The
// migrations that created them stay registered so the history still replays:
// a new database creates these tables and then drops them here.
func migrateDropRemovedAgencyTables(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate drop removed agency tables: transaction is nil")
	}
	for _, table := range removedAgencyTables {
		if _, err := tx.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			return fmt.Errorf("drop removed agency table %s: %w", table, err)
		}
	}
	return nil
}
