package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func TestMobileChatWaitingCountOmitsUnavailableCount(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	if count := bridge.app.mobileChatWaitingCount("main"); count != nil {
		t.Fatalf("unready read returned a count: %d", *count)
	}
}

func mobileFieldsTopicBridge(t *testing.T) (*mobileBridge, string, string) {
	t.Helper()
	bridge, tag, topic := mobileTopicTestBridge(t)
	// C1 assigns derived tags from an already collected registry snapshot.
	if len(mobileTopicTestList(t, bridge)) != 1 {
		t.Fatal("missing readable topic control")
	}
	return bridge, tag, topic
}

func mobileFieldsPage(t *testing.T, bridge *mobileBridge, path string) (mobileChatDetailView, []byte) {
	t.Helper()
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("page: %d %s", response.Code, response.Body.String())
	}
	mobileTopicTestFrame(t, response.Body.String())
	raw := mobileTestDecodeReply(t, response).Data
	var page mobileChatDetailView
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page, raw
}

func mobileFieldsStoreChips(t *testing.T, a *App, row storage.StreamItemRow, chips []string) {
	t.Helper()
	reserved, _, err := a.store.ReserveAnswerChips(t.Context(), row, "en", time.Now().UnixMilli())
	if err != nil || !reserved {
		t.Fatalf("reserve: %v %v", reserved, err)
	}
	if ok, err := a.store.CompleteAnswerChips(t.Context(), row, chips); err != nil || !ok {
		t.Fatalf("complete: %v %v", ok, err)
	}
}

