package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mobileBriefParagraphFixture(t *testing.T, language string, narrative []MobileBriefPara) (*mobileBridge, *App, string, MobileBriefV1) {
	t.Helper()
	rig, svc, brief := cardRig(t)
	stored, ok := svc.store.load(brief.BriefID)
	if !ok {
		t.Fatal("brief fixture was not stored")
	}
	if len(stored.Narrative) == 0 || len(stored.Narrative[0].Refs) == 0 {
		t.Fatal("brief fixture has no readable paragraph source")
	}
	refs := append([]string(nil), stored.Narrative[0].Refs...)
	for i := range narrative {
		narrative[i].Refs = append([]string(nil), refs...)
	}
	stored.Narrative = narrative
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	if err := rig.app.store.SetUIState(t.Context(), uiLanguageStateKey, language); err != nil {
		t.Fatal(err)
	}
	delivered, ok := rig.app.todayChatDeliveredBrief(brief.BriefID)
	if !ok {
		t.Fatal("brief fixture was not delivered")
	}
	main, err := rig.app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge, rig.app, main.LocalID, delivered
}

func mobileBriefParagraphBody(t *testing.T, key, briefID, version string, paragraph *int) string {
	t.Helper()
	raw, err := json.Marshal(mobileBriefParagraphInput{
		IdempotencyKey: key, BriefID: briefID, BriefVersion: version, Paragraph: paragraph,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mobileBriefParagraphIndex(index int) *int {
	return &index
}

func TestMobileBriefParagraphPostsQuotedEnglishAndChinese(t *testing.T) {
	for _, tc := range []struct {
		name, language, prefix string
	}{
		{"english", "en", "About this part of your brief:"},
		{"chinese", "zh-CN", "关于简报里的这一段："},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := "The release is ready.\nThe review is next."
			if tc.language == "zh-CN" {
				text = "发布已准备好。\n接下来进行评审。"
			}
			bridge, app, chatID, brief := mobileBriefParagraphFixture(t, tc.language, []MobileBriefPara{
				{Text: text},
				{Text: "Would you like to discuss it?"},
			})
			body := mobileBriefParagraphBody(t, "paragraph", brief.BriefID, brief.ConversationKey, mobileBriefParagraphIndex(0))
			reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost,
				"/chats/"+chatID+"/brief-paragraph", body)
			if reply.Code != http.StatusOK {
				t.Fatalf("paragraph: %d %s", reply.Code, reply.Body.String())
			}
			rows, err := app.store.LoadStreamItemsLatest(t.Context(), chatID, 50)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.prefix + "\n\n> " + strings.ReplaceAll(text, "\n", "\n> ")
			for _, row := range rows.Items {
				if row.Kind == "assistant" && row.Content == want {
					return
				}
			}
			t.Fatalf("quoted message %q was not posted: %+v", want, rows.Items)
		})
	}
}

func TestMobileBriefParagraphIdempotentReplayPostsOnce(t *testing.T) {
	bridge, app, chatID, brief := mobileBriefParagraphFixture(t, "en", []MobileBriefPara{
		{Text: "The release is ready."},
		{Text: "Would you like to discuss it?"},
	})
	body := mobileBriefParagraphBody(t, "same-key", brief.BriefID, brief.ConversationKey, mobileBriefParagraphIndex(0))
	for range 2 {
		reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost,
			"/chats/"+chatID+"/brief-paragraph", body)
		if reply.Code != http.StatusOK {
			t.Fatalf("paragraph replay: %d %s", reply.Code, reply.Body.String())
		}
	}
	rows, err := app.store.LoadStreamItemsLatest(t.Context(), chatID, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := "About this part of your brief:\n\n> The release is ready."
	count := 0
	for _, row := range rows.Items {
		if row.Kind == "assistant" && row.Content == want {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("quoted paragraph was posted %d times", count)
	}
}

func TestMobileBriefParagraphInvalidTargetsReturnConflict(t *testing.T) {
	bridge, app, chatID, brief := mobileBriefParagraphFixture(t, "en", []MobileBriefPara{
		{Text: "The release is ready."},
		{Text: "Would you like to discuss it?"},
	})
	path := "/chats/" + chatID + "/brief-paragraph"
	request := func(key, version string, index int) *httptest.ResponseRecorder {
		t.Helper()
		return mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path,
			mobileBriefParagraphBody(t, key, brief.BriefID, version, mobileBriefParagraphIndex(index)))
	}
	for _, tc := range []struct {
		name, version string
		index         int
	}{
		{"stale", strings.Repeat("0", 64), 0},
		{"closing", brief.ConversationKey, 1},
		{"out of range", brief.ConversationKey, 2},
		{"negative", brief.ConversationKey, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if reply := request(tc.name, tc.version, tc.index); reply.Code != http.StatusConflict {
				t.Fatalf("invalid paragraph: %d %s", reply.Code, reply.Body.String())
			}
		})
	}
	svc, ok := app.mobileBriefSource().(*mobileBriefService)
	if !ok {
		t.Fatal("brief fixture service has unexpected type")
	}
	stored, ok := svc.store.load(brief.BriefID)
	if !ok {
		t.Fatal("brief fixture disappeared")
	}
	stored.Narrative[0].Text = " \n "
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	current, ok := app.todayChatDeliveredBrief(brief.BriefID)
	if !ok {
		t.Fatal("brief fixture was not delivered")
	}
	if reply := request("empty", current.ConversationKey, 0); reply.Code != http.StatusConflict {
		t.Fatalf("empty paragraph: %d %s", reply.Code, reply.Body.String())
	}
	app.closedEyes.enabled.Store(true)
	if reply := request("closed-eyes", current.ConversationKey, 1); reply.Code != http.StatusConflict {
		t.Fatalf("Closed Eyes: %d %s", reply.Code, reply.Body.String())
	}
}

