package main

import (
	"reflect"
	"testing"
	"time"
)

func TestPeoplePurposeFastReadPreservesClosureWithoutCollectingUnrelatedSources(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Mei")
	peopleTestPurpose(t, a, "Review the plan", person.DisplayName, PurposeOwnerUser, time.Time{})
	now := time.Now()
	full := a.collectTodayChatSources(t.Context(), now).Purposes
	collections := 0
	a.todayChatCollectionObserver = func() { collections++ }
	fast := a.peoplePurposeLoops(t.Context(), now)
	if !reflect.DeepEqual(fast, full) || collections != 0 {
		t.Fatalf("Purpose closure changed or unrelated sources collected: reads=%d", collections)
	}
}

func TestPageWarmerWaitsForBothServicesAndFirstPaint(t *testing.T) {
	a := newPageFirstReadTestApp(t)
	allowPageWarmer(t, a)
	a.pageFirstReadsPainted()
	if a.pageWarm.started {
		t.Fatal("warmer started before services were ready")
	}
	a.pageFirstReadsReady()
	done := a.pageWarm.done
	a.pageFirstReadsReady()
	a.pageFirstReadsPainted()
	if done == nil || done != a.pageWarm.done {
		t.Fatal("first paint did not coalesce warmer startup")
	}
	a.stopPageFirstReads()
	select {
	case <-done:
	default:
		t.Fatal("shutdown did not join the warm worker")
	}
}
