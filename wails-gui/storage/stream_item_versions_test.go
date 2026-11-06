package storage

import (
	"testing"
)

// Every write to a session's stream items changes its version: an append, an
// in-place edit or withdrawal, and a removal. Other sessions are unaffected.
func TestStreamItemVersionChangesOnEveryItemWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if err := s.SaveProject(ctx, ProjectRow{ID: "p"}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"main", "elsewhere"} {
		if err := s.SaveSession(ctx, SessionRow{LocalID: session, ProjectID: "p", Kind: SessionKindMain}); err != nil {
			t.Fatal(err)
		}
	}
	version := func(session string) int64 {
		t.Helper()
		v, err := s.StreamItemVersion(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := version("main"); v != 0 {
		t.Fatalf("a session with no items should be version 0, got %d", v)
	}
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "a", Kind: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	afterAppend := version("main")
	if afterAppend == 0 {
		t.Fatal("an append did not change the version")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE stream_items SET content = 'edited' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	afterEdit := version("main")
	if afterEdit <= afterAppend {
		t.Fatalf("an edit did not change the version: %d -> %d", afterAppend, afterEdit)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE stream_items SET tool_input = '{"withdrawn":true}' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	afterWithdraw := version("main")
	if afterWithdraw <= afterEdit {
		t.Fatalf("a withdrawal did not change the version: %d -> %d", afterEdit, afterWithdraw)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM stream_items WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	if afterDelete := version("main"); afterDelete <= afterWithdraw {
		t.Fatalf("a removal did not change the version: %d -> %d", afterWithdraw, afterDelete)
	}
	if other := version("elsewhere"); other != 0 {
		t.Fatalf("another session's version moved: %d", other)
	}
}
