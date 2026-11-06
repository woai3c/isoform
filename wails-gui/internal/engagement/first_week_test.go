package engagement

import (
	"strings"
	"testing"
	"time"
)

func TestFirstWeekFixtureInstallSchedule(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	// This install spans the November clock change: days are calendar days.
	installed := time.Date(2026, 10, 31, 23, 45, 0, 0, loc)
	for day := 0; day <= 8; day++ {
		now := installed.AddDate(0, 0, day)
		if got := FirstWeekDay(installed, now); got != day {
			t.Fatalf("day %d = %d", day, got)
		}
	}
	if got := FirstWeekDay(installed, time.Date(2026, 11, 1, 0, 1, 0, 0, loc)); got != 1 {
		t.Fatalf("next local day = %d", got)
	}
	if FirstWeekDay(time.Time{}, installed) != -1 || FirstWeekDay(installed, installed.Add(-time.Minute)) != -1 {
		t.Fatal("missing/future install enrolled")
	}
}

func TestFirstWeekCapQuietHoursBudgetAndSwitches(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	m := Moment{Kind: KindFirstWeek, DedupeKey: "day2", CreatedAt: now, Params: map[string]string{"stage": "commitment"}}
	spec, _ := Spec(m.Kind)
	prefs := DefaultPrefs()
	prefs.FirstWeek = true
	state := NewPaceState()
	first := Pace(m, spec, prefs, state, now)
	if first.Mode != ModeInterrupt {
		t.Fatal(first)
	}
	state.Record(m, first, now)
	m.DedupeKey = "another-source"
	if got := Pace(m, spec, prefs, state, now); got.Reason != "first week daily cap" {
		t.Fatal(got)
	}
	recapSpec, _ := Spec(KindWeeklyRecap)
	if got := Pace(Moment{Kind: KindWeeklyRecap}, recapSpec, prefs, state, now); got.Reason != "first week daily cap" {
		t.Fatal(got)
	}
	// Ordinary discovery cannot race the chosen first-week source.
	ordinary, _ := Spec(KindCommitmentSlipping)
	if got := Pace(Moment{Kind: ordinary.Kind}, ordinary, prefs, NewPaceState(), now); got.Reason != "first week discovery" {
		t.Fatal(got)
	}
	brief, _ := Spec(KindBriefReady)
	if got := Pace(Moment{Kind: brief.Kind}, brief, prefs, NewPaceState(), now); got.Reason != "first week discovery" {
		t.Fatal("automatic brief added a second chat message", got)
	}
	state = NewPaceState()
	quiet := now.Add(13 * time.Hour)
	if got := Pace(m, spec, prefs, state, quiet); got.Reason != "quiet hours" || got.HoldUntil.Hour() != 8 {
		t.Fatal(got)
	}
	state.Day, state.InterruptsToday = now.Format("2006-01-02"), prefs.DailyInterruptBudget
	if got := Pace(m, spec, prefs, state, now); got.Mode != ModeInterrupt {
		t.Fatal(got)
	}
	prefs.Enabled[KindFirstWeek] = false
	if got := Pace(m, spec, prefs, state, now); got.Reason != "kind off" {
		t.Fatal(got)
	}
	prefs.Enabled[KindFirstWeek] = true
	prefs.MutedUntil[KindFirstWeek] = now.Add(time.Hour)
	if got := Pace(m, spec, prefs, state, now); got.Reason != "muted" {
		t.Fatal(got)
	}
}

func TestFirstWeekCopyIsCompleteAndCalm(t *testing.T) {
	for _, lang := range Languages {
		for _, stage := range []string{"welcome", "brief", "commitment", "person", "meeting"} {
			for _, timing := range []string{"2026-10-19 08:00", "off", ""} {
				copy := FirstWeekCopy(stage, timing, lang)
				if copy == "" || !ValidBody(copy) {
					t.Fatalf("%s %s: %s", lang, stage, copy)
				}
				if strings.Contains(copy, "Mac") || strings.Contains(copy, "iPhone") {
					t.Fatal(copy)
				}
			}
		}
	}
}
