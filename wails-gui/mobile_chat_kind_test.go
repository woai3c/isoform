package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"wails-gui/storage"
)

// The phone's chat list names each chat's kind (main, side, chat, handoff)
// and always starts with the main chat, however old it is.
func TestMobileChatListReportsKindsAndPutsTheMainChatFirst(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	ctx := context.Background()
	main, err := bridge.app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Two chats saved after the main chat are newer, so recency alone would
	// push the main chat past a limit of 2.
	time.Sleep(2 * time.Millisecond)
	if err := store.SaveSession(ctx, storage.SessionRow{LocalID: "side-1", ProjectID: main.ProjectID, WorkspaceID: main.WorkspaceID, Label: "Pricing", Kind: storage.SessionKindSide, ParentID: main.LocalID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := store.SaveSession(ctx, storage.SessionRow{LocalID: "newer-1", ProjectID: main.ProjectID, WorkspaceID: main.WorkspaceID, Label: "Newer plain chat"}); err != nil {
		t.Fatal(err)
	}

	recorder := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats?limit=2", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", recorder.Code, recorder.Body.String())
	}
	var reply mobileTestReply
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Chats []mobileChatSummaryView `json:"chats"`
	}
	if err := json.Unmarshal(reply.Data, &data); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, chat := range data.Chats {
		kinds[chat.ID] = chat.Kind
	}
	if len(data.Chats) != 3 || data.Chats[0].ID != main.LocalID || data.Chats[0].Kind != mobileChatKindMain {
		t.Fatalf("main chat is not first (fetched past the limit): %+v", data.Chats)
	}
	if kinds["side-1"] != mobileChatKindSide || kinds["newer-1"] != mobileChatKindChat {
		t.Fatalf("kinds = %v, chats %+v", kinds, data.Chats)
	}

	// Without a limit squeeze the plain chat keeps kind "chat".
	recorder = mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats", "")
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reply.Data, &data); err != nil {
		t.Fatal(err)
	}
	kinds = map[string]string{}
	for _, chat := range data.Chats {
		kinds[chat.ID] = chat.Kind
	}
	if kinds[mobileTestChatID] != mobileChatKindChat || data.Chats[0].Kind != mobileChatKindMain {
		t.Fatalf("kinds = %v", kinds)
	}

	// The detail header carries the kind too.
	recorder = mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/side-1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Chat mobileChatSummaryView `json:"chat"`
	}
	if err := json.Unmarshal(reply.Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Chat.Kind != mobileChatKindSide {
		t.Fatalf("detail kind = %q", detail.Chat.Kind)
	}
}
