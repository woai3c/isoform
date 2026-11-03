package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func proactiveItemsTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "items.db")
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project := ProjectRow{ID: "p", Name: "Project", Path: filepath.Join(t.TempDir(), "repo")}
	if err := store.EnsureProjectStructure(t.Context(), project, []WorkspaceRow{{ID: "w", ProjectID: "p", Label: "Main", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"chat", "other"} {
		if err := store.SaveSession(t.Context(), SessionRow{LocalID: id, ProjectID: "p", WorkspaceID: "w", Label: id}); err != nil {
			t.Fatal(err)
		}
	}
	return store, path
}

func appendProactiveTestItem(t *testing.T, store *Store, session string, item StreamItemRow) int {
	t.Helper()
	order, err := store.AppendStreamItem(t.Context(), session, item)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func TestProactiveStreamLookupsBoundRowsAndSkipUnrelatedToolOutput(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "root-row", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"old-root"}`})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "digest-row", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"digest","items":[null,"not-json",{"momentId":"old-child"}]}`})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "broken-row", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{`})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "broken-items", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":{"row":{"momentId":"not-a-child"}}}`})
	// A large tool result can even carry matching-looking JSON. Neither its
	// content nor its metadata belongs in the bounded proactive read.
	large := strings.Repeat("large tool output ", 64*1024)
	for _, id := range []string{"large-a", "large-b", "large-c"} {
		appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: id, Kind: "result", ToolName: "Read", Content: large, ToolInput: `{"momentId":"unrelated","items":[{"momentId":"unrelated-child"}]}`})
	}
	for _, id := range []string{"recent-a", "recent-b"} {
		payload, _ := json.Marshal(map[string]string{"momentId": id})
		appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: id, Kind: "system", ToolName: "main_chat_proactive", ToolInput: string(payload)})
	}
	appendProactiveTestItem(t, store, "other", StreamItemRow{ID: "other-root", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"old-root"}`})
	for momentID, rowID := range map[string]string{"old-root": "root-row", "digest": "digest-row", "old-child": "digest-row", "recent-a": "recent-a", "missing": "", "unrelated": "", "unrelated-child": "", "not-a-child": "", "": ""} {
		row, err := store.FindProactiveStreamItem(t.Context(), "chat", momentID)
		if err != nil {
			t.Fatalf("lookup %q: %v", momentID, err)
		}
		if rowID == "" {
			if row != nil {
				t.Fatalf("unrelated row matched %q: %+v", momentID, row)
			}
		} else if row == nil || row.ID != rowID || row.SessionLocalID != "chat" {
			t.Fatalf("lookup %q: %+v, want %s", momentID, row, rowID)
		}
	}
	// Both lookups must find identities directly, without parsing old cards.
	for _, tc := range []struct{ query, index string }{
		{findProactiveRootSQL, "idx_stream_items_proactive_moment"},
		{findProactiveChildSQL, "idx_proactive_children_identity"},
	} {
		plan, err := store.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+tc.query, "chat", "old-child")
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for plan.Next() {
			var id, parent, unused int
			var detail string
			if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := plan.Err(); err != nil {
			t.Fatal(err)
		}
		_ = plan.Close()
		if !strings.Contains(strings.Join(details, "\n"), "USING INDEX "+tc.index) {
			t.Fatalf("lookup did not use its bounded index %s: %v", tc.index, details)
		}
	}
}

func TestProactiveChildLookupSearchesIdentityInsteadOfLifetimeCards(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	items := make([]StreamItemRow, 2048)
	for i := range items {
		items[i] = StreamItemRow{
			ID: fmt.Sprintf("digest-%d", i), Kind: "system", ToolName: "main_chat_proactive",
			ToolInput: fmt.Sprintf(`{"momentId":"root-%d","items":[{"momentId":"child-%d"}]}`, i, i),
		}
	}
	if err := store.SaveStreamItems(t.Context(), "chat", items); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"child-0", "child-2047", "brand-new-moment"} {
		row, err := store.FindProactiveStreamItem(t.Context(), "chat", id)
		if err != nil {
			t.Fatal(err)
		}
		if id == "brand-new-moment" {
			if row != nil {
				t.Fatalf("new moment matched %+v", row)
			}
		} else if row == nil || row.ID != strings.Replace(id, "child-", "digest-", 1) {
			t.Fatalf("child %s: %+v", id, row)
		}
	}
	plan, err := store.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+findProactiveChildSQL, "chat", "brand-new-moment")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	var details []string
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "(session_local_id=? AND moment_id=?)") || strings.Contains(joined, "SCAN ") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("new-moment miss must use an exact identity search, not scan lifetime cards: %s", joined)
	}
}

func TestProactiveChildIdentitiesFollowEditsReplacementAndDeletion(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	assertLookup := func(session, moment, want string) {
		t.Helper()
		row, err := store.FindProactiveStreamItem(t.Context(), session, moment)
		if err != nil {
			t.Fatal(err)
		}
		if (want == "" && row != nil) || (want != "" && (row == nil || row.ID != want)) {
			t.Fatalf("lookup %s/%s = %+v, want %s", session, moment, row, want)
		}
	}
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "digest", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"root","items":[{"momentId":"old"}]}`})
	updated := `{"momentId":"root","items":[null,"not-json",2,{}, {"momentId":5},{"momentId":""},{"momentId":"new"},{"momentId":"new"}]}`
	if err := store.UpdateStreamItemToolInput(t.Context(), "digest", updated); err != nil {
		t.Fatal(err)
	}
	assertLookup("chat", "old", "")
	assertLookup("chat", "new", "digest")
	assertLookup("chat", "5", "")
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM stream_item_proactive_children WHERE stream_item_id = 'digest'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid/duplicate child identities were indexed: count=%d, err=%v", count, err)
	}
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "later", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"later-root","items":[{"momentId":"new"}]}`})
	if _, err := store.MoveStreamItemPresentationToEnd(t.Context(), "digest", "", updated, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertLookup("chat", "new", "later")
	if _, err := store.db.Exec(`UPDATE stream_items SET session_local_id = 'other' WHERE id = 'later'`); err != nil {
		t.Fatal(err)
	}
	assertLookup("chat", "new", "digest")
	assertLookup("other", "new", "later")
	if _, err := store.db.Exec(`UPDATE stream_items SET tool_name = 'Read' WHERE id = 'later'`); err != nil {
		t.Fatal(err)
	}
	assertLookup("other", "new", "")
	if err := store.SaveStreamItems(t.Context(), "chat", []StreamItemRow{{ID: "digest", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":[{"momentId":"replacement"}]}`}}); err != nil {
		t.Fatal(err)
	}
	assertLookup("chat", "new", "")
	assertLookup("chat", "replacement", "digest")
	if err := store.SaveStreamItems(t.Context(), "chat", []StreamItemRow{{ID: "digest", Kind: "assistant", Content: "Replaced"}}); err != nil {
		t.Fatal(err)
	}
	assertLookup("chat", "replacement", "")
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "deleted", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":[{"momentId":"deleted-child"}]}`})
	if err := store.DeleteSession(t.Context(), "chat"); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM stream_item_proactive_children`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted/replaced rows left child identities: count=%d, err=%v", count, err)
	}
}

func TestProactiveChildIdentityFailureRollsBackParentWrites(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	before := `{"momentId":"root","items":[{"momentId":"original"}]}`
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "digest", Kind: "system", ToolName: "main_chat_proactive", ToolInput: before})
	if _, err := store.db.Exec(`CREATE TRIGGER refuse_proactive_child BEFORE INSERT ON stream_item_proactive_children WHEN NEW.moment_id = 'refused' BEGIN SELECT RAISE(ABORT, 'identity write refused'); END`); err != nil {
		t.Fatal(err)
	}
	refused := StreamItemRow{ID: "refused-row", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":[{"momentId":"refused"}]}`}
	if err := store.UpdateStreamItemToolInput(t.Context(), "digest", refused.ToolInput); err == nil {
		t.Fatal("parent update succeeded despite failed identity update")
	}
	if _, err := store.AppendStreamItem(t.Context(), "chat", refused); err == nil {
		t.Fatal("parent insert succeeded despite failed identity insert")
	}
	if err := store.SaveStreamItems(t.Context(), "chat", []StreamItemRow{{ID: "batch-first", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":[{"momentId":"batch-child"}]}`}, refused}); err == nil {
		t.Fatal("batch succeeded despite failed identity insert")
	}
	rows, err := store.LoadStreamItems(t.Context(), "chat")
	if err != nil || len(rows) != 1 || rows[0].ToolInput != before {
		t.Fatalf("failed identity update left partial parent writes: %+v, %v", rows, err)
	}
	row, err := store.FindProactiveStreamItem(t.Context(), "chat", "original")
	if err != nil || row == nil || row.ID != "digest" {
		t.Fatalf("failed parent update lost original identity: %+v, %v", row, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM stream_item_proactive_children`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed writes left child identities: count=%d, err=%v", count, err)
	}
}

func TestStatusEntryLookupTracksMovesAndRollsBack(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "status", Kind: "system", ToolName: "main_chat_status", ToolInput: `{"entries":[{"id":"first"}]}`})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "reply", Kind: "assistant"})
	appendProactiveTestItem(t, store, "other", StreamItemRow{ID: "other-status", Kind: "system", ToolName: "main_chat_status", ToolInput: `{"entries":[{"id":"first"}]}`})
	if _, err := store.MoveStreamItemPresentationToEnd(t.Context(), "status", "Two updates", `{"entries":[null,{"id":1},{"id":"first"},{"id":"second"},{"id":"second"}]}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		row, err := store.FindStatusStreamItem(t.Context(), "chat", id)
		if err != nil || row == nil || row.ID != "status" || row.SortOrder != 2 {
			t.Fatalf("moved entry %s lookup = %+v, %v", id, row, err)
		}
	}
	plan, err := store.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+findStatusStreamItemSQL, "chat", "missing")
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	_ = plan.Close()
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "(session_local_id=? AND entry_id=?)") || strings.Contains(joined, "SCAN ") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("status lookup scans historical rows: %s", joined)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER refuse_status_identity_move BEFORE UPDATE OF updated_at ON sessions BEGIN SELECT RAISE(ABORT, 'session write refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveStreamItemPresentationToEnd(t.Context(), "status", "Failed update", `{"entries":[{"id":"uncommitted"}]}`, time.Now()); err == nil {
		t.Fatal("failed status transaction unexpectedly succeeded")
	}
	for _, id := range []string{"", "missing", "uncommitted", "1"} {
		if row, err := store.FindStatusStreamItem(t.Context(), "chat", id); err != nil || row != nil {
			t.Fatalf("missing/invalid/rolled-back entry %q matched: %+v, %v", id, row, err)
		}
	}
	if row, err := store.FindStatusStreamItem(t.Context(), "chat", "first"); err != nil || row == nil || row.SortOrder != 2 {
		t.Fatalf("failed move changed the original identity: %+v, %v", row, err)
	}
	if err := store.SaveStreamItems(t.Context(), "chat", []StreamItemRow{{ID: "status", Kind: "assistant"}}); err != nil {
		t.Fatal(err)
	}
	if row, err := store.FindStatusStreamItem(t.Context(), "chat", "first"); err != nil || row != nil {
		t.Fatalf("replaced status retained identity: %+v, %v", row, err)
	}
	if err := store.DeleteStreamItems(t.Context(), "other"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM stream_item_status_entries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("replaced/deleted status left identity entries: count=%d, err=%v", count, err)
	}
}

