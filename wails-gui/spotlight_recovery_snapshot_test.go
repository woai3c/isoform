package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"wails-gui/storage"
)

func TestSpotlightSnapshotReconcilesLiveHistoryAndPermission(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	if err := app.store.EnsureProjectStructure(ctx, storage.ProjectRow{ID: "p", Name: "P", Path: filepath.Join(t.TempDir(), "p")}, []storage.WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SaveSession(ctx, storage.SessionRow{LocalID: "s", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(ctx, "s", storage.StreamItemRow{ID: "user", Kind: "user", Content: "Keep this"}); err != nil {
		t.Fatal(err)
	}
	state := &spotlightTagState{done: make(chan struct{}), sortOrder: 1, streaming: true}
	app.spotlightTags = map[string]*spotlightTagState{"s": state}
	app.spotlightStream = newSpotlightStreamHub()
	app.bridgeAgentEventToSpotlight(AgentEvent{Type: "assistant", Tag: "s", RunID: "run", Content: "Partial response"}, state)
	app.bridgeAgentEventToSpotlight(AgentEvent{Type: "permission_request", Tag: "s", EventID: "permission", SessionID: "agent", ToolUseID: "tool-1"}, state)

	hb := &hostBridge{app: app}
	rec := httptest.NewRecorder()
	hb.handleSpotlightSnapshot(rec, httptest.NewRequest(http.MethodGet, "/api/spotlight/snapshot?sessionLocalId=s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items      []StreamItemData `json:"items"`
		Sequence   uint64           `json:"sequence"`
		Streaming  bool             `json:"streaming"`
		Permission *AgentEvent      `json:"permission"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].Content != "Keep this" || got.Items[1].Content != "Partial response" || !got.Streaming || got.Permission == nil || got.Permission.EventID != "permission" || got.Sequence != 2 {
		t.Fatalf("snapshot=%+v", got)
	}
	replay, cancel, err := app.spotlightStream.Subscribe("s", got.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	app.bridgeAgentEventToSpotlight(AgentEvent{Type: "assistant", Tag: "s", RunID: "run", Content: " continued"}, state)
	if next := <-replay; next.Sequence != got.Sequence+1 {
		t.Fatalf("next=%+v", next)
	}
	app.bridgeAgentEventToSpotlight(AgentEvent{Type: "tool_result", Tag: "s", ToolUseID: "tool-1", Content: "Done"}, state)
	snapshot, err := app.spotlightSnapshot(ctx, "s", state)
	if err != nil || snapshot.Permission != nil {
		t.Fatalf("resolved permission snapshot=%+v err=%v", snapshot, err)
	}

	state.mu.Lock()
	state.persistErr = errors.New("write failed")
	state.mu.Unlock()
	rec = httptest.NewRecorder()
	hb.handleSpotlightSnapshot(rec, httptest.NewRequest(http.MethodGet, "/api/spotlight/snapshot?sessionLocalId=s", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("failed persistence status=%d", rec.Code)
	}
}

func TestSpotlightPersistRetryReusesUserMessage(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	if err := app.store.EnsureProjectStructure(ctx, storage.ProjectRow{ID: "p", Name: "P", Path: filepath.Join(t.TempDir(), "p")}, []storage.WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SaveSession(ctx, storage.SessionRow{LocalID: "s", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	state := &spotlightTagState{done: closedChan()}
	id, order, err := app.persistSpotlightUserMessage("s", "Keep this", "request", state)
	if err != nil {
		t.Fatal(err)
	}
	againID, againOrder, err := app.persistSpotlightUserMessage("s", "Keep this", "request", state)
	if err != nil || id != againID || order != againOrder {
		t.Fatalf("retry=%s/%d, want %s/%d: %v", againID, againOrder, id, order, err)
	}
	if _, _, err := app.persistSpotlightUserMessage("s", "Different text", "request", state); err == nil {
		t.Fatal("accepted different text for the same request")
	}
	rows, err := app.store.LoadStreamItems(ctx, "s")
	if err != nil || len(rows) != 1 || rows[0].Content != "Keep this" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestSpotlightDoesNotRepeatUnknownPriorRequest(t *testing.T) {
	app := mainChatTestApp(t)
	app.ctx = context.Background()
	app.spotlightStream = newSpotlightStreamHub()
	ctx := context.Background()
	if err := app.store.EnsureProjectStructure(ctx, storage.ProjectRow{ID: "p", Name: "P", Path: filepath.Join(t.TempDir(), "p")}, []storage.WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SaveSession(ctx, storage.SessionRow{LocalID: "spotlight_req", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(ctx, "spotlight_req", storage.StreamItemRow{ID: "spotlight_req-u-req", Kind: "user", Content: "Keep this"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := app.startSpotlightSubmission("Keep this", "", "", "req"); err == nil {
			t.Fatal("restarted an unknown prior request")
		}
	}
	rows, err := app.store.LoadStreamItems(ctx, "spotlight_req")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}
