package storage

import (
	"path/filepath"
	"testing"
)

func TestAgentRunRouteRetainsModelAndActionReceiptAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.db")
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	row := AgentRunRouteRow{RunID: "chat-run", SessionID: "chat", Backend: "claude-code", CandidateBackends: `["claude-code","native"]`, FallbackFrom: `[]`, Status: "running"}
	if err := store.RecordAgentRunRoute(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.Model, row.ActionsStarted, row.Status = "claude-opus", 2, ""
	if err := store.RecordAgentRunRoute(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.Model, row.ActionsStarted, row.Status = "", 0, "blocked"
	if err := store.RecordAgentRunRoute(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.AgentRunRoute(ctx, row.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-opus" || got.ActionsStarted != 2 || got.Status != "blocked" || got.Backend != "claude-code" || got.SessionID != "chat" || got.UpdatedAt == 0 {
		t.Fatalf("lost route receipt: %+v", got)
	}
}

func TestAgentRunRouteDoesNotMixAttempts(t *testing.T) {
	store := newTestStore(t)
	for _, row := range []AgentRunRouteRow{
		{RunID: "assistant", Backend: "claude-code", Model: "claude-opus", Status: "error"},
		{RunID: "backup", Backend: "native", Model: "local-model", FallbackFrom: `["claude-code"]`, Status: "done"},
	} {
		if err := store.RecordAgentRunRoute(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	assistant, err := store.AgentRunRoute(ctx, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := store.AgentRunRoute(ctx, "backup")
	if err != nil {
		t.Fatal(err)
	}
	if assistant.Status != "error" || assistant.Model != "claude-opus" || backup.Backend != "native" || backup.Model != "local-model" || backup.FallbackFrom != `["claude-code"]` {
		t.Fatalf("attempts mixed: %+v %+v", assistant, backup)
	}
}

func TestEndInterruptedAgentRunRoutes(t *testing.T) {
	store := newTestStore(t)
	for _, row := range []AgentRunRouteRow{
		{RunID: "talking", Backend: "native", BackendReason: "Isoform is the backup after the user's assistants", Status: "running"},
		{RunID: "acting", Backend: "native", Status: "running", ActionsStarted: 2},
		{RunID: "silent", Backend: "claude-code", Status: "running"},
		{RunID: "finished", Backend: "native", BackendReason: "kept", Status: "done"},
		{RunID: "failed", Backend: "claude-code", BackendReason: "kept", Status: "error"},
	} {
		if err := store.RecordAgentRunRoute(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.EndInterruptedAgentRunRoutes(ctx)
	if err != nil || n != 3 {
		t.Fatalf("ended %d, err=%v; want 3", n, err)
	}
	for _, tc := range []struct{ runID, status, reason string }{
		{"talking", "error", "Isoform is the backup after the user's assistants; " + InterruptedRunReason},
		{"acting", "blocked", InterruptedRunReason},
		{"silent", "error", InterruptedRunReason},
		{"finished", "done", "kept"},
		{"failed", "error", "kept"},
	} {
		got, err := store.AgentRunRoute(ctx, tc.runID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tc.status || got.BackendReason != tc.reason {
			t.Errorf("%s: status=%q reason=%q, want %q %q", tc.runID, got.Status, got.BackendReason, tc.status, tc.reason)
		}
	}
	if n, err := store.EndInterruptedAgentRunRoutes(ctx); err != nil || n != 0 {
		t.Fatalf("second pass ended %d, err=%v", n, err)
	}
}
