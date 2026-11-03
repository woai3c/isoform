package main

import (
	"context"
	"testing"
	"time"
)

func TestTodayRefreshedMemoryUsesSharedReadGate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provenance memoryReadProvenance
		paused     bool
	}{
		{name: "read blocked", provenance: memoryReadProvenance{Version: 2, ReadBlocked: true}},
		{name: "flattened sensitive app", provenance: memoryReadProvenance{Version: 2, Apps: []string{"1Password"}}},
		{name: "flattened missing session", provenance: memoryReadProvenance{Version: 2, Sessions: []string{"missing"}}},
		{name: "closed eyes", provenance: memoryReadProvenance{Version: 2}, paused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTodayContextTestApp(t, true)
			writeTodayContextMemory(t, "edited", "# Safe original\nOriginal body")
			if got := a.collectTodayChatSources(context.Background(), time.Now()); len(got.Memories) != 1 {
				t.Fatal("readable control missing")
			}
			writeTodayContextMemory(t, "edited", renderOKFDocument(map[string]any{
				"source": "user_edit", "read_provenance": memoryReadProvenanceJSON(tc.provenance),
			}, "# Refreshed title\nRestricted refreshed body"))
			a.closedEyes.enabled.Store(tc.paused)
			// Final Today admission owns these barriers. The read callback must
			// use the shared content gate, without acquiring file writer locks.
			memoryMutationMu.Lock()
			defer memoryMutationMu.Unlock()
			memoryFileLocks.publication.Lock()
			defer memoryFileLocks.publication.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if got := a.collectTodayChatSources(ctx, time.Now()); len(got.Memories) != 0 {
				t.Fatalf("restricted refreshed body admitted: %+v", got.Memories)
			}
		})
	}
}
