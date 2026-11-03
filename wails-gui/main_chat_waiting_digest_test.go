package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMainChatWaitingStateDigestContainsOnlyWaitingChildren(t *testing.T) {
	fixture, _ := newWaitingStateFixture(t)
	input := `{"momentId":"digest-test","kind":"digest","state":"pending","headline":"Daily updates","items":[{"momentId":"pending-child","kind":"custom_pending","state":"pending","headline":"Pending"},{"momentId":"resolved-child","kind":"custom_resolved","state":"resolved","headline":"Resolved"}],"extension" : { "preserve" : "exact", "nested": [1,  2] }}`
	row := storage.StreamItemRow{
		ID: "waiting-digest", Kind: "system", Content: "Daily updates",
		ToolName: mainChatProactiveTool, ToolInput: input, CreatedAt: time.Now().UnixMilli(),
	}
	if _, err := fixture.app.store.AppendStreamItem(t.Context(), fixture.app.GetMainChatWaiting().SessionID, row); err != nil {
		t.Fatal(err)
	}
	fixture.app.mainChatRowsChanged(fixture.app.GetMainChatWaiting().SessionID)

	state := waitForMainChatWaiting(t, fixture.app, func(state MainChatWaitingState) bool { return state.Count == 1 })
	if len(state.Items) != 1 || state.Items[0].ID != row.ID {
		t.Fatalf("waiting state items=%+v, want digest row %q", state.Items, row.ID)
	}
	list, err := fixture.app.ListMainChatWaitingItems("", 5)
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != row.ID {
		t.Fatalf("waiting list=%+v err=%v", list, err)
	}
	for _, item := range []StreamItemData{state.Items[0], list.Items[0]} {
		var projected map[string]json.RawMessage
		if err := json.Unmarshal([]byte(item.ToolInput), &projected); err != nil {
			t.Fatalf("invalid digest projection %q: %v", item.ToolInput, err)
		}
		var children []json.RawMessage
		if err := json.Unmarshal(projected["items"], &children); err != nil {
			t.Fatal(err)
		}
		if len(children) != 1 {
			t.Errorf("digest projection kept %d children, want only pending child: %s", len(children), item.ToolInput)
			continue
		}
		var child mainChatProactive
		if err := json.Unmarshal(children[0], &child); err != nil {
			t.Fatal(err)
		}
		if child.Headline != "Pending" || child.State != "pending" {
			t.Errorf("digest projection child=%+v, want pending child", child)
		}
	}
	_, _, filtered := projectMainChatWaitingRows(
		[]storage.StreamItemRow{row},
		[]StreamItemData{{ID: row.ID, ToolInput: input}},
		time.Now(),
		5,
	)
	var preserved map[string]json.RawMessage
	if err := json.Unmarshal([]byte(filtered[0].ToolInput), &preserved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preserved["extension"], []byte(`{ "preserve" : "exact", "nested": [1,  2] }`)) {
		t.Errorf("digest projection changed unrelated field: %s", preserved["extension"])
	}

	stream, err := fixture.app.LoadStreamItemsPage(state.SessionID, -1, 10)
	if err != nil || len(stream.Items) == 0 {
		t.Fatalf("main stream=%+v err=%v", stream, err)
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stream.Items[0].ToolInput), &full); err != nil {
		t.Fatal(err)
	}
	var fullChildren []json.RawMessage
	if err := json.Unmarshal(full["items"], &fullChildren); err != nil || len(fullChildren) != 2 {
		t.Fatalf("main stream digest children=%s err=%v, want both children", full["items"], err)
	}
}