func TestMobileMainChatFieldsAndLegacyDecode(t *testing.T) {
	bridge, tag, topic := mobileFieldsTopicBridge(t)
	seedTopicMessage(t, bridge.app, tag, "question", topic, time.Now().UnixMilli())
	answer, err := bridge.app.store.LoadStreamItem(t.Context(), "reply-question")
	if err != nil || answer == nil {
		t.Fatal(err)
	}
	mobileFieldsStoreChips(t, bridge.app, *answer, []string{"Review the plan", "Open the draft"})
	cardID := bridge.app.postProactiveItem(mainChatRowTopicCard("card"), "card")
	bridge.app.postProactiveItem(mainChatProactive{MomentID: "routine", Kind: "capture_paused", State: "pending"}, "routine")
	withoutCount, _ := mobileFieldsPage(t, bridge, "/chats/"+tag)
	if withoutCount.WaitingCount != nil || len(withoutCount.Items) != 4 {
		t.Fatalf("unready waiting state blocked the transcript: %+v", withoutCount)
	}
	overview, err := bridge.app.GetMainChatOverview()
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.mainChatWaiting.state = MainChatWaitingState{Ready: true, SessionID: tag, Day: overview.Day, Count: overview.Waiting}
	page, raw := mobileFieldsPage(t, bridge, "/chats/"+tag)
	if err != nil || page.WaitingCount == nil || *page.WaitingCount != overview.Waiting || overview.Waiting != 1 {
		t.Fatalf("waiting=%v overview=%+v err=%v", page.WaitingCount, overview, err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("items=%+v", page.Items)
	}
	stored, err := bridge.app.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]storage.StreamItemRow{}
	for _, row := range stored {
		rows[row.ID] = row
	}
	for _, item := range page.Items {
		row := rows[item.ID]
		if item.RowClass != string(mainChatRowClass(row, time.Now())) || item.LocalDay != time.UnixMilli(row.CreatedAt).In(time.Local).Format("2006-01-02") {
			t.Fatalf("metadata=%+v", item)
		}
		if item.Role == "assistant" {
			if !reflect.DeepEqual(item.Chips, []string{"Review the plan", "Open the draft"}) {
				t.Fatalf("chips=%v", item.Chips)
			}
		} else if len(item.Chips) != 0 {
			t.Fatalf("non-assistant chips=%+v", item)
		}
		if item.ID == "question" || item.ID == "reply-question" || item.ID == cardID {
			if !reflect.DeepEqual(item.TopicIDs, []string{topic}) {
				t.Fatalf("tags=%+v", item)
			}
		}
	}
	// Frozen pre-C3 phone response schema: tolerant reads ignore all additions.
	var legacy struct {
		Chat  mobileChatSummaryView `json:"chat"`
		Items []struct {
			Seq       int64  `json:"seq"`
			ID        string `json:"id"`
			Role      string `json:"role"`
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			ToolName  string `json:"toolName"`
			At        string `json:"at"`
			Proactive *struct {
				MomentID string `json:"momentId"`
				Draft    bool   `json:"draft"`
			} `json:"proactive"`
		} `json:"items"`
		Cursor  int64 `json:"cursor"`
		HasMore bool  `json:"hasMore"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil || legacy.Chat.ID != tag || len(legacy.Items) != len(page.Items) || legacy.Cursor != page.Cursor || legacy.HasMore != page.HasMore {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	for i, item := range legacy.Items {
		current := page.Items[i]
		if item.ID != current.ID || item.Seq != current.Seq || item.Text != current.Text || item.Role != current.Role || item.Kind != current.Kind || item.At != current.At || item.ToolName != current.ToolName {
			t.Fatalf("legacy item=%+v current=%+v", item, current)
		}
	}
}

func TestMobileMainChatFieldsOmittedFromOtherChatsAndZeroWaiting(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	if _, err := store.AppendStreamItem(t.Context(), mobileTestChatID, storage.StreamItemRow{ID: "old", Kind: "assistant", Content: "Kept"}); err != nil {
		t.Fatal(err)
	}
	page, raw := mobileFieldsPage(t, bridge, "/chats/"+mobileTestChatID)
	for _, field := range []string{"rowClass", "topicIds", "localDay", "chips", "waitingCount"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("other chat has %s: %s", field, raw)
		}
	}
	if len(page.Items) != 1 || page.Items[0].Text != "Kept" {
		t.Fatalf("page=%+v", page)
	}
	main, err := bridge.app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.mainChatWaiting.state = MainChatWaitingState{Ready: true, SessionID: main.LocalID, Day: mainChatDayStart(time.Now()).Format("2006-01-02")}
	page, _ = mobileFieldsPage(t, bridge, "/chats/"+main.LocalID)
	if page.WaitingCount == nil || *page.WaitingCount != 0 {
		t.Fatalf("zero=%+v", page)
	}
}

func TestMobileMainChatTopicPrivacyAndEvidenceWithdrawal(t *testing.T) {
	for _, change := range []string{"closed_eyes", "sensitive", "private", "protected", "forgotten", "row_evidence"} {
		t.Run(change, func(t *testing.T) {
			bridge, tag, topic := mobileFieldsTopicBridge(t)
			seedTopicMessage(t, bridge.app, tag, "question", topic, time.Now().UnixMilli())
			cardID := bridge.app.postProactiveItem(mainChatRowTopicCard("card"), "card")
			page, _ := mobileFieldsPage(t, bridge, "/chats/"+tag)
			found := false
			for _, item := range page.Items {
				if item.ID == cardID && len(item.TopicIDs) == 1 {
					found = true
				}
			}
			if !found {
				t.Fatal("missing readable system-tag control")
			}
			switch change {
			case "closed_eyes":
				bridge.app.closedEyes.enabled.Store(true)
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{"Code": "Code"})
			case "private":
				if _, err := bridge.app.activity.store.db.Exec(`UPDATE activity_events SET data='{"windowTitle":"Atlas - Incognito"}' WHERE session_id='session-atlas'`); err != nil {
					t.Fatal(err)
				}
			case "protected":
				if _, err := bridge.app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`); err != nil {
					t.Fatal(err)
				}
			case "forgotten":
				if err := bridge.app.activity.store.DeleteSession("session-atlas"); err != nil {
					t.Fatal(err)
				}
			case "row_evidence":
				card := mainChatRowTopicCard("card")
				card.Evidence[0].ID = "unreadable"
				if err := bridge.app.store.UpdateStreamItemToolInput(t.Context(), cardID, mainChatViewPayload(t, card)); err != nil {
					t.Fatal(err)
				}
			}
			allowed := map[string]bool{}
			for _, topic := range mobileTopicTestList(t, bridge) {
				allowed[topic.ID] = true
			}
			page, _ = mobileFieldsPage(t, bridge, "/chats/"+tag)
			for _, item := range page.Items {
				for _, id := range item.TopicIDs {
					if !allowed[id] {
						t.Fatalf("hidden topic: %+v", item)
					}
				}
				if item.ID == cardID && len(item.TopicIDs) != 0 {
					t.Fatalf("unreadable system evidence: %+v", item)
				}
			}
			if change == "row_evidence" && !allowed[topic] {
				t.Fatal("topic control lost when only row evidence changed")
			}
		})
	}
}

