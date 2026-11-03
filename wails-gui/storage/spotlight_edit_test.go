package storage

import (
	"context"
	"testing"
)

func TestReplaceUserTurnIsAtomicAndChecksIdentity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.EnsureProjectStructure(ctx, ProjectRow{ID: "p", Name: "P"}, []WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, SessionRow{LocalID: "s", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	order, err := store.AppendStreamItem(ctx, "s", StreamItemRow{ID: "user", Kind: "user", Content: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendStreamItem(ctx, "s", StreamItemRow{ID: "answer", Kind: "assistant", Content: "Keep until accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceUserTurn(ctx, "s", "wrong-id", order, "Edited"); err == nil {
		t.Fatal("accepted an unknown message")
	}
	rows, err := store.LoadStreamItems(ctx, "s")
	if err != nil || len(rows) != 2 || rows[0].Content != "Original" || rows[1].ID != "answer" {
		t.Fatalf("failed edit changed history: %+v, %v", rows, err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_spotlight_delete BEFORE DELETE ON stream_items BEGIN SELECT RAISE(ABORT, 'delete rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceUserTurn(ctx, "s", "user", order, "Edited"); err == nil {
		t.Fatal("accepted a failed truncation")
	}
	rows, err = store.LoadStreamItems(ctx, "s")
	if err != nil || len(rows) != 2 || rows[0].Content != "Original" || rows[1].ID != "answer" {
		t.Fatalf("failed transaction changed history: %+v, %v", rows, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_spotlight_delete`); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceUserTurn(ctx, "s", "user", order, "Edited"); err != nil {
		t.Fatal(err)
	}
	rows, err = store.LoadStreamItems(ctx, "s")
	if err != nil || len(rows) != 1 || rows[0].Content != "Edited" {
		t.Fatalf("successful edit: %+v, %v", rows, err)
	}
}
