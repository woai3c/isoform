package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMobileChatListSkipsHiddenSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	project := ProjectRow{ID: "project-1", Name: "Project", Path: filepath.Join(dir, "repo")}
	workspace := WorkspaceRow{ID: "workspace-1", ProjectID: "project-1", Label: "Main", IsDefault: true}
	if err := store.EnsureProjectStructure(ctx, project, []WorkspaceRow{workspace}, nil); err != nil {
		t.Fatal(err)
	}
	for _, session := range []SessionRow{
		{LocalID: "visible", ProjectID: "project-1", WorkspaceID: "workspace-1", Label: "Visible"},
		{LocalID: "cron", ProjectID: "project-1", WorkspaceID: "workspace-1", Label: "Cron", Hidden: true},
	} {
		if err := store.SaveSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.MobileChatList(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "visible" || rows[0].Label != "Visible" {
		t.Fatalf("list returned %+v", rows)
	}
	if _, found, err := store.MobileChat(ctx, "cron"); err != nil || found {
		t.Fatalf("hidden session is visible to the phone: found=%v err=%v", found, err)
	}
	row, found, err := store.MobileChat(ctx, "visible")
	if err != nil || !found || row.Label != "Visible" || row.ProjectPath != project.Path {
		t.Fatalf("visible session: %+v found=%v err=%v", row, found, err)
	}
}

func TestLoadStreamItemsAfterPagesBySortOrder(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := store.EnsureProjectStructure(ctx, ProjectRow{ID: "p", Name: "P", Path: filepath.Join(dir, "p")}, []WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, SessionRow{LocalID: "chat", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := store.AppendStreamItem(ctx, "chat", StreamItemRow{ID: string(rune('a' + i)), Kind: "user", Content: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.LoadStreamItemsAfter(ctx, "chat", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].SortOrder != 2 || page.Items[1].SortOrder != 3 || !page.HasMore {
		t.Fatalf("first page: %+v", page)
	}
	page, err = store.LoadStreamItemsAfter(ctx, "chat", 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SortOrder != 4 || page.HasMore {
		t.Fatalf("last page: %+v", page)
	}
}

func TestDeleteStreamItemByIDKeepsOtherRows(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := store.EnsureProjectStructure(ctx, ProjectRow{ID: "p", Name: "P", Path: filepath.Join(dir, "p")}, []WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, SessionRow{LocalID: "chat", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"phone-user", "desktop-reply"} {
		if _, err := store.AppendStreamItem(ctx, "chat", StreamItemRow{ID: id, Kind: "user", Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteStreamItemByID(ctx, "phone-user"); err != nil {
		t.Fatal(err)
	}
	items, err := store.LoadStreamItems(ctx, "chat")
	if err != nil || len(items) != 1 || items[0].ID != "desktop-reply" {
		t.Fatalf("after deleting one row: %+v (err %v)", items, err)
	}
}
