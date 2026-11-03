package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
	"yansu-agent/agent"
)

const mobileTestChatID = "chat-1"

// mobileTestChatBridge returns a bridge over a real store with one visible chat
// session, and no agent runner.
func mobileTestChatBridge(t *testing.T) (*mobileBridge, *storage.Store) {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "yansu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dir := t.TempDir()
	project := storage.ProjectRow{ID: "project-1", Name: "Repo", Path: filepath.Join(dir, "repo")}
	workspaces := []storage.WorkspaceRow{{ID: "workspace-1", ProjectID: project.ID, Label: "Main", IsDefault: true}}
	if err := store.EnsureProjectStructure(t.Context(), project, workspaces, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(t.Context(), storage.SessionRow{LocalID: mobileTestChatID, ProjectID: project.ID, WorkspaceID: "workspace-1", Label: "Design notes"}); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, activeTags: map[string]bool{}}
	bridge, err := newMobileBridge(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge, store
}

func mobileTestChatCall(bridge *mobileBridge, device mobileDeviceRecord, method, path, body string) *httptest.ResponseRecorder {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, "/mobile/v1"+path, nil)
	} else {
		request = httptest.NewRequest(method, "/mobile/v1"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	bridge.handleAuthorized(recorder, request, device)
	return recorder
}

// mobileTestReply decodes the standard response document.
type mobileTestReply struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

func mobileTestDecodeReply(t *testing.T, recorder *httptest.ResponseRecorder) mobileTestReply {
	t.Helper()
	var reply mobileTestReply
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode reply %q: %v", recorder.Body.String(), err)
	}
	return reply
}

type mobileTestChatDetail struct {
	Chat struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		State string `json:"state"`
		Kind  string `json:"kind"`
	} `json:"chat"`
	Items   []mobileChatItemView `json:"items"`
	Cursor  int64                `json:"cursor"`
	HasMore bool                 `json:"hasMore"`
}

func mobileTestChatScopes() []string {
	return []string{mobileScopeChatRead, mobileScopeChatWrite, mobileScopeMemoryFix, mobileScopeHandoff}
}

// mobileTestTurns records the prompts a test override received.
type mobileTestTurns struct {
	mu      sync.Mutex
	prompts []string
}

func (turns *mobileTestTurns) record(prompt string) {
	turns.mu.Lock()
	turns.prompts = append(turns.prompts, prompt)
	turns.mu.Unlock()
}

func (turns *mobileTestTurns) count() int {
	turns.mu.Lock()
	defer turns.mu.Unlock()
	return len(turns.prompts)
}

// mobileTestMessageOverride answers a turn without a model: "hold" waits until
// the turn is cancelled, anything else gets a canned reply that is stored the
// way the renderer would store it.
func mobileTestMessageOverride(turns *mobileTestTurns) func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
	return func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
		turns.record(prompt)
		app.emitAgentEvent(AgentEvent{Type: "run_created", Tag: chatID, RunID: "run-" + turnID})
		if prompt == "hold" {
			<-cancelled
		} else {
			app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: chatID, RunID: "run-" + turnID, Content: "Canned reply."})
			if _, err := app.store.AppendStreamItem(context.Background(), chatID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "assistant", Content: "Canned reply."}); err != nil {
				panic(err)
			}
		}
		app.emitAgentEvent(AgentEvent{Type: "close", Tag: chatID, RunID: "run-" + turnID})
	}
}

// mobileTestResetChatRunner joins every override before cleanup closes its store
// or restores package-level test seams. A cancelled turn can still emit events
// and write its final rows until the override returns.
func mobileTestResetChatRunner(t *testing.T, bridge *mobileBridge) {
	t.Helper()
	bridge.chat.mu.Lock()
	for _, cancel := range bridge.chat.overrides {
		cancel()
	}
	bridge.chat.mu.Unlock()
	mobileTestWaitFor(t, "chat runners to finish before cleanup", func() bool {
		bridge.chat.mu.Lock()
		defer bridge.chat.mu.Unlock()
		return len(bridge.chat.overrides) == 0
	})
	mobileChatRunnerOverride = nil
}

func TestMobileChatCleanupJoinsRunnerBeforeClosingStore(t *testing.T) {
	finished := make(chan error, 1)
	t.Run("held turn", func(t *testing.T) {
		bridge, _ := mobileTestChatBridge(t)
		mobileChatRunnerOverride = func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
			<-cancelled
			_, err := app.store.AppendStreamItem(context.Background(), chatID, storage.StreamItemRow{
				ID: uuid.NewString(), Kind: "assistant", Content: "Stopped.",
			})
			finished <- err
		}
		t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
		reply := mobileTestChatCall(bridge, mobileTestDevice(mobileTestChatScopes()...), http.MethodPost,
			"/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"cleanup","text":"hold"}`)
		if reply.Code != http.StatusAccepted {
			t.Fatalf("send: %d %s", reply.Code, reply.Body.String())
		}
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("runner lost its store during cleanup: %v", err)
		}
	default:
		t.Fatal("cleanup returned before the runner finished")
	}
}

