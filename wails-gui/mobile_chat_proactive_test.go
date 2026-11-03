package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// These rows also supply the sealed chat response in the protocol vectors.
// The wire-shaped fixtures exercise decoding as well as projection.
func mobileProactiveTestRows() []storage.StreamItemRow {
	at := time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC).UnixMilli()
	return []storage.StreamItemRow{
		{
			ID: "moment-brief", SortOrder: 0, Kind: "system", ToolName: mainChatProactiveTool,
			Content: "Your morning brief is ready.", CreatedAt: at,
			ToolInput: `{"momentId":"moment-brief","kind":"brief_ready","headline":"Your morning brief is ready.","body":"Open your brief when you have a moment.","draft":false,"actionIds":["open"],"actionLabels":{"open":"Play"},"target":{"kind":"today"},"evidence":[{"kind":"brief","id":"brief-2026-10-16","at":"2026-10-16T18:00:00Z","target":{"kind":"today"}}],"state":"pending"}`,
		},
		{
			ID: "status-2026-10-16", SortOrder: 1, Kind: "system", ToolName: mainChatStatusTool,
			Content: "2 updates", CreatedAt: at,
			ToolInput: `{"period":"2026-10-16T13:00:00-07:00","entries":[{"id":"digest-1","kind":"digest_done","text":"Your digest is ready.","target":{"kind":"today"}},{"id":"capture-1","kind":"capture_paused","text":"Screen capture has been off for 20 minutes.","target":{"kind":"settings","id":"activity"}}]}`,
		},
	}
}

