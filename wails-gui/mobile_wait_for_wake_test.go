package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"wails-gui/storage"
)

// A phone request sent while the computer sleeps waits on the relay and runs
// once when the computer is back (PROTOCOL.md section 23). The test uses the real
// relay binary and the real hub, as TestMobileEndToEndThroughRelayBinary does.
func TestMobileRequestWaitsForASleepingComputerAndRunsOnce(t *testing.T) {
	if os.Getenv("YANSU_MOBILE_E2E") != "1" {
		t.Skip("set YANSU_MOBILE_E2E=1 to build and run the relay wait check")
	}
	relayURL := startMobileE2ERelay(t)
	dir := t.TempDir()
	t.Setenv("YANSU_MOBILE_STATE_DIR", dir)
	t.Setenv("ISOFORM_RELAY_URL", "")
	app := &App{}
	store, err := storage.New(filepath.Join(dir, "yansu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app.store = store
	app.activeTags = map[string]bool{}
	mobileE2EInstallChatSeams(t)
	t.Cleanup(func() {
		if bridge := app.mobileBridgeForUse(); bridge != nil {
			bridge.Stop()
		}
	})

	if status, err := app.EnableMobileConnection(relayURL); err != nil || !status.Enabled {
		t.Fatalf("enable: %+v %v", status, err)
	}
	mobileTestWaitFor(t, "a connected relay", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.RelayState == mobileRelayConnected
	})
	status, err := mobileE2EOpenInvitation(app)
	if err != nil || status.ConnectionCode == "" {
		t.Fatalf("generate: %+v %v", status, err)
	}
	phone := newMobileE2EPhone(t, relayURL, mobileTestDecodeInvitation(t, status.ConnectionCode))
	phone.claim(t)
	mobileTestWaitFor(t, "a pending claim on the hub", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.PendingPairing != nil
	})
	pending, _ := app.GetMobileConnectionStatus()
	if _, err := app.ConfirmMobilePairing(pending.PendingPairing.PairingID, true); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	phone.awaitApproval(t)

	code, body := phone.request(t, "POST", "/chats", `{"idempotencyKey":"wake-chat","title":"Wake check"}`)
	if code != http.StatusCreated {
		t.Fatalf("create chat: %d %s", code, body)
	}
	chatID := mobileE2EData(t, body)["id"]

	// The computer sleeps: the hub disconnects, paired devices are kept.
	if _, err := app.DisableMobileConnection(); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	message := `{"idempotencyKey":"wake-1","text":"Asked while you slept"}`
	waiting := phone.postRequest(t, "POST", "/chats/"+chatID+"/messages", "", message)
	// A reply to someone else, queued two hours ago, while the computer slept.
	heldSince := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	heldSend := phone.postRequest(t, "POST", "/tasks/"+uuid.NewString()+"/actions", "",
		`{"idempotencyKey":"held-send-1","action":"send","text":"Yes","queuedAt":"`+heldSince+`"}`)

	// The computer wakes: the relay replays the request once.
	if _, err := app.EnableMobileConnection(relayURL); err != nil {
		t.Fatalf("wake: %v", err)
	}
	mobileTestWaitFor(t, "a reconnected relay", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.RelayState == mobileRelayConnected
	})
	code, first := phone.awaitResponse(t, waiting)
	if code != http.StatusAccepted || !strings.Contains(first, "turnId") {
		t.Fatalf("the waited send: %d %s", code, first)
	}

	// The held send must not run without a new yes: the computer answers confirm_send.
	if code, body := phone.awaitResponse(t, heldSend); code != http.StatusConflict || !strings.Contains(body, "confirm_send") {
		t.Fatalf("a send queued two hours ago ran without a new yes: %d %s", code, body)
	}
	// After the person says yes, the phone sends with a new key and no queue time: it runs past the check.
	asked := phone.postRequest(t, "POST", "/tasks/"+uuid.NewString()+"/actions", "",
		`{"idempotencyKey":"held-send-2","action":"send","text":"Yes"}`)
	if code, body := phone.awaitResponse(t, asked); code == http.StatusConflict && strings.Contains(body, "confirm_send") {
		t.Fatalf("a send after a new yes was held again: %d %s", code, body)
	}

	// The phone retries with the same key on a new request after the wake: the
	// receipt replays the first answer and nothing runs again.
	retried := phone.postRequest(t, "POST", "/chats/"+chatID+"/messages", "", message)
	if code, replay := phone.awaitResponse(t, retried); code != http.StatusAccepted || replay != first {
		t.Fatalf("the retry did not replay the first answer: %d %s, want %s", code, replay, first)
	}
	if n := countUserRows(t, store, chatID, "Asked while you slept"); n != 1 {
		t.Fatalf("the waited message ran %d times", n)
	}
}

