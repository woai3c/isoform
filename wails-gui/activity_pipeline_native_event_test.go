package main

import (
	"context"
	"testing"
	"time"
)

func TestPipelineRunWritesWithoutNativeEventContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "nil"},
		{name: "ordinary", ctx: context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewActivityStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			app := &App{ctx: tc.ctx, activity: &activityMonitorState{store: store}}
			triager := NewActivityTriager(app, nil)
			now := time.Now().UnixMilli()
			run := ActivityPipelineRunRow{Kind: "tick", StartedAt: now, CompletedAt: now + 1, TriageStatus: "skipped", TriageReason: "nothing_pending"}
			triager.recordPipelineRun(&run)
			if run.ID == 0 {
				t.Fatal("native event context must not be required to save diagnostics")
			}
			saved, err := store.GetActivityPipelineRunByID(run.ID)
			if err != nil || saved.TriageReason != run.TriageReason {
				t.Fatalf("inserted diagnostic = %+v, error = %v", saved, err)
			}
			run.CompletedAt = now + 2
			run.Error = "retry"
			triager.recordPipelineRun(&run)
			saved, err = store.GetActivityPipelineRunByID(run.ID)
			if err != nil || saved.ID != run.ID || saved.CompletedAt != run.CompletedAt || saved.Error != "retry" {
				t.Fatalf("updated diagnostic = %+v, error = %v", saved, err)
			}
		})
	}
}
