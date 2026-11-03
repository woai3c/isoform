package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestVoiceReviewProactivePostingAndActionsOnSchema60(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compatible.db")
	store, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		DROP TRIGGER stream_items_proactive_children_insert;
		DROP TRIGGER stream_items_proactive_children_update;
		DROP TABLE stream_item_proactive_children;
		DROP TRIGGER stream_items_status_entries_insert;
		DROP TRIGGER stream_items_status_entries_update;
		DROP TABLE stream_item_status_entries;
		DROP INDEX idx_stream_items_proactive_moment;
		DROP INDEX idx_stream_items_session_tool_order;
		PRAGMA user_version=60;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := &App{store: store, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
	moment := engagement.Moment{ID: "compatible-card", Kind: engagement.KindBriefReady, SubjectID: "brief"}
	if !app.postEngagementReport(moment, engagementText{PushBody: "Your brief is ready.", Title: "Your morning brief is ready."}) {
		t.Fatal("compatible schema lost proactive card")
	}
	if state, err := app.ResolveMainChatProactiveAction(moment.ID, "open"); err != nil || state != "opened" {
		t.Fatalf("compatible card action = %q, %v", state, err)
	}
	if id := app.postMainChatStatus(mainChatStatusEntry{ID: "compatible-status", Kind: "digest_done", Text: "Updates are ready."}, time.Now()); id == "" {
		t.Fatal("compatible schema lost status row")
	}
	if len(mainChatProactiveItems(t, app)) != 1 || len(mainChatStatusEntries(t, app)) != 1 {
		t.Fatal("compatible schema lost structured items")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != storage.SchemaVersion {
		t.Fatalf("schema 60 was not migrated to the current schema: %d, %v", version, err)
	}
}
