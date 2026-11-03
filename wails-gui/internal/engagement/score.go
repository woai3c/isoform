package engagement

import "time"

// Urgency grows as DueAt approaches and saturates once it has passed. A
// moment with no DueAt is as urgent as one due within a day.
func Urgency(m Moment, now time.Time) float64 {
	if m.DueAt.IsZero() {
		return 0.6
	}
	remaining := m.DueAt.Sub(now)
	switch {
	case remaining <= 0:
		return 1.0
	case remaining <= time.Hour:
		return 1.0
	case remaining <= 4*time.Hour:
		return 0.85
	case remaining <= 24*time.Hour:
		return 0.6
	default:
		return 0.4
	}
}

// Score ranks a moment: kind weight times urgency times stakes, less the
// fatigue the user has shown lately. Fatigue is in [0, 1] and comes from
// recent dismiss and mute receipts. The result is in [0, 1].
func Score(m Moment, spec KindSpec, prefs Prefs, fatigue float64, now time.Time) float64 {
	stakes := m.Stakes
	if stakes <= 0 {
		stakes = 0.5
	}
	stakes = clamp01(stakes)
	raw := prefs.Weight(spec) * Urgency(m, now) * stakes
	return clamp01(raw - clamp01(fatigue)*0.5)
}
