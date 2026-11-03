package persistence

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"wails-gui/storage"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	return NewService(store)
}

func seedProjectData() ProjectData {
	return ProjectData{
		ID:             "project-1",
		Name:           "Demo",
		Path:           "/tmp/demo",
		IsYansuProject: true,
		Workspaces: []WorkspaceData{
			{
				ID:           "workspace-1",
				Label:        "Default",
				IsDefault:    true,
				WorktreePath: "/tmp/demo",
				BaseBranch:   "main",
				Sessions: []SessionData{
					{
						LocalID:     "session-1",
						WorkspaceID: "workspace-1",
						SessionID:   "claude-session-1",
						Label:       "Session 1",
						Model:       "sonnet",
					},
				},
			},
		},
	}
}

func TestServiceLoadAllProjectsIncludesSessionMetadataOnly(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	project := seedProjectData()
	if err := svc.SaveProjectState(ctx, project); err != nil {
		t.Fatalf("save project: %v", err)
	}

	if _, err := svc.PersistStreamItem(ctx, "session-1", StreamItemData{
		ID:      "item-1",
		Kind:    "user",
		Content: "hello",
	}); err != nil {
		t.Fatalf("append stream item: %v", err)
	}

	loaded, err := svc.LoadAllProjects(ctx)
	if err != nil {
		t.Fatalf("load projects: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 project, got %d", len(loaded))
	}
	if len(loaded[0].Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(loaded[0].Workspaces))
	}
	if len(loaded[0].Workspaces[0].Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(loaded[0].Workspaces[0].Sessions))
	}
	session := loaded[0].Workspaces[0].Sessions[0]
	if session.StreamItemCount != 1 {
		t.Fatalf("expected stream item count 1, got %d", session.StreamItemCount)
	}
	if len(session.Items) != 0 {
		t.Fatalf("expected metadata-only load to omit stream items, got %d", len(session.Items))
	}
}

