package main

import "context"

type todayTurnSourceKey struct{}
type todayTurnSource struct{ action string }

// A per-turn cell travels only through admission; it is never process-global.
func setTodayTurnSource(ctx context.Context, action string) {
	switch action {
	case "T10", "T12", "T15", "T68", "T81", "T84":
		if source, ok := ctx.Value(todayTurnSourceKey{}).(*todayTurnSource); ok {
			source.action = action
		}
	}
}

func todayTurnProperties(source *todayTurnSource) map[string]any {
	if source == nil || source.action == "" {
		return nil
	}
	return map[string]any{"source": map[string]any{"surface": "today", "action": source.action}}
}

// RecordTodayAction accepts only the fixed, content-free product-map fields.
// Product telemetry enforces the schema and consent again at enqueue time.
func (a *App) RecordTodayAction(surface, action, kind, outcome string, msToResult int) {
	var event string
	switch surface {
	case "today":
		event = "today.action"
	case "phone_today":
		event = "phone_today.action"
	default:
		return
	}
	if a.telemetry != nil {
		a.telemetry.Capture(event, map[string]any{
			"action": action, "kind": kind, "outcome": outcome, "ms_to_result": msToResult,
		})
	}
}

// RecordTodaySectionSeen records a visible section without item data.
func (a *App) RecordTodaySectionSeen(section string, rows int) {
	if a.telemetry != nil {
		a.telemetry.Capture("today.section_seen", map[string]any{"section": section, "rows": rows})
	}
}
