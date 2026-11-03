package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"wails-gui/storage"
)

// mainChatFoldSummary is each day's fold as the Chat page and Today's panel
// draw it (buildMainChatDays): the count of routine updates and the ids of
// the cards left open.
type mainChatFoldSummary struct {
	Updates map[string]int
	Open    map[string][]string
}

func mainChatFoldOf(t *testing.T, rows []struct{ ID, Kind, ToolName, ToolInput, RowClass, LocalDay string }) mainChatFoldSummary {
	t.Helper()
	fold := mainChatFoldSummary{Updates: map[string]int{}, Open: map[string][]string{}}
	for _, row := range rows {
		if row.LocalDay == "" {
			t.Fatalf("row %s has no local day", row.ID)
		}
		switch MainChatRowClass(row.RowClass) {
		case MainChatRowRoutine:
			if row.ToolName == mainChatStatusTool {
				var status mainChatStatus
				if err := json.Unmarshal([]byte(row.ToolInput), &status); err != nil {
					t.Fatalf("status %s: %v", row.ID, err)
				}
				fold.Updates[row.LocalDay] += len(status.Entries)
			} else {
				fold.Updates[row.LocalDay]++
			}
		case MainChatRowConversation:
		default:
			fold.Open[row.LocalDay] = append(fold.Open[row.LocalDay], row.ID)
		}
	}
	for day := range fold.Open {
		sort.Strings(fold.Open[day])
	}
	return fold
}

// chat-review v1.0.3 #2: one stream, folded one way, wherever it shows. The
// phone's payload carries the same row class and local day as the computer's
// page, so each day has the same count and the same open cards.
func TestMobileMainChatFoldsEachDayLikeTheChatPage(t *testing.T) {
	bridge, session, _ := mobileFieldsTopicBridge(t)
	app := bridge.app
	now := time.Now()
	day := func(back int, hour int) int64 {
		local := now.In(time.Local)
		return time.Date(local.Year(), local.Month(), local.Day()-back, hour, 0, 0, 0, time.Local).UnixMilli()
	}
	status := func(id string, entries int) string {
		value := mainChatStatus{Period: "day"}
		for i := range entries {
			value.Entries = append(value.Entries, mainChatStatusEntry{ID: fmt.Sprintf("%s-%d", id, i), Kind: "observing", Text: "Watched Mail"})
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, row := range []storage.StreamItemRow{
		{ID: "fold-q-2", Kind: "user", Content: "What changed?", CreatedAt: day(2, 9)},
		{ID: "fold-a-2", Kind: "assistant", Content: "The memo moved.", CreatedAt: day(2, 9) + 1000},
		{ID: "fold-status-2", Kind: "system", ToolName: mainChatStatusTool, ToolInput: status("fold-status-2", 5), CreatedAt: day(2, 11)},
		{ID: "fold-status-1", Kind: "system", ToolName: mainChatStatusTool, ToolInput: status("fold-status-1", 3), CreatedAt: day(1, 10)},
		{ID: "fold-status-0", Kind: "system", ToolName: mainChatStatusTool, ToolInput: status("fold-status-0", 2), CreatedAt: day(0, 0) + 1000},
	} {
		if _, err := app.store.AppendStreamItem(t.Context(), session, row); err != nil {
			t.Fatal(err)
		}
	}
	needs := mainChatRowTopicCard("fold-needs")
	needs.Kind = "decision_due"
	if id := app.postProactiveItem(needs, "fold-needs"); id == "" {
		t.Fatal("missing card fixture")
	}

	desktop, err := app.LoadStreamItemsPage(session, -1, 200)
	if err != nil {
		t.Fatal(err)
	}
	var page []struct{ ID, Kind, ToolName, ToolInput, RowClass, LocalDay string }
	for _, item := range desktop.Items {
		page = append(page, struct{ ID, Kind, ToolName, ToolInput, RowClass, LocalDay string }{item.ID, item.Kind, item.ToolName, item.ToolInput, item.RowClass, item.LocalDay})
	}
	app.mainChatWaiting.state = MainChatWaitingState{}
	phonePage, _ := mobileFieldsPage(t, bridge, "/chats/"+session)
	var phone []struct{ ID, Kind, ToolName, ToolInput, RowClass, LocalDay string }
	for _, item := range phonePage.Items {
		input := ""
		if item.Status != nil {
			raw, err := json.Marshal(item.Status)
			if err != nil {
				t.Fatal(err)
			}
			input = string(raw)
		}
		phone = append(phone, struct{ ID, Kind, ToolName, ToolInput, RowClass, LocalDay string }{item.ID, item.Kind, item.ToolName, input, item.RowClass, item.LocalDay})
	}
	pageFold, phoneFold := mainChatFoldOf(t, page), mainChatFoldOf(t, phone)
	if !reflect.DeepEqual(pageFold, phoneFold) {
		t.Fatalf("phone folds %+v, page folds %+v", phoneFold, pageFold)
	}
	today := time.UnixMilli(day(0, 12)).Format("2006-01-02")
	twoBack := time.UnixMilli(day(2, 12)).Format("2006-01-02")
	if pageFold.Updates[twoBack] != 5 || pageFold.Updates[today] != 2 || len(pageFold.Open[today]) != 1 {
		t.Fatalf("fold = %+v", pageFold)
	}
}
