package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func mobileTestDraftBridge(t *testing.T) (*mobileBridge, *fakeProactiveSendTransport) {
	t.Helper()
	app, fake, _ := outboundSendTestCard(t)
	var err error
	app.handoffStore, err = handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(app, filepath.Join(t.TempDir(), "mobile"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge, fake
}

func mobileTestDraftList(t *testing.T, bridge *mobileBridge) MobileTaskPage {
	t.Helper()
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("tasks answered %d %s", code, body)
	}
	var reply struct {
		Data MobileTaskPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Data
}

func TestMobileDraftTaskWaitsInNeedsYouWithItsCard(t *testing.T) {
	bridge, fake := mobileTestDraftBridge(t)
	page := mobileTestDraftList(t, bridge)
	if len(page.Tasks) != 1 || page.Counts.NeedsYou != 1 {
		t.Fatalf("page = %+v", page)
	}
	row := page.Tasks[0]
	if !strings.HasPrefix(row.ID, mobileDraftTaskPrefix) || row.Status != "draft_ready" || row.Group != mobileTaskGroupNeedsYou || row.Title == "" || row.Source != "mail" {
		t.Fatalf("draft row = %+v", row)
	}
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/tasks/"+row.ID, "")
	if code != http.StatusOK {
		t.Fatalf("detail answered %d %s", code, body)
	}
	detail := mobileTestDecodeThread(t, body)
	draft := detail.Draft
	if draft == nil || draft.Channel != "mail" || draft.Body != "Yes, that works." || draft.State != "ready" ||
		draft.Revision == "" || draft.Recipients == nil || draft.Recipients.To[0] != "Dana Ruiz" || draft.Name != "Dana Ruiz" {
		t.Fatalf("draft = %+v", draft)
	}
	if draft.Why == nil || draft.Why.Source != "mail" || draft.Why.Text != "Dana Ruiz is waiting on your reply." ||
		draft.Why.At != time.UnixMilli(mobileTestDraftInboundAt(t, bridge)).UTC().Format(time.RFC3339) {
		t.Fatalf("why = %+v", draft.Why)
	}
	if strings.Join(detail.Actions, ",") != "send,not_now" {
		t.Fatalf("actions = %v", detail.Actions)
	}
	if last := detail.Events[len(detail.Events)-1]; last.Text != "Here's the reply. Nothing goes until you tap Send." {
		t.Fatalf("events = %+v", detail.Events)
	}
	if fake.count() != 0 {
		t.Fatal("reading a draft must never send it")
	}
}

func TestMobileDraftTaskRejectsStaleRevisionBeforeSend(t *testing.T) {
	bridge, fake := mobileTestDraftBridge(t)
	id := mobileTestDraftList(t, bridge).Tasks[0].ID
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff, mobileScopeChatWrite)
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/"+id+"/actions",
		`{"idempotencyKey":"stale-revision","action":"send","draftRevision":"old"}`)
	if code != http.StatusConflict || !strings.Contains(body, "draft changed") || fake.count() != 0 {
		t.Fatalf("stale revision answered %d, sends %d, body %s", code, fake.count(), body)
	}
}

func TestMobileTasksWaitingProjectionRespectsChatScope(t *testing.T) {
	bridge, _ := mobileTestDraftBridge(t)
	card := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "calendar-card", Kind: "calendar_conflict", Headline: "Private calendar item"}}
	bridge.today.mu.Lock()
	bridge.today.value = &MobileTodayV1{NeedsYou: []mobileTodayNeed{{ID: "calendar-card", ChatID: "chat", Card: &card}}}
	bridge.today.mu.Unlock()
	code, full := mobileTestGoalCall(t, bridge, mobileTestDevice(mobileScopeBriefRead, mobileScopeChatRead), http.MethodGet, "/tasks", "")
	if code != http.StatusOK || !strings.Contains(full, "Private calendar item") || !strings.Contains(full, `"waitingCount":2`) {
		t.Fatalf("full task projection: %d %s", code, full)
	}
	code, limited := mobileTestGoalCall(t, bridge, mobileTestDevice(mobileScopeBriefRead), http.MethodGet, "/tasks", "")
	if code != http.StatusOK || strings.Contains(limited, "Private calendar item") || !strings.Contains(limited, `"waitingCount":1`) {
		t.Fatalf("limited task projection: %d %s", code, limited)
	}
}

func mobileTestDraftInboundAt(t *testing.T, bridge *mobileBridge) int64 {
	t.Helper()
	rows, err := bridge.app.store.ListMailMessages(context.Background(), 0)
	if err != nil || len(rows) == 0 {
		t.Fatalf("mail rows %v", err)
	}
	return rows[0].At
}

