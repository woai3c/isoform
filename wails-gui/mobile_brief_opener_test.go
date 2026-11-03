package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// "Talk about this brief" on the phone (backlog item 58): Isoform says the
// brief's opener first in the main chat, once, with the computer's words.
func TestPhoneTalkAboutThisBriefPostsTheOpenerOnce(t *testing.T) {
	rig, _, brief := cardRig(t)
	app := rig.app
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("view = %+v, %v", view, err)
	}
	opener, ok := mobileBriefFindCard(*view.Brief, mobileBriefOpenerCardID)
	if !ok {
		t.Fatal("the brief has no opener")
	}
	bridge, err := newMobileBridge(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	path := "/chats/" + main.LocalID + "/brief-opener"
	body := func(key, version string) string {
		return `{"idempotencyKey":"` + key + `","briefId":"` + brief.BriefID + `","briefVersion":"` + version + `"}`
	}

	for _, scopes := range [][]string{{mobileScopeChatWrite}, {mobileScopeBriefRead}} {
		if reply := mobileTestChatCall(bridge, mobileTestDevice(scopes...), http.MethodPost, path, body("scope", view.Brief.ConversationKey)); reply.Code != http.StatusForbidden {
			t.Fatalf("scopes %v: %d", scopes, reply.Code)
		}
	}
	if reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, body("stale", strings.Repeat("0", 64))); reply.Code != http.StatusConflict {
		t.Fatalf("a stale revision opened the brief: %d %s", reply.Code, reply.Body.String())
	}
	for _, key := range []string{"open-1", "open-2"} {
		reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, body(key, view.Brief.ConversationKey))
		if reply.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", key, reply.Code, reply.Body.String())
		}
		var result struct {
			Message string            `json:"message"`
			Answers []MobileBriefCard `json:"answers"`
		}
		if err := json.Unmarshal(mobileTestDecodeReply(t, reply).Data, &result); err != nil || result.Message != opener.Message || len(result.Answers) != 2 {
			t.Fatalf("opener and answer chips: %+v %v", result, err)
		}
		for _, answer := range result.Answers {
			if answer.Kind != mobileBriefCardOpenerAnswer || answer.Mode != mobileBriefCardSend || answer.Label == "" || answer.Message == "" || len(answer.Refs) == 0 {
				t.Fatalf("invalid opener answer: %+v", answer)
			}
		}
	}
	page, err := app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 50)
	if err != nil {
		t.Fatal(err)
	}
	said := 0
	for _, row := range page.Items {
		if row.Kind == "assistant" && row.Content == opener.Message {
			said++
		}
		if row.Kind == "user" {
			t.Fatalf("the opener sent a user message: %q", row.Content)
		}
	}
	if opener.Message == "" || said != 1 {
		t.Fatalf("Isoform's opener %q was said %d times", opener.Message, said)
	}
}
