package main

import "testing"

// R64: Today's launch read and Phone (Health) name the next brief the same way,
// and a switched-off schedule names none.
func TestNextBriefTimeIsTheSameOnEveryReader(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(10, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	settings := svc.config.load()
	now := svc.now()
	latest, ok := svc.store.latest()
	launch := svc.withNextRun(svc.withPending(mobileBriefHealthView(now, svc.location, settings, latest, ok, svc.runs.list()), now, settings, latest, ok), now, settings)
	phone := svc.Health()
	if phone.NextRunAt != launch.NextRunAt || phone.NextSlot != launch.NextSlot {
		t.Fatalf("Phone names %q (%s), launch read names %q (%s)", phone.NextRunAt, phone.NextSlot, launch.NextRunAt, launch.NextSlot)
	}
	if phone.NextRunAt == "" {
		t.Fatal("an enabled schedule names no next brief")
	}
	off := settings
	off.Enabled = false
	if got := svc.withNextRun(MobileBriefHealth{NextRunAt: "stale", NextSlot: "morning"}, now, off); got.NextRunAt != "" || got.NextSlot != "" {
		t.Fatalf("a switched-off schedule names %q (%s)", got.NextRunAt, got.NextSlot)
	}
}
