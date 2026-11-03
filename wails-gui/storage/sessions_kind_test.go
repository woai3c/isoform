package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func seedKindProject(t *testing.T, s *Store) {
	t.Helper()
	if err := s.EnsureProjectStructure(ctx,
		ProjectRow{ID: "__standalone_chat__", Name: "General Chat", Path: ""},
		[]WorkspaceRow{{ID: "ws-standalone", ProjectID: "__standalone_chat__", Label: "General Chat", IsDefault: true}},
		nil,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

// v54 adds sessions.kind and sessions.parent_id. Rows written before the
// migration keep "" for both, and the migration is safe to re-run.
func TestMigrateV54AddsSessionKindAndParent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kind.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("initial New: %v", err)
	}
	seedKindProject(t, s)
	if err := s.SaveSession(ctx, SessionRow{LocalID: "legacy", ProjectID: "__standalone_chat__", WorkspaceID: "ws-standalone", Label: "Transcript: Zoom"}); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 53`); err != nil {
		raw.Close()
		t.Fatalf("rewind user_version: %v", err)
	}
	raw.Close()

	reopened, err := New(dbPath)
	if err != nil {
		t.Fatalf("reopen (v54 re-run): %v", err)
	}
	defer reopened.Close()

	var version int
	if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
	legacy, err := reopened.LoadSession(ctx, "legacy")
	if err != nil {
		t.Fatalf("LoadSession legacy: %v", err)
	}
	if legacy.Kind != "" || legacy.ParentID != "" {
		t.Fatalf("legacy row kind=%q parent=%q, want both empty", legacy.Kind, legacy.ParentID)
	}
	if _, err := reopened.LoadMainSession(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LoadMainSession on a database without a main chat: err=%v, want sql.ErrNoRows", err)
	}
}

// kind and parent_id round-trip through every session read path, and a forked
// side chat becomes a plain chat again.
func TestSessionKindAndParentRoundTrip(t *testing.T) {
	s := newTestStore(t)
	seedKindProject(t, s)

	main := SessionRow{LocalID: "main-1", ProjectID: "__standalone_chat__", WorkspaceID: "ws-standalone", Label: "Isoform", Kind: SessionKindMain}
	side := SessionRow{LocalID: "side-1", ProjectID: "__standalone_chat__", WorkspaceID: "ws-standalone", Label: "Pricing page", Kind: SessionKindSide, ParentID: "main-1"}
	plain := SessionRow{LocalID: "plain-1", ProjectID: "__standalone_chat__", WorkspaceID: "ws-standalone", Label: "Handoff: thing"}
	for _, row := range []SessionRow{main, side, plain} {
		if err := s.SaveSession(ctx, row); err != nil {
			t.Fatalf("save %s: %v", row.LocalID, err)
		}
	}

	got, err := s.LoadMainSession(ctx)
	if err != nil {
		t.Fatalf("LoadMainSession: %v", err)
	}
	if got.LocalID != "main-1" || got.Kind != SessionKindMain || got.Hidden {
		t.Fatalf("main = %+v", got)
	}

	sides, err := s.LoadSessionsByParent(ctx, "main-1")
	if err != nil {
		t.Fatalf("LoadSessionsByParent: %v", err)
	}
	if len(sides) != 1 || sides[0].LocalID != "side-1" || sides[0].Kind != SessionKindSide || sides[0].ParentID != "main-1" {
		t.Fatalf("side chats = %+v", sides)
	}

	byWorkspace, err := s.LoadAllSessionsByWorkspace(ctx)
	if err != nil {
		t.Fatalf("LoadAllSessionsByWorkspace: %v", err)
	}
	kinds := map[string]string{}
	parents := map[string]string{}
	for _, row := range byWorkspace["ws-standalone"] {
		kinds[row.LocalID] = row.Kind
		parents[row.LocalID] = row.ParentID
	}
	if kinds["main-1"] != SessionKindMain || kinds["side-1"] != SessionKindSide || kinds["plain-1"] != "" || parents["side-1"] != "main-1" {
		t.Fatalf("kinds=%v parents=%v", kinds, parents)
	}

	all, err := s.LoadAllSessions(ctx)
	if err != nil {
		t.Fatalf("LoadAllSessions: %v", err)
	}
	found := false
	for _, row := range all["__standalone_chat__"] {
		if row.LocalID == "side-1" && row.Kind == SessionKindSide && row.ParentID == "main-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("LoadAllSessions lost kind/parent: %+v", all["__standalone_chat__"])
	}

	// A save that carries no kind (an older renderer snapshot) keeps the row's
	// kind and parent instead of demoting it.
	if err := s.SaveSession(ctx, SessionRow{LocalID: "side-1", ProjectID: "__standalone_chat__", WorkspaceID: "ws-standalone", Label: "Pricing page (renamed)"}); err != nil {
		t.Fatal(err)
	}
	kept, err := s.LoadSession(ctx, "side-1")
	if err != nil || kept.Kind != SessionKindSide || kept.ParentID != "main-1" || kept.Label != "Pricing page (renamed)" {
		t.Fatalf("save without kind changed the row: %+v err=%v", kept, err)
	}

	// A hidden main chat is still found, so startup can restore it instead
	// of creating a second one.
	if err := s.SoftDeleteSession(ctx, "main-1"); err != nil {
		t.Fatal(err)
	}
	hidden, err := s.LoadMainSession(ctx)
	if err != nil || hidden.LocalID != "main-1" || !hidden.Hidden {
		t.Fatalf("hidden main: %+v err=%v", hidden, err)
	}

	// The store itself refuses to delete the main chat, directly or by cascade.
	if err := s.RestoreSession(ctx, "main-1"); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"session":        s.DeleteSession(ctx, "main-1"),
		"workspace":      s.DeleteWorkspace(ctx, "ws-standalone"),
		"project":        s.DeleteProject(ctx, "__standalone_chat__"),
		"workspace+grps": s.DeleteWorkspaceWithGroupCleanup(ctx, "ws-standalone"),
		"project+grps":   s.DeleteProjectWithGroupCleanup(ctx, "__standalone_chat__"),
	} {
		if !errors.Is(err, ErrMainChatProtected) {
			t.Fatalf("delete %s containing the main chat: err = %v, want ErrMainChatProtected", name, err)
		}
	}
	if _, err := s.LoadSession(ctx, "main-1"); err != nil {
		t.Fatalf("main chat gone after refused deletes: %v", err)
	}
	if err := s.DeleteSession(ctx, "plain-1"); err != nil {
		t.Fatalf("deleting a plain chat: %v", err)
	}

	// Forking a side chat produces an ordinary chat.
	forked, _, err := s.ForkSession(ctx, "side-1", "fork-1", "")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forked.Kind != "" || forked.ParentID != "" {
		t.Fatalf("forked kind=%q parent=%q, want empty", forked.Kind, forked.ParentID)
	}

	// EnsureProjectStructure carries the kind on first insert.
	if err := s.EnsureProjectStructure(ctx,
		ProjectRow{ID: "__standalone_chat__", Name: "General Chat", Path: ""},
		nil,
		[]SessionRow{{LocalID: "side-2", ProjectID: "__standalone_chat__", WorkspaceID: "ws-standalone", Label: "Side", Kind: SessionKindSide, ParentID: "main-1"}},
	); err != nil {
		t.Fatalf("EnsureProjectStructure: %v", err)
	}
	side2, err := s.LoadSession(ctx, "side-2")
	if err != nil || side2.Kind != SessionKindSide || side2.ParentID != "main-1" {
		t.Fatalf("side-2 = %+v err=%v", side2, err)
	}

	// The phone list and detail carry kind and parent.
	mobile, err := s.MobileChatList(ctx, 10)
	if err != nil {
		t.Fatalf("MobileChatList: %v", err)
	}
	mobileKinds := map[string]string{}
	for _, row := range mobile {
		mobileKinds[row.ID] = row.Kind + "/" + row.ParentID
	}
	if mobileKinds["side-1"] != "side/main-1" || mobileKinds["fork-1"] != "/" || mobileKinds["plain-1"] != "" {
		t.Fatalf("mobile kinds = %v", mobileKinds)
	}
	detail, found, err := s.MobileChat(ctx, "side-1")
	if err != nil || !found || detail.Kind != SessionKindSide || detail.ParentID != "main-1" {
		t.Fatalf("mobile detail = %+v found=%v err=%v", detail, found, err)
	}
}
