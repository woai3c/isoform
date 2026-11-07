package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"wails-gui/storage"
)

func TestMobileChatAskedAtMarksTheFirstAnswerToEachQuestion(t *testing.T) {
	at := func(minute int) int64 { return time.Date(2026, 10, 31, 7, minute, 0, 0, time.UTC).UnixMilli() }
	rows := []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "First question", SortOrder: 0, CreatedAt: at(1)},
		{ID: "u2", Kind: "user", Content: "Second question", SortOrder: 1, CreatedAt: at(2)},
		{ID: "a1", Kind: "assistant", Content: "First answer", SortOrder: 2, CreatedAt: at(40)},
		{ID: "a2", Kind: "assistant", Content: "More of it", SortOrder: 3, CreatedAt: at(41)},
		{ID: "u3", Kind: "user", Content: "Third question", SortOrder: 4, CreatedAt: at(42)},
		{ID: "a3", Kind: "assistant", Content: "Third answer", SortOrder: 5, CreatedAt: at(43)},
	}
	page := mobileChatPageWithView(rows, false, true, 0, nil)
	asked := map[string]string{}
	for _, item := range page.Items {
		asked[item.ID] = item.AskedAt
	}
	want := map[string]string{
		"u1": "", "u2": "", "a1": "2026-10-31T07:02:00Z", "a2": "", "u3": "", "a3": "2026-10-31T07:42:00Z",
	}
	for id, value := range want {
		if asked[id] != value {
			t.Fatalf("askedAt for %s = %q, want %q (all %v)", id, asked[id], value, asked)
		}
	}
	data, err := json.Marshal(page.Items[0])
	if err != nil || strings.Contains(string(data), "askedAt") {
		t.Fatalf("a question carries askedAt: %s %v", data, err)
	}
}

func TestMobileChatMessageValidatesTheWaitingFields(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	device := mobileTestDevice(mobileTestChatScopes()...)
	path := "/chats/" + mobileTestChatID + "/messages"
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, body := range []string{
		`{"idempotencyKey":"w1","text":"hi","queuedAt":"yesterday"}`,
		`{"idempotencyKey":"w2","text":"hi","queuedAt":"` + future + `"}`,
		`{"idempotencyKey":"w3","text":"hi","undone":-1}`,
		`{"idempotencyKey":"w4","text":"hi","undone":101}`,
	} {
		if reply := mobileTestChatCall(bridge, device, http.MethodPost, path, body); reply.Code != http.StatusBadRequest ||
			!strings.Contains(reply.Body.String(), "waiting fields") {
			t.Fatalf("%s: %d %s", body, reply.Code, reply.Body.String())
		}
	}
	queued := time.Now().Add(-26 * time.Hour).UTC().Format(time.RFC3339)
	reply := mobileTestChatCall(bridge, device, http.MethodPost, path,
		`{"idempotencyKey":"w5","text":"hi","queuedAt":"`+queued+`","undone":2}`)
	if reply.Code != http.StatusAccepted {
		t.Fatalf("a message that waited 26 hours was refused: %d %s", reply.Code, reply.Body.String())
	}
	mobileTestWaitFor(t, "the turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
}

func TestMobileChatWaitedAnswersShareOneNotification(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	previous := mobileChatSettleDelay
	mobileChatSettleDelay = 10 * time.Millisecond
	var mu sync.Mutex
	var notices []mobileNotice
	mobileChatAnswerPublishOverride = func(n mobileNotice) {
		mu.Lock()
		notices = append(notices, n)
		mu.Unlock()
	}
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() {
		mobileTestResetChatRunner(t, bridge)
		mobileChatSettleDelay = previous
		mobileChatAnswerPublishOverride = nil
	})
	device := mobileTestDevice(mobileTestChatScopes()...)
	path := "/chats/" + mobileTestChatID + "/messages"
	published := func(n int) []mobileNotice {
		mobileTestWaitFor(t, "the answer notification", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(notices) >= n
		})
		mu.Lock()
		defer mu.Unlock()
		return append([]mobileNotice(nil), notices...)
	}
	send := func(key string, queued bool) {
		body := `{"idempotencyKey":"` + key + `","text":"question ` + key + `"}`
		if queued {
			at := time.Now().Add(-90 * time.Minute).UTC().Format(time.RFC3339)
			body = `{"idempotencyKey":"` + key + `","text":"question ` + key + `","queuedAt":"` + at + `"}`
		}
		if reply := mobileTestChatCall(bridge, device, http.MethodPost, path, body); reply.Code != http.StatusAccepted {
			t.Fatalf("send %s: %d %s", key, reply.Code, reply.Body.String())
		}
		mobileTestWaitFor(t, "the turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
	}

	send("q1", true)
	first := published(1)[0]
	send("q2", true)
	both := published(2)
	second := both[1]
	if first.Kind != "chat_answer" || first.Body != "Isoform answered." || first.DestinationKind != "chat" || first.DestinationID != mobileTestChatID {
		t.Fatalf("first notification %+v", first)
	}
	if second.Body != "Isoform answered." ||
		second.Collapse != first.Collapse || second.MomentID != first.MomentID || second.ID == first.ID {
		t.Fatalf("second notification %+v, first %+v", second, first)
	}
	page, err := store.LoadStreamItemsLatest(context.Background(), mobileTestChatID, 50)
	if err != nil {
		t.Fatal(err)
	}
	firstAnswer := ""
	for _, item := range page.Items {
		if item.Kind == "assistant" {
			firstAnswer = item.ID
			break
		}
	}
	if first.MomentID != firstAnswer {
		t.Fatalf("the notification opens %q, want the first answer %q", first.MomentID, firstAnswer)
	}
	if err := second.validate(); err != nil {
		t.Fatalf("the notification is not publishable: %v", err)
	}

	// A message that did not wait gets no answer notification.
	send("q3", false)
	time.Sleep(100 * time.Millisecond)
	if got := published(2); len(got) != 2 {
		t.Fatalf("a message that did not wait was notified: %+v", got[2:])
	}

	// Reading the chat ends the batch.
	if reply := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID, ""); reply.Code != http.StatusOK {
		t.Fatalf("read: %d %s", reply.Code, reply.Body.String())
	}
	send("q4", true)
	third := published(3)[2]
	if third.Collapse == first.Collapse || third.Body != "Isoform answered." {
		t.Fatalf("a read batch was continued: %+v", third)
	}
}