func TestMobileChatProactiveAndStatusRoundTrip(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	rows := mobileProactiveTestRows()
	for _, row := range rows {
		if _, err := store.AppendStreamItem(t.Context(), mobileTestChatID, row); err != nil {
			t.Fatal(err)
		}
	}
	recorder := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/"+mobileTestChatID+"?after=0", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("chat response: %d %s", recorder.Code, recorder.Body.String())
	}
	data := mobileTestDecodeReply(t, recorder).Data
	var detail mobileTestChatDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Items) != 2 || detail.Cursor != 2 || detail.HasMore {
		t.Fatalf("chat page: %+v", detail)
	}
	var proactive mainChatProactive
	var status mainChatStatus
	if err := json.Unmarshal([]byte(rows[0].ToolInput), &proactive); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(rows[1].ToolInput), &status); err != nil {
		t.Fatal(err)
	}
	// Legacy rows have no source receipt. Every wire representation uses
	// localized generic copy while the stored source rows stay intact.
	proactive.Headline, proactive.Body = "Your brief is ready.", ""
	proactive.Evidence = nil
	proactive.ActionIDs = append(proactive.ActionIDs, "mute_kind")
	proactive.ActionLabels["mute_kind"] = "Mute this kind"
	proactive.Localized = map[string]mainChatProactiveCopy{
		"en":    {Headline: "Your brief is ready.", Body: "", ActionLabels: map[string]string{"open": "Play", "mute_kind": "Mute this kind"}},
		"zh-CN": {Headline: "你的简报已经准备好了。", Body: "", ActionLabels: map[string]string{"open": "播放", "mute_kind": "静音此类消息"}},
	}
	status.Entries[0].Localized = map[string]string{"en": "Your updates are ready.", "zh-CN": "你的最新动态已准备好。"}
	status.Entries[0].Text = "Your updates are ready."
	status.Entries[1].Text = "Isoform stopped watching for a while."
	status.Entries[1].Localized = map[string]string{"en": "Isoform stopped watching for a while.", "zh-CN": "Isoform 暂停观察了一会儿。"}
	status.Entries[1].Target = mainChatTarget{Kind: "settings", ID: "activity"}
	proactiveResult := detail.Items[0].Proactive
	if detail.Items[0].Kind != "proactive" || proactiveResult == nil ||
		proactiveResult.MomentID != proactive.MomentID || proactiveResult.Kind != proactive.Kind ||
		proactiveResult.Headline != "Your brief is ready." ||
		proactiveResult.Localized["zh-CN"].Headline != "你的简报已经准备好了。" {
		t.Fatalf("proactive round trip: %+v", detail.Items[0])
	}
	for _, labels := range []map[string]string{proactiveResult.ActionLabels, proactiveResult.Localized["en"].ActionLabels, proactiveResult.Localized["zh-CN"].ActionLabels} {
		for _, label := range labels {
			if label == "Open" || label == "Dismiss" || label == "Open calendar" || label == "Open brief" {
				t.Fatalf("legacy button label crossed the mobile boundary: %q", label)
			}
		}
	}
	if detail.Items[1].Kind != "status" || !reflect.DeepEqual(detail.Items[1].Status, &status) {
		t.Fatalf("status round trip: %+v", detail.Items[1])
	}
	// A decoder from before these optional fields still receives readable text.
	var legacy struct {
		Items []struct {
			Seq      int64  `json:"seq"`
			Role     string `json:"role"`
			Kind     string `json:"kind"`
			Text     string `json:"text"`
			ToolName string `json:"toolName"`
			At       string `json:"at"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	for i, item := range legacy.Items {
		wantText := "Your brief is ready."
		if i == 1 {
			// A legacy capture entry cannot establish its current source.
			wantText = "Your updates are ready.\nIsoform stopped watching for a while."
		}
		if item.Text != wantText || item.Role != "system" || item.Seq != int64(i+1) || item.At == "" {
			t.Fatalf("old-client fallback %d: %+v", i, item)
		}
		if detail.Items[i].ID != rows[i].ID {
			t.Fatalf("stable item identity %d: %q, want %q", i, detail.Items[i].ID, rows[i].ID)
		}
	}
}

func TestMobileChatMessageSendReportRoundTrip(t *testing.T) {
	for _, channel := range []string{"mail", "slack"} {
		report := mainChatReport{Source: mainChatSourceMessageSend, Key: "message_send:m:r", Channel: channel, SentAt: 1234, PrincipalID: "person-1", Target: mainChatTarget{Kind: mainChatTargetNone}}
		payload, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		row := storage.StreamItemRow{ID: "send-" + channel, Kind: "system", Content: "Sent via " + channel, ToolName: mainChatReportTool, ToolInput: string(payload), CreatedAt: 1234}
		view, ok := mobileChatItemFromRow(row)
		if !ok || view.Kind != "text" || view.Text != row.Content || view.Report == nil || view.Report.Channel != channel || view.Report.SentAt != 1234 {
			t.Fatalf("%s report projection: %+v", channel, view)
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		var decoded mobileChatItemView
		if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded.Report, &report) {
			t.Fatalf("%s report round trip: %+v %v", channel, decoded.Report, err)
		}
	}
}

func TestMobileChatMovedStatusKeepsStableIdentity(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	row := mobileProactiveTestRows()[1]
	order, err := store.AppendStreamItem(t.Context(), mobileTestChatID, row)
	if err != nil {
		t.Fatal(err)
	}
	row.SortOrder = order
	before, _ := mobileChatItemFromRow(row)
	if _, err := store.AppendStreamItem(t.Context(), mobileTestChatID, storage.StreamItemRow{ID: "later-user", Kind: "user", Content: "A later message"}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.MoveStreamItemPresentationToEnd(t.Context(), row.ID, "Updated status", row.ToolInput, time.Now())
	if err != nil || updated == nil {
		t.Fatalf("move status: %+v, %v", updated, err)
	}
	after, _ := mobileChatItemFromRow(*updated)
	if before.ID == "" || after.ID != before.ID || after.Seq <= before.Seq || after.Text != "Updated status" {
		t.Fatalf("status lost stable identity or new cursor: before=%+v after=%+v", before, after)
	}
	recorder := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/"+mobileTestChatID+"?after=2", "")
	var detail mobileTestChatDetail
	if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Items) != 1 || detail.Items[0].ID != before.ID || detail.Items[0].Kind != "status" {
		t.Fatalf("incremental read missed moved status: %+v", detail)
	}
}

func TestMobileChatStructuredFallbackKeepsCursorMoving(t *testing.T) {
	proactive := mobileProactiveTestRows()[0]
	cases := []struct {
		name, tool, payload string
	}{
		{"malformed", mainChatProactiveTool, `{"momentId":`},
		{"missing identity", mainChatProactiveTool, `{"headline":"A note"}`},
		{"wrong field type", mainChatProactiveTool, `{"momentId":"m","kind":"brief_ready","headline":12,"state":"pending"}`},
		{"null", mainChatProactiveTool, `null`},
		{"malformed status", mainChatStatusTool, `{"period":`},
		{"empty status", mainChatStatusTool, `{"period":"2026-10-16","entries":[]}`},
		{"wrong report source", mainChatReportTool, `{"source":"schedule","channel":"mail","sentAt":1}`},
		{"wrong report channel", mainChatReportTool, `{"source":"message_send","channel":"other","sentAt":1}`},
		{"missing report time", mainChatReportTool, `{"source":"message_send","channel":"mail"}`},
		{"oversized report", mainChatReportTool, strings.Repeat(" ", mobileChatPageBudget+1)},
		{"unknown tool", "future_report", proactive.ToolInput},
		{"oversized input", mainChatProactiveTool, strings.Repeat(" ", mobileChatPageBudget+1)},
		{"oversized escaped item", mainChatProactiveTool, strings.Replace(proactive.ToolInput, "Open your brief when you have a moment.", strings.Repeat(`\"`, 15000), 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := proactive
			row.ToolName, row.ToolInput = tc.tool, tc.payload
			page := mobileChatPageFromRows([]storage.StreamItemRow{row}, false, false, 0)
			if len(page.Items) != 1 || page.Cursor != 1 || page.HasMore {
				t.Fatalf("damaged metadata blocked pagination: %+v", page)
			}
			view := page.Items[0]
			if view.Kind != "text" || view.Text != row.Content || view.Proactive != nil || view.Status != nil || view.Report != nil {
				t.Fatalf("damaged metadata did not fall back: %+v", view)
			}
		})
	}
}

