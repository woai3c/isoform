package main

import "testing"

// A service built without its run store (tests, or the Today warm-up racing
// start) reads no runs instead of panicking, as the schedule config already
// does. The finished-brief read reaches upcomingBrief, so it must not crash.
func TestMobileBriefRunStoreNilListIsEmpty(t *testing.T) {
	var runs *mobileBriefRunStore
	if got := runs.list(); len(got) != 0 {
		t.Fatalf("nil run store listed %d runs", len(got))
	}
}

func TestUpcomingBriefToleratesPartialService(t *testing.T) {
	svc := &mobileBriefService{store: newMobileBriefStore(t.TempDir())}
	state := svc.upcomingBrief()
	if len(state.Runs) != 0 {
		t.Fatalf("partial service read %d runs", len(state.Runs))
	}
	if state.Settings != defaultMobileBriefSettings() {
		t.Fatalf("partial service settings = %+v, want defaults", state.Settings)
	}
}
