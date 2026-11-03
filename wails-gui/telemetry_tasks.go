package main

import "strconv"

// The Tasks product map's row ids (docs/product-map/desktop-tasks-activity.md):
// Tasks, Handoffs, Deliverables (All done), a schedule's detail and entries.
var tasksTelemetryRows = map[byte]int{'T': 31, 'H': 6, 'D': 3, 'C': 16, 'E': 3}

func tasksTelemetryActionID(action string) bool {
	if len(action) != 3 {
		return false
	}
	max, ok := tasksTelemetryRows[action[0]]
	if !ok || action[1] < '0' || action[1] > '9' || action[2] < '0' || action[2] > '9' {
		return false
	}
	n, err := strconv.Atoi(action[1:])
	return err == nil && n > 0 && n <= max
}

func tasksTelemetrySection(section string) bool {
	switch section {
	case "needs_you", "up_next", "running", "scheduled", "done_this_week", "all_done":
		return true
	default:
		return false
	}
}

// RecordTasksAction accepts only a Tasks map id and the closed enums; product
// telemetry enforces the schema and consent again at enqueue time.
func (a *App) RecordTasksAction(action, kind, outcome string, msToResult int) {
	if a.telemetry != nil {
		a.telemetry.Capture("tasks.action", map[string]any{
			"action": action, "kind": kind, "outcome": outcome, "ms_to_result": msToResult,
		})
	}
}

// RecordTasksSectionSeen records a visible Tasks section without item data.
func (a *App) RecordTasksSectionSeen(section string, rows int) {
	if a.telemetry != nil {
		a.telemetry.Capture("tasks.section_seen", map[string]any{"section": section, "rows": rows})
	}
}