func TestMoveStreamItemPresentationUpdatesOrderingAndCursorAtomically(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	before := time.Now().Add(-time.Hour)
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "status", Kind: "system", ToolName: "main_chat_status", Content: "First update", ToolInput: `{"entries":[1]}`, CreatedAt: before.UnixMilli()})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "reply", Kind: "assistant", Content: "A later reply"})
	appendProactiveTestItem(t, store, "other", StreamItemRow{ID: "unrelated-status", Kind: "system", Content: "Other chat"})
	at := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	updated, err := store.MoveStreamItemPresentationToEnd(t.Context(), "status", "Two updates", `{"entries":[1,2]}`, at)
	if err != nil || updated == nil {
		t.Fatalf("move: %+v, %v", updated, err)
	}
	if updated.ID != "status" || updated.SortOrder != 2 || updated.CreatedAt != at.UnixMilli() || updated.Content != "Two updates" || updated.ToolInput != `{"entries":[1,2]}` {
		t.Fatalf("updated row: %+v", updated)
	}
	page, err := store.LoadStreamItemsAfter(t.Context(), "chat", 1, 50)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "status" || page.HasMore {
		t.Fatalf("old cursor missed status: %+v, %v", page, err)
	}
	all, err := store.LoadStreamItems(t.Context(), "chat")
	if err != nil || len(all) != 2 || all[0].ID != "reply" || all[1].ID != "status" {
		t.Fatalf("row was duplicated or did not move last: %+v, %v", all, err)
	}
	session, err := store.LoadSession(t.Context(), "chat")
	if err != nil || session == nil || session.UpdatedAt != at.UnixMilli() {
		t.Fatalf("session timestamp: %+v, %v", session, err)
	}
	if row, err := store.MoveStreamItemPresentationToEnd(t.Context(), "missing", "", "", at); err != nil || row != nil {
		t.Fatalf("missing row: %+v, %v", row, err)
	}
}