func TestMobileChatRoutesEnforceScopesAndValidation(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	mobileMemoryRejectOverride = func(memoryID, reason string) error {
		if memoryID == "missing" {
			return &fs.PathError{Op: "stat", Path: memoryID, Err: fs.ErrNotExist}
		}
		return nil
	}
	t.Cleanup(func() {
		mobileTestResetChatRunner(t, bridge)
		mobileMemoryRejectOverride = nil
	})
	all := mobileTestChatScopes()
	// An iPhone paired before the Desk was removed still holds its scopes.
	retired := append(mobileTestChatScopes(), mobileScopeDeskRead, mobileScopeDecide)
	message := `{"idempotencyKey":"m1","text":"hello"}`
	cases := []struct {
		name, method, path, body string
		scopes                   []string
		want                     int
		contains                 string
	}{
		{"list needs chat:read", http.MethodGet, "/chats", "", []string{mobileScopeDeskRead}, http.StatusForbidden, "chat:read"},
		{"list rejects a zero limit", http.MethodGet, "/chats?limit=0", "", []string{mobileScopeChatRead}, http.StatusBadRequest, "between 1 and 50"},
		{"list rejects a limit above 50", http.MethodGet, "/chats?limit=51", "", []string{mobileScopeChatRead}, http.StatusBadRequest, "between 1 and 50"},
		{"list returns visible chats", http.MethodGet, "/chats", "", []string{mobileScopeChatRead}, http.StatusOK, `"chats"`},
		{"create needs chat:write", http.MethodPost, "/chats", `{"idempotencyKey":"c1","title":"x"}`, []string{mobileScopeChatRead}, http.StatusForbidden, "chat:write"},
		{"create needs a key", http.MethodPost, "/chats", `{"idempotencyKey":"","title":"x"}`, all, http.StatusBadRequest, "idempotency key"},
		{"create rejects long titles", http.MethodPost, "/chats", `{"idempotencyKey":"c2","title":"` + strings.Repeat("t", 201) + `"}`, all, http.StatusBadRequest, "200 bytes"},
		{"detail needs chat:read", http.MethodGet, "/chats/" + mobileTestChatID, "", []string{mobileScopeChatWrite}, http.StatusForbidden, "chat:read"},
		{"detail rejects a bad id", http.MethodGet, "/chats/bad%20id", "", all, http.StatusBadRequest, "not valid"},
		{"detail rejects a negative cursor", http.MethodGet, "/chats/" + mobileTestChatID + "?after=-1", "", all, http.StatusBadRequest, "sequence number"},
		{"detail is 404 for an unknown chat", http.MethodGet, "/chats/nope", "", all, http.StatusNotFound, "no longer on your computer"},
		{"detail reads a chat", http.MethodGet, "/chats/" + mobileTestChatID, "", all, http.StatusOK, `"items"`},
		{"message needs chat:write", http.MethodPost, "/chats/" + mobileTestChatID + "/messages", message, []string{mobileScopeChatRead}, http.StatusForbidden, "chat:write"},
		{"message needs a key", http.MethodPost, "/chats/" + mobileTestChatID + "/messages", `{"idempotencyKey":"","text":"hi"}`, all, http.StatusBadRequest, "idempotency key"},
		{"message rejects empty text", http.MethodPost, "/chats/" + mobileTestChatID + "/messages", `{"idempotencyKey":"m2","text":"   "}`, all, http.StatusBadRequest, "1 to 16384 bytes"},
		{"message rejects text over 16 KiB", http.MethodPost, "/chats/" + mobileTestChatID + "/messages", `{"idempotencyKey":"m3","text":"` + strings.Repeat("a", 16*1024+1) + `"}`, all, http.StatusBadRequest, "1 to 16384 bytes"},
		{"message to an unknown chat is 404", http.MethodPost, "/chats/nope/messages", `{"idempotencyKey":"m4","text":"hi"}`, all, http.StatusNotFound, "no longer on your computer"},
		{"message is accepted", http.MethodPost, "/chats/" + mobileTestChatID + "/messages", message, all, http.StatusAccepted, `"turnId"`},
		{"cancel answers with the outcome", http.MethodPost, "/chats/" + mobileTestChatID + "/cancel", `{"idempotencyKey":"x1"}`, all, http.StatusOK, `"cancelled"`},
		{"permission for an unknown request is 404", http.MethodPost, "/chats/" + mobileTestChatID + "/permissions/none", `{"idempotencyKey":"p1","optionId":"allow"}`, all, http.StatusNotFound, "no such pending request"},
		{"memory correction needs memory:correct", http.MethodPost, "/memory/mem-1/reject-attribution", `{"idempotencyKey":"r1","reason":"no"}`, []string{mobileScopeChatWrite}, http.StatusForbidden, "memory:correct"},
		{"memory correction removes the entry", http.MethodPost, "/memory/mem-1/reject-attribution", `{"idempotencyKey":"r2","reason":"no"}`, all, http.StatusOK, `"removed":true`},
		{"memory correction is 404 for an unknown entry", http.MethodPost, "/memory/missing/reject-attribution", `{"idempotencyKey":"r3","reason":"no"}`, all, http.StatusNotFound, "no longer on your computer"},
		{"directive retract needs the stores", http.MethodPost, "/self/directives/dir-1/retract", `{"idempotencyKey":"d1"}`, all, http.StatusServiceUnavailable, "not ready"},
		{"the Desk is gone", http.MethodGet, "/desk", "", retired, http.StatusNotFound, "route not found"},
		{"decisions are gone", http.MethodPost, "/decision", `{"idempotencyKey":"k1"}`, retired, http.StatusNotFound, "route not found"},
		{"new purposes are gone", http.MethodPost, "/purposes", `{"idempotencyKey":"v1"}`, retired, http.StatusNotFound, "route not found"},
		{"purpose revise is gone", http.MethodPost, "/purposes/op-1/revise", `{"idempotencyKey":"v2","title":"t","desiredOutcome":"d"}`, retired, http.StatusNotFound, "route not found"},
		{"purpose dismiss is gone", http.MethodPost, "/purposes/op-1/dismiss", `{"idempotencyKey":"v3","reason":"done"}`, retired, http.StatusNotFound, "route not found"},
		{"initiatives are gone", http.MethodGet, "/initiatives/i-1", "", retired, http.StatusNotFound, "route not found"},
		{"Self update review is gone", http.MethodPost, "/self/updates/op-1/review", `{"idempotencyKey":"s1","decision":"accept"}`, retired, http.StatusNotFound, "route not found"},
		{"handoff needs handoff:create", http.MethodPost, "/handoffs", `{"idempotencyKey":"h1","title":"t","brief":"b","workDir":""}`, []string{mobileScopeChatRead}, http.StatusForbidden, "handoff:create"},
		{"handoff needs a title", http.MethodPost, "/handoffs", `{"idempotencyKey":"h2","title":"  ","brief":"b","workDir":""}`, all, http.StatusBadRequest, "1 to 200 bytes"},
		{"handoff needs an absolute folder", http.MethodPost, "/handoffs", `{"idempotencyKey":"h3","title":"t","brief":"b","workDir":"relative"}`, all, http.StatusBadRequest, "absolute folder"},
		{"handoff needs the handoff store", http.MethodPost, "/handoffs", `{"idempotencyKey":"h4","title":"t","brief":"b","workDir":""}`, all, http.StatusServiceUnavailable, "not ready"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := mobileTestChatCall(bridge, mobileTestDevice(tc.scopes...), tc.method, tc.path, tc.body)
			if recorder.Code != tc.want || !strings.Contains(recorder.Body.String(), tc.contains) {
				t.Fatalf("status %d, want %d, body %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestMobileChatCreateAndMessageReplayFromReceipts(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	device := mobileTestDevice(mobileTestChatScopes()...)

	first := mobileTestChatCall(bridge, device, http.MethodPost, "/chats", `{"idempotencyKey":"create-1","title":"From the phone"}`)
	again := mobileTestChatCall(bridge, device, http.MethodPost, "/chats", `{"idempotencyKey":"create-1","title":"From the phone"}`)
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated || first.Body.String() != again.Body.String() {
		t.Fatalf("create was not replayed: %d %s / %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(mobileTestDecodeReply(t, first).Data, &created); err != nil || created.ID == "" {
		t.Fatalf("created id: %v %s", err, first.Body.String())
	}
	if rows, err := store.MobileChatList(t.Context(), 10); err != nil || len(rows) != 2 {
		t.Fatalf("a replayed create must not add a second chat: %d rows, err %v", len(rows), err)
	}

	sent := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-1","text":"Summarize"}`)
	resent := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-1","text":"Summarize"}`)
	if sent.Code != http.StatusAccepted || resent.Body.String() != sent.Body.String() {
		t.Fatalf("send was not replayed: %s / %s", sent.Body.String(), resent.Body.String())
	}
	mobileTestWaitFor(t, "the first turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
	if turns.count() != 1 {
		t.Fatalf("a replayed send ran the turn %d times", turns.count())
	}

	// The same key on another route is a conflict, not a replay of the wrong answer.
	conflict := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+created.ID+"/messages", `{"idempotencyKey":"send-1","text":"Summarize"}`)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "another request") {
		t.Fatalf("key reused on another route: %d %s", conflict.Code, conflict.Body.String())
	}

	// A different phone may use the same idempotency text without colliding.
	other := mobileTestDevice(mobileTestChatScopes()...)
	other.DeviceID = "other-device"
	otherSend := mobileTestChatCall(bridge, other, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-1","text":"Summarize"}`)
	if otherSend.Code != http.StatusAccepted || otherSend.Body.String() == sent.Body.String() {
		t.Fatalf("another device's key collided: %d %s", otherSend.Code, otherSend.Body.String())
	}
	mobileTestWaitFor(t, "the second turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
}

func TestMobileChatAcceptedMessageNamesItsStoredItem(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })

	var accepted []struct {
		TurnID string `json:"turnId"`
		Seq    int64  `json:"seq"`
	}
	for _, send := range []string{`{"idempotencyKey":"seq-1","text":"yes"}`, `{"idempotencyKey":"seq-2","text":"yes"}`} {
		reply := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", send)
		if reply.Code != http.StatusAccepted {
			t.Fatalf("send: %d %s", reply.Code, reply.Body.String())
		}
		var body struct {
			TurnID string `json:"turnId"`
			Seq    int64  `json:"seq"`
		}
		if err := json.Unmarshal(mobileTestDecodeReply(t, reply).Data, &body); err != nil || body.TurnID == "" {
			t.Fatalf("accepted body: %v %s", err, reply.Body.String())
		}
		accepted = append(accepted, body)
		mobileTestWaitFor(t, "the turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
	}

	// Two messages with the same text are told apart by the seq the Mac gave each.
	detail := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID, "")
	var page mobileTestChatDetail
	if err := json.Unmarshal(mobileTestDecodeReply(t, detail).Data, &page); err != nil {
		t.Fatal(err)
	}
	for _, sent := range accepted {
		found := false
		for _, item := range page.Items {
			found = found || (item.Seq == sent.Seq && item.Role == "user" && item.Text == "yes")
		}
		if !found {
			t.Fatalf("seq %d does not name a stored user item: %+v", sent.Seq, page.Items)
		}
	}
	if accepted[0].Seq >= accepted[1].Seq {
		t.Fatalf("seqs out of order: %+v", accepted)
	}

	replay := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"seq-1","text":"yes"}`)
	if !strings.Contains(replay.Body.String(), fmt.Sprintf(`"seq":%d`, accepted[0].Seq)) {
		t.Fatalf("a replay must answer the same seq: %s", replay.Body.String())
	}
}

func TestMobileChatBusyChatAnswers409AndKeepsNoRow(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	bridge.app.mu.Lock()
	bridge.app.activeTags[mobileTestChatID] = true
	bridge.app.mu.Unlock()

	busy := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"busy-1","text":"hi"}`)
	if busy.Code != http.StatusConflict || mobileTestDecodeReply(t, busy).Error != "busy" {
		t.Fatalf("busy send: %d %s", busy.Code, busy.Body.String())
	}
	items, err := store.LoadStreamItems(t.Context(), mobileTestChatID)
	if err != nil || len(items) != 0 {
		t.Fatalf("a rejected send wrote %d rows (err %v)", len(items), err)
	}
	// A busy answer is not stored, so the same key runs once the chat is idle.
	bridge.app.mu.Lock()
	delete(bridge.app.activeTags, mobileTestChatID)
	bridge.app.mu.Unlock()
	mobileChatRunnerOverride = func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
		app.emitAgentEvent(AgentEvent{Type: "close", Tag: chatID})
	}
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	retry := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"busy-1","text":"hi"}`)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry after busy: %d %s", retry.Code, retry.Body.String())
	}
}

func TestMobileChatSourceGateCoversEarlierPagesAndTransientPermission(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	rows := []storage.StreamItemRow{
		{ID: "safe-prefix", Kind: "user", Content: "ordinary text"},
		// Earlier builds retained references without the durable source proof.
		{ID: "legacy-idea", Kind: "user", Content: "withdrawn source", AutomationContext: `{"offerReferences":[{"id":"suggestion:old","version":"old-version"}]}`},
		{ID: "derived-reply", Kind: "assistant", Content: "withdrawn source echo"},
		{ID: "derived-tool", Kind: "tool", ToolInput: `{"query":"withdrawn source"}`},
		{ID: "later-user", Kind: "user", Content: "follow up"},
	}
	for _, row := range rows {
		if _, err := store.AppendStreamItem(t.Context(), mobileTestChatID, row); err != nil {
			t.Fatal(err)
		}
	}
	bridge.chat.pending["source-permission"] = &mobileChatPermission{RequestID: "source-permission", ChatID: mobileTestChatID, Text: "withdrawn source permission"}
	bridge.chat.byChat[mobileTestChatID] = "source-permission"
	for _, query := range []string{"?limit=2", "?after=2&limit=2", "?after=4&limit=2"} {
		recorder := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID+query, "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", query, recorder.Code, recorder.Body.String())
		}
		var detail mobileTestChatDetail
		if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail.Items) != 0 || detail.Cursor < 4 {
			t.Fatalf("%s disclosed dependent rows or lost its cursor: %+v", query, detail)
		}
	}
	first := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID+"?after=0&limit=2", "")
	var detail mobileTestChatDetail
	if err := json.Unmarshal(mobileTestDecodeReply(t, first).Data, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Items) != 1 || detail.Items[0].Text != "ordinary text" || detail.Cursor != 2 || !detail.HasMore {
		t.Fatalf("unrelated prefix or hidden-row pagination changed: %+v", detail)
	}
}

func TestMobileChatPageStaysWithinSealedLimit(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	ctx := t.Context()
	// Quote-heavy text escapes at its worst, so twenty large rows exceed one page.
	big := strings.Repeat(`"`, mobileChatTextMax)
	for i := 0; i < 20; i++ {
		if _, err := store.AppendStreamItem(ctx, mobileTestChatID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "user", Content: big}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AppendStreamItem(ctx, mobileTestChatID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "thinking", Content: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendStreamItem(ctx, mobileTestChatID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "assistant", Content: "last"}); err != nil {
		t.Fatal(err)
	}

	var seqs []int64
	var after int64
	pages := 0
	for {
		recorder := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID+"?after="+strconv.FormatInt(after, 10), "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("page after %d: %d %s", after, recorder.Code, recorder.Body.String())
		}
		plaintext, err := json.Marshal(mobileResponsePlaintext{ID: uuid.NewString(), Status: http.StatusOK, Body: strings.TrimSuffix(recorder.Body.String(), "\n")})
		if err != nil || len(plaintext) > mobileMaxMessagePlaintext {
			t.Fatalf("page after %d is %d bytes sealed, over the %d limit", after, len(plaintext), mobileMaxMessagePlaintext)
		}
		var detail mobileTestChatDetail
		if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &detail); err != nil {
			t.Fatal(err)
		}
		for _, item := range detail.Items {
			if len(item.Text) > mobileChatTextMax {
				t.Fatalf("item %d has %d bytes of text", item.Seq, len(item.Text))
			}
			seqs = append(seqs, item.Seq)
		}
		pages++
		if !detail.HasMore {
			break
		}
		if detail.Cursor <= after || pages > 40 {
			t.Fatalf("cursor did not advance: %d after %d", detail.Cursor, after)
		}
		after = detail.Cursor
	}
	// Thinking rows are internal and never appear; the other 21 rows arrive once, in order.
	if len(seqs) != 21 || pages < 2 {
		t.Fatalf("walked %d items over %d pages: %v", len(seqs), pages, seqs)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("sequence is not monotonic: %v", seqs)
		}
	}
	if seqs[0] != 1 {
		t.Fatalf("the first sequence number is %d, want 1 (seq is sort order plus one)", seqs[0])
	}
}

