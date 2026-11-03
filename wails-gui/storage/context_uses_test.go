package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func waitContextUses(t *testing.T, s *Store, kind, id string, total int) EntryUses {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		got, err := s.EntryUses(context.Background(), kind, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total == total {
			return got
		}
		select {
		case <-timer.C:
			t.Fatalf("uses=%+v, want %d", got, total)
		case <-ticker.C:
		}
	}
}

func TestContextUseMigrationFromPreviousFixture(t *testing.T) {
	// C5 is v64: it follows #140's v63 and later migrations (People v65) build on it.
	if schemaVersion < 64 || supportedSchemaVersion != schemaVersion {
		t.Fatalf("C5 v64 must be implemented and the ceiling raised with it: schema=%d supported=%d", schemaVersion, supportedSchemaVersion)
	}
	// v63 is emitted by #140's actual migration, never a newer DB restamped.
	for _, previous := range []string{"62", "63"} {
		t.Run("v"+previous, func(t *testing.T) {
			fixture, err := os.ReadFile("testdata/schema-v" + previous + ".sql")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "sessions.db")
			old, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := old.Exec(string(fixture)); err != nil {
				t.Fatal(err)
			}
			if _, err := old.Exec(`INSERT INTO ui_state(key,value) VALUES('manual','kept')`); err != nil {
				t.Fatal(err)
			}
			if previous == "63" {
				if _, err := old.Exec(`INSERT INTO projects(id,name,path,created_at,updated_at) VALUES('p','Manual','/fixture',1,1);
			INSERT INTO sessions(local_id,project_id,label,model,created_at,updated_at) VALUES('s','p','Manual','model',1,1);
			INSERT INTO stream_items(id,session_local_id,kind,sort_order,created_at) VALUES('i','s','user',0,1);
			INSERT INTO stream_item_topics(stream_item_id,session_id,topic_id,created_at) VALUES('i','s','topic',1);`); err != nil {
					t.Fatal(err)
				}
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				s, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				var version int
				var value string
				if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
					t.Fatalf("version=%d err=%v", version, err)
				}
				if err := s.db.QueryRow(`SELECT value FROM ui_state WHERE key='manual'`).Scan(&value); err != nil || value != "kept" {
					t.Fatalf("manual=%q err=%v", value, err)
				}
				var tableCount int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='context_uses' AND type='table'`).Scan(&tableCount); err != nil || tableCount != 1 {
					t.Fatalf("missing v64 log table: %d %v", tableCount, err)
				}
				if previous == "63" {
					var topic string
					if err := s.db.QueryRow(`SELECT topic_id FROM stream_item_topics WHERE stream_item_id='i'`).Scan(&topic); err != nil || topic != "topic" {
						t.Fatalf("v63 topic lost: %q %v", topic, err)
					}
					var triggers int
					if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN ('stream_items_topic_insert','stream_items_topic_move','stream_items_topic_run_update')`).Scan(&triggers); err != nil || triggers != 3 {
						t.Fatalf("v63 triggers lost: %d %v", triggers, err)
					}
				}
				if err := s.migrateContextUses(); err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if len(migrationSnapshots(t, path)) != 1 {
				t.Fatal("upgrade did not take exactly one snapshot")
			}
		})
	}
}

func TestContextUseRound1ReusedIDAcceptsNewUses(t *testing.T) {
	for _, kind := range []string{"memory", "knowledge"} {
		t.Run(kind, func(t *testing.T) {
			s := newTestStore(t)
			epoch := s.ContextUseEpoch()
			s.RecordContextUses(kind, []string{"reused"}, "chat", "same-session")
			waitContextUses(t, s, kind, "reused", 1)
			if err := s.DeleteContextUses(t.Context(), kind, "reused"); err != nil {
				t.Fatal(err)
			}
			s.RecordContextUses(kind, []string{"reused"}, "chat", "same-session")
			waitContextUses(t, s, kind, "reused", 1)
			// A completion retained by an old send/tool must not log against
			// the recreated entry, nor erase its new receipt.
			s.RecordCapturedContextUseBatch([]ContextUse{
				{Kind: kind, ID: "reused", Surface: "assistant_tool", Ref: "stale-read", Epoch: epoch},
				{Kind: kind, ID: "control", Surface: "chat", Ref: "sentinel", Epoch: epoch},
			})
			waitContextUses(t, s, kind, "control", 1)
			got, err := s.EntryUses(t.Context(), kind, "reused")
			if err != nil || got.Total != 1 || len(got.Surfaces) != 1 || got.Surfaces[0].Surface != "chat" {
				t.Fatalf("new lifetime uses=%+v %v", got, err)
			}
		})
	}
}