func TestMoveStreamItemPresentationRollsBackWhenSessionUpdateFails(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "status", Kind: "system", Content: "Before", ToolInput: `{"entries":[1]}`, CreatedAt: 123})
	if _, err := store.db.Exec(`CREATE TRIGGER refuse_status_session_update BEFORE UPDATE OF updated_at ON sessions BEGIN SELECT RAISE(ABORT, 'session write refused'); END`); err != nil {
		t.Fatal(err)
	}
	if row, err := store.MoveStreamItemPresentationToEnd(t.Context(), "status", "After", `{"entries":[1,2]}`, time.Now()); err == nil || row != nil {
		t.Fatalf("failed session update reported success: %+v, %v", row, err)
	}
	row, err := store.LoadStreamItem(t.Context(), "status")
	if err != nil || row == nil || row.SortOrder != 0 || row.Content != "Before" || row.ToolInput != `{"entries":[1]}` || row.CreatedAt != 123 {
		t.Fatalf("failed transaction left a partial status update: %+v, %v", row, err)
	}
}

func TestProactiveLookupIndexesUpgradeExistingRows(t *testing.T) {
	store, path := proactiveItemsTestStore(t)
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "legacy-card", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"legacy","items":[null,"not-json",{"momentId":1},{"momentId":"legacy-child"},{"momentId":"legacy-child"}]}`})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "legacy-malformed", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{`})
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "legacy-status", Kind: "system", ToolName: "main_chat_status", ToolInput: `{"entries":[null,"not-json",{"id":2},{"id":"legacy-entry"},{"id":"legacy-entry"}]}`})
	if _, err := store.db.Exec(`
		DROP TRIGGER IF EXISTS stream_items_proactive_children_insert;
		DROP TRIGGER IF EXISTS stream_items_proactive_children_update;
		DROP TABLE IF EXISTS stream_item_proactive_children;
		DROP TRIGGER IF EXISTS stream_items_status_entries_insert;
		DROP TRIGGER IF EXISTS stream_items_status_entries_update;
		DROP TABLE IF EXISTS stream_item_status_entries;
		DROP INDEX idx_stream_items_proactive_moment;
		DROP INDEX idx_stream_items_session_tool_order;
		PRAGMA user_version = 57;
	`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for _, id := range []string{"legacy", "legacy-child"} {
		row, err := reopened.FindProactiveStreamItem(t.Context(), "chat", id)
		if err != nil || row == nil || row.ID != "legacy-card" {
			t.Fatalf("migrated lookup %s: %+v, %v", id, row, err)
		}
	}
	var count int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM stream_item_proactive_children`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration must backfill valid child identities once: count=%d, err=%v", count, err)
	}
	if row, err := reopened.FindStatusStreamItem(t.Context(), "chat", "legacy-entry"); err != nil || row == nil || row.ID != "legacy-status" {
		t.Fatalf("migration lost status identity: %+v, %v", row, err)
	}
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM stream_item_status_entries`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration must backfill valid status identities once: count=%d, err=%v", count, err)
	}
}