func TestMobileChatProjectionBoundsTextAndSummarizesTools(t *testing.T) {
	long := strings.Repeat("x", mobileChatTextMax*2)
	view, ok := mobileChatItemFromRow(storage.StreamItemRow{SortOrder: 4, Kind: "result", ToolName: "Read", Content: long, CreatedAt: time.Now().UnixMilli()})
	if !ok || view.Seq != 5 || view.Role != "tool" || view.Kind != "tool_result" || len(view.Text) > mobileChatTextMax || !strings.HasSuffix(view.Text, "[truncated]") {
		t.Fatalf("result projection: %+v", view)
	}
	secret := "line one\nline two " + strings.Repeat("y", 500)
	input, _ := json.Marshal(map[string]string{"file_path": "notes.md", "content": secret})
	tool, ok := mobileChatItemFromRow(storage.StreamItemRow{SortOrder: 0, Kind: "tool", ToolName: "Write", ToolInput: string(input)})
	if !ok || tool.Kind != "tool_use" || strings.Contains(tool.Text, "\n") || len(tool.Text) > mobileChatToolSummaryMax || !strings.Contains(tool.Text, "file_path: notes.md") {
		t.Fatalf("tool summary: %+v", tool)
	}
	if strings.Contains(tool.Text, strings.Repeat("y", 200)) {
		t.Fatal("tool summary carried more file contents than its bound")
	}
	if _, ok := mobileChatItemFromRow(storage.StreamItemRow{Kind: "thinking", Content: "internal"}); ok {
		t.Fatal("thinking rows must not reach the phone")
	}
	if got := mobileChatClip("h\u00e9llo", 2); got != "h" {
		t.Fatalf("clip split a rune: %q", got)
	}
}

