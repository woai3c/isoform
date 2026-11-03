package storage

import (
	"testing"
	"time"
)

func TestModelCallsRecordListAndPrune(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	old := ModelCallRow{At: now.Add(-48 * time.Hour).UnixMilli(), SessionID: "s1", Requested: "group/yansu-chat", Provider: "claude", Model: "claude/claude-sonnet-5-5", Source: "chat"}
	recent := ModelCallRow{SessionID: "s2", RunID: "r2", Requested: "group/yansu-light", Provider: "codex", Model: "codex/gpt-5.6-luna", Priority: "background", Source: "ephemeral"}
	if err := s.RecordModelCall(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordModelCall(ctx, recent); err != nil {
		t.Fatal(err)
	}
	calls, err := s.RecentModelCalls(ctx, 0)
	if err != nil || len(calls) != 2 {
		t.Fatalf("RecentModelCalls = %+v %v", calls, err)
	}
	if calls[0].Provider != "codex" || calls[0].At == 0 || calls[0].Priority != "background" {
		t.Fatalf("newest first with defaults applied: %+v", calls[0])
	}
	n, err := s.PruneModelCalls(ctx, now.Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("PruneModelCalls = %d %v", n, err)
	}
	calls, _ = s.RecentModelCalls(ctx, 10)
	if len(calls) != 1 || calls[0].SessionID != "s2" {
		t.Fatalf("after prune: %+v", calls)
	}
}
