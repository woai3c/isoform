package main

import "testing"

// The Today warm-up calls Schedule on whatever brief service the bridge has;
// one built with a store but no config store (as some tests build it) must answer with the defaults, not panic.
func TestMobileBriefScheduleWithoutConfigStore(t *testing.T) {
	svc := &mobileBriefService{store: newMobileBriefStore(t.TempDir())}
	if _, err := svc.Schedule(); err != nil {
		t.Fatalf("Schedule without a config store: %v", err)
	}
}