func TestMobileDraftTaskSendsYourEditOnceAndEndsWithItsOutcome(t *testing.T) {
	bridge, fake := mobileTestDraftBridge(t)
	id := mobileTestDraftList(t, bridge).Tasks[0].ID
	reader := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff)
	if code, body := mobileTestGoalCall(t, bridge, reader, http.MethodPost, "/tasks/"+id+"/actions",
		`{"idempotencyKey":"s0","action":"send"}`); code != http.StatusForbidden || !strings.Contains(body, mobileScopeChatWrite) {
		t.Fatalf("send without chat:write answered %d %s", code, body)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff, mobileScopeChatWrite)
	code, rawDetail := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/tasks/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("detail answered %d %s", code, rawDetail)
	}
	initial := mobileTestDecodeThread(t, rawDetail)
	if initial.Draft == nil || initial.Draft.Revision == "" {
		t.Fatal("draft revision missing")
	}
	request, err := json.Marshal(taskActionInput{IdempotencyKey: "s1", Action: "send", Text: "Yes, Thursday works. Talk then.", DraftRevision: initial.Draft.Revision})
	if err != nil {
		t.Fatal(err)
	}
	body := string(request)
	code, response := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/"+id+"/actions", body)
	if code != http.StatusOK {
		t.Fatalf("send answered %d %s", code, response)
	}
	if fake.count() != 1 || fake.calls[0].Text != "Yes, Thursday works. Talk then." || !strings.Contains(fake.calls[0].Revision, "-edit-") {
		t.Fatalf("sent = %+v", fake.calls)
	}
	detail := mobileTestDecodeThread(t, response)
	if detail.Draft == nil || detail.Draft.State != "sent" || detail.Draft.Body != "Yes, Thursday works. Talk then." ||
		detail.Outcome == nil || detail.Outcome.Kind != "replied" || detail.Outcome.Params["person"] != "Dana Ruiz" {
		t.Fatalf("after send = %+v", detail)
	}
	if replay, again := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/"+id+"/actions", body); replay != code || again != response || fake.count() != 1 {
		t.Fatalf("replay sent twice or changed: %d %d", replay, fake.count())
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/"+id+"/actions",
		`{"idempotencyKey":"s2","action":"send"}`); code != http.StatusOK || fake.count() != 1 {
		t.Fatalf("a sent draft must not send again: %d %d", code, fake.count())
	}
	page := mobileTestDraftList(t, bridge)
	if len(page.Tasks) != 1 || page.Tasks[0].Group != mobileTaskGroupDone || page.Tasks[0].Outcome == nil || page.Counts.NeedsYou != 0 {
		t.Fatalf("after send, page = %+v", page)
	}
}

func TestMobileDraftTaskChecksFirstWhenYouAlreadyReplied(t *testing.T) {
	bridge, fake := mobileTestDraftBridge(t)
	id := mobileTestDraftList(t, bridge).Tasks[0].ID
	inbound := mobileTestDraftInboundAt(t, bridge)
	replied := storage.MailMessageRow{ID: "sent-by-you", Mailbox: storage.MailboxSent, To: "dana@example.com",
		Subject: "Re: Project review", ThreadKey: mailThreadKey("Re: Project review"), At: inbound + int64(time.Hour/time.Millisecond)}
	if _, err := bridge.app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{replied}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff, mobileScopeChatWrite)
	code, response := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/"+id+"/actions", `{"idempotencyKey":"c1","action":"send"}`)
	if code != http.StatusOK || fake.count() != 0 {
		t.Fatalf("already answered must not send: %d %d %s", code, fake.count(), response)
	}
	detail := mobileTestDecodeThread(t, response)
	if detail.Outcome == nil || detail.Outcome.Kind != "you_did" || detail.Outcome.By != "you" || detail.Outcome.Params["app"] != "Mail" ||
		detail.Draft.State != "not_sent" || len(detail.Actions) != 0 {
		t.Fatalf("closed as yours = %+v", detail)
	}
	if last := detail.Events[len(detail.Events)-1]; !strings.Contains(last.Text, "You replied to Dana Ruiz yourself from Mail") {
		t.Fatalf("events = %+v", detail.Events)
	}
}

// The phone shows a reply's recipient by name only (PROTOCOL.md section 23): the display name as the mail shows it,
// never an address.
func TestMobileDraftRecipientNameIsTheDisplayNameNeverAnAddress(t *testing.T) {
	cases := map[string]string{
		"Sam Lee <sam.lee@example.com>":   "Sam Lee",
		`"Sam Lee" <sam.lee@example.com>`: "Sam Lee",
		"Sam Lee":                         "Sam Lee",
		"sam.lee@example.com":             "",
		"<sam.lee@example.com>":           "",
		"":                                "",
		"   ":                             "",
	}
	for raw, want := range cases {
		if got := mobileNameOnly(raw); got != want {
			t.Errorf("mobileNameOnly(%q) = %q, want %q", raw, got, want)
		}
	}
}

// With no name, the words about the reply say so instead of leaving a gap or an address.
func TestMobileDraftCopyWithNoNameSaysSomeoneOrYourReply(t *testing.T) {
	if got := mobileDraftCopy("en", "why", ""); got != "Someone is waiting on your reply." {
		t.Fatalf("why = %q", got)
	}
	if got := mobileDraftCopy("en", "replied", ""); got != "You replied yourself from Mail, so I closed this." {
		t.Fatalf("replied = %q", got)
	}
	if got := mobileDraftCopy("en", "why", "Sam Lee"); got != "Sam Lee is waiting on your reply." {
		t.Fatalf("named why = %q", got)
	}
	for _, text := range []string{mobileDraftCopy("zh-CN", "why", ""), mobileDraftCopy("zh-CN", "replied", "")} {
		if strings.Contains(text, "@") || strings.HasPrefix(text, " ") {
			t.Fatalf("zh-CN no-name copy = %q", text)
		}
	}
}