func TestServiceForkSessionReturnsLazyMetadata(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.SaveProjectState(ctx, seedProjectData()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PersistStreamItem(ctx, "session-1", StreamItemData{
		ID:      "item-1",
		Kind:    "user",
		Content: "Original question",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := svc.ForkSession(ctx, "session-1", "Session 1 · Branch")
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != "project-1" {
		t.Fatalf("project id = %q", result.ProjectID)
	}
	if result.Session.LocalID == "" || result.Session.LocalID == "session-1" {
		t.Fatalf("invalid forked local id %q", result.Session.LocalID)
	}
	if result.Session.SessionID != "" || result.Session.StreamItemCount != 1 {
		t.Fatalf("forked metadata = %+v", result.Session)
	}
	if result.Session.Items != nil {
		t.Fatalf("fork response should remain metadata-only, got %d items", len(result.Session.Items))
	}

	page, err := svc.LoadStreamItemsPage(ctx, result.Session.LocalID, -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Content != "Original question" {
		t.Fatalf("forked history = %+v", page.Items)
	}
}

func TestServiceLoadStreamItemsPageLatestAndBefore(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.SaveProjectState(ctx, seedProjectData()); err != nil {
		t.Fatalf("save project: %v", err)
	}

	for i := 0; i < 4; i++ {
		if _, err := svc.PersistStreamItem(ctx, "session-1", StreamItemData{
			ID:      fmt.Sprintf("item-%d", i),
			Kind:    "assistant",
			Content: "chunk",
		}); err != nil {
			t.Fatalf("append stream item %d: %v", i, err)
		}
	}

	latest, err := svc.LoadStreamItemsPage(ctx, "session-1", -1, 2)
	if err != nil {
		t.Fatalf("load latest page: %v", err)
	}
	if len(latest.Items) != 2 {
		t.Fatalf("expected 2 latest items, got %d", len(latest.Items))
	}
	if !latest.HasMore {
		t.Fatalf("expected hasMore=true for latest page")
	}
	if latest.NextCursor != 2 {
		t.Fatalf("expected nextCursor=2, got %d", latest.NextCursor)
	}

	before, err := svc.LoadStreamItemsPage(ctx, "session-1", latest.NextCursor, 2)
	if err != nil {
		t.Fatalf("load older page: %v", err)
	}
	if len(before.Items) != 2 {
		t.Fatalf("expected 2 older items, got %d", len(before.Items))
	}
	if before.HasMore {
		t.Fatalf("expected hasMore=false for older page")
	}
	if before.NextCursor != 0 {
		t.Fatalf("expected nextCursor=0, got %d", before.NextCursor)
	}
}

func TestServicePersistStreamItem_PreservesImages(t *testing.T) {
	// Regression for the "uploaded image disappears on chat reload" bug:
	// PersistStreamItem used to drop Images because the wire type had no
	// field for it. This test pins the full round-trip through the
	// service layer so a future refactor of StreamItemData can't silently
	// regress it again.
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.SaveProjectState(ctx, seedProjectData()); err != nil {
		t.Fatalf("save project: %v", err)
	}

	const imagesJSON = `[{"mediaType":"image/png","dataBase64":"iVBORw0KGgo="},{"mediaType":"image/jpeg","dataBase64":"/9j/4AAQ"}]`
	if _, err := svc.PersistStreamItem(ctx, "session-1", StreamItemData{
		ID:      "user-with-images",
		Kind:    "user",
		Content: "what's in this screenshot?",
		Images:  imagesJSON,
	}); err != nil {
		t.Fatalf("persist stream item: %v", err)
	}
	if _, err := svc.PersistStreamItem(ctx, "session-1", StreamItemData{
		ID:      "user-no-images",
		Kind:    "user",
		Content: "plain text",
	}); err != nil {
		t.Fatalf("persist plain stream item: %v", err)
	}

	page, err := svc.LoadStreamItemsPage(ctx, "session-1", -1, 10)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}

	byID := map[string]StreamItemData{}
	for _, it := range page.Items {
		byID[it.ID] = it
	}

	if got := byID["user-with-images"].Images; got != imagesJSON {
		t.Errorf("images round-trip: got %q, want %q", got, imagesJSON)
	}
	if got := byID["user-no-images"].Images; got != "" {
		t.Errorf("plain item should have no images, got %q", got)
	}
}

func TestServiceReorderProjectsAndNextSortOrder(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	projectOne := seedProjectData()
	projectOne.SortOrder = 0
	if err := svc.SaveProjectState(ctx, projectOne); err != nil {
		t.Fatalf("save project one: %v", err)
	}

	projectTwo := seedProjectData()
	projectTwo.ID = "project-2"
	projectTwo.Name = "Demo Two"
	projectTwo.Path = "/tmp/demo-two"
	projectTwo.SortOrder = 1
	projectTwo.Workspaces[0].ID = "workspace-2"
	projectTwo.Workspaces[0].Sessions[0].LocalID = "session-2"
	projectTwo.Workspaces[0].Sessions[0].WorkspaceID = "workspace-2"
	projectTwo.Workspaces[0].Sessions[0].SessionID = "claude-session-2"
	if err := svc.SaveProjectState(ctx, projectTwo); err != nil {
		t.Fatalf("save project two: %v", err)
	}

	next, err := svc.NextProjectSortOrder(ctx)
	if err != nil {
		t.Fatalf("next sort order: %v", err)
	}
	if next != 2 {
		t.Fatalf("next sort order = %d, want 2", next)
	}

	if err := svc.ReorderProjects(ctx, []string{"project-2", "project-1"}); err != nil {
		t.Fatalf("reorder projects: %v", err)
	}

	loaded, err := svc.LoadAllProjects(ctx)
	if err != nil {
		t.Fatalf("load projects after reorder: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(loaded))
	}
	if loaded[0].ID != "project-2" || loaded[0].SortOrder != 0 {
		t.Fatalf("first project = %+v, want ID=project-2 sortOrder=0", loaded[0])
	}
	if loaded[1].ID != "project-1" || loaded[1].SortOrder != 1 {
		t.Fatalf("second project = %+v, want ID=project-1 sortOrder=1", loaded[1])
	}
}
