package main

import (
	"context"
	"testing"
)

// The cached repeated-onset set follows the rows it reads. Task episodes are
// immutable (database triggers abort every UPDATE and DELETE), so the only change
// that can reach it is an insert: a new episode that repeats an onset. That insert
// must reach the next read, and the revision must move with it.
func TestRepeatedOnsetSetFollowsNewRepeatedOnsets(t *testing.T) {
	store := newTestStore(t)
	anchor := taskEpisodeOnsetAnchor{Ref: "event:keyboard:77", SourceKind: "activity_event", SourceID: "keyboard-77", ObservedAt: 2_000}
	first := createHistoricalOnsetEpisodeFixture(t, store, "revision-first", anchor, 2_100)
	ctx := context.Background()

	fresh := func() map[string]struct{} {
		t.Helper()
		index, err := store.taskEpisodeOnsetIdentityIndexAtContext(ctx, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return index.repeatedEpisodeIDs()
	}
	cached := func() map[string]struct{} {
		t.Helper()
		set, err := store.taskEpisodeRepeatedOnsetSetContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return set
	}
	assertSame := func(step string, got, want map[string]struct{}) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: cached %v differs from a fresh scan %v", step, got, want)
		}
		for k := range want {
			if _, ok := got[k]; !ok {
				t.Fatalf("%s: cached %v differs from a fresh scan %v", step, got, want)
			}
		}
	}

	// One episode alone is not repeated.
	assertSame("one episode", cached(), fresh())
	if len(cached()) != 0 {
		t.Fatal("a single onset was reported as repeated")
	}

	// A read with no change is served from the cache.
	scans := repeatedOnsetScans.Load()
	revision, err := store.taskEpisodeReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached()
	cached()
	if repeatedOnsetScans.Load() != scans {
		t.Fatal("an unchanged read rescanned")
	}

	// A second episode with the same onset is an insert: the next read sees it.
	createHistoricalOnsetEpisodeFixture(t, store, "revision-second", anchor, 2_200)
	after, err := store.taskEpisodeReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after == revision {
		t.Fatal("an insert did not move the read revision")
	}
	got := cached()
	assertSame("after a repeat", got, fresh())
	if _, ok := got[first.ID]; !ok {
		t.Fatalf("the first episode is not repeated after the second arrived: %v", got)
	}
	if repeatedOnsetScans.Load() == scans {
		t.Fatal("the insert did not force a rescan")
	}

	// Immutability holds: the rows the set reads cannot be deleted under the cache.
	if _, err := store.db.Exec(`DELETE FROM task_episodes WHERE id = ?`, first.ID); err == nil {
		t.Fatal("a task episode was deleted; the immutability trigger is gone")
	}
}