// A paragraph tap from a brief whose revision moved quotes the paragraph only
// when the phone sends the words it showed and the brief still says them.
func TestMobileBriefParagraphStaleVersionQuotesTheWordsStillInTheBrief(t *testing.T) {
	bridge, app, chatID, brief := mobileBriefParagraphFixture(t, "en", []MobileBriefPara{
		{Text: "The release is ready."},
		{Text: "The review is next."},
		{Text: "Would you like to discuss it?"},
	})
	path := "/chats/" + chatID + "/brief-paragraph"
	stale := strings.Repeat("0", 64)
	request := func(key string, index int, text string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(mobileBriefParagraphInput{IdempotencyKey: key, BriefID: brief.BriefID,
			BriefVersion: stale, Paragraph: mobileBriefParagraphIndex(index), Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, string(raw))
	}
	if reply := request("no-text", 1, ""); reply.Code != http.StatusConflict {
		t.Fatalf("stale paragraph without its words: %d %s", reply.Code, reply.Body.String())
	}
	if reply := request("gone", 1, "A paragraph the brief no longer says."); reply.Code != http.StatusConflict {
		t.Fatalf("stale paragraph that changed: %d %s", reply.Code, reply.Body.String())
	}
	// The paragraph moved from index 0 to 1 in the phone's older revision; its words find it.
	if reply := request("moved", 0, "The review is next."); reply.Code != http.StatusOK {
		t.Fatalf("stale paragraph still in the brief: %d %s", reply.Code, reply.Body.String())
	}
	rows, err := app.store.LoadStreamItemsLatest(t.Context(), chatID, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := "About this part of your brief:\n\n> The review is next."
	for _, row := range rows.Items {
		if row.Kind == "assistant" && row.Content == want {
			return
		}
	}
	t.Fatalf("quoted message %q was not posted: %+v", want, rows.Items)
}

func TestMobileBriefParagraphMainChatScopesAndInput(t *testing.T) {
	bridge, _, chatID, brief := mobileBriefParagraphFixture(t, "en", []MobileBriefPara{{Text: "The release is ready."}})
	path := "/chats/" + chatID + "/brief-paragraph"
	body := mobileBriefParagraphBody(t, "scope", brief.BriefID, brief.ConversationKey, mobileBriefParagraphIndex(0))
	for _, scopes := range [][]string{{mobileScopeChatWrite}, {mobileScopeBriefRead}} {
		if reply := mobileTestChatCall(bridge, mobileTestDevice(scopes...), http.MethodPost, path, body); reply.Code != http.StatusForbidden {
			t.Fatalf("scopes %v: %d %s", scopes, reply.Code, reply.Body.String())
		}
	}
	if got := mobileRouteFamily(http.MethodPost, path); got != "POST /chats/*/brief-paragraph" {
		t.Fatalf("route family = %q", got)
	}
	if reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost,
		"/chats/not-main/brief-paragraph", body); reply.Code != http.StatusNotFound {
		t.Fatalf("non-main chat: %d %s", reply.Code, reply.Body.String())
	}
	if reply := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path,
		`{"idempotencyKey":"missing-index","briefId":"`+brief.BriefID+`","briefVersion":"`+brief.ConversationKey+`"}`); reply.Code != http.StatusBadRequest {
		t.Fatalf("missing paragraph index: %d %s", reply.Code, reply.Body.String())
	}
}
