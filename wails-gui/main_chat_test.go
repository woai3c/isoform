package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"wails-gui/storage"
)

// mainChatTestApp builds an App over a fresh store with nothing else wired,
// the way the phone and handoff tests do.
func mainChatTestApp(t *testing.T) *App {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "yansu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{store: store, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
}

func TestEnsureMainChatCreatesOneIsoformSession(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()

	first, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatalf("ensureMainChat: %v", err)
	}
	if first.Kind != storage.SessionKindMain || first.Label != mainChatTitle || first.ParentID != "" {
		t.Fatalf("main chat = %+v", first)
	}
	if first.ProjectID != standaloneChatProjectID {
		t.Fatalf("main chat project = %q, want the standalone project", first.ProjectID)
	}

	// A second call finds the same row instead of creating another.
	again, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatalf("second ensureMainChat: %v", err)
	}
	if again.LocalID != first.LocalID {
		t.Fatalf("second ensure created %s, want %s", again.LocalID, first.LocalID)
	}

	// The startup hook and the renderer binding both resolve to it.
	app.ensureMainChatOnStartup()
	info, err := app.GetMainChat()
	if err != nil {
		t.Fatalf("GetMainChat: %v", err)
	}
	if info.Session.LocalID != first.LocalID || info.Session.Kind != storage.SessionKindMain || info.ProjectID != first.ProjectID || info.WorkspaceID != first.WorkspaceID {
		t.Fatalf("GetMainChat = %+v", info)
	}

	mains := 0
	all, err := app.store.LoadAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rows := range all {
		for _, row := range rows {
			if row.Kind == storage.SessionKindMain {
				mains++
			}
		}
	}
	if mains != 1 {
		t.Fatalf("main chats = %d, want 1", mains)
	}
}

func TestEnsureMainChatRestoresHiddenAndRefusesDelete(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()

	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SoftDeleteSession(ctx, main.LocalID); err != nil {
		t.Fatal(err)
	}
	restored, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restored.LocalID != main.LocalID || restored.Hidden {
		t.Fatalf("restored = %+v, want the same visible row", restored)
	}
	visible, err := app.store.LoadSessions(ctx, main.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range visible {
		if row.LocalID == main.LocalID {
			found = true
		}
	}
	if !found {
		t.Fatal("main chat is still hidden after ensureMainChat")
	}

	if err := app.DeleteSessionData(main.LocalID); !errors.Is(err, errMainChatProtected) {
		t.Fatalf("DeleteSessionData(main) err = %v, want errMainChatProtected", err)
	}
	if err := app.DeleteWorkspaceData(main.WorkspaceID); !errors.Is(err, errMainChatProtected) {
		t.Fatalf("DeleteWorkspaceData(main workspace) err = %v, want errMainChatProtected", err)
	}
	if err := app.DeleteProjectData(main.ProjectID); !errors.Is(err, errMainChatProtected) {
		t.Fatalf("DeleteProjectData(main project) err = %v, want errMainChatProtected", err)
	}
	if _, err := app.store.LoadSession(ctx, main.LocalID); err != nil {
		t.Fatalf("main chat gone after refused delete: %v", err)
	}
}
