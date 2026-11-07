package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mobileBriefConversationPath(briefID string) string {
	return "/briefs/" + briefID + "/conversation"
}

// mobileBriefConversationFixture is the chat fixture with the brief service
// running, so the route reads the brief through the same package gate as GET
// /briefs/{id}.
func mobileBriefConversationFixture(t *testing.T) (*mobileBridge, string, MobileBriefV1, *upcomingCaptureRunner) {
	t.Helper()
	bridge, chatID, brief, runner := mobileBriefChatFixture(t)
	svc := newMobileBriefService(bridge.app)
	svc.app = bridge.app
	svc.power = func(string) (func(), error) { return func() {}, nil }
	svc.speaker = mobileBriefSpeaker{}
	bridge.app.mobileBridgeMu.Lock()
	bridge.app.mobileBriefSvc = svc
	bridge.app.mobileBridgeMu.Unlock()
	return bridge, chatID, brief, runner
}

func TestMobileBriefConversationRequiresBothScopes(t *testing.T) {
	bridge := &mobileBridge{}
	for _, scopes := range [][]string{nil, {mobileScopeChatWrite}, {mobileScopeBriefRead}} {
		reply := mobileTestChatCall(bridge, mobileTestDevice(scopes...), http.MethodPost, mobileBriefConversationPath("2026-11-06-morning"), "")
		if reply.Code != http.StatusForbidden {
			t.Fatalf("scopes %v: %d %s", scopes, reply.Code, reply.Body.String())
		}
	}
}

// The same brief asked twice gives one conversation: the same answer, the same
// main chat, no opener posted and no brief or turn made.
func TestMobileBriefConversationIsIdempotentAndMakesNoBrief(t *testing.T) {
	bridge, chatID, brief, runner := mobileBriefConversationFixture(t)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	before, ok := newMobileBriefStore(dir).load(brief.BriefID)
	if !ok {
		t.Fatal("fixture has no stored brief")
	}
	rowsBefore, err := bridge.app.store.LoadStreamItems(t.Context(), chatID)
	if err != nil {
		t.Fatal(err)
	}

	path := mobileBriefConversationPath(brief.BriefID)
	first := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, "")
	if first.Code != http.StatusOK {
		t.Fatalf("conversation: %d %s", first.Code, first.Body.String())
	}
	var answer struct {
		BriefID      string `json:"briefId"`
		BriefVersion string `json:"briefVersion"`
		ChatID       string `json:"chatId"`
	}
	if err := json.Unmarshal(mobileTestDecodeReply(t, first).Data, &answer); err != nil {
		t.Fatalf("invalid answer %q: %v", first.Body.String(), err)
	}
	if answer.BriefID != brief.BriefID || answer.BriefVersion != brief.ConversationKey || answer.ChatID != chatID {
		t.Fatalf("answer = %+v, want brief %s at %s in chat %s", answer, brief.BriefID, brief.ConversationKey, chatID)
	}

	second := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, "")
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Fatalf("second ask differs: %d %s", second.Code, second.Body.String())
	}

	rowsAfter, err := bridge.app.store.LoadStreamItems(t.Context(), chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsAfter) != len(rowsBefore) {
		t.Fatalf("the conversation posted rows: before %d, after %d", len(rowsBefore), len(rowsAfter))
	}
	after, ok := newMobileBriefStore(dir).load(brief.BriefID)
	if !ok || after.ConversationKey != before.ConversationKey || after.BriefID != before.BriefID {
		t.Fatalf("the stored brief changed: %q -> %q", before.ConversationKey, after.ConversationKey)
	}
	select {
	case task := <-runner.inputs:
		t.Fatalf("the conversation started a turn: %+v", task)
	case <-time.After(100 * time.Millisecond):
	}
}

// A brief the computer withholds or withdraws gets no words back: the refusal
// is a 404 or 409, names no conversation and carries no narrative text, and
// nothing is posted or started.
func TestMobileBriefConversationGivesNoWordsForAWithheldBrief(t *testing.T) {
	for _, change := range []string{"Closed Eyes", "delivery filter", "unknown brief"} {
		t.Run(change, func(t *testing.T) {
			bridge, chatID, brief, runner := mobileBriefConversationFixture(t)
			briefID := brief.BriefID
			switch change {
			case "Closed Eyes":
				bridge.app.closedEyes.enabled.Store(true)
			case "delivery filter":
				dir, _ := memoryDir()
				if err := os.Remove(filepath.Join(dir, "launch.md")); err != nil {
					t.Fatal(err)
				}
			case "unknown brief":
				briefID = "2026-01-01-morning"
			}
			if change != "unknown brief" {
				if delivered := bridge.app.mobileBriefForDelivery(brief); len(delivered.Narrative) != 0 {
					t.Fatal("fixture did not withdraw the readable brief")
				}
			}
			reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, mobileBriefConversationPath(briefID), "")
			if reply.Code != http.StatusNotFound && reply.Code != http.StatusConflict {
				t.Fatalf("withheld brief: %d %s, want 404 or 409", reply.Code, reply.Body.String())
			}
			// The phone tells an unknown brief from an older computer by this message: an unknown brief is
			// "brief not found", never the route-missing answer that means the computer is too old.
			if change == "unknown brief" {
				if got := mobileTestDecodeReply(t, reply).Error; got != "brief not found" {
					t.Fatalf("unknown brief answered %q, want \"brief not found\"", got)
				}
			}
			body := reply.Body.String()
			if strings.Contains(body, "chatId") || strings.Contains(body, "briefVersion") {
				t.Fatalf("refusal names a conversation: %s", body)
			}
			for _, paragraph := range brief.Narrative {
				if text := strings.TrimSpace(paragraph.Text); text != "" && strings.Contains(body, text) {
					t.Fatalf("refusal carries brief words: %s", body)
				}
			}
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), chatID)
			if err != nil || len(rows) != 0 {
				t.Fatalf("refused request posted rows: %+v %v", rows, err)
			}
			select {
			case <-runner.inputs:
				t.Fatal("refused request started a turn")
			default:
			}
		})
	}
}