func TestMobileMainChatTopicWithdrawalDuringReadKeepsConversation(t *testing.T) {
	bridge, tag, topic := mobileFieldsTopicBridge(t)
	seedTopicMessage(t, bridge.app, tag, "question", topic, time.Now().UnixMilli())
	bridge.app.postProactiveItem(mainChatRowTopicCard("card"), "card")
	called := false
	bridge.app.mainChatRowTopicsBeforeReadPublish = func() {
		called = true
		if _, err := bridge.app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`); err != nil {
			t.Fatal(err)
		}
	}
	page, _ := mobileFieldsPage(t, bridge, "/chats/"+tag)
	if !called || len(page.Items) == 0 {
		t.Fatalf("hook=%v page=%+v", called, page)
	}
	for _, item := range page.Items {
		if len(item.TopicIDs) != 0 {
			t.Fatalf("racing tags=%+v", item)
		}
	}
}

func TestMobileMainChatChipsClosedEyesAndBounds(t *testing.T) {
	bridge, tag, _ := mobileFieldsTopicBridge(t)
	row := storage.StreamItemRow{ID: "answer", SessionLocalID: tag, Kind: "assistant", Content: "Answer", RunID: "run"}
	if _, err := bridge.app.store.AppendStreamItem(t.Context(), tag, row); err != nil {
		t.Fatal(err)
	}
	mobileFieldsStoreChips(t, bridge.app, row, []string{strings.Repeat("文", 70), "Second", "Third"})
	page, _ := mobileFieldsPage(t, bridge, "/chats/"+tag)
	if len(page.Items) != 1 || len(page.Items[0].Chips) != 2 || utf8.RuneCountInString(page.Items[0].Chips[0]) != 60 {
		t.Fatalf("bounds=%+v", page)
	}
	bridge.app.closedEyes.enabled.Store(true)
	page, raw := mobileFieldsPage(t, bridge, "/chats/"+tag)
	if len(page.Items) != 1 || len(page.Items[0].Chips) != 0 || strings.Contains(string(raw), `"chips"`) {
		t.Fatalf("Closed Eyes=%s", raw)
	}
}

func TestMobileMainChatFieldsBudgetAndTextCaps(t *testing.T) {
	bridge, tag, _ := mobileFieldsTopicBridge(t)
	for i := 0; i < 30; i++ {
		row := storage.StreamItemRow{ID: fmt.Sprintf("answer-%d", i), SessionLocalID: tag, Kind: "assistant", Content: strings.Repeat(`"`, mobileChatTextMax*2), RunID: fmt.Sprintf("run-%d", i)}
		if _, err := bridge.app.store.AppendStreamItem(t.Context(), tag, row); err != nil {
			t.Fatal(err)
		}
		mobileFieldsStoreChips(t, bridge.app, row, []string{strings.Repeat("文", 60), strings.Repeat(`"`, 60)})
	}
	newest, _ := mobileFieldsPage(t, bridge, "/chats/"+tag)
	if !newest.HasMore || len(newest.Items) == 0 || newest.Items[len(newest.Items)-1].ID != "answer-29" {
		t.Fatalf("newest page=%+v", newest)
	}
	var after int64
	count, pages := 0, 0
	for {
		page, _ := mobileFieldsPage(t, bridge, "/chats/"+tag+"?after="+strconv.FormatInt(after, 10))
		used := 0
		for _, item := range page.Items {
			used += mobileChatItemCost(item)
			if len(item.Text) > mobileChatTextMax || !strings.HasSuffix(item.Text, " [truncated]") || len(item.Chips) != 2 || item.RowClass != "conversation" {
				t.Fatalf("item=%+v", item)
			}
		}
		if used > mobileChatPageBudget || len(page.Items) > mobileChatItemsMax {
			t.Fatalf("budget=%d items=%d", used, len(page.Items))
		}
		count += len(page.Items)
		pages++
		if !page.HasMore {
			break
		}
		if page.Cursor <= after || pages > 30 {
			t.Fatal("cursor stalled")
		}
		after = page.Cursor
	}
	if count != 30 || pages < 2 {
		t.Fatalf("count=%d pages=%d", count, pages)
	}
}