func TestMobileChatAnswerNoticeCopy(t *testing.T) {
	now := time.Now()
	batch := mobileChatAnswerBatch{collapse: "chat-answers-" + uuid.NewString(), firstID: uuid.NewString(), at: now}
	for language, want := range map[string]string{
		"en":    "Isoform answered.",
		"zh-CN": "Isoform 已回复。",
	} {
		n := mobileChatAnswerNotice(mobileTestChatID, batch, language, now)
		if n.Body != want {
			t.Fatalf("%s body %q", language, n.Body)
		}
		if err := n.validate(); err != nil {
			t.Fatalf("%s: %v", language, err)
		}
	}
}

func TestPhoneChatTelemetryAcceptsOnlyItsFields(t *testing.T) {
	cases := []struct {
		event string
		in    map[string]any
		ok    bool
		key   string
	}{
		{"phone_chat.queued", nil, true, ""},
		{"phone_chat.sent_after_wait", map[string]any{"wait_minutes": 95}, true, "wait_minutes"},
		{"phone_chat.sent_after_wait", map[string]any{"wait_minutes": -1}, false, ""},
		{"phone_chat.sent_after_wait", map[string]any{"wait_minutes": "95"}, false, ""},
		{"phone_chat.undone", map[string]any{"count": 2}, true, "count"},
		{"phone_chat.undone", map[string]any{"text": "secret"}, false, ""},
	}
	for _, tc := range cases {
		out, ok := productTelemetryProperties(tc.event, tc.in)
		if ok != tc.ok {
			t.Fatalf("%s %v: ok %v", tc.event, tc.in, ok)
		}
		if !ok {
			continue
		}
		if _, leaked := out["text"]; leaked {
			t.Fatalf("%s leaked text", tc.event)
		}
		if tc.key != "" && out[tc.key] != tc.in[tc.key] {
			t.Fatalf("%s %s = %v", tc.event, tc.key, out[tc.key])
		}
	}
}

func TestMobileChatQueuedAtAndWaitMinutes(t *testing.T) {
	now := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	if at, ok := mobileChatQueuedAt("", now); !ok || !at.IsZero() {
		t.Fatal("an absent queue time is valid and zero")
	}
	if _, ok := mobileChatQueuedAt(now.Add(4*time.Minute).Format(time.RFC3339), now); !ok {
		t.Fatal("a little clock skew is accepted")
	}
	at, ok := mobileChatQueuedAt("2026-10-30T22:02:00-07:00", now)
	if !ok || mobileChatWaitMinutes(at, now) != 178 {
		t.Fatalf("wait minutes = %d, %v", mobileChatWaitMinutes(at, now), ok)
	}
}

// A phone that lists no main chat sends to the main-chat alias: the computer makes sure its main chat exists and
// takes the message (PROTOCOL.md 15.5a).
func TestMobileChatMainAliasCreatesTheMainChatAndSends(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	device := mobileTestDevice(mobileTestChatScopes()...)
	if _, err := store.LoadMainSession(t.Context()); err == nil {
		t.Fatal("the fixture should start without a main chat")
	}
	reply := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/main/messages", `{"idempotencyKey":"main-1","text":"hi"}`)
	if reply.Code != http.StatusAccepted {
		t.Fatalf("a send to the main alias was refused: %d %s", reply.Code, reply.Body.String())
	}
	main, err := store.LoadMainSession(t.Context())
	if err != nil {
		t.Fatalf("the main chat was not created: %v", err)
	}
	mobileTestWaitFor(t, "the turn to finish", func() bool { return !bridge.chatRunning(main.LocalID) })
}

// A message from the phone is stored with its origin, and its answer lands in the same chat (#635).
func TestMobileChatPhoneMessageCarriesOriginAndAnswersInTheSameChat(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	device := mobileTestDevice(mobileTestChatScopes()...)
	reply := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages",
		`{"idempotencyKey":"origin-1","text":"Hello from my phone"}`)
	if reply.Code != http.StatusAccepted {
		t.Fatalf("the phone message was refused: %d %s", reply.Code, reply.Body.String())
	}
	mobileTestWaitFor(t, "the turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
	items, err := store.LoadStreamItems(t.Context(), mobileTestChatID)
	if err != nil {
		t.Fatal(err)
	}
	var sent, answer *storage.StreamItemRow
	for i := range items {
		switch {
		case items[i].Kind == "user" && items[i].Content == "Hello from my phone":
			sent = &items[i]
		case items[i].Kind == "assistant" && items[i].Content == "Canned reply.":
			answer = &items[i]
		}
	}
	if sent == nil || sent.ToolInput != `{"origin":"phone"}` {
		t.Fatalf("the phone message is not marked as from the phone: %+v", sent)
	}
	if answer == nil || answer.SessionLocalID != mobileTestChatID {
		t.Fatalf("the answer did not land in the same chat: %+v", answer)
	}
}