func TestMobileChatDigestPreservesStructuredChildren(t *testing.T) {
	row := mobileProactiveTestRows()[0]
	child := row.ToolInput
	row.ToolInput = `{"momentId":"digest-1","kind":"digest","headline":"One thing to catch up on","body":"Your afternoon digest.","draft":false,"actionIds":[],"actionLabels":{},"target":{"kind":"today"},"evidence":[],"state":"pending","items":[` + child + `]}`
	view, ok := mobileChatItemFromRow(row)
	if !ok || view.Proactive == nil || len(view.Proactive.Items) != 1 {
		t.Fatalf("digest projection: %+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded mobileChatItemView
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, view) || decoded.Proactive.Items[0].MomentID != row.ID {
		t.Fatalf("digest child did not round trip: %+v", decoded)
	}
}

func mobileTestProactiveActionBridge(t *testing.T, digest bool) (*mobileBridge, *storage.Store) {
	t.Helper()
	bridge, store := mobileTestChatBridge(t)
	chat, err := store.LoadSession(t.Context(), mobileTestChatID)
	if err != nil {
		t.Fatal(err)
	}
	chat.Kind = storage.SessionKindMain
	if err := store.SaveSession(t.Context(), *chat); err != nil {
		t.Fatal(err)
	}
	row := mobileProactiveTestRows()[0]
	if digest {
		row.ToolInput = `{"momentId":"digest-1","kind":"digest","state":"pending","items":[` + row.ToolInput + `]}`
	}
	if _, err := store.AppendStreamItem(t.Context(), mobileTestChatID, row); err != nil {
		t.Fatal(err)
	}
	return bridge, store
}

func TestMobileChatProactiveActionKeepsMuteOnItsScopedRoute(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	app := bridge.app
	app.engagement = newEngagementService(store, nil, engagementDelivery{})
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	item, ok := app.proactiveItem(engagement.Moment{ID: "phone-mute-boundary", Kind: engagement.KindBriefReady, SubjectID: "brief-1"}, engagementText{})
	if !ok || app.postProactiveItem(item, item.MomentID) == "" {
		t.Fatal("card not saved")
	}
	card := mainChatProactiveItems(t, app)[0]
	if !strings.Contains(strings.Join(card.ActionIDs, ","), engagement.ActionMuteKind) {
		t.Fatal("fixture must offer the mute action")
	}
	beforePrefs := app.engagement.Prefs()
	for _, scopes := range [][]string{{mobileScopeChatWrite}, {mobileScopeChatWrite, mobileScopePush}} {
		response := mobileTestChatCall(bridge, mobileTestDevice(scopes...), http.MethodPost,
			"/chats/"+main.LocalID+"/proactive/actions",
			`{"momentId":"phone-mute-boundary","idempotencyKey":"generic-mute","actionId":"mute_kind"}`)
		if response.Code != http.StatusForbidden {
			t.Fatalf("generic route accepted mute with %v: %d %s", scopes, response.Code, response.Body.String())
		}
	}
	card = mainChatProactiveItems(t, app)[0]
	if card.State != "pending" || card.KindMuted || !reflect.DeepEqual(beforePrefs, app.engagement.Prefs()) {
		t.Fatalf("generic mute changed the card or preference: %+v", card)
	}
	if states := engagementReceiptStates(t, app, item.MomentID, string(engagement.ChannelPush)); len(states) != 0 {
		t.Fatalf("refused mute created phone receipts: %v", states)
	}
}

func TestMobileChatProactiveActionUsesDesktopResolverAndReplaysReceipt(t *testing.T) {
	for _, digest := range []bool{false, true} {
		name := "root"
		if digest {
			name = "digest-child"
		}
		t.Run(name, func(t *testing.T) {
			bridge, store := mobileTestProactiveActionBridge(t, digest)
			device := mobileTestDevice(mobileScopeChatWrite)
			path := "/chats/" + mobileTestChatID + "/proactive/actions"
			body := `{"momentId":"moment-brief","idempotencyKey":"proactive-open","actionId":"open"}`
			first := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
			if first.Code != http.StatusOK {
				t.Fatalf("action = %d %s", first.Code, first.Body.String())
			}
			var result struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(mobileTestDecodeReply(t, first).Data, &result); err != nil || result.State != "opened" {
				t.Fatalf("result = %+v, %v", result, err)
			}
			row, err := store.FindProactiveStreamItem(t.Context(), mobileTestChatID, "moment-brief")
			if err != nil || row == nil {
				t.Fatalf("load acted card: %+v, %v", row, err)
			}
			var card mainChatProactive
			if err := json.Unmarshal([]byte(row.ToolInput), &card); err != nil {
				t.Fatal(err)
			}
			if digest {
				card = card.Items[0]
			}
			if card.State != "opened" {
				t.Fatalf("desktop resolver did not save the card outcome: %+v", card)
			}
			// Remove the source after the receipt. A repeat must replay success
			// without consulting or executing the now-missing card again.
			if err := store.DeleteStreamItemByID(t.Context(), row.ID); err != nil {
				t.Fatal(err)
			}
			replayed := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
			if replayed.Code != first.Code || replayed.Body.String() != first.Body.String() {
				t.Fatalf("receipt not replayed: %d %s", replayed.Code, replayed.Body.String())
			}
			changed := mobileTestChatCall(bridge, device, http.MethodPost, path, `{"momentId":"moment-brief","idempotencyKey":"proactive-open","actionId":"dismiss"}`)
			if changed.Code != http.StatusConflict {
				t.Fatalf("different choice reused the receipt: %d %s", changed.Code, changed.Body.String())
			}
			otherCard := mobileTestChatCall(bridge, device, http.MethodPost, path,
				`{"momentId":"another-moment","idempotencyKey":"proactive-open","actionId":"open"}`)
			if otherCard.Code != http.StatusConflict {
				t.Fatalf("different card reused the receipt: %d %s", otherCard.Code, otherCard.Body.String())
			}
		})
	}
}

func TestMobileChatProactiveActionRefusesUnstoredAuthority(t *testing.T) {
	tests := []struct {
		name, chat, moment, body string
		scope                    string
		status                   int
	}{
		{"scope required", mobileTestChatID, "moment-brief", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open"}`, mobileScopeChatRead, http.StatusForbidden},
		{"key required", mobileTestChatID, "moment-brief", `{"momentId":"moment-brief","actionId":"open"}`, mobileScopeChatWrite, http.StatusBadRequest},
		{"action required", mobileTestChatID, "moment-brief", `{"momentId":"moment-brief","idempotencyKey":"k"}`, mobileScopeChatWrite, http.StatusBadRequest},
		{"moment required", mobileTestChatID, "", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open"}`, mobileScopeChatWrite, http.StatusBadRequest},
		{"moment bounded", mobileTestChatID, strings.Repeat("a", mobileChatPageBudget+1), `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open"}`, mobileScopeChatWrite, http.StatusBadRequest},
		{"unknown chat", "other-chat", "moment-brief", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open"}`, mobileScopeChatWrite, http.StatusNotFound},
		{"unknown moment", mobileTestChatID, "other-moment", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open"}`, mobileScopeChatWrite, http.StatusNotFound},
		{"unoffered action", mobileTestChatID, "moment-brief", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"send"}`, mobileScopeChatWrite, http.StatusBadRequest},
		{"client target rejected", mobileTestChatID, "moment-brief", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open","target":{"kind":"mail"}}`, mobileScopeChatWrite, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge, store := mobileTestProactiveActionBridge(t, false)
			path := "/chats/" + tt.chat + "/proactive/actions"
			body := strings.Replace(tt.body, `"momentId":"moment-brief"`, `"momentId":"`+tt.moment+`"`, 1)
			response := mobileTestChatCall(bridge, mobileTestDevice(tt.scope), http.MethodPost, path, body)
			if response.Code != tt.status {
				t.Fatalf("action = %d %s, want %d", response.Code, response.Body.String(), tt.status)
			}
			row, err := store.FindProactiveStreamItem(t.Context(), mobileTestChatID, "moment-brief")
			if err != nil || row == nil || !strings.Contains(row.ToolInput, `"state":"pending"`) {
				t.Fatalf("refusal changed the card: %+v, %v", row, err)
			}
		})
	}
}

func TestMobileChatProactiveActionPreservesStoredIdentityOutsideURLAlphabet(t *testing.T) {
	bridge, store := mobileTestProactiveActionBridge(t, false)
	row := mobileProactiveTestRows()[0]
	momentID := "handoff:work:blocked:Review /Users/you/报告 first:100"
	var card mainChatProactive
	if err := json.Unmarshal([]byte(row.ToolInput), &card); err != nil {
		t.Fatal(err)
	}
	card.MomentID = momentID
	payload, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateStreamItemToolInput(t.Context(), row.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"momentId": momentID, "actionId": "open", "idempotencyKey": "path-identity"})
	if err != nil {
		t.Fatal(err)
	}
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost,
		"/chats/"+mobileTestChatID+"/proactive/actions", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("stored identity refused: %d %s", response.Code, response.Body.String())
	}
	after, err := store.FindProactiveStreamItem(t.Context(), mobileTestChatID, momentID)
	if err != nil || after == nil || !strings.Contains(after.ToolInput, `"state":"opened"`) {
		t.Fatalf("exact stored identity not resolved: %+v, %v", after, err)
	}
}

func TestMobileChatProactiveActionRejectsSideAndHiddenChats(t *testing.T) {
	for _, name := range []string{"side", "hidden"} {
		t.Run(name, func(t *testing.T) {
			bridge, store := mobileTestProactiveActionBridge(t, false)
			chat, err := store.LoadSession(t.Context(), mobileTestChatID)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "side":
				chat.Kind = storage.SessionKindSide
			case "hidden":
				chat.Hidden = true
			}
			if err := store.SaveSession(t.Context(), *chat); err != nil {
				t.Fatal(err)
			}
			response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost,
				"/chats/"+mobileTestChatID+"/proactive/actions", `{"momentId":"moment-brief","idempotencyKey":"k","actionId":"open"}`)
			if response.Code != http.StatusNotFound {
				t.Fatalf("non-main or hidden chat accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMobileChatProactiveActionRefusalKeepsTheStoredCard(t *testing.T) {
	bridge, store := mobileTestProactiveActionBridge(t, false)
	row, err := store.FindProactiveStreamItem(t.Context(), mobileTestChatID, "moment-brief")
	if err != nil || row == nil {
		t.Fatalf("load card: %+v, %v", row, err)
	}
	payload := `{"momentId":"moment-brief","kind":"suggestion","state":"pending","actionIds":["do_once"],"target":{"kind":"suggestion","id":"missing-suggestion"}}`
	if err := store.UpdateStreamItemToolInput(t.Context(), row.ID, payload); err != nil {
		t.Fatal(err)
	}
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost,
		"/chats/"+mobileTestChatID+"/proactive/actions", `{"momentId":"moment-brief","idempotencyKey":"refused","actionId":"do_once"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("refused action = %d %s", response.Code, response.Body.String())
	}
	after, err := store.FindProactiveStreamItem(t.Context(), mobileTestChatID, "moment-brief")
	if err != nil || after == nil || after.ToolInput != payload {
		t.Fatalf("refusal changed the card: %+v, %v", after, err)
	}
}

func TestMobileChatProactiveActionDoesNotClaimAnAlreadyHandledChoice(t *testing.T) {
	for _, state := range []string{"acted", "dismissed", "deferred", "expired"} {
		t.Run(state, func(t *testing.T) {
			bridge, store := mobileTestProactiveActionBridge(t, false)
			row := mobileProactiveTestRows()[0]
			payload := strings.Replace(row.ToolInput, `"state":"pending"`, `"state":"`+state+`"`, 1)
			if err := store.UpdateStreamItemToolInput(t.Context(), row.ID, payload); err != nil {
				t.Fatal(err)
			}
			response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost,
				"/chats/"+mobileTestChatID+"/proactive/actions", `{"momentId":"moment-brief","idempotencyKey":"new-choice","actionId":"open"}`)
			if response.Code != http.StatusConflict {
				t.Fatalf("previous outcome claimed as a new choice: %d %s", response.Code, response.Body.String())
			}
			// Desktop repeat semantics are deliberately unchanged. Mobile replay
			// must instead come from the receipt for its exact original choice.
			if got, err := bridge.app.ResolveMainChatProactiveAction("moment-brief", "open"); err != nil || got != state {
				t.Fatalf("desktop replay changed: %q, %v", got, err)
			}
		})
	}
}

func TestMobileChatProactiveConcurrentChoicesCannotClaimTheSameTerminalResult(t *testing.T) {
	bridge, store := mobileTestProactiveActionBridge(t, false)
	row := mobileProactiveTestRows()[0]
	payload := strings.Replace(row.ToolInput, `"actionIds":["open"]`, `"actionIds":["dismiss"]`, 1)
	if err := store.UpdateStreamItemToolInput(t.Context(), row.ID, payload); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, key := range []string{"first-choice", "second-choice"} {
		go func(key string) {
			<-start
			response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost,
				"/chats/"+mobileTestChatID+"/proactive/actions", `{"momentId":"moment-brief","idempotencyKey":"`+key+`","actionId":"dismiss"}`)
			results <- response.Code
		}(key)
	}
	close(start)
	first, second := <-results, <-results
	if !((first == http.StatusOK && second == http.StatusConflict) || (first == http.StatusConflict && second == http.StatusOK)) {
		t.Fatalf("concurrent new choices = %d and %d, want one success and one conflict", first, second)
	}
}

func TestMobileChatProactiveWorkPermissionUsesTheExactLiveRequest(t *testing.T) {
	for _, action := range []string{"approve", "decline"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("YANSU_AGENT_HOME", t.TempDir())
			app, runner := newMainChatPermissionTestApp(t)
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := newMobileBridge(app, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			response := awaitWorkPermission(t, runner, "work-agent")
			app.emitAgentEvent(workPermissionEvent("proactive-phone-answer"))
			cards := mainChatProactiveItems(t, app)
			if len(cards) != 1 || cards[0].Permission == nil {
				t.Fatalf("expected one permission card: %+v", cards)
			}
			path := "/chats/" + main.LocalID + "/proactive/actions"
			device := mobileTestDevice(mobileScopeChatWrite)
			body := `{"momentId":"` + cards[0].MomentID + `","idempotencyKey":"permission-choice","actionId":"` + action + `"}`
			for i := 0; i < 2; i++ {
				answer := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
				if answer.Code != http.StatusOK {
					t.Fatalf("action/replay = %d %s", answer.Code, answer.Body.String())
				}
			}
			select {
			case result := <-response:
				if result.Granted != (action == "approve") || result.Always || result.SessionAlways {
					t.Fatalf("permission choice changed scope: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("action did not reach the existing permission manager")
			}
			if runner.calls.Load() != 1 {
				t.Fatalf("replay answered the runner %d times", runner.calls.Load())
			}
			other := "approve"
			if action == "approve" {
				other = "decline"
			}
			stale := mobileTestChatCall(bridge, device, http.MethodPost, path,
				`{"momentId":"`+cards[0].MomentID+`","idempotencyKey":"different-choice","actionId":"`+other+`"}`)
			if stale.Code != http.StatusConflict || runner.calls.Load() != 1 {
				t.Fatalf("stale permission choice accepted: %d %s; calls=%d", stale.Code, stale.Body.String(), runner.calls.Load())
			}
			// The original work controls can finish while a phone still holds a
			// pending card (or while its terminal presentation is awaiting repair).
			// The live request boundary must refuse the phone's different choice.
			row, err := app.store.FindProactiveStreamItem(t.Context(), main.LocalID, cards[0].MomentID)
			if err != nil || row == nil {
				t.Fatalf("load permission presentation: %+v, %v", row, err)
			}
			pending, err := json.Marshal(cards[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := app.store.UpdateStreamItemToolInput(t.Context(), row.ID, string(pending)); err != nil {
				t.Fatal(err)
			}
			stalePresentation := mobileTestChatCall(bridge, device, http.MethodPost, path,
				`{"momentId":"`+cards[0].MomentID+`","idempotencyKey":"stale-presentation","actionId":"`+other+`"}`)
			if stalePresentation.Code != http.StatusConflict || runner.calls.Load() != 1 {
				t.Fatalf("obsolete presentation bypassed live request: %d %s; calls=%d", stalePresentation.Code, stalePresentation.Body.String(), runner.calls.Load())
			}
		})
	}
}

func TestMobileChatProactiveAcceptedPermissionSurvivesPresentationFailure(t *testing.T) {
	for _, failure := range []struct {
		name                  string
		presentation, receipt bool
		committedReceipt      bool
		engagementTable       string
	}{
		{name: "presentation", presentation: true},
		{name: "receipt", receipt: true},
		{name: "presentation-and-receipt", presentation: true, receipt: true},
		{name: "receipt-committed-before-verification", receipt: true, committedReceipt: true},
		{name: "receipt-then-engagement-delivery", receipt: true, engagementTable: "engagement_moments"},
		{name: "receipt-then-engagement-opened", receipt: true, engagementTable: "engagement_receipts"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			app, runner := newMainChatPermissionTestApp(t)
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := newMobileBridge(app, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			response := awaitWorkPermission(t, runner, "work-agent")
			app.emitAgentEvent(workPermissionEvent("phone-storage-retry"))
			card := mainChatProactiveItems(t, app)[0]
			if failure.presentation {
				if _, err := runner.db.Exec(`CREATE TRIGGER refuse_permission_update BEFORE UPDATE OF tool_input ON stream_items BEGIN SELECT RAISE(FAIL, 'presentation unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if failure.receipt {
				if _, err := runner.db.Exec(`CREATE TRIGGER refuse_permission_receipt BEFORE INSERT ON mobile_receipts BEGIN SELECT RAISE(FAIL, 'receipt unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			path := "/chats/" + main.LocalID + "/proactive/actions"
			device := mobileTestDevice(mobileScopeChatWrite)
			body := `{"momentId":"` + card.MomentID + `","idempotencyKey":"accepted-permission","actionId":"approve"}`
			answer := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
			want := http.StatusOK
			if failure.receipt {
				want = http.StatusServiceUnavailable
			}
			if answer.Code != want {
				t.Errorf("accepted permission response = %d %s, want %d", answer.Code, answer.Body.String(), want)
			}
			select {
			case result := <-response:
				if !result.Granted || result.Always || result.SessionAlways {
					t.Fatalf("permission changed scope: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("action did not reach the real permission manager")
			}
			assertPending := func() {
				t.Helper()
				if !failure.receipt {
					return
				}
				read := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/"+main.LocalID+"?after=0", "")
				if read.Code != http.StatusOK {
					t.Fatalf("read awaiting receipt: %d %s", read.Code, read.Body.String())
				}
				var detail mobileTestChatDetail
				if err := json.Unmarshal(mobileTestDecodeReply(t, read).Data, &detail); err != nil {
					t.Fatal(err)
				}
				if len(detail.Items) != 1 || detail.Items[0].Proactive == nil || detail.Items[0].Proactive.State != "pending" {
					t.Fatalf("receipt failure stranded the phone's retry action: %+v", detail.Items)
				}
			}
			assertPending()
			// Repeated runner events must not publish the in-memory acceptance
			// as a terminal card before the phone can retrieve its durable result.
			app.emitAgentEvent(workPermissionEvent("phone-storage-retry"))
			assertPending()
			app.emitAgentEvent(AgentEvent{Type: "close", Tag: "work-chat", SessionID: "work-agent"})
			assertPending()
			for _, changed := range []string{
				strings.Replace(body, `"approve"`, `"decline"`, 1),
				strings.Replace(body, `"accepted-permission"`, `"another-key"`, 1),
			} {
				refused := mobileTestChatCall(bridge, device, http.MethodPost, path, changed)
				if refused.Code != http.StatusConflict {
					t.Errorf("different choice or retry key inherited acceptance: %d %s", refused.Code, refused.Body.String())
				}
			}
			if failure.presentation {
				if _, err := runner.db.Exec(`DROP TRIGGER refuse_permission_update`); err != nil {
					t.Fatal(err)
				}
			}
			if failure.receipt {
				if _, err := runner.db.Exec(`DROP TRIGGER refuse_permission_receipt`); err != nil {
					t.Fatal(err)
				}
			}
			if failure.engagementTable != "" {
				app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
				if _, err := runner.db.Exec(`CREATE TRIGGER refuse_permission_measurement BEFORE INSERT ON ` + failure.engagementTable + ` BEGIN SELECT RAISE(FAIL, 'measurement unavailable'); END`); err != nil {
					t.Fatal(err)
				}
				retry := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
				if retry.Code != http.StatusServiceUnavailable || runner.calls.Load() != 1 {
					t.Fatalf("measurement failure lost the accepted retry: %d %s; runner calls=%d", retry.Code, retry.Body.String(), runner.calls.Load())
				}
				assertPending()
				if _, err := runner.db.Exec(`DROP TRIGGER refuse_permission_measurement`); err != nil {
					t.Fatal(err)
				}
			}
			if failure.committedReceipt {
				// Reproduce a committed receipt whose verification read failed:
				// accepted memory and presentation still await reconciliation.
				app.workPermissions.mu.Lock()
				accepted := app.workPermissions.acceptedMobile[card.MomentID]
				app.workPermissions.mu.Unlock()
				if accepted == nil {
					t.Fatal("accepted retry identity was lost before persistence")
				}
				if err := app.store.SaveMobileReceipt(t.Context(), *accepted.mobileReceipt); err != nil {
					t.Fatal(err)
				}
			}
			// Presentation repair and runner close must not consume the accepted
			// retry identity while its durable receipt is still missing.
			app.retryWorkPermissionWrites()
			retry := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
			if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), `"state":"acted"`) || runner.calls.Load() != 1 {
				t.Fatalf("accepted replay = %d %s, runner calls=%d", retry.Code, retry.Body.String(), runner.calls.Load())
			}
			if got := mainChatProactiveItems(t, app)[0].State; got != "acted" {
				t.Fatalf("repaired presentation = %q", got)
			}
			app.workPermissions.mu.Lock()
			remaining := len(app.workPermissions.acceptedMobile)
			app.workPermissions.mu.Unlock()
			if remaining != 0 {
				t.Fatal("durable replay left an accepted retry blocking presentation")
			}
			// A fresh app has no live permission or accepted in-memory outcome.
			// It must replay the separately persisted transport receipt.
			reopened, err := newMobileBridge(&App{store: app.store}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reopened.Stop)
			replay := mobileTestChatCall(reopened, device, http.MethodPost, path, body)
			if replay.Code != http.StatusOK || replay.Body.String() != retry.Body.String() || runner.calls.Load() != 1 {
				t.Fatalf("durable replay = %d %s, runner calls=%d", replay.Code, replay.Body.String(), runner.calls.Load())
			}
		})
	}
}

func TestMobileChatSendPendingOutcomesReturnServiceUnavailable(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		name := "unknown"
		if accepted {
			name = "accepted-finalization"
		}
		t.Run(name, func(t *testing.T) {
			bridge, store := mobileTestChatBridge(t)
			chat, err := store.LoadSession(t.Context(), mobileTestChatID)
			if err != nil {
				t.Fatal(err)
			}
			chat.Kind = storage.SessionKindMain
			if err := store.SaveSession(t.Context(), *chat); err != nil {
				t.Fatal(err)
			}
			app, fake, moment := outboundSendTestCardWithApp(t, bridge.app)
			card := outboundStoredCard(t, app, moment.ID)
			attempt := outboundRowForMainChatDraft(&card, moment.DedupeKey)
			if _, claimed, err := store.ClaimOutboundSend(t.Context(), attempt, time.Now().UnixMilli()); err != nil || !claimed {
				t.Fatalf("claim=%v err=%v", claimed, err)
			}
			if accepted {
				sentAt := time.Now().UnixMilli()
				if err := store.MarkOutboundSendAccepted(t.Context(), moment.ID, card.DraftContent.Revision, sentAt, sentAt+time.Hour.Milliseconds()); err != nil {
					t.Fatal(err)
				}
				app.engagement = &engagementService{}
			} else if err := store.MarkOutboundSendFailed(t.Context(), moment.ID, card.DraftContent.Revision, true); err != nil {
				t.Fatal(err)
			}
			response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost,
				"/chats/"+mobileTestChatID+"/proactive/actions",
				`{"momentId":"`+moment.ID+`","idempotencyKey":"send-pending","actionId":"send"}`)
			if accepted {
				// Mail took it: the phone reads sent, and the receipt is saved
				// apart from the send (Chat review §2), never a second email.
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"acted"`) || fake.count() != 0 {
					t.Fatalf("response=%d %s calls=%d", response.Code, response.Body.String(), fake.count())
				}
				return
			}
			if response.Code != http.StatusServiceUnavailable || fake.count() != 0 {
				t.Fatalf("response=%d %s calls=%d", response.Code, response.Body.String(), fake.count())
			}
		})
	}
}