func TestMobileChatCoalescerPublishesOncePerWindow(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	clock := func() time.Time { return now }
	c := newMobileChatCoalescer(time.Second, clock)

	if publish, wait := c.request("chat"); !publish || wait != 0 {
		t.Fatalf("first change must publish at once: %v %v", publish, wait)
	}
	now = now.Add(300 * time.Millisecond)
	if publish, wait := c.request("chat"); publish || wait != 700*time.Millisecond {
		t.Fatalf("change inside the window must wait for its end: %v %v", publish, wait)
	}
	now = now.Add(100 * time.Millisecond)
	if publish, wait := c.request("chat"); publish || wait != 0 {
		t.Fatalf("a trailing publish is already armed: %v %v", publish, wait)
	}
	now = now.Add(600 * time.Millisecond)
	c.fired("chat")
	now = now.Add(100 * time.Millisecond)
	if publish, wait := c.request("chat"); publish || wait != 900*time.Millisecond {
		t.Fatalf("a publish right after the trailing one must wait a full window: %v %v", publish, wait)
	}
	if publish, _ := c.request("other"); !publish {
		t.Fatal("chats are coalesced independently")
	}
}

func TestMobileChatTurnEventCarriesNoContent(t *testing.T) {
	raw, err := json.Marshal(mobileChatTurnEvent{Event: mobileEventChatTurn, ChatID: "c", TurnID: "t", State: mobileChatEventDone, Seq: 7, At: "2026-10-10T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"event", "chatId", "turnId", "state", "seq", "at"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("event misses %q: %s", key, raw)
		}
	}
	if len(fields) != 6 {
		t.Fatalf("event carries extra fields: %s", raw)
	}
}

