package engagement

import (
	"testing"
	"time"
)

func TestEveryLevelWritesEveryKind(t *testing.T) {
	for _, level := range Levels() {
		settings, ok := SettingsFor(level)
		if !ok {
			t.Fatalf("%s has no settings", level)
		}
		for _, kind := range AllKinds() {
			if _, present := settings.Enabled[kind]; !present {
				t.Fatalf("%s does not decide %s", level, kind)
			}
		}
	}
	if _, ok := SettingsFor(LevelCustom); ok {
		t.Fatal("custom is not a level that can be applied")
	}
	if _, ok := ParseLevel("custom"); ok {
		t.Fatal("custom must not parse as a level the user picks")
	}
	if level, ok := ParseLevel("fewer"); !ok || level != LevelFewer {
		t.Fatalf("fewer parsed as %q %v", level, ok)
	}
}

func TestLevelsAreOrderedFromQuietToLoud(t *testing.T) {
	var lastOn, lastBudget = -1, -1
	for _, level := range Levels() {
		settings, _ := SettingsFor(level)
		on := 0
		for _, enabled := range settings.Enabled {
			if enabled {
				on++
			}
		}
		if on < lastOn || settings.DailyInterruptBudget < lastBudget {
			t.Fatalf("%s turns on %d kinds with budget %d, less than the quieter level (%d, %d)", level, on, settings.DailyInterruptBudget, lastOn, lastBudget)
		}
		lastOn, lastBudget = on, settings.DailyInterruptBudget
	}
	off, _ := SettingsFor(LevelOff)
	for kind, enabled := range off.Enabled {
		if enabled {
			t.Fatalf("off leaves %s on", kind)
		}
	}
	more, _ := SettingsFor(LevelMore)
	for kind, enabled := range more.Enabled {
		if !enabled {
			t.Fatalf("more leaves %s off", kind)
		}
	}
	prefs, _ := ApplyLevel(DefaultPrefs(), LevelMore)
	for _, kind := range AllKinds() {
		spec, _ := Spec(kind)
		if prefs.Weight(spec) != 1 {
			t.Fatalf("more must weight %s at 1.0, got %v", kind, prefs.Weight(spec))
		}
	}
}

func TestNormalIsTheShippedDefault(t *testing.T) {
	prefs, ok := ApplyLevel(DefaultPrefs(), LevelNormal)
	if !ok {
		t.Fatal("normal must apply")
	}
	defaults := DefaultPrefs()
	if prefs.DailyInterruptBudget != defaults.DailyInterruptBudget || prefs.Quiet != defaults.Quiet || len(prefs.DigestTimes) != len(defaults.DigestTimes) {
		t.Fatalf("normal differs from the defaults: %+v", prefs)
	}
	for _, kind := range AllKinds() {
		spec, _ := Spec(kind)
		if prefs.KindEnabled(spec) != spec.DefaultOn {
			t.Fatalf("normal changes %s from its default", kind)
		}
		if prefs.Weight(spec) != spec.Weight {
			t.Fatalf("normal changes the weight of %s", kind)
		}
	}
	if LevelOf(DefaultPrefs()) != LevelNormal {
		t.Fatalf("the defaults must read as normal, got %s", LevelOf(DefaultPrefs()))
	}
}

func TestFewerKeepsWhatNeedsTheUserAndTheBrief(t *testing.T) {
	prefs, _ := ApplyLevel(DefaultPrefs(), LevelFewer)
	on := map[Kind]bool{}
	for _, kind := range AllKinds() {
		spec, _ := Spec(kind)
		on[kind] = prefs.KindEnabled(spec)
	}
	for _, kind := range []Kind{KindWorkBlocked, KindCommitmentSlipping, KindCapturePaused, KindWorkFinished, KindBriefReady, KindBriefMissed, KindScheduleMissed} {
		if !on[kind] {
			t.Fatalf("fewer must keep %s on", kind)
		}
	}
	for _, kind := range []Kind{KindMeetingEnded, KindSuggestion, KindGoalCheckIn, KindPersonReappeared, KindLearnedToConfirm} {
		if on[kind] {
			t.Fatalf("fewer must turn %s off", kind)
		}
	}
	blocked, _ := Spec(KindWorkBlocked)
	if got := prefs.Weight(blocked); got >= blocked.Weight || got <= 0 {
		t.Fatalf("fewer must scale weights down, got %v for %v", got, blocked.Weight)
	}
	if prefs.DailyInterruptBudget != 3 || len(prefs.DigestTimes) != 1 {
		t.Fatalf("fewer budget and digests: %+v", prefs)
	}
	// Quiet hours cover the evening and the early morning.
	loc := time.UTC
	if !prefs.Quiet.Contains(time.Date(2026, 10, 14, 22, 0, 0, 0, loc)) || prefs.Quiet.Contains(time.Date(2026, 10, 14, 12, 0, 0, 0, loc)) {
		t.Fatalf("fewer quiet window is wrong: %+v", prefs.Quiet)
	}
}

