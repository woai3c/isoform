package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/storage"
)

// A note edited outside the app must withdraw the chat history bound to it on
// the next read, even when the note had settled and its memory snapshot was
// checked just before the edit. Before recheckMemoryFolder, the first recompute
// read the stale snapshot and the verdict stayed cached until the next change.
func TestTopicHistoryDropsOnAnOutsideEditOfASettledNote(t *testing.T) {
	a, tag := topicChatTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "atlas.md"), old, old); err != nil {
		t.Fatal(err)
	}
	metadata, err := a.topicChatAdmissionMetadataFromSources(t.Context(), []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}, a.collectTodayChatSources(t.Context(), time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "w-0", Kind: "user", Content: "recent topic", TopicID: "atlas", AutomationContext: metadata}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	defer a.finishChatPreparation(tag, cancel)
	if rows, bound, err := a.sourceBoundChatHistory(ctx, tag); err != nil || !bound || len(rows) != 1 {
		t.Fatalf("before the edit the history should be bound to its note: rows=%d bound=%v err=%v", len(rows), bound, err)
	}
	writeTodayContextMemory(t, "atlas", "# Atlas\nCorrected source")
	rows, bound, err := a.sourceBoundChatHistory(ctx, tag)
	if err == nil && bound && len(rows) == 1 {
		t.Fatal("the outside edit went unseen: the bound history stayed shown")
	}
	// The verdict must not stay stale on later reads either.
	for i := 0; i < 3; i++ {
		rows, bound, err = a.sourceBoundChatHistory(ctx, tag)
		if err == nil && bound && len(rows) == 1 {
			t.Fatalf("read %d after the edit still shows the bound history", i)
		}
	}
}
