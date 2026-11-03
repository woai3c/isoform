package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func mobileMainChatWorkBridge(t *testing.T) (*mobileBridge, *storage.SessionRow) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "mcw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	app := mainChatTestApp(t)
	app.handoffStore, err = handoff.NewStore(filepath.Join(home, "handoff"))
	if err != nil {
		t.Fatal(err)
	}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(app, filepath.Join(home, "mobile"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge, main
}

func mobileReadMainChatWork(t *testing.T, bridge *mobileBridge, chatID string) (mobileTestChatDetail, json.RawMessage) {
	t.Helper()
	recorder := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/"+chatID+"?after=0", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("main chat response: %d %s", recorder.Code, recorder.Body.String())
	}
	data := mobileTestDecodeReply(t, recorder).Data
	var detail mobileTestChatDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Chat.ID != chatID || detail.Chat.Kind != mobileChatKindMain {
		t.Fatalf("work was not read from the main chat: %+v", detail.Chat)
	}
	return detail, data
}

// Decode wire names independently of the hub's Go types: a changed JSON tag
// must not silently pass by using the same changed type on both sides.
func mobileAssertProactiveWireFields(t *testing.T, data json.RawMessage, field string, want map[string]string) map[string]json.RawMessage {
	t.Helper()
	var outer struct {
		Items []struct {
			Proactive map[string]json.RawMessage `json:"proactive"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &outer); err != nil || len(outer.Items) == 0 {
		t.Fatalf("phone wire page: %s (%v)", data, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(outer.Items[0].Proactive[field], &fields); err != nil {
		t.Fatalf("phone wire %s object: %s (%v)", field, data, err)
	}
	for key, expected := range want {
		var got string
		if err := json.Unmarshal(fields[key], &got); err != nil || got != expected {
			t.Fatalf("phone wire %s.%s = %s, want %q (%v)", field, key, fields[key], expected, err)
		}
	}
	return fields
}

func TestMobileMainChatWorkResultsRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		status handoff.Status
		kind   string
	}{
		{handoff.StatusCompleted, "work_finished"},
		{handoff.StatusNeedsReview, "work_finished"},
		{handoff.StatusBlocked, "work_blocked"},
		{handoff.StatusAborted, "work_blocked"},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			bridge, main := mobileMainChatWorkBridge(t)
			app := bridge.app
			const sessionID = "work-history"
			const result = "The launch memo is ready.\n\n<deliverables>\nhttps://example.com/memo | Launch memo\n</deliverables>"
			if err := app.store.SaveSession(t.Context(), storage.SessionRow{LocalID: sessionID, ProjectID: main.ProjectID, WorkspaceID: main.WorkspaceID, Label: "Launch memo"}); err != nil {
				t.Fatal(err)
			}
			if _, err := app.store.AppendStreamItem(t.Context(), sessionID, storage.StreamItemRow{ID: "work-result", Kind: "assistant", Content: result}); err != nil {
				t.Fatal(err)
			}
			entry, err := app.handoffStore.Save(handoff.Entry{ID: "hf-mobile-result", Title: "Launch memo", Status: tc.status, ChatSessionID: sessionID})
			if err != nil {
				t.Fatal(err)
			}
			app.postHandoffReport(entry)
			app.postHandoffReport(entry)
			detail, data := mobileReadMainChatWork(t, bridge, main.LocalID)
			if len(detail.Items) != 1 || detail.Cursor != 1 || detail.HasMore {
				t.Fatalf("replayed work report changed the page: %+v", detail)
			}
			item := detail.Items[0]
			if item.Kind != "proactive" || item.Role != "system" || item.ID == "" || item.Proactive == nil || item.Proactive.Kind != tc.kind {
				t.Fatalf("work proactive JSON: %+v", item)
			}
			work := item.Proactive.Work
			if work == nil || work.HandoffID != entry.ID || work.Status != tc.status || !strings.Contains(work.Result, "launch memo") || work.Summary == "" {
				t.Fatalf("work snapshot missing from phone payload: %+v", work)
			}
			if len(work.Deliverables) != 1 || work.Deliverables[0].URL != "https://example.com/memo" || work.Deliverables[0].Label != "Launch memo" {
				t.Fatalf("declared deliverable missing from phone payload: %+v", work.Deliverables)
			}
			wire := mobileAssertProactiveWireFields(t, data, "work", map[string]string{
				"handoffId": entry.ID, "status": string(tc.status), "summary": work.Summary, "result": work.Result,
			})
			var links []map[string]string
			if err := json.Unmarshal(wire["deliverables"], &links); err != nil || len(links) != 1 || links[0]["label"] != "Launch memo" || links[0]["url"] != "https://example.com/memo" {
				t.Fatalf("phone wire deliverables: %s (%v)", wire["deliverables"], err)
			}
			if item.Proactive.Target.Kind != mainChatTargetHandoff || item.Proactive.Target.ID != entry.ID {
				t.Fatalf("work action points outside its handoff: %+v", item.Proactive.Target)
			}
			wantActions := []string{"review", "stop", "dismiss", "mute_kind"}
			if tc.status == handoff.StatusCompleted || tc.status == handoff.StatusAborted {
				wantActions = []string{"review", "dismiss", "mute_kind"}
			}
			if !reflect.DeepEqual(item.Proactive.ActionIDs, wantActions) {
				t.Fatalf("work actions: %v", item.Proactive.ActionIDs)
			}
			for _, language := range []string{"en", "zh-CN"} {
				copy := item.Proactive.Localized[language]
				if copy.Headline == "" || copy.ActionLabels["review"] == "" || copy.ActionLabels["stop"] == "" || copy.ActionLabels["dismiss"] == "" {
					t.Fatalf("missing %s phone copy: %+v", language, copy)
				}
			}
			var legacy struct {
				Items []struct {
					Text string `json:"text"`
				} `json:"items"`
			}
			if err := json.Unmarshal(data, &legacy); err != nil || len(legacy.Items) != 1 || !strings.Contains(legacy.Items[0].Text, "launch memo") {
				t.Fatalf("legacy phone lost readable work summary: %s (%v)", data, err)
			}
			for i := 0; i < 2; i++ {
				if _, err := app.ResolveMainChatProactiveAction(item.Proactive.MomentID, "review"); err != nil {
					t.Fatal(err)
				}
			}
			reviewed, _ := mobileReadMainChatWork(t, bridge, main.LocalID)
			if len(reviewed.Items) != 2 || reviewed.Items[1].Role != "assistant" || reviewed.Items[1].Kind != "text" || !strings.Contains(reviewed.Items[1].Text, "https://example.com/memo") {
				t.Fatalf("review did not reach the main chat once: %+v", reviewed)
			}
			history, err := app.store.LoadStreamItems(t.Context(), sessionID)
			if err != nil || len(history) != 1 || history[0].Content != result {
				t.Fatalf("review changed the work history: %+v, %v", history, err)
			}
		})
	}
}

func TestMobileMainChatWorkPermissionRoundTrip(t *testing.T) {
	bridge, main := mobileMainChatWorkBridge(t)
	app := bridge.app
	entry, err := app.handoffStore.Save(handoff.Entry{ID: "hf-mobile-permission", Title: "Launch memo", Status: handoff.StatusInProgress, ChatSessionID: "work-history"})
	if err != nil {
		t.Fatal(err)
	}
	event := AgentEvent{
		Type: "permission_request", EventID: "permission-event", Tag: entry.ChatSessionID,
		SessionID: "work-runner-session", ToolUseID: "write-memo", ToolName: "WriteFile",
		Content: "Allow writing the launch memo?", ToolInput: json.RawMessage(`{"path":"report/memo.md"}`),
		PermissionOptions: []agent.PermissionOption{{ID: "allow_once", Name: "Allow once"}, {ID: "reject", Name: "Decline"}},
	}
	app.emitAgentEvent(event)
	app.emitAgentEvent(event)
	detail, data := mobileReadMainChatWork(t, bridge, main.LocalID)
	if len(detail.Items) != 1 || detail.Cursor != 1 || detail.Items[0].Proactive == nil {
		t.Fatalf("permission replay appended another main-chat request: %+v", detail)
	}
	item := detail.Items[0]
	card := item.Proactive
	request := card.Permission
	if item.Kind != "proactive" || card.Kind != "decision_due" || card.State != "pending" || request == nil {
		t.Fatalf("decision_due JSON: %+v", item)
	}
	if request.RequestID == "" || request.RequestID != card.MomentID || request.SessionID != event.SessionID || request.HandoffID != entry.ID || request.ToolName != event.ToolName {
		t.Fatalf("permission identity missing from JSON: %+v", request)
	}
	if request.ApproveOptionID != "allow_once" || request.DeclineOptionID != "reject" || !reflect.DeepEqual(card.ActionIDs, []string{"approve", "decline", "mute_kind"}) {
		t.Fatalf("permission options changed: %+v", card)
	}
	mobileAssertProactiveWireFields(t, data, "permission", map[string]string{
		"requestId": request.RequestID, "sessionId": event.SessionID, "handoffId": entry.ID,
		"toolName": event.ToolName, "details": request.Details,
		"approveOptionId": "allow_once", "declineOptionId": "reject",
	})
	if !strings.Contains(request.Details, "report/memo.md") || !strings.Contains(item.Text, event.Content) {
		t.Fatalf("phone lost request details or legacy text: %s", data)
	}
	path := "/chats/" + main.LocalID + "/permissions/" + request.RequestID
	for _, tc := range []struct {
		name   string
		device mobileDeviceRecord
		body   string
		status int
	}{
		{"scope required", mobileTestDevice(mobileScopeChatRead), `{"idempotencyKey":"work-scope","optionId":"allow_once"}`, http.StatusForbidden},
		{"offered option required", mobileTestDevice(mobileScopeChatWrite), `{"idempotencyKey":"work-option","optionId":"allow_always"}`, http.StatusBadRequest},
		{"live runner required", mobileTestDevice(mobileScopeChatWrite), `{"idempotencyKey":"work-stale","optionId":"allow_once"}`, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := mobileTestChatCall(bridge, tc.device, http.MethodPost, path, tc.body)
			if recorder.Code != tc.status {
				t.Fatalf("permission response: %d %s; want %d", recorder.Code, recorder.Body.String(), tc.status)
			}
		})
	}
	app.emitAgentEvent(AgentEvent{Type: "tool_result", Tag: entry.ChatSessionID, ToolUseID: event.ToolUseID})
	expired, _ := mobileReadMainChatWork(t, bridge, main.LocalID)
	if len(expired.Items) != 1 || expired.Items[0].ID != item.ID || expired.Items[0].Seq != item.Seq || expired.Items[0].Proactive.State != "expired" {
		t.Fatalf("resolved request lost stable identity on the phone: %+v", expired)
	}
}

func TestMobileMainChatWorkPermissionAnswerAndReplay(t *testing.T) {
	for _, tc := range []struct {
		optionID string
		source   string
	}{
		{"allow_once", "main"},
		{"reject", "main"},
		{"allow_once", "work"},
		{"reject", "work"},
	} {
		t.Run(tc.source+"/"+tc.optionID, func(t *testing.T) {
			optionID := tc.optionID
			home, err := os.MkdirTemp("/tmp", "mcp-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(home) })
			t.Setenv("HOME", home)
			app, runner := newMainChatPermissionTestApp(t)
			app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := newMobileBridge(app, filepath.Join(home, "mobile"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			response := awaitWorkPermission(t, runner, "work-agent")
			app.emitAgentEvent(workPermissionEvent("mobile-answer"))
			detail, _ := mobileReadMainChatWork(t, bridge, main.LocalID)
			if len(detail.Items) != 1 || detail.Items[0].Proactive == nil || detail.Items[0].Proactive.Permission == nil {
				t.Fatalf("phone has no work request: %+v", detail)
			}
			card := detail.Items[0]
			requestID := card.Proactive.Permission.RequestID
			original, pending := bridge.chatPermissionView("work-chat")
			if !pending || original(1).Permission.RequestID != requestID {
				t.Fatal("main and execution chat permissions have different identities")
			}
			chatID := main.LocalID
			if tc.source == "work" {
				chatID = "work-chat"
			}
			path := "/chats/" + chatID + "/permissions/" + requestID
			device := mobileTestDevice(mobileScopeChatWrite)
			body := `{"idempotencyKey":"mobile-work-answer","optionId":"` + optionID + `"}`
			for i := 0; i < 2; i++ {
				recorder := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
				if recorder.Code != http.StatusOK {
					t.Fatalf("phone answer/replay: %d %s", recorder.Code, recorder.Body.String())
				}
				var answer struct {
					Answered bool `json:"answered"`
				}
				if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &answer); err != nil || !answer.Answered {
					t.Fatalf("permission reply JSON: %s (%v)", recorder.Body.String(), err)
				}
			}
			select {
			case result := <-response:
				if result.Granted != (optionID == "allow_once") || result.Always || result.SessionAlways {
					t.Fatalf("phone choice changed permission scope: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("phone answer did not reach the work's permission manager")
			}
			resolved, _ := mobileReadMainChatWork(t, bridge, main.LocalID)
			if len(resolved.Items) != 1 || resolved.Items[0].ID != card.ID || resolved.Items[0].Proactive.State != "acted" || runner.calls.Load() != 1 {
				t.Fatalf("phone answer replay duplicated or failed to resolve: %+v, calls=%d", resolved, runner.calls.Load())
			}
			if tc.source == "main" {
				phone := engagementReceiptStates(t, app, requestID, string(engagement.ChannelPush))
				computer := engagementReceiptStates(t, app, requestID, string(engagement.ChannelDesktop))
				if !phone[storage.EngagementReceiptOpened] || computer[storage.EngagementReceiptOpened] || phone[storage.EngagementReceiptActed] {
					t.Fatalf("phone permission answer recorded the wrong surface or completion: phone=%v computer=%v", phone, computer)
				}
			}
			if _, pending := bridge.chatPermissionView("work-chat"); pending {
				t.Fatal("phone answer left the execution chat's permission pending")
			}
			// A second device or renderer can have a different transport receipt.
			// The request id itself must still prevent a second runner response.
			mainPath := "/chats/" + main.LocalID + "/permissions/" + requestID
			recorder := mobileTestChatCall(bridge, device, http.MethodPost, mainPath, `{"idempotencyKey":"mobile-work-replay","optionId":"`+optionID+`"}`)
			if recorder.Code != http.StatusOK || runner.calls.Load() != 1 {
				t.Fatalf("new receipt replayed runner response: %d %s, calls=%d", recorder.Code, recorder.Body.String(), runner.calls.Load())
			}
		})
	}
}

func TestMobileWorkPermissionOriginalRouteCannotAnswerReplacedRequest(t *testing.T) {
	bridge, main := mobileMainChatWorkBridge(t)
	app := bridge.app
	if _, err := app.handoffStore.Save(handoff.Entry{ID: "permission-work", Status: handoff.StatusInProgress, ChatSessionID: "work-chat"}); err != nil {
		t.Fatal(err)
	}
	app.emitAgentEvent(workPermissionEvent("first"))
	bridge.chat.mu.Lock()
	previous := bridge.chat.pending[bridge.chat.byChat["work-chat"]]
	bridge.chat.mu.Unlock()
	if previous == nil || previous.WorkRequestID == "" || previous.RequestID != previous.WorkRequestID {
		t.Fatalf("original permission has no stable work identity: %+v", previous)
	}
	app.emitAgentEvent(workPermissionEvent("second"))
	if err := app.respondToWorkPermission(previous.AgentSessionID, "allow_once", previous.WorkRequestID); err == nil || !strings.Contains(err.Error(), "no longer pending") {
		t.Fatalf("a handler holding the previous request reached the next permission: %v", err)
	}
	path := "/chats/work-chat/permissions/" + previous.RequestID
	recorder := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite), http.MethodPost, path, `{"idempotencyKey":"previous-work-request","optionId":"allow_once"}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("replaced original request route: %d %s", recorder.Code, recorder.Body.String())
	}
	detail, _ := mobileReadMainChatWork(t, bridge, main.LocalID)
	if len(detail.Items) != 2 || detail.Items[0].Proactive.State != "expired" || detail.Items[1].Proactive.State != "pending" {
		t.Fatalf("old response changed the next permission: %+v", detail)
	}
}