func TestMobileChatPermissionIsVisibleOnlyWhilePending(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	bridge.observeChatEvent(AgentEvent{
		Type: "permission_request", Tag: mobileTestChatID, SessionID: "agent-session-1", ToolName: "ExitPlanMode",
		Content:           "Approve the plan?",
		PermissionOptions: []agent.PermissionOption{{ID: "acceptEdits", Name: "Accept"}, {ID: "reject", Name: "Reject"}},
	})
	recorder := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID, "")
	var detail mobileTestChatDetail
	if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Chat.State != mobileChatStateWaiting || len(detail.Items) == 0 {
		t.Fatalf("pending permission not shown: %+v", detail)
	}
	last := detail.Items[len(detail.Items)-1]
	if last.Kind != "permission_request" || last.Permission == nil || len(last.Permission.Options) != 2 || last.Seq != 1 {
		t.Fatalf("permission item: %+v", last)
	}
	requestID := last.Permission.RequestID

	bad := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/permissions/"+requestID, `{"idempotencyKey":"perm-1","optionId":"unknown"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("an option that was not offered: %d %s", bad.Code, bad.Body.String())
	}
	// Without a runner the answer cannot reach the blocked loop, so the phone is told it is no longer pending.
	stale := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/permissions/"+requestID, `{"idempotencyKey":"perm-2","optionId":"reject"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("an answer with no runner: %d %s", stale.Code, stale.Body.String())
	}

	bridge.observeChatEvent(AgentEvent{Type: "tool_result", Tag: mobileTestChatID, ToolName: "ExitPlanMode", Content: "done"})
	recorder = mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID, "")
	if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Chat.State == mobileChatStateWaiting {
		t.Fatal("a permission outlived the event that ended it")
	}
}

