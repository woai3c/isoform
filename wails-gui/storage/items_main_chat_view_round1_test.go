package storage

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestMainChatRound1ViewReadsOpenCandidatesInsteadOfClosedHistory(t *testing.T) {
	s := topicTestStore(t)
	items := []StreamItemRow{}
	for i := 0; i < 300; i++ {
		items = append(items, StreamItemRow{ID: fmt.Sprintf("closed-%d", i), Kind: "system", ToolName: "main_chat_proactive", CreatedAt: 1, ToolInput: `{"kind":"waiting_on_reply","state":"dismissed"}`})
	}
	items = append(items,
		StreamItemRow{ID: "open", Kind: "system", ToolName: "main_chat_proactive", CreatedAt: 2, ToolInput: `{"kind":"decision_due","state":"opened"}`},
		StreamItemRow{ID: "routine", Kind: "system", ToolName: "main_chat_proactive", CreatedAt: 2, ToolInput: `{"kind":"brief_ready","state":"pending"}`},
		StreamItemRow{ID: "closed-digest", Kind: "system", ToolName: "main_chat_proactive", CreatedAt: 2, ToolInput: `{"kind":"digest","state":"pending","items":[{"kind":"waiting_on_reply","state":"dismissed"}]}`},
		StreamItemRow{ID: "open-digest", Kind: "system", ToolName: "main_chat_proactive", CreatedAt: 2, ToolInput: `{"kind":"digest","state":"pending","items":[{"kind":"waiting_on_reply","state":"opened"}]}`})
	if err := s.SaveStreamItems(t.Context(), "main", items); err != nil {
		t.Fatal(err)
	}
	result, err := s.ReadMainChatView(t.Context(), math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("read %d candidates from closed history, want 2", len(result.Items))
	}
	for _, row := range result.Items {
		if row.ID != "open" && row.ID != "open-digest" {
			t.Fatalf("closed/routine candidate %s", row.ID)
		}
	}
	var definition string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_stream_items_main_chat_open'`).Scan(&definition); err != nil || !strings.Contains(definition, "pending") {
		t.Fatalf("missing open-card access index: %s %v", definition, err)
	}
}

func TestMainChatRound1WaitingScanUsesIndexAndCursorLimit(t *testing.T) {
	s := topicTestStore(t)
	for i := 0; i < 8; i++ {
		if _, err := s.AppendStreamItem(t.Context(), "main", StreamItemRow{ID: fmt.Sprintf("open-%d", i), Kind: "system", ToolName: "main_chat_proactive", CreatedAt: int64(i + 1), ToolInput: `{"kind":"decision_due","state":"pending"}`}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ReadMainChatWaitingCandidates(t.Context(), math.MaxInt64, "~", 3)
	if err != nil || len(page.Items) != 3 || page.Items[0].ID != "open-7" || page.Items[2].ID != "open-5" {
		t.Fatalf("bounded scan %+v %v", page, err)
	}
	next, err := s.ReadMainChatWaitingCandidates(t.Context(), page.Items[2].CreatedAt, page.Items[2].ID, 3)
	if err != nil || len(next.Items) != 3 || next.Items[0].ID != "open-4" {
		t.Fatalf("exclusive cursor %+v %v", next, err)
	}
	plan, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM stream_items WHERE session_local_id=? AND `+mainChatOpenCandidatePredicate+` ORDER BY created_at DESC,id DESC LIMIT ?`, "main", 3)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	details := ""
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details += detail
	}
	if err = plan.Err(); err != nil || !strings.Contains(details, "USING INDEX idx_stream_items_main_chat_open") {
		t.Fatalf("unindexed open scan %s %v", details, err)
	}
}

func TestMainChatRound1TopicBatchRollsBackAllRows(t *testing.T) {
	s := topicTestStore(t)
	batch := []StreamItemTopicInsert{}
	for _, id := range []string{"one", "two"} {
		if _, err := s.AppendStreamItem(t.Context(), "main", StreamItemRow{ID: id, Kind: "system", ToolName: "main_chat_report"}); err != nil {
			t.Fatal(err)
		}
		batch = append(batch, StreamItemTopicInsert{ItemID: id, SessionID: "main", TopicIDs: []string{"atlas"}})
	}
	revision, err := s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reject := fmt.Errorf("withdrawn")
	if err = s.InsertStreamItemTopicBatchIfCurrent(t.Context(), batch, revision, func() error { return reject }); err != reject {
		t.Fatalf("batch validation %v", err)
	}
	metadata, err := s.LoadMainChatRowMetadata(t.Context(), "main", []string{"one", "two"})
	if err != nil || len(metadata["one"].TopicIDs) != 0 || len(metadata["two"].TopicIDs) != 0 {
		t.Fatalf("partially published batch %+v %v", metadata, err)
	}
}
