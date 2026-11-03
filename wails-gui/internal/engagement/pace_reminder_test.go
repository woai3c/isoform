package engagement

import (
	"testing"
	"time"
)

func TestReminderIgnoresKindMutesButOtherKindsDoNot(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	prefs := Prefs{Enabled: map[Kind]bool{}, MutedUntil: map[Kind]time.Time{
		KindReminder:       now.Add(24 * time.Hour),
		KindPlaybookResult: now.Add(24 * time.Hour),
	}}
	reminder, _ := Spec(KindReminder)
	if v := Pace(Moment{Kind: KindReminder, DedupeKey: "reminder:a:1"}, reminder, prefs, NewPaceState(), now); v.Mode != ModeInterrupt {
		t.Fatalf("a muted reminder kind still delivers the user's reminder: %+v", v)
	}
	playbook, _ := Spec(KindPlaybookResult)
	if v := Pace(Moment{Kind: KindPlaybookResult, DedupeKey: "playbook_result:r"}, playbook, prefs, NewPaceState(), now); v.Mode != ModeSuppress || v.Reason != "muted" {
		t.Fatalf("other kinds keep their mute: %+v", v)
	}
}
