package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSearchReadVisibilityLiteralAndSparseCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if err := s.EnsureProjectStructure(ctx, ProjectRow{ID: "p", Name: "P", Path: filepath.Join(t.TempDir(), "p")}, []WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "s", ProjectID: "p", WorkspaceID: "w", Label: "Old title"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO stream_items(id,session_local_id,kind,content,sort_order,created_at) VALUES('first','s','user','100% literal_ ÉCOLE',7,1000),('later','s','assistant','later',100,2000)`); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.SearchChats(ctx, []string{"100%", "literal_", "école"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "first" {
		t.Fatalf("literal Unicode: %+v", rows)
	}
	cursor, err := s.FindVisibleStreamItemCursor(ctx, "s", "first")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.LoadStreamItemsBefore(ctx, "s", cursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 8 || len(page.Items) != 1 || page.Items[0].ID != "first" {
		t.Fatalf("sparse page: %d %+v", cursor, page)
	}
	after, err := s.ReadRevision(ctx)
	if err != nil || before != after {
		t.Fatalf("read mutated store: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE workspaces SET hidden=1 WHERE id='w'`); err != nil {
		t.Fatal(err)
	}
	rows, err = s.SearchChats(ctx, []string{"literal_"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("hidden workspace: %+v %v", rows, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SearchChats(cancelled, []string{"literal_"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
