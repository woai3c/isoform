package engagement

import (
	"testing"
	"time"
)

func TestQuietOnTodayAllowed(t *testing.T) {
	for _, kind := range AllKinds() {
		allowed := QuietOnTodayAllowed(kind)
		switch kind {
		case KindDecisionDue:
			if allowed {
				t.Fatalf("%s waits on the user's answer and must interrupt", kind)
			}
		default:
			if !allowed {
				t.Fatalf("%s may show as a quiet line on Today", kind)
			}
		}
	}
}

func TestPrefsKindQuietOnToday(t *testing.T) {
	prefs := DefaultPrefs()
	for _, kind := range AllKinds() {
		if prefs.KindQuietOnToday(kind) {
			t.Fatalf("%s is loud by default", kind)
		}
	}
	prefs.QuietOnToday[KindCapturePaused] = true
	if !prefs.KindQuietOnToday(KindCapturePaused) {
		t.Fatal("a quiet kind reports its mode")
	}
}

func TestQuietPaceIgnoresAndDoesNotRecordNotificationLimits(t *testing.T) {
	now := time.Now()
	kind := KindTopicFinds
	prefs := DefaultPrefs()
	prefs.QuietOnToday[kind] = true
	spec, _ := Spec(kind)
	state := NewPaceState()
	state.Day = now.Format("2006-01-02")
	state.InterruptsToday = 6
	state.SentByKindToday[kind] = 99
	state.LastSentByKey["same-key"] = now
	moment := Moment{Kind: kind, DedupeKey: "same-key"}
	verdict := Pace(moment, spec, prefs, state, now)
	if verdict.Mode != ModeQuiet {
		t.Fatalf("quiet pacing: %+v", verdict)
	}
	state.Record(moment, verdict, now)
	if state.InterruptsToday != 6 || state.SentByKindToday[kind] != 99 || !state.LastSentByKey["same-key"].Equal(now) {
		t.Fatalf("quiet changed pacing: %+v", state)
	}
	if QuietSuggestionAllowed(KindReminder) || !QuietOnTodayAllowed(KindReminder) {
		t.Fatal("a reminder may only be quieted explicitly")
	}
}