const dropProactiveStructuresForTest = `
	DROP TRIGGER IF EXISTS stream_items_proactive_children_insert;
	DROP TRIGGER IF EXISTS stream_items_proactive_children_update;
	DROP TABLE IF EXISTS stream_item_proactive_children;
	DROP TRIGGER IF EXISTS stream_items_status_entries_insert;
	DROP TRIGGER IF EXISTS stream_items_status_entries_update;
	DROP TABLE IF EXISTS stream_item_status_entries;
	DROP INDEX IF EXISTS idx_stream_items_proactive_moment;
	DROP INDEX IF EXISTS idx_stream_items_session_tool_order;
`

func TestProactiveStructuresRepairAcrossSchemaVersions(t *testing.T) {
	for _, version := range []int{58, 59, 60} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			store, path := proactiveItemsTestStore(t)
			appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "old", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"old-root","items":[{"momentId":"old-child"}]}`})
			appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "status", Kind: "system", ToolName: "main_chat_status", ToolInput: `{"entries":[{"id":"old-status"}]}`})
			if _, err := store.db.Exec(dropProactiveStructuresForTest + fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			for _, identity := range []string{"old-root", "old-child"} {
				row, err := reopened.FindProactiveStreamItem(t.Context(), "chat", identity)
				if err != nil || row == nil || row.ID != "old" {
					t.Fatalf("repaired identity %s = %+v, %v", identity, row, err)
				}
			}
			if row, err := reopened.FindStatusStreamItem(t.Context(), "chat", "old-status"); err != nil || row == nil || row.ID != "status" {
				t.Fatalf("repaired status = %+v, %v", row, err)
			}
			appendProactiveTestItem(t, reopened, "chat", StreamItemRow{ID: "new", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"momentId":"new-root","items":[{"momentId":"new-child"}]}`})
			if row, err := reopened.FindProactiveStreamItem(t.Context(), "chat", "new-child"); err != nil || row == nil || row.ID != "new" {
				t.Fatalf("repaired insert trigger = %+v, %v", row, err)
			}
			if err := reopened.UpdateStreamItemToolInput(t.Context(), "new", `{"momentId":"new-root","items":[{"momentId":"changed-child"}]}`); err != nil {
				t.Fatal(err)
			}
			if row, err := reopened.FindProactiveStreamItem(t.Context(), "chat", "changed-child"); err != nil || row == nil || row.ID != "new" {
				t.Fatalf("repaired update trigger = %+v, %v", row, err)
			}
			if err := reopened.UpdateStreamItemToolInput(t.Context(), "status", `{"entries":[{"id":"changed-status"}]}`); err != nil {
				t.Fatal(err)
			}
			if row, err := reopened.FindStatusStreamItem(t.Context(), "chat", "changed-status"); err != nil || row == nil || row.ID != "status" {
				t.Fatalf("repaired status trigger = %+v, %v", row, err)
			}
			var stored int
			want := max(version, schemaVersion)
			if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&stored); err != nil || stored != want {
				t.Fatalf("stored version = %d, %v; want %d", stored, err, want)
			}
		})
	}
}

