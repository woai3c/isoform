package main

import (
	"testing"
	"time"
)

func TestTodayObservationPauseSinceUsesTheActivePause(t *testing.T) {
	rig := newHandoffTestRig(t)
	started := time.Now().Add(-time.Hour).UnixMilli()
	rig.app.saveObservationPauses([]observationPause{{Start: started - 1000, End: started - 500}, {Start: started}})
	rig.app.recordTodayObservationPauseStart(true, time.UnixMilli(started))
	rig.app.closedEyes.enabled.Store(true)
	got, err := rig.app.GetTodayObservationPauseSince()
	if err != nil || got != started {
		t.Fatalf("pause start = %d, %v", got, err)
	}
	rig.app.closedEyes.policyPending.Store(true)
	got, err = rig.app.GetTodayObservationPauseSince()
	if err != nil || got != 0 {
		t.Fatalf("unknown policy guessed a pause start = %d, %v", got, err)
	}
	rig.app.closedEyes.policyPending.Store(false)
	rig.app.closedEyes.enabled.Store(false)
	got, err = rig.app.GetTodayObservationPauseSince()
	if err != nil || got != 0 {
		t.Fatalf("resumed pause start = %d, %v", got, err)
	}
	rig.app.closedEyes.enabled.Store(true)
	rig.app.recordTodayObservationPauseStart(false, time.Now())
	got, err = rig.app.GetTodayObservationPauseSince()
	if err != nil || got != 0 {
		t.Fatalf("unknown pause start = %d, %v", got, err)
	}
}

func TestTodayObservationPauseSinceRecordsNewTimedPause(t *testing.T) {
	app := newTimedPauseFixture(t)
	before := time.Now().UnixMilli()
	if err := app.PauseObservationFor(60); err != nil {
		t.Fatal(err)
	}
	start, err := app.GetTodayObservationPauseSince()
	if err != nil || start < before || start > time.Now().UnixMilli() {
		t.Fatalf("pause start = %d, %v", start, err)
	}
	if err := app.ResumeObservation(); err != nil {
		t.Fatal(err)
	}
	if start, err = app.GetTodayObservationPauseSince(); err != nil || start != 0 {
		t.Fatalf("resumed start = %d, %v", start, err)
	}
}

func TestRestoringLegacyPauseDoesNotInventAStart(t *testing.T) {
	app := newTimedPauseFixture(t)
	if err := app.PauseObservationFor(60); err != nil {
		t.Fatal(err)
	}
	app.recordTodayObservationPauseStart(false, time.Now()) // pre-build pause has no recorded start
	app.stopObservationResumeTimer()
	app.closedEyes.enabled.Store(false)
	app.restoreObservationPolicy()
	if got, err := app.GetTodayObservationPauseSince(); err != nil || got != 0 {
		t.Fatalf("legacy pause start = %d, %v", got, err)
	}
}

func TestMenuObservationRecordsAndClearsPauseStart(t *testing.T) {
	app := newTimedPauseFixture(t)
	before := time.Now().UnixMilli()
	app.toggleMenuBarObservation()
	start, err := app.GetTodayObservationPauseSince()
	if err != nil || start < before || start > time.Now().UnixMilli() {
		t.Fatalf("menu pause start = %d, %v", start, err)
	}
	app.toggleMenuBarObservation()
	if got, err := app.GetTodayObservationPauseSince(); err != nil || got != 0 {
		t.Fatalf("menu resume start = %d, %v", got, err)
	}
}