func TestMobileMainChatEnrichmentCannotStallOversizedStructuredRow(t *testing.T) {
	// Find a payload that fits until the additive metadata is included.
	row := storage.StreamItemRow{ID: "near-budget", Kind: "system", Content: "Fallback", ToolName: mainChatProactiveTool, CreatedAt: time.Now().UnixMilli()}
	low, high := 0, mobileChatPageBudget
	for low+1 < high {
		n := (low + high) / 2
		row.ToolInput = mainChatViewPayload(t, mainChatProactive{MomentID: "card", Kind: "decision_due", State: "pending", Headline: "Check", Body: strings.Repeat("x", n)})
		view, _ := mobileChatItemFromRow(row)
		if view.Proactive != nil {
			low = n
		} else {
			high = n
		}
	}
	row.ToolInput = mainChatViewPayload(t, mainChatProactive{MomentID: "card", Kind: "decision_due", State: "pending", Headline: "Check", Body: strings.Repeat("x", low)})
	control, _ := mobileChatItemFromRow(row)
	if control.Proactive == nil {
		t.Fatal("missing structured control")
	}
	enrich := mobileMainChatEnricher([]StreamItemData{{ID: row.ID, RowClass: "waiting", LocalDay: "2026-10-22"}}, nil, nil)
	enriched := control
	enrich(&enriched)
	if mobileChatItemCost(enriched) <= mobileChatPageBudget {
		t.Fatal("fixture does not cross budget")
	}
	for _, newest := range []bool{false, true} {
		page := mobileChatPageWithView([]storage.StreamItemRow{row}, false, newest, 0, enrich)
		if page.HasMore || page.Cursor != 1 || len(page.Items) != 1 || page.Items[0].Text != "Fallback" || page.Items[0].Proactive != nil || page.Items[0].RowClass != "waiting" {
			t.Fatalf("newest=%v page=%+v", newest, page)
		}
	}
}

func TestMobileMainChatEmptyAndSourceFreeReadDoesNotWaitForPublication(t *testing.T) {
	for _, withUser := range []bool{false, true} {
		t.Run(strconv.FormatBool(withUser), func(t *testing.T) {
			bridge, store := mobileTestChatBridge(t)
			main, err := bridge.app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if withUser {
				if _, err := store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "user", Kind: "user", Content: "Kept"}); err != nil {
					t.Fatal(err)
				}
			}
			// Admission can hold this fence while an independent phone polls.
			// No optional private data on this page requires taking it again.
			bridge.app.closedEyes.mu.Lock()
			done := make(chan int, 1)
			go func() {
				response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/"+main.LocalID, "")
				done <- response.Code
			}()
			var status int
			select {
			case status = <-done:
				bridge.app.closedEyes.mu.Unlock()
			case <-time.After(2 * time.Second):
				bridge.app.closedEyes.mu.Unlock()
				<-done
				t.Fatal("source-free read blocked on the publication fence")
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d", status)
			}
		})
	}
}
