package engagement

import "time"

// Level is the one dial the user turns: how much Isoform speaks up on its
// own. A level writes every kind switch, the kind weights, the daily
// interruption budget, the digest times and the quiet hours in one step
// (docs/notifications-design.md section 3). Changing one of those afterwards
// makes the effective level LevelCustom until the user picks a level again.
type Level string

const (
	LevelOff    Level = "off"
	LevelFewer  Level = "fewer"
	LevelNormal Level = "normal"
	LevelMore   Level = "more"
	// LevelCustom is never applied; it reports that the switches or rules no
	// longer match a level.
	LevelCustom Level = "custom"
)

// Levels lists the levels the dial offers, quietest first.
func Levels() []Level {
	return []Level{LevelOff, LevelFewer, LevelNormal, LevelMore}
}

// ParseLevel accepts a level the UI or the phone sent. LevelCustom is not a
// level the user can pick, so it is rejected here.
func ParseLevel(raw string) (Level, bool) {
	switch Level(raw) {
	case LevelOff, LevelFewer, LevelNormal, LevelMore:
		return Level(raw), true
	default:
		return "", false
	}
}

// LevelSettings is what one level writes.
type LevelSettings struct {
	// Enabled is the switch for every kind. Every kind in AllKinds has an entry.
	Enabled map[Kind]bool
	// WeightScale multiplies each kind's default weight. Zero means the
	// defaults are kept.
	WeightScale float64
	// WeightAll, when set, gives every kind this weight instead of a scaled
	// default; More uses it so every kind ranks equally.
	WeightAll float64
	// DailyInterruptBudget is the active deliveries allowed per local day.
	DailyInterruptBudget int
	DigestTimes          []time.Duration
	Quiet                QuietWindow
}

// fewerKinds are the kinds that stay on at LevelFewer: things that need the
// user, finished work, the brief itself, and scheduled work that silently
// stopped running.
var fewerKinds = map[Kind]bool{
	KindWorkBlocked:        true,
	KindCommitmentSlipping: true,
	KindCapturePaused:      true,
	KindWorkFinished:       true,
	KindBriefReady:         true,
	KindBriefMissed:        true,
	KindScheduleMissed:     true,
	KindPlaybookResult:     true,
	KindReminder:           true,
	KindAnswerReady:        true,
}

// SettingsFor returns what a level writes. The second result is false for
// LevelCustom and unknown levels.
func SettingsFor(level Level) (LevelSettings, bool) {
	enabled := make(map[Kind]bool, len(AllKinds()))
	switch level {
	case LevelOff:
		for _, kind := range AllKinds() {
			enabled[kind] = false
		}
		return LessIsOff(enabled), true
	case LevelFewer:
		for _, kind := range AllKinds() {
			enabled[kind] = fewerKinds[kind]
		}
		return LevelSettings{
			Enabled:              enabled,
			WeightScale:          0.75,
			DailyInterruptBudget: 3,
			DigestTimes:          []time.Duration{18 * time.Hour},
			Quiet:                QuietWindow{Start: 21 * time.Hour, End: 9 * time.Hour},
		}, true
	case LevelNormal:
		defaults := DefaultPrefs()
		for _, kind := range AllKinds() {
			spec, _ := Spec(kind)
			enabled[kind] = spec.DefaultOn
		}
		return LevelSettings{
			Enabled:              enabled,
			WeightScale:          1,
			DailyInterruptBudget: defaults.DailyInterruptBudget,
			DigestTimes:          append([]time.Duration(nil), defaults.DigestTimes...),
			Quiet:                defaults.Quiet,
		}, true
	case LevelMore:
		for _, kind := range AllKinds() {
			enabled[kind] = true
		}
		return LevelSettings{
			Enabled:              enabled,
			WeightAll:            1,
			DailyInterruptBudget: 10,
			DigestTimes:          []time.Duration{8*time.Hour + 30*time.Minute, 12 * time.Hour, 15 * time.Hour, 18 * time.Hour},
			Quiet:                QuietWindow{Start: 23 * time.Hour, End: 7 * time.Hour},
		}, true
	default:
		return LevelSettings{}, false
	}
}

