package main

import (
	"testing"
	"time"
)

// The triager's long reads of task episodes run on the read pool, so they never hold the
// activity store's single writer connection (R24, 2026-11-05: a triager scan holding it
// stalled every reader of sessions and activity behind it).
func TestTaskEpisodeListDoesNotWaitForTheWriter(t *testing.T) {
	store := newTestStore(t)
	writer, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	done := make(chan error, 1)
	go func() {
		_, err := store.ListTaskEpisodes("", 10)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("list with the writer held: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a task episode list waited for the activity writer connection")
	}
}
