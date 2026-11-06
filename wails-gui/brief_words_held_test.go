package main

import (
	"testing"
	"time"
)

// R40: while a store is still opening, the brief's words are held back and the
// by-id read says why, so the card waits instead of failing. Audio is held by
// the same read, so Hear it speaks nothing the read withheld.
func TestBriefWordsHeldWhileTheStoreOpens(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 10, 9, 9, 5, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.mobileBriefSvc = svc
	morning := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 9)
	if err := svc.store.save(morning, clock.Now()); err != nil {
		t.Fatal(err)
	}

	mobileBriefGateHold = func() bool { return true }
	t.Cleanup(func() { mobileBriefGateHold = nil })
	held, err := svc.app.GetTodayBriefByID(morning.BriefID)
	if err != nil {
		t.Fatal(err)
	}
	if held.WordsWithheld != mobileBriefWithheldNotReady {
		t.Fatalf("held read reason = %q, want %q", held.WordsWithheld, mobileBriefWithheldNotReady)
	}
	if held.Brief != nil && mobileBriefHasWrittenText(*held.Brief) {
		t.Fatal("a held read carried words")
	}
	if _, ok := svc.Audio(morning.BriefID); ok {
		t.Fatal("audio was served while the words are held")
	}

}