func TestProactiveStructureReopenSkipsLifetimeBackfill(t *testing.T) {
	store, path := proactiveItemsTestStore(t)
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "old", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":[{"momentId":"old-child"}]}`})
	// Rebuilding these already-complete indexes would walk every stored card.
	if _, err := store.db.Exec(`CREATE TRIGGER reject_identity_rebuild BEFORE DELETE ON stream_item_proactive_children BEGIN SELECT RAISE(ABORT, 'unexpected lifetime backfill'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if row, err := reopened.FindProactiveStreamItem(t.Context(), "chat", "old-child"); err != nil || row == nil || row.ID != "old" {
		t.Fatalf("existing identity = %+v, %v", row, err)
	}
}

func TestProactiveStructureRepairBackfillsAfterMissingTrigger(t *testing.T) {
	store, path := proactiveItemsTestStore(t)
	if _, err := store.db.Exec(`DROP TRIGGER stream_items_proactive_children_insert`); err != nil {
		t.Fatal(err)
	}
	appendProactiveTestItem(t, store, "chat", StreamItemRow{ID: "gap", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"items":[{"momentId":"gap-child"}]}`})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if row, err := reopened.FindProactiveStreamItem(t.Context(), "chat", "gap-child"); err != nil || row == nil || row.ID != "gap" {
		t.Fatalf("gap identity was not repaired = %+v, %v", row, err)
	}
}