func TestOffSilencesEverythingAllDay(t *testing.T) {
	prefs, _ := ApplyLevel(DefaultPrefs(), LevelOff)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, kind := range AllKinds() {
		spec, _ := Spec(kind)
		verdict := Pace(Moment{Kind: kind, DedupeKey: string(kind) + ":x", CreatedAt: now}, spec, prefs, NewPaceState(), now)
		if verdict.Mode != ModeSuppress {
			t.Fatalf("off must suppress %s, got %+v", kind, verdict)
		}
	}
	for _, at := range []time.Time{now, now.Add(11 * time.Hour), time.Date(2026, 10, 14, 23, 58, 59, 0, time.UTC), time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)} {
		if !prefs.Quiet.Contains(at) {
			t.Fatalf("off quiet hours must cover %s: %+v", at, prefs.Quiet)
		}
	}
	// Every level's times are whole minutes, so persisting them as minutes
	// and loading them back still reads as the same level.
	for _, level := range Levels() {
		settings, _ := SettingsFor(level)
		for _, offset := range append([]time.Duration{settings.Quiet.Start, settings.Quiet.End}, settings.DigestTimes...) {
			if offset%time.Minute != 0 {
				t.Fatalf("%s has a time that is not a whole minute: %s", level, offset)
			}
		}
	}
	if prefs.DailyInterruptBudget <= 0 {
		t.Fatal("off must keep a cap, because zero means no cap in the pacing rules")
	}
	if len(prefs.DigestTimes) != 0 {
		t.Fatal("off has no digests")
	}
}

func TestLevelOfReportsCustomAfterOneChange(t *testing.T) {
	prefs, _ := ApplyLevel(DefaultPrefs(), LevelMore)
	if LevelOf(prefs) != LevelMore {
		t.Fatalf("applied more reads as %s", LevelOf(prefs))
	}
	prefs.Enabled[KindGoalCheckIn] = false
	if LevelOf(prefs) != LevelCustom {
		t.Fatalf("one switch off must read as custom, got %s", LevelOf(prefs))
	}
	prefs, _ = ApplyLevel(prefs, LevelFewer)
	if LevelOf(prefs) != LevelFewer {
		t.Fatalf("re-applying a level must restore it, got %s", LevelOf(prefs))
	}
	prefs.DailyInterruptBudget = 4
	if LevelOf(prefs) != LevelCustom {
		t.Fatal("a budget change must read as custom")
	}
}

func TestApplyLevelKeepsMutes(t *testing.T) {
	prefs := DefaultPrefs()
	until := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	prefs.MutedUntil[KindPersonReappeared] = until
	prefs, _ = ApplyLevel(prefs, LevelMore)
	if prefs.MutedUntil[KindPersonReappeared] != until {
		t.Fatal("a level must not clear a mute")
	}
}

func TestFillMissingKindsFollowsTheSavedLevel(t *testing.T) {
	for _, level := range []Level{LevelOff, LevelFewer, LevelNormal} {
		saved, _ := ApplyLevel(DefaultPrefs(), level)
		stored := map[Kind]bool{}
		for _, kind := range AllKinds() {
			if kind != KindTopicFinds {
				stored[kind] = true
			}
		}
		// The kind arrived after the level was saved: no switch for it.
		delete(saved.Enabled, KindTopicFinds)
		filled := FillMissingKinds(saved, stored)
		spec, _ := Spec(KindTopicFinds)
		settings, _ := SettingsFor(level)
		if got := filled.KindEnabled(spec); got != settings.Enabled[KindTopicFinds] {
			t.Fatalf("%s: topic_finds enabled = %v, want %v", level, got, settings.Enabled[KindTopicFinds])
		}
		if LevelOf(filled) != level {
			t.Fatalf("%s: the level reads as %s after filling", level, LevelOf(filled))
		}
	}
	custom := DefaultPrefs()
	custom.Enabled = map[Kind]bool{KindWorkBlocked: true, KindSuggestion: false}
	filled := FillMissingKinds(custom, map[Kind]bool{KindWorkBlocked: true, KindSuggestion: true})
	spec, _ := Spec(KindTopicFinds)
	if !filled.KindEnabled(spec) {
		t.Fatal("a custom set changed a new kind's default")
	}
}