func TestMobileChatHandoffCreatesOneTimeWork(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = store
	device := mobileTestDevice(mobileTestChatScopes()...)
	recorder := mobileTestChatCall(bridge, device, http.MethodPost, "/handoffs", `{"idempotencyKey":"hand-1","title":"Draft the Q3 notes","brief":"Use the Figma file.","workDir":""}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &created); err != nil || !strings.HasPrefix(created.ID, "hf_") {
		t.Fatalf("created id: %v %s", err, recorder.Body.String())
	}
	entry, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != handoff.StatusActive || entry.Source != handoff.SourceMobile || strings.TrimSpace(entry.Body) != "Use the Figma file." || entry.Title != "Draft the Q3 notes" {
		t.Fatalf("stored handoff: %+v", entry)
	}
	replay := mobileTestChatCall(bridge, device, http.MethodPost, "/handoffs", `{"idempotencyKey":"hand-1","title":"Draft the Q3 notes","brief":"Use the Figma file.","workDir":""}`)
	if replay.Body.String() != recorder.Body.String() {
		t.Fatalf("handoff was not replayed: %s", replay.Body.String())
	}
	if all, _ := store.List(); len(all) != 1 {
		t.Fatalf("a replayed handoff created %d entries", len(all))
	}
}

func TestMobileChatCorrectionsRunOnTheSameReceipts(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	var removed []string
	mobileMemoryRejectOverride = func(memoryID, reason string) error {
		removed = append(removed, memoryID+":"+reason)
		return nil
	}
	t.Cleanup(func() { mobileMemoryRejectOverride = nil })
	device := mobileTestDevice(mobileTestChatScopes()...)
	first := mobileTestChatCall(bridge, device, http.MethodPost, "/memory/mem-9/reject-attribution", `{"idempotencyKey":"mem-1","reason":"Not me"}`)
	again := mobileTestChatCall(bridge, device, http.MethodPost, "/memory/mem-9/reject-attribution", `{"idempotencyKey":"mem-1","reason":"Not me"}`)
	if first.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Fatalf("correction replay: %s / %s", first.Body.String(), again.Body.String())
	}
	if len(removed) != 1 || removed[0] != "mem-9:Not me" {
		t.Fatalf("the correction ran %d times: %v", len(removed), removed)
	}
}

// mobileTestShortSettle makes the final chat_turn event, and the hub's own
// copy of a turn, land almost at once.
func mobileTestShortSettle(t *testing.T) {
	t.Helper()
	previous := mobileChatSettleDelay
	mobileChatSettleDelay = 20 * time.Millisecond
	t.Cleanup(func() { mobileChatSettleDelay = previous })
}

func TestMobileChatHubStoresTheTurnWhenNoWindowDid(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	mobileTestShortSettle(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	// A turn with no desktop window: events flow, nothing is stored by a renderer.
	mobileChatRunnerOverride = func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
		app.emitAgentEvent(AgentEvent{Type: "run_created", Tag: chatID, RunID: "run-1", SessionID: "agent-1"})
		app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: chatID, RunID: "run-1", Content: "Looking at the notes"})
		app.emitAgentEvent(AgentEvent{Type: "thinking", Tag: chatID, RunID: "run-1", Content: "private"})
		app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: chatID, RunID: "run-1", Content: " now."})
		app.emitAgentEvent(AgentEvent{Type: "tool_use", Tag: chatID, RunID: "run-1", ToolName: "Read", ToolUseID: "toolu_1", ToolInput: json.RawMessage(`{"file_path":"notes.md"}`)})
		app.emitAgentEvent(AgentEvent{Type: "tool_result", Tag: chatID, RunID: "run-1", ToolName: "Read", ToolUseID: "toolu_1", Content: strings.Repeat("x", mobileChatReplyToolMax+100)})
		app.emitAgentEvent(AgentEvent{Type: "run_created", Tag: chatID, RunID: "run-2", SessionID: "agent-1"})
		app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: chatID, RunID: "run-2", Content: "Three decisions, two follow-ups."})
		app.emitAgentEvent(AgentEvent{Type: "close", Tag: chatID, RunID: "run-2"})
	}
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })

	sent := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-1","text":"Summarize"}`)
	if sent.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", sent.Code, sent.Body.String())
	}
	mobileTestWaitFor(t, "the hub to store the turn", func() bool {
		items, _ := store.LoadStreamItems(t.Context(), mobileTestChatID)
		return len(items) >= 5
	})
	items, err := store.LoadStreamItems(t.Context(), mobileTestChatID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(items))
	for _, item := range items {
		kinds = append(kinds, item.Kind)
	}
	if strings.Join(kinds, ",") != "user,assistant,tool,result,assistant" {
		t.Fatalf("stored rows: %v", kinds)
	}
	if items[1].Content != "Looking at the notes now." || items[1].RunID != "run-1" {
		t.Fatalf("first assistant row: %+v", items[1])
	}
	if items[2].ToolName != "Read" || items[2].ToolUseID != "toolu_1" || items[2].ToolInput != `{"file_path":"notes.md"}` || items[2].Content != "" {
		t.Fatalf("tool row: %+v", items[2])
	}
	if len(items[3].Content) != mobileChatReplyToolMax || items[3].ToolUseID != "toolu_1" || items[3].ToolName != "Read" {
		t.Fatalf("result row must be clipped to %d bytes: %d", mobileChatReplyToolMax, len(items[3].Content))
	}
	if items[4].Content != "Three decisions, two follow-ups." || items[4].RunID != "run-2" {
		t.Fatalf("second assistant row: %+v", items[4])
	}
	for i := 1; i < len(items); i++ {
		if items[i].SortOrder <= items[i-1].SortOrder {
			t.Fatalf("rows out of order: %+v", items)
		}
	}

	// The phone reads the reply the way it reads every turn.
	recorder := mobileTestChatCall(bridge, device, http.MethodGet, "/chats/"+mobileTestChatID, "")
	var detail mobileTestChatDetail
	if err := json.Unmarshal(mobileTestDecodeReply(t, recorder).Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Chat.State != mobileChatStateIdle || len(detail.Items) != 5 || detail.Items[2].Kind != "tool_use" || detail.Items[3].Kind != "tool_result" || detail.Items[4].Text != "Three decisions, two follow-ups." {
		t.Fatalf("phone view after the hub stored the turn: %+v", detail)
	}

	// A failed turn keeps its error, so the phone can show why nothing came back.
	mobileChatRunnerOverride = func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
		app.emitAgentEvent(AgentEvent{Type: "run_created", Tag: chatID, RunID: "run-3"})
		app.emitAgentEvent(AgentEvent{Type: "error", Tag: chatID, RunID: "run-3", Error: "model capacity"})
		app.emitAgentEvent(AgentEvent{Type: "close", Tag: chatID, RunID: "run-3"})
	}
	failed := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-2","text":"Again"}`)
	if failed.Code != http.StatusAccepted {
		t.Fatalf("second send: %d %s", failed.Code, failed.Body.String())
	}
	mobileTestWaitFor(t, "the hub to store the error", func() bool {
		items, _ := store.LoadStreamItems(t.Context(), mobileTestChatID)
		return len(items) >= 7
	})
	items, _ = store.LoadStreamItems(t.Context(), mobileTestChatID)
	last := items[len(items)-1]
	if len(items) != 7 || items[5].Kind != "user" || last.Kind != "error" || last.Content != "model capacity" {
		t.Fatalf("error turn rows: %+v", items)
	}
}