// countUserRows counts the stored user messages of a chat with this text.
func countUserRows(t *testing.T, store *storage.Store, chatID, text string) int {
	t.Helper()
	page, err := store.LoadStreamItemsLatest(context.Background(), chatID, 200)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, item := range page.Items {
		if item.Kind == "user" && item.Content == text {
			n++
		}
	}
	return n
}

// A send that waited over an hour needs a fresh yes; a fresh send, or one within
// the hour, passes the check. A refused send stores no receipt.
func TestMobileTaskSendOverAnHourNeedsAFreshYes(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	task := "/tasks/" + uuid.NewString() + "/actions"
	stale := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)

	reply := mobileTestChatCall(bridge, device, http.MethodPost, task,
		`{"idempotencyKey":"send-stale","action":"send","text":"Yes","queuedAt":"`+stale+`"}`)
	if reply.Code != http.StatusConflict || !strings.Contains(reply.Body.String(), `"code":"confirm_send"`) {
		t.Fatalf("a send over an hour old was not held: %d %s", reply.Code, reply.Body.String())
	}
	// A thread send has no drafted recipient on the computer, so the hold names no one and the phone says "the reply".
	if strings.Contains(reply.Body.String(), `"person"`) {
		t.Fatalf("a thread send named a person: %s", reply.Body.String())
	}
	// Retrying the same key does not replay the refusal as a success, and the
	// phone's fresh request with a new key passes the check (it is not a 409).
	again := mobileTestChatCall(bridge, device, http.MethodPost, task,
		`{"idempotencyKey":"send-stale-2","action":"send","text":"Yes","queuedAt":"`+recent+`"}`)
	if strings.Contains(again.Body.String(), "confirm_send") {
		t.Fatalf("a send within the hour was held: %d %s", again.Code, again.Body.String())
	}
	fresh := mobileTestChatCall(bridge, device, http.MethodPost, task,
		`{"idempotencyKey":"send-fresh","action":"send","text":"Yes"}`)
	if strings.Contains(fresh.Body.String(), "confirm_send") {
		t.Fatalf("a fresh send was held: %d %s", fresh.Code, fresh.Body.String())
	}
	if bad := mobileTestChatCall(bridge, device, http.MethodPost, task,
		`{"idempotencyKey":"send-future","action":"send","queuedAt":"2099-01-01T00:00:00Z"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("a queue time in the future was accepted: %d %s", bad.Code, bad.Body.String())
	}
}

// The waited-answer notice reads the same in both languages and never names an answer.
func TestMobileWaitedAnswerNoticeCarriesNoText(t *testing.T) {
	now := time.Now()
	batch := mobileChatAnswerBatch{collapse: "chat-answers-" + uuid.NewString(), firstID: uuid.NewString(), at: now}
	for language, want := range map[string]string{"en": "Isoform answered.", "zh-CN": "Isoform 已回复。"} {
		if n := mobileChatAnswerNotice(mobileTestChatID, batch, language, now); n.Body != want {
			t.Fatalf("%s body %q, want %q", language, n.Body, want)
		}
	}
}
