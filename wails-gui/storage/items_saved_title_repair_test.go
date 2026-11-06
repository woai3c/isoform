package storage

import "testing"

func TestSavedProactiveTitleRepairCompareAndSwap(t *testing.T) {
	s, _ := proactiveItemsTestStore(t)
	if _, err := s.db.Exec(`UPDATE sessions SET kind='main' WHERE local_id='chat'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendStreamItem(t.Context(), "chat", StreamItemRow{ID: "card", Kind: "system", ToolName: "main_chat_proactive", ToolInput: `{"headline":"before"}`}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.SavedProactiveInputs(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := s.UpdateStreamItemToolInput(t.Context(), "card", `{"headline":"concurrent"}`); err != nil {
		t.Fatal(err)
	}
	ok, err := s.ReplaceProactiveInputIfUnchanged(t.Context(), "card", rows[0].ToolInput, `{"headline":"repair"}`)
	if err != nil || ok {
		t.Fatalf("stale write=%v err=%v", ok, err)
	}
	row, err := s.LoadStreamItem(t.Context(), "card")
	if err != nil || row.ToolInput != `{"headline":"concurrent"}` {
		t.Fatalf("card=%+v err=%v", row, err)
	}
}
