package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func topicTestStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SaveProject(ctx, ProjectRow{ID: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "main", ProjectID: "p", Kind: SessionKindMain}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTopicTagsAtomicRunInheritanceAndDelete(t *testing.T) {
	s := topicTestStore(t)
	ctx := t.Context()
	if _, err := s.AppendUserStreamItemAtomic(ctx, "main", StreamItemRow{ID: "user", Kind: "user", Content: "words", TopicID: "alpha"}); err != nil {
		t.Fatal(err)
	}
	// An unrelated run must never inherit the active topic.
	for _, row := range []StreamItemRow{
		{ID: "other", Kind: "assistant", RunID: "other-run"},
		{ID: "early", Kind: "assistant", RunID: "run"},
	} {
		if _, err := s.AppendStreamItem(ctx, "main", row); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.BindStreamItemTopicRun(ctx, "main", "user", "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "late", Kind: "assistant", RunID: "run", AutomationContext: "[]"}); err != nil {
		t.Fatal(err)
	}
	assertTags := func(want int) {
		t.Helper()
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_topics WHERE topic_id = 'alpha'`).Scan(&n); err != nil || n != want {
			t.Fatalf("tags=%d want=%d err=%v", n, want, err)
		}
	}
	assertTags(3)
	// Bulk saves update rows in place: REPLACE would cascade away the tags,
	// and a stale renderer's blank RunID would break future inheritance.
	if err := s.SaveStreamItems(ctx, "main", []StreamItemRow{{ID: "user", Kind: "user", Content: "words", RunID: "stale-wrong-run"}, {ID: "late", Kind: "assistant", Content: "saved", RunID: "run"}}); err != nil {
		t.Fatal(err)
	}
	assertTags(3)
	user, err := s.LoadStreamItem(ctx, "user")
	if err != nil || user == nil || user.RunID != "run" {
		t.Fatalf("bulk save lost run: %+v %v", user, err)
	}

	if _, err := s.db.Exec(`DELETE FROM stream_items WHERE id = 'early'`); err != nil {
		t.Fatal(err)
	}
	assertTags(2)
	if err := s.BindStreamItemTopicRun(ctx, "main", "user", "fallback-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "fallback", Kind: "assistant", RunID: "fallback-run"}); err != nil {
		t.Fatal(err)
	}
	assertTags(3)
	// Rejected publication rolls back the row and its topic together.
	revision, err := s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AppendUserStreamItemAtomicIfCurrent(ctx, "main", StreamItemRow{ID: "rejected", Kind: "user", TopicID: "alpha"}, revision, func() error { return context.Canceled })
	if err == nil {
		t.Fatal("rejection admitted")
	}
	assertTags(3)
	var rejected int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_items WHERE id = 'rejected'`).Scan(&rejected); err != nil || rejected != 0 {
		t.Fatalf("partial row=%d err=%v", rejected, err)
	}
}

func TestTopicMigrationFromRealV62Schema(t *testing.T) {
	schema, err := os.ReadFile("testdata/schema-v62.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,name,path,created_at,updated_at) VALUES('p','kept','',1,1);
        INSERT INTO sessions(local_id,project_id,label,model,kind,created_at,updated_at) VALUES('main','p','kept','','main',1,1);
        INSERT INTO stream_items(id,session_local_id,kind,content,sort_order,created_at) VALUES('old','main','user','kept words',0,1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		s, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		var stamp, tags int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&stamp); err != nil || stamp != schemaVersion {
			t.Fatalf("stamp=%d err=%v", stamp, err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_topics`).Scan(&tags); err != nil || tags != 0 {
			t.Fatalf("migration inferred tags=%d err=%v", tags, err)
		}
		rows, err := s.LoadStreamItems(t.Context(), "main")
		if err != nil || len(rows) != 1 || rows[0].Content != "kept words" {
			t.Fatalf("history changed: %+v %v", rows, err)
		}
		if _, err := s.AppendUserStreamItemAtomic(t.Context(), "main", StreamItemRow{ID: "new", Kind: "user", TopicID: "topic"}); i == 0 && err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`DELETE FROM stream_items WHERE id = 'new'`); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestTopicRunProofSurvivesUserDeletionAndBulkSave(t *testing.T) {
	s := topicTestStore(t)
	ctx := t.Context()
	const proof = `{"topicContext":{"version":1,"refs":[{"sourceKind":"memory","sourceId":"source"}],"fingerprint":"source-hash"}}`
	if _, err := s.AppendUserStreamItemAtomic(ctx, "main", StreamItemRow{ID: "user", Kind: "user", TopicID: "topic", AutomationContext: proof}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindStreamItemTopicRun(ctx, "main", "user", "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "assistant", Kind: "assistant", RunID: "run"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveStreamItems(ctx, "main", []StreamItemRow{{ID: "assistant", Kind: "assistant", RunID: "run", Content: "saved"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM stream_items WHERE id='user'`); err != nil {
		t.Fatal(err)
	}
	reply, err := s.LoadStreamItem(ctx, "assistant")
	if err != nil || reply == nil || reply.AutomationContext != proof {
		t.Fatalf("orphaned response lost proof: %+v %v", reply, err)
	}
	// Late rows still inherit the proof and tag from the surviving response.
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "late", Kind: "assistant", RunID: "run", AutomationContext: "[]"}); err != nil {
		t.Fatal(err)
	}
	late, err := s.LoadStreamItem(ctx, "late")
	if err != nil || late.AutomationContext != proof {
		t.Fatalf("late proof: %+v %v", late, err)
	}
}

func TestTopicMigrationRepairsStampedV63TriggersAndProofs(t *testing.T) {
	s := topicTestStore(t)
	const proof = `{"topicContext":{"version":1,"refs":[{"sourceKind":"memory","sourceId":"source"}],"fingerprint":"source-hash"}}`
	ctx := t.Context()
	if _, err := s.AppendUserStreamItemAtomic(ctx, "main", StreamItemRow{ID: "user", Kind: "user", TopicID: "topic", AutomationContext: proof}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindStreamItemTopicRun(ctx, "main", "user", "run"); err != nil {
		t.Fatal(err)
	}
	// Model an earlier stamped v63: tags existed, but these trigger bodies did
	// not propagate the original topicContext to response rows.
	if _, err := s.db.Exec(`DROP TRIGGER stream_items_topic_insert; DROP TRIGGER stream_items_topic_run_update;
        CREATE TRIGGER stream_items_topic_insert AFTER INSERT ON stream_items BEGIN SELECT 1; END;
        CREATE TRIGGER stream_items_topic_run_update AFTER UPDATE OF run_id ON stream_items BEGIN SELECT 1; END;`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []StreamItemRow{{ID: "assistant", Kind: "assistant", RunID: "run"}, {ID: "orphan", Kind: "assistant", RunID: "lost-run"}} {
		if _, err := s.AppendStreamItem(ctx, "main", row); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO stream_item_topics SELECT id,session_local_id,'topic',created_at FROM stream_items WHERE id=?`, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopened, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := reopened.LoadStreamItem(ctx, "assistant")
		if err != nil || reply.AutomationContext != proof {
			t.Fatalf("stamped upgrade proof: %+v %v", reply, err)
		}
		orphan, err := reopened.LoadStreamItem(ctx, "orphan")
		if err != nil || orphan.AutomationContext != `{"topicContext":{"version":0}}` {
			t.Fatalf("unproven history not denied: %+v %v", orphan, err)
		}
		if i == 0 {
			if _, err := reopened.db.Exec(`DELETE FROM stream_items WHERE id='user'; DROP TRIGGER stream_items_topic_insert;`); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := reopened.AppendStreamItem(ctx, "main", StreamItemRow{ID: "late", Kind: "assistant", RunID: "run"}); err != nil {
				t.Fatal(err)
			}
			late, err := reopened.LoadStreamItem(ctx, "late")
			if err != nil || late.AutomationContext != proof {
				t.Fatalf("repaired insert: %+v %v", late, err)
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTopicTagsRemovedByBulkDeletes(t *testing.T) {
	for _, operation := range []string{"items", "rewind", "session", "workspace", "project"} {
		t.Run(operation, func(t *testing.T) {
			s := topicTestStore(t)
			ctx := t.Context()
			if err := s.SaveWorkspace(ctx, WorkspaceRow{ID: "w", ProjectID: "p"}); err != nil {
				t.Fatal(err)
			}
			// Bulk parent deletion correctly protects the main session. Seed a normal
			// session's tags directly to exercise the cascade independently of admission.
			if err := s.SaveSession(ctx, SessionRow{LocalID: "bulk", ProjectID: "p", WorkspaceID: "w"}); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveProject(ctx, ProjectRow{ID: "other", Path: "/fixture/other"}); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveSession(ctx, SessionRow{LocalID: "unrelated", ProjectID: "other"}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"bulk", "unrelated"} {
				if _, err := s.AppendStreamItem(ctx, id, StreamItemRow{ID: id + "-row", Kind: "user", Content: "kept words"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, `INSERT INTO stream_item_topics(stream_item_id,session_id,topic_id,created_at) VALUES(?,?,'atlas',1)`, id+"-row", id); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "project" {
				// Keep the main-chat guard intact while making this fixture's parent
				// eligible for deletion; production never deletes the main project.
				if err := s.DeleteSession(ctx, "bulk"); err != nil {
					t.Fatal(err)
				}
				if err := s.SaveProject(ctx, ProjectRow{ID: "deletable", Path: "/fixture/deletable"}); err != nil {
					t.Fatal(err)
				}
				if err := s.SaveSession(ctx, SessionRow{LocalID: "bulk", ProjectID: "deletable"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.AppendStreamItem(ctx, "bulk", StreamItemRow{ID: "bulk-row", Kind: "user"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, `INSERT INTO stream_item_topics VALUES('bulk-row','bulk','atlas',1)`); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch operation {
			case "items":
				err = s.DeleteStreamItems(ctx, "bulk")
			case "rewind":
				err = s.DeleteStreamItemsFrom(ctx, "bulk", 0)
			case "session":
				err = s.DeleteSession(ctx, "bulk")
			case "workspace":
				err = s.DeleteWorkspace(ctx, "w")
			case "project":
				err = s.DeleteProject(ctx, "deletable")
			}
			if err != nil {
				t.Fatal(err)
			}
			var deleted, kept int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_item_topics WHERE session_id='bulk'`).Scan(&deleted); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_item_topics WHERE session_id='unrelated'`).Scan(&kept); err != nil {
				t.Fatal(err)
			}
			if deleted != 0 || kept != 1 {
				t.Fatalf("deleted tags=%d unrelated tags=%d", deleted, kept)
			}
		})
	}
}

func TestTopicDerivedTagsInsertIdempotencyStatusGuardAndCascade(t *testing.T) {
	s := topicTestStore(t)
	ctx := t.Context()
	for _, row := range []StreamItemRow{{ID: "card", Kind: "system", ToolName: "main_chat_proactive"}, {ID: "status", Kind: "system", ToolName: "main_chat_status"}} {
		if _, err := s.AppendStreamItem(ctx, "main", row); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.InsertStreamItemTopics(ctx, "card", "main", []string{"atlas", "atlas"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertStreamItemTopics(ctx, "status", "main", []string{"atlas"}); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.LoadMainChatRowMetadata(ctx, "main", []string{"card", "status"})
	if err != nil || len(metadata["card"].TopicIDs) != 1 || len(metadata["status"].TopicIDs) != 0 {
		t.Fatalf("metadata %+v %v", metadata, err)
	}
	if _, err = s.db.Exec(`DELETE FROM stream_items WHERE id='card'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_topics`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d %v", count, err)
	}
}
func TestTopicDerivedTagRevisionAndValidationRollback(t *testing.T) {
	s := topicTestStore(t)
	ctx := t.Context()
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "card", Kind: "system", ToolName: "main_chat_proactive"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetUIState(ctx, "privacy", "changed"); err != nil {
		t.Fatal(err)
	}
	if err = s.InsertStreamItemTopicsIfCurrent(ctx, "card", "main", []string{"atlas"}, before, nil); err != ErrReadRevisionChanged {
		t.Fatalf("stale write %v", err)
	}
	current, err := s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reject := errors.New("source withdrawn")
	if err = s.InsertStreamItemTopicsIfCurrent(ctx, "card", "main", []string{"atlas"}, current, func() error { return reject }); err != reject {
		t.Fatalf("validation %v", err)
	}
	metadata, err := s.LoadMainChatRowMetadata(ctx, "main", []string{"card"})
	if err != nil || len(metadata["card"].TopicIDs) != 0 {
		t.Fatalf("rollback %+v %v", metadata, err)
	}
}

func TestTopicDerivedBackfillBoundsAndRewindCascade(t *testing.T) {
	s := topicTestStore(t)
	ctx := t.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 502; i++ {
		if _, err = tx.ExecContext(ctx, `INSERT INTO stream_items(id,session_local_id,kind,tool_name,sort_order,created_at) VALUES(?,'main','system','main_chat_report',?,?)`, fmt.Sprintf("report-%03d", i), i, i+1); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.LoadMainChatTopicBackfillRows(ctx, 1)
	if err != nil || len(rows) != 500 || rows[0].ID != "report-501" || rows[499].ID != "report-002" {
		t.Fatalf("bounded rows %d %v", len(rows), err)
	}
	if err = s.InsertStreamItemTopics(ctx, "report-501", "main", []string{"atlas"}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteStreamItemsFrom(ctx, "main", 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_topics`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rewind cascade %d %v", n, err)
	}
}

func TestTopicBackfillCompletionCommitsWithMembership(t *testing.T) {
	s := topicTestStore(t)
	ctx := t.Context()
	if _, err := s.AppendStreamItem(ctx, "main", StreamItemRow{ID: "card", Kind: "system", ToolName: "main_chat_proactive"}); err != nil {
		t.Fatal(err)
	}
	batch := []StreamItemTopicInsert{{ItemID: "card", SessionID: "main", TopicIDs: []string{"atlas"}}}
	const key = "backfill-complete"
	check := func(want int, marker string) {
		t.Helper()
		metadata, err := s.LoadMainChatRowMetadata(ctx, "main", []string{"card"})
		if err != nil || len(metadata["card"].TopicIDs) != want {
			t.Fatalf("membership %+v err=%v", metadata, err)
		}
		if got, err := s.GetUIState(ctx, key); err != nil || got != marker {
			t.Fatalf("completion=%q want=%q err=%v", got, marker, err)
		}
	}
	revision, err := s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reject := errors.New("source withdrawn")
	if err := s.CompleteStreamItemTopicBackfillIfCurrent(ctx, batch, revision, key, func() error { return reject }); !errors.Is(err, reject) {
		t.Fatalf("source withdrawal: %v", err)
	}
	check(0, "")
	// A real SQLite marker failure must roll back the membership inserts too.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_backfill_marker BEFORE INSERT ON ui_state WHEN NEW.key='backfill-complete' BEGIN SELECT RAISE(ABORT,'marker unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	revision, err = s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteStreamItemTopicBackfillIfCurrent(ctx, batch, revision, key, nil); err == nil {
		t.Fatal("marker failure accepted")
	}
	check(0, "")
	if _, err := s.db.Exec(`DROP TRIGGER reject_backfill_marker`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteStreamItemTopicBackfillIfCurrent(ctx, batch, revision, key, nil); !errors.Is(err, ErrReadRevisionChanged) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	check(0, "")
	revision, err = s.ReadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteStreamItemTopicBackfillIfCurrent(ctx, batch, revision, key, nil); err != nil {
		t.Fatal(err)
	}
	check(1, "true")
}