func TestMobileChatHubStaysOutWhenAWindowStoredTheTurn(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	mobileTestShortSettle(t)
	device := mobileTestDevice(mobileTestChatScopes()...)
	turns := &mobileTestTurns{}
	// The canned override stores the reply the way the desktop renderer does.
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })

	sent := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-1","text":"Summarize"}`)
	if sent.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", sent.Code, sent.Body.String())
	}
	mobileTestWaitFor(t, "the turn to finish", func() bool { return !bridge.chatRunning(mobileTestChatID) })
	time.Sleep(10 * mobileChatSettleDelay)
	items, err := store.LoadStreamItems(t.Context(), mobileTestChatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Kind != "user" || items[1].Kind != "assistant" || items[1].Content != "Canned reply." {
		t.Fatalf("the hub must not store a second copy of a turn a window stored: %+v", items)
	}

	// A cancelled turn with nothing said stores nothing either.
	held := mobileTestChatCall(bridge, device, http.MethodPost, "/chats/"+mobileTestChatID+"/messages", `{"idempotencyKey":"send-2","text":"hold"}`)
	if held.Code != http.StatusAccepted {
		t.Fatalf("held send: %d %s", held.Code, held.Body.String())
	}
	mobileTestWaitFor(t, "the held turn to start", func() bool { return turns.count() == 2 })
	if !bridge.cancelChatTurn(mobileTestChatID) {
		t.Fatal("cancel found no running turn")
	}
	mobileTestWaitFor(t, "the held turn to end", func() bool { return !bridge.chatRunning(mobileTestChatID) })
	time.Sleep(10 * mobileChatSettleDelay)
	items, _ = store.LoadStreamItems(t.Context(), mobileTestChatID)
	if len(items) != 3 || items[2].Kind != "user" {
		t.Fatalf("a cancelled turn with no reply added rows: %+v", items)
	}
}

