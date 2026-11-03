package main

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/storage"
)

func newCompactBoundaryTestStore(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.New(filepath.Join(t.TempDir(), "compact-boundary-test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Sessions created outside the frontend (browser-server API, headless callers)
// have no sessions row; stream_items.session_local_id carries a FOREIGN KEY on
// sessions(local_id), so persisting the compaction boundary used to fail with
// "FOREIGN KEY constraint failed". persistCompactionBoundary must skip quietly
// for such sessions and still persist normally for frontend-owned ones.
func TestPersistCompactionBoundary_UnpersistedSessionSkips(t *testing.T) {
	store := newCompactBoundaryTestStore(t)
	a := &App{store: store}

	var logBuf bytes.Buffer
	prevOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prevOut)

	a.persistCompactionBoundary("ghost-session-tag", "summary text")

	if strings.Contains(logBuf.String(), "FOREIGN KEY") {
		t.Fatalf("persistCompactionBoundary hit the FK constraint instead of skipping:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "skip persisting boundary") {
		t.Fatalf("expected skip log line for unpersisted session, got:\n%s", logBuf.String())
	}

	items, err := store.LoadStreamItems(context.Background(), "ghost-session-tag")
	if err != nil {
		t.Fatalf("LoadStreamItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no stream items for unpersisted session, got %d", len(items))
	}
}

func TestPersistCompactionBoundary_PersistedSessionWritesRows(t *testing.T) {
	store := newCompactBoundaryTestStore(t)
	ctx := context.Background()

	if err := store.SaveProject(ctx, storage.ProjectRow{ID: "proj-1", Name: "Test", Path: "/tmp"}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveSession(ctx, storage.SessionRow{
		LocalID:   "real-session-tag",
		ProjectID: "proj-1",
		Label:     "Test session",
		Model:     "sonnet",
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	a := &App{store: store}
	a.persistCompactionBoundary("real-session-tag", "summary text")

	items, err := store.LoadStreamItems(ctx, "real-session-tag")
	if err != nil {
		t.Fatalf("LoadStreamItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected compact_summary + compact_boundary rows, got %d items", len(items))
	}
	kinds := map[string]bool{}
	for _, it := range items {
		kinds[it.Kind] = true
	}
	if !kinds["compact_summary"] || !kinds["compact_boundary"] {
		t.Fatalf("expected compact_summary and compact_boundary kinds, got %v", kinds)
	}
}
