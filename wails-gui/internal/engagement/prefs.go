package engagement

import "time"

// QuietWindow is a daily window, as offsets from local midnight, during which
// only time-sensitive moments may interrupt. Start after End means the window
// wraps midnight (22:00 to 08:00).
type QuietWindow struct {
	Start time.Duration
	End   time.Duration
}

// Contains reports whether t falls inside the window, in t's location.
func (q QuietWindow) Contains(t time.Time) bool {
	if q.Start == q.End {
		return false
	}
	offset := sinceMidnight(t)
	if q.Start < q.End {
		return offset >= q.Start && offset < q.End
	}
	return offset >= q.Start || offset < q.End
}

// EndAfter returns the next moment at or after t when the window ends. When t
// is outside the window it returns t.
func (q QuietWindow) EndAfter(t time.Time) time.Time {
	if !q.Contains(t) {
		return t
	}
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	end := midnight.Add(q.End)
	if !end.After(t) {
		end = midnight.AddDate(0, 0, 1).Add(q.End)
	}
	return end
}

func sinceMidnight(t time.Time) time.Duration {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return t.Sub(midnight)
}

// Prefs are the user's settings layered over the kind specs.
type Prefs struct {
	// FirstWeek is a runtime gate for days 2 to 7. Requested work retains
	// its lifecycle; automatic messages use one chosen check-in.
	FirstWeek bool
	// Weights override a kind's default weight; absent means the spec weight.
	Weights map[Kind]float64
	// Enabled overrides DefaultOn; absent means the spec default.
	Enabled map[Kind]bool
	// MutedUntil silences a kind until the time passes.
	MutedUntil map[Kind]time.Time
	// QuietOnToday marks kinds delivered as one quiet line on Today instead of
	// interrupting; absent means the kind delivers normally.
	QuietOnToday map[Kind]bool
	Quiet        QuietWindow
	// DailyInterruptBudget caps active deliveries per local day. Time-sensitive
	// deliveries are not counted against it, so a real deadline still arrives.
	DailyInterruptBudget int
	// DigestTimes are offsets from local midnight when held passive moments
	// become eligible for a quiet digest. The service bundles up to five.
	DigestTimes []time.Duration
	// QuietBreakSpacing is the minimum gap between two interruptions inside
	// quiet hours.
	QuietBreakSpacing time.Duration
}

// DefaultPrefs returns the shipped defaults: quiet from 22:00 to 08:00, six
// interruptions a day, digests at 08:30, 13:00 and 18:00, one quiet-hours
// break every two hours at most.
func DefaultPrefs() Prefs {
	return Prefs{
		Weights:              map[Kind]float64{},
		Enabled:              map[Kind]bool{},
		MutedUntil:           map[Kind]time.Time{},
		QuietOnToday:         map[Kind]bool{},
		Quiet:                QuietWindow{Start: 22 * time.Hour, End: 8 * time.Hour},
		DailyInterruptBudget: 6,
		DigestTimes:          []time.Duration{8*time.Hour + 30*time.Minute, 13 * time.Hour, 18 * time.Hour},
		QuietBreakSpacing:    2 * time.Hour,
	}
}

// Weight returns the effective weight of a kind, clamped to [0, 1].
func (p Prefs) Weight(spec KindSpec) float64 {
	w := spec.Weight
	if override, ok := p.Weights[spec.Kind]; ok {
		w = override
	}
	return clamp01(w)
}

// Enabled reports whether the user hears about a kind at all.
func (p Prefs) KindEnabled(spec KindSpec) bool {
	if override, ok := p.Enabled[spec.Kind]; ok {
		return override
	}
	return spec.DefaultOn
}

// KindDeliveryMode is the per-kind destination policy, separate from pacing.
type KindDeliveryMode string

const (
	KindDeliveryNormal       KindDeliveryMode = "normal"
	KindDeliveryQuietOnToday KindDeliveryMode = "quiet_on_today"
)

// KindDeliveryMode returns the effective mode; old preferences deliver normally.
func (p Prefs) KindDeliveryMode(kind Kind) KindDeliveryMode {
	if p.KindQuietOnToday(kind) {
		return KindDeliveryQuietOnToday
	}
	return KindDeliveryNormal
}

// KindQuietOnToday reports whether a kind shows as a quiet line on Today
// instead of interrupting.
func (p Prefs) KindQuietOnToday(kind Kind) bool {
	return QuietOnTodayAllowed(kind) && p.QuietOnToday[kind]
}

// NextDigest returns the first digest time after t, in t's location.
func (p Prefs) NextDigest(t time.Time) time.Time {
	if len(p.DigestTimes) == 0 {
		return t
	}
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	var best time.Time
	for day := 0; day < 2; day++ {
		for _, offset := range p.DigestTimes {
			candidate := midnight.AddDate(0, 0, day).Add(offset)
			if candidate.After(t) && (best.IsZero() || candidate.Before(best)) {
				best = candidate
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return t
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