func TestMobileChatReplyFoldsEventsLikeTheRenderer(t *testing.T) {
	reply := &mobileChatReply{turnID: "t"}
	reply.note(AgentEvent{Type: "assistant", RunID: "r1", Content: "a"})
	reply.note(AgentEvent{Type: "assistant", RunID: "r1", Content: "b"})
	reply.note(AgentEvent{Type: "tool_use", RunID: "r1", ToolName: "Bash", ToolUseID: "u1"})
	reply.note(AgentEvent{Type: "tool_result", RunID: "r1", ToolName: "Bash", ToolUseID: "u1"})
	reply.note(AgentEvent{Type: "assistant", RunID: "r1", Content: "c"})
	reply.note(AgentEvent{Type: "assistant", RunID: "r2", Content: "d"})
	reply.note(AgentEvent{Type: "assistant", RunID: "r2", Content: ""})
	reply.note(AgentEvent{Type: "error", RunID: "r2", Content: "fallback text"})
	reply.flush()
	want := []struct{ kind, content, run string }{
		{"assistant", "ab", "r1"}, {"tool", "", "r1"}, {"result", "Result received.", "r1"}, {"assistant", "c", "r1"}, {"assistant", "d", "r2"}, {"error", "fallback text", "r2"},
	}
	if len(reply.rows) != len(want) {
		t.Fatalf("rows: %+v", reply.rows)
	}
	for i, row := range reply.rows {
		if row.Kind != want[i].kind || row.Content != want[i].content || row.RunID != want[i].run || row.ID == "" {
			t.Fatalf("row %d = %+v, want %+v", i, row, want[i])
		}
	}
	reply.flush()
	if len(reply.rows) != len(want) {
		t.Fatal("a second flush with nothing open added a row")
	}
}