func TestContextUseDedupSurfacesIdentifiersAndCounts(t *testing.T) {
	s := newTestStore(t)
	at := time.Now().UnixMilli()
	// Exact deterministic boundary: 10 minutes is a new use; ref and surface
	// are independent even within the same window.
	uses := []ContextUse{
		{"memory", "m1", "chat", "s1", at, 0}, {"memory", "m1", "chat", "s1", at + 1, 0},
		{"memory", "m1", "chat", "s1", at + int64(10*time.Minute/time.Millisecond), 0},
		{"memory", "m1", "chat", "s2", at + 2, 0}, {"memory", "m1", "today_chat", "s1", at + 3, 0},
		{"knowledge", "m1", "assistant_tool", "", at + 4, 0},
	}
	for _, use := range uses {
		if err := s.insertContextUse(context.Background(), use); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.EntryUses(context.Background(), "memory", "m1")
	if err != nil || got.Total != 4 || len(got.Surfaces) != 2 || got.LastAt != at+600000 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	counts, err := s.EntryUseCounts(context.Background(), "memory", []string{"m1", "missing", "m1"})
	if err != nil || !reflect.DeepEqual(counts, map[string]int{"m1": 4, "missing": 0}) {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	s.RecordContextUseBatch([]ContextUse{
		{Kind: "memory", ID: "captured text\nsecret", Surface: "chat", Ref: "s1"},
		{Kind: "memory", ID: "m1", Surface: "chat", Ref: "text with spaces"},
		{Kind: "unknown", ID: "m1", Surface: "chat", Ref: "s1"},
		{Kind: "memory", ID: "m1", Surface: "secret text", Ref: "s1"},
		{Kind: "memory", ID: "async", Surface: "brief", Ref: "brief-1"},
		{Kind: "memory", ID: "async", Surface: "brief", Ref: "brief-1"},
	})
	waitContextUses(t, s, "memory", "async", 1)
	var columns []string
	rows, err := s.db.Query(`PRAGMA table_info(context_uses)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var i, notNull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&i, &name, &typ, &notNull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	rows.Close()
	if !reflect.DeepEqual(columns, []string{"id", "entry_kind", "entry_id", "surface", "used_at", "ref"}) {
		t.Fatalf("unexpected payload columns: %v", columns)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM context_uses WHERE entry_kind NOT IN ('memory','knowledge') OR entry_id LIKE '% %' OR ref LIKE '% %'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("text stored: %d %v", count, err)
	}
}

func TestContextUseDeleteDiscardsQueuedRowsAndPreservesOtherKinds(t *testing.T) {
	s := newTestStore(t)
	s.RecordContextUses("knowledge", []string{"entry"}, "chat", "s1")
	waitContextUses(t, s, "knowledge", "entry", 1)
	// Hold the persistence barrier, then fill the queue. Delete must discard
	// these pending rows, whichever side of the worker wake it wins.
	s.contextUses.persistMu.Lock()
	s.RecordContextUses("memory", []string{"entry", "control"}, "chat", "s1")
	s.contextUses.persistMu.Unlock()
	if err := s.DeleteContextUses(context.Background(), "memory", "entry"); err != nil {
		t.Fatal(err)
	}
	// Drain the first batch before admitting another. The best-effort producer
	// intentionally drops a batch if the worker owns the queue lock; this
	// erasure guard must coordinate that admission rather than assume a retry.
	waitContextUses(t, s, "memory", "control", 1)
	s.RecordCapturedContextUseBatch([]ContextUse{
		{Kind: "memory", ID: "entry", Surface: "chat", Ref: "late-session"},
		{Kind: "memory", ID: "control", Surface: "chat", Ref: "late-session"},
	})
	waitContextUses(t, s, "memory", "control", 2)
	got, _ := s.EntryUses(context.Background(), "memory", "entry")
	control, _ := s.EntryUses(context.Background(), "knowledge", "entry")
	if got.Total != 0 || control.Total != 1 {
		t.Fatalf("deleted=%+v control=%+v", got, control)
	}
}

func TestContextUseBoundedQueueAndFailureNeverBlocksProducer(t *testing.T) {
	s := newTestStore(t)
	s.contextUses.persistMu.Lock()
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = strings.Repeat("x", i%200+1)
	}
	s.RecordContextUses("memory", ids, "chat", "s1")
	s.contextUses.mu.Lock()
	if len(s.contextUses.pending) > contextUseQueueLimit {
		t.Fatal("unbounded queue")
	}
	// The producer must return while the writer lock is held.
	done := make(chan struct{})
	go func() { s.RecordContextUses("memory", []string{"blocked"}, "chat", "s1"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("producer waited on log")
	}
	s.contextUses.mu.Unlock()
	s.contextUses.persistMu.Unlock()
	if _, err := s.db.Exec(`DROP TABLE context_uses`); err != nil {
		t.Fatal(err)
	}
	s.RecordContextUses("memory", []string{"failed"}, "chat", "s1")
}

func TestContextUseStartupPrunesOnlyOlderThan400Days(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prune.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id  string
		age int
	}{{"old", 401}, {"recent", 399}} {
		if err := s.insertContextUse(context.Background(), ContextUse{"memory", tc.id, "chat", "s1", time.Now().AddDate(0, 0, -tc.age).UnixMilli(), 0}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, _ := s.EntryUses(context.Background(), "memory", "old")
	recent, _ := s.EntryUses(context.Background(), "memory", "recent")
	if old.Total != 0 || recent.Total != 1 {
		t.Fatalf("prune old=%+v recent=%+v", old, recent)
	}
}

func TestContextUseWritesDoNotInvalidateSourceAdmission(t *testing.T) {
	s, _ := proactiveItemsTestStore(t)
	before, err := s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s.RecordContextUses("memory", []string{"entry"}, "chat", "previous-chat")
	waitContextUses(t, s, "memory", "entry", 1)
	after, err := s.ReadRevision(t.Context())
	if err != nil || before != after {
		t.Fatalf("use log invalidated sources: before=%+v after=%+v err=%v", before, after, err)
	}
	release, err := s.LockReadRevision(t.Context(), before)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	order, err := s.AppendUserStreamItemAtomicIfCurrent(t.Context(), "chat", StreamItemRow{ID: "sent", Kind: "user", Content: "Visible request"}, before, nil)
	if err != nil || order != 0 {
		t.Fatalf("use log failed send: order=%d err=%v", order, err)
	}
	current, err := s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUIState(t.Context(), "source-policy", "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendUserStreamItemAtomicIfCurrent(t.Context(), "chat", StreamItemRow{ID: "stale", Kind: "user"}, current, nil); err == nil {
		t.Fatal("real source write was ignored")
	}
	before, err = s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteContextUses(t.Context(), "memory", "entry"); err != nil {
		t.Fatal(err)
	}
	after, err = s.ReadRevision(t.Context())
	if err != nil || before != after {
		t.Fatalf("receipt erasure invalidated sources: %+v %+v %v", before, after, err)
	}
}

func TestContextUseRevisionNeverIgnoresTriggeredSourceWrites(t *testing.T) {
	s, _ := proactiveItemsTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER use_source_write AFTER INSERT ON context_uses BEGIN UPDATE sessions SET label='changed' WHERE local_id='chat'; END`); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s.RecordContextUses("memory", []string{"entry"}, "chat", "session")
	waitContextUses(t, s, "memory", "entry", 1)
	after, err := s.ReadRevision(t.Context())
	if err != nil || before == after {
		t.Fatalf("source trigger write ignored: %+v %+v %v", before, after, err)
	}
}

func TestContextUseBatchDoesNotCollideOnIdentifierSeparators(t *testing.T) {
	s := newTestStore(t)
	s.RecordContextUseBatch([]ContextUse{
		{Kind: "memory", ID: "m:chat", Surface: "brief", Ref: "ref"},
		{Kind: "memory", ID: "m", Surface: "chat", Ref: "brief:ref"},
	})
	waitContextUses(t, s, "memory", "m:chat", 1)
	waitContextUses(t, s, "memory", "m", 1)
}