// LessIsOff is the Off level: every kind off, the smallest budget the pacing
// rules treat as a cap (zero means no cap in Prefs), no digests, and quiet
// hours that cover the whole day. Every kind being off is what silences
// Isoform; the budget and the quiet window only keep a kind the user turns
// back on by hand from running unpaced.
func LessIsOff(enabled map[Kind]bool) LevelSettings {
	return LevelSettings{
		Enabled:              enabled,
		DailyInterruptBudget: 1,
		DigestTimes:          []time.Duration{},
		// Start equals end means no quiet hours, so the day ends at 23:59.
		// Times are whole minutes because the rules are persisted as minutes;
		// a finer value would load back changed and read as custom.
		Quiet: QuietWindow{Start: 0, End: 24*time.Hour - time.Minute},
	}
}

// ApplyLevel returns prefs rewritten by the level: every kind switch, every
// kind weight, the budget, the digest times and the quiet window. Mutes are
// kept, because they are the user's explicit answer to one kind. The second
// result is false when the level cannot be applied, and prefs is returned as
// it was.
func ApplyLevel(prefs Prefs, level Level) (Prefs, bool) {
	settings, ok := SettingsFor(level)
	if !ok {
		return prefs, false
	}
	out := prefs
	out.Enabled = make(map[Kind]bool, len(settings.Enabled))
	out.Weights = make(map[Kind]float64, len(settings.Enabled))
	for _, kind := range AllKinds() {
		out.Enabled[kind] = settings.Enabled[kind]
		spec, _ := Spec(kind)
		switch {
		case settings.WeightAll > 0:
			out.Weights[kind] = clamp01(settings.WeightAll)
		case settings.WeightScale > 0:
			out.Weights[kind] = clamp01(spec.Weight * settings.WeightScale)
		}
	}
	out.DailyInterruptBudget = settings.DailyInterruptBudget
	out.DigestTimes = append([]time.Duration(nil), settings.DigestTimes...)
	out.Quiet = settings.Quiet
	return out, true
}

// LevelOf reports which level the prefs match exactly on every switch, the
// budget, the digest times and the quiet window, or LevelCustom. Weights are
// not compared, because a weight the user never sees cannot be their custom
// choice.
func LevelOf(prefs Prefs) Level {
	for _, level := range Levels() {
		settings, _ := SettingsFor(level)
		if prefsMatchLevel(prefs, settings) {
			return level
		}
	}
	return LevelCustom
}

func prefsMatchLevel(prefs Prefs, settings LevelSettings) bool {
	if prefs.DailyInterruptBudget != settings.DailyInterruptBudget || prefs.Quiet != settings.Quiet {
		return false
	}
	if len(prefs.DigestTimes) != len(settings.DigestTimes) {
		return false
	}
	for i, offset := range settings.DigestTimes {
		if prefs.DigestTimes[i] != offset {
			return false
		}
	}
	for _, kind := range AllKinds() {
		spec, _ := Spec(kind)
		if prefs.KindEnabled(spec) != settings.Enabled[kind] {
			return false
		}
	}
	return true
}

// FillMissingKinds gives kinds the user's saved settings do not mention
// yet (kinds added after the user picked a level) the switch of the level
// the saved kinds match, so someone who chose Off or Fewer does not start
// hearing about a new kind that is on by default. stored names the kinds
// that have a saved switch. When the saved kinds match no level (Custom),
// a new kind keeps its default.
func FillMissingKinds(prefs Prefs, stored map[Kind]bool) Prefs {
	var missing []Kind
	for _, kind := range AllKinds() {
		if !stored[kind] {
			missing = append(missing, kind)
		}
	}
	if len(missing) == 0 || len(stored) == 0 {
		return prefs
	}
	for _, level := range Levels() {
		settings, _ := SettingsFor(level)
		if !storedMatchLevel(prefs, stored, settings) {
			continue
		}
		if prefs.Enabled == nil {
			prefs.Enabled = map[Kind]bool{}
		}
		for _, kind := range missing {
			prefs.Enabled[kind] = settings.Enabled[kind]
		}
		return prefs
	}
	return prefs
}

// storedMatchLevel is prefsMatchLevel over the saved kinds only.
func storedMatchLevel(prefs Prefs, stored map[Kind]bool, settings LevelSettings) bool {
	if prefs.DailyInterruptBudget != settings.DailyInterruptBudget || prefs.Quiet != settings.Quiet {
		return false
	}
	if len(prefs.DigestTimes) != len(settings.DigestTimes) {
		return false
	}
	for i, offset := range settings.DigestTimes {
		if prefs.DigestTimes[i] != offset {
			return false
		}
	}
	for kind := range stored {
		spec, ok := Spec(kind)
		if !ok {
			continue
		}
		if prefs.KindEnabled(spec) != settings.Enabled[kind] {
			return false
		}
	}
	return true
}
