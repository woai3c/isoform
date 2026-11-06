package engagement

import (
	"testing"
	"time"
)

// ST86: a scheduled brief pushes to the phone in the first week, one push per
// brief, still behind quiet hours, the switch and the cap. A brief the person
// asked for arrives without a buzz. A missed brief stays under the first-week rule.
func TestBriefPushFirstWeekRules(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	spec, ok := Spec(KindBriefReady)
	if !ok {
		t.Fatal("brief_ready has no spec")
	}
	prefs := DefaultPrefs()
	prefs.FirstWeek = true

	scheduled := Moment{Kind: KindBriefReady, SubjectID: "brief-1", DedupeKey: DedupeKey(KindBriefReady, "brief-1")}
	t.Run("scheduled brief buzzes on day two", func(t *testing.T) {
		got := Pace(scheduled, spec, prefs, NewPaceState(), now)
		if got.Mode != ModeInterrupt || got.Interruption != InterruptionActive {
			t.Fatalf("scheduled brief in first week = %+v", got)
		}
	})

	t.Run("brief made by hand arrives without a buzz", func(t *testing.T) {
		manual := scheduled
		manual.Params = map[string]string{BriefRequestParam: BriefRequestManual}
		got := Pace(manual, spec, prefs, NewPaceState(), now)
		if got.Mode != ModeInterrupt || got.Interruption != InterruptionPassive {
			t.Fatalf("manual brief in first week = %+v", got)
		}
	})

	t.Run("one push per brief", func(t *testing.T) {
		state := NewPaceState()
		first := Pace(scheduled, spec, prefs, state, now)
		state.Record(scheduled, first, now)
		retry := Pace(scheduled, spec, prefs, state, now.Add(time.Hour))
		if retry.Mode != ModeSuppress || retry.Reason != "cooldown" {
			t.Fatalf("a retry of the same brief pushed again: %+v", retry)
		}
	})

	t.Run("quiet hours hold the scheduled brief until they end", func(t *testing.T) {
		night := time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)
		got := Pace(scheduled, spec, prefs, NewPaceState(), night)
		if got.Mode != ModeHold || got.Reason != "quiet hours" || got.HoldUntil.Hour() != 8 {
			t.Fatalf("scheduled brief during quiet hours = %+v", got)
		}
	})

	t.Run("the brief switch still turns it off", func(t *testing.T) {
		off := DefaultPrefs()
		off.FirstWeek = true
		off.Enabled[KindBriefReady] = false
		if got := Pace(scheduled, spec, off, NewPaceState(), now); got.Reason != "kind off" {
			t.Fatalf("switch off = %+v", got)
		}
	})

	t.Run("a missed brief stays under the first-week rule", func(t *testing.T) {
		missed, _ := Spec(KindBriefMissed)
		got := Pace(Moment{Kind: KindBriefMissed, SubjectID: "brief-1", DedupeKey: DedupeKey(KindBriefMissed, "brief-1")}, missed, prefs, NewPaceState(), now)
		if got.Mode != ModeSuppress || got.Reason != "first week discovery" {
			t.Fatalf("missed brief in first week = %+v", got)
		}
	})
}
