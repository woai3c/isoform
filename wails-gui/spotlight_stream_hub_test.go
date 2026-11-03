package main

import (
	"errors"
	"testing"
)

func TestSpotlightStreamResumesFromLastReceivedEvent(t *testing.T) {
	hub := newSpotlightStreamHub()
	hub.Publish("chat", spotlightStreamEvent{Kind: "agent_event", Data: "first"})
	first, cancel, err := hub.Subscribe("chat", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-first; got.Sequence != 1 || got.Data != "first" {
		t.Fatalf("first=%+v", got)
	}
	cancel()
	hub.Publish("chat", spotlightStreamEvent{Kind: "agent_event", Data: "missed"})
	resumed, stop, err := hub.Subscribe("chat", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if got := <-resumed; got.Sequence != 2 || got.Data != "missed" {
		t.Fatalf("replayed=%+v", got)
	}
	hub.Publish("chat", spotlightStreamEvent{Kind: "agent_event", Data: "live"})
	if got := <-resumed; got.Sequence != 3 || got.Data != "live" {
		t.Fatalf("live=%+v", got)
	}
}

func TestSpotlightStreamReportsExpiredReplay(t *testing.T) {
	hub := newSpotlightStreamHub()
	for i := 0; i < spotlightBacklogCap+1; i++ {
		hub.Publish("chat", spotlightStreamEvent{Kind: "agent_event", Data: "x"})
	}
	if _, _, err := hub.Subscribe("chat", 0); !errors.Is(err, errSpotlightReplayUnavailable) {
		t.Fatalf("expired cursor: %v", err)
	}
}

func TestSpotlightStreamDisconnectsSlowSubscriberAndReplays(t *testing.T) {
	hub := newSpotlightStreamHub()
	events, cancel, err := hub.Subscribe("chat", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	for i := 0; i < 70; i++ {
		hub.Publish("chat", spotlightStreamEvent{Kind: "agent_event", Data: "x"})
	}
	var last uint64
	for event := range events {
		last = event.Sequence
	}
	if last != 64 {
		t.Fatalf("last received=%d", last)
	}
	replay, stop, err := hub.Subscribe("chat", last)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for want := uint64(65); want <= 70; want++ {
		if got := <-replay; got.Sequence != want {
			t.Fatalf("got=%d want=%d", got.Sequence, want)
		}
	}
}
