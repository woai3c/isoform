package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChipMigrationFromV65AndOlderCeiling(t *testing.T) {
	fixture, err := os.ReadFile("testdata/schema-v65.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,name,path,created_at,updated_at) VALUES('p','Manual','',1,1);
        INSERT INTO sessions(local_id,project_id,label,model,kind,created_at,updated_at) VALUES('main','p','Manual','','main',1,1);
        INSERT INTO stream_items(id,session_local_id,kind,content,sort_order,created_at) VALUES('kept','main','assistant','Kept answer',0,1);
        INSERT INTO stream_item_topics(stream_item_id,session_id,topic_id,created_at) VALUES('kept','main','topic',1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		s, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		row, err := s.LoadStreamItem(t.Context(), "kept")
		if err != nil || row == nil || row.Content != "Kept answer" {
			t.Fatalf("history=%+v err=%v", row, err)
		}
		var topics, chips, version int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_topics WHERE topic_id='topic'`).Scan(&topics); err != nil || topics != 1 {
			t.Fatalf("topics=%d err=%v", topics, err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_chips`).Scan(&chips); err != nil || chips != 0 {
			t.Fatalf("chips=%d err=%v", chips, err)
		}
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion || supportedSchemaVersion != schemaVersion {
			t.Fatalf("version=%d ceiling=%d err=%v", version, supportedSchemaVersion, err)
		}
		var refusal *SchemaVersionError
		if _, err := checkSchemaVersionCeiling(s.db, 65); !errors.As(err, &refusal) || refusal.StoredVersion != schemaVersion || refusal.SupportedVersion != 65 {
			t.Fatalf("older ceiling refusal=%+v err=%v", refusal, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func chipAnswer(t *testing.T, s *Store, id string) StreamItemRow {
	t.Helper()
	row := StreamItemRow{ID: id, Kind: "assistant", Content: "Want to open the plan?", RunID: "run"}
	if _, err := s.AppendStreamItem(t.Context(), "main", row); err != nil {
		t.Fatal(err)
	}
	row.SessionLocalID = "main"
	return row
}

func TestChipsSingleFlightHourlyBudgetSurvivesDeletesAndReopen(t *testing.T) {
	s := topicTestStore(t)
	now := time.Now().UnixMilli()
	for i := range 61 {
		row := chipAnswer(t, s, fmt.Sprint(i))
		ok, count, err := s.ReserveAnswerChips(t.Context(), row, "en", now)
		if err != nil || ok != (i < 60) || count != min(i, 60) {
			t.Fatalf("attempt %d: ok=%t count=%d err=%v", i, ok, count, err)
		}
		if i == 0 {
			if again, _, err := s.ReserveAnswerChips(t.Context(), row, "en", now); again || err != nil {
				t.Fatalf("duplicate reservation=%t %v", again, err)
			}
		}
		if err := s.DeleteStreamItems(t.Context(), "main"); err != nil {
			t.Fatal(err)
		}
	}
	// Reopen the actual same database; the budget has no row identifiers.
	var path string
	var seq int
	var name string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row := chipAnswer(t, reopened, "after-reopen")
	if ok, count, err := reopened.ReserveAnswerChips(t.Context(), row, "en", now); err != nil || ok || count != 60 {
		t.Fatalf("restart cap=%t %d %v", ok, count, err)
	}
	if ok, count, err := reopened.ReserveAnswerChips(t.Context(), row, "en", now+time.Hour.Milliseconds()); err != nil || !ok || count != 0 {
		t.Fatalf("next hour=%t %d %v", ok, count, err)
	}
}

func TestChipsCascadeRewindMoveAndLatePublication(t *testing.T) {
	for _, action := range []string{"delete", "rewind", "move", "session", "edit"} {
		t.Run(action, func(t *testing.T) {
			s := topicTestStore(t)
			row := chipAnswer(t, s, "answer")
			if ok, _, err := s.ReserveAnswerChips(t.Context(), row, "en", time.Now().UnixMilli()); !ok || err != nil {
				t.Fatalf("reserve=%t %v", ok, err)
			}
			if ok, err := s.CompleteAnswerChips(t.Context(), row, []string{"Open the plan"}); !ok || err != nil {
				t.Fatalf("complete=%t %v", ok, err)
			}
			var err error
			switch action {
			case "delete":
				err = s.DeleteStreamItems(t.Context(), "main")
			case "rewind":
				err = s.DeleteStreamItemsFrom(t.Context(), "main", 0)
			case "move":
				if err := s.SaveSession(t.Context(), SessionRow{LocalID: "side", ProjectID: "p"}); err != nil {
					t.Fatal(err)
				}
				_, err = s.db.Exec(`UPDATE stream_items SET session_local_id='side' WHERE id='answer'`)
			case "session":
				_, err = s.db.Exec(`DELETE FROM sessions WHERE local_id='main'`)
			case "edit":
				_, err = s.db.Exec(`UPDATE stream_items SET content='changed' WHERE id='answer'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := s.CompleteAnswerChips(t.Context(), row, []string{"Late result"}); ok || err != nil {
				t.Fatalf("late publication=%t %v", ok, err)
			}
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_item_chips`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("cascade=%d %v", count, err)
			}
		})
	}
}

func TestChipsReadMainAssistantOnlyAndBatchLimit(t *testing.T) {
	s := topicTestStore(t)
	ids := []string{}
	for i := range 101 {
		row := chipAnswer(t, s, fmt.Sprint(i))
		// Read tests seed rows directly: the generation budget is independent.
		if _, err := s.db.Exec(`INSERT INTO stream_item_chips VALUES(?,?,'run','["Open plan"]','en',1)`, row.ID, row.SessionLocalID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	if _, err := s.db.Exec(`UPDATE stream_items SET kind='user' WHERE id='0'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(t.Context(), SessionRow{LocalID: "side", ProjectID: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE stream_items SET session_local_id='side' WHERE id='1'`); err != nil {
		t.Fatal(err)
	}
	// Seed invalid projections after the invalidation triggers have run, so
	// this actually tests the read gate rather than merely the cascades.
	if _, err := s.db.Exec(`INSERT INTO stream_item_chips VALUES('0','main','run','["Hidden user"]','en',1);
        INSERT INTO stream_item_chips VALUES('1','side','run','["Hidden side"]','en',1);`); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListMainChatAnswerChips(t.Context(), ids)
	if err != nil || len(got) != 98 || len(got["2"]) != 1 {
		t.Fatalf("read=%+v err=%v", got, err)
	}
	for _, excluded := range []string{"0", "1", "100"} {
		if _, ok := got[excluded]; ok {
			t.Fatalf("returned excluded row %s", excluded)
		}
	}
}

func TestChipOrdinaryUserRunSurvivesStaleRendererAndFallback(t *testing.T) {
	s := topicTestStore(t)
	row := StreamItemRow{ID: "user", Kind: "user", Content: "Open the plan"}
	if _, err := s.AppendStreamItem(t.Context(), "main", row); err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"first", "fallback"} {
		if err := s.BindAnswerChipUserRun(t.Context(), "main", row.ID, run); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveStreamItems(t.Context(), "main", []StreamItemRow{row}); err != nil {
			t.Fatal(err)
		}
		got, err := s.LoadStreamItem(t.Context(), row.ID)
		if err != nil || got.RunID != run {
			t.Fatalf("lost bound run: %+v %v", got, err)
		}
	}
}
