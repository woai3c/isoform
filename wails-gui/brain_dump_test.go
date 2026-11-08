package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/internal/platform/mailheaders"
	"wails-gui/storage"
)

func TestBrainDumpValidation(t *testing.T) {
	loc := time.FixedZone("local", -7*60*60)
	items, err := decodeBrainDump(`{"items":[{"title":"Reply to Lena","who":"draft","person":"Lena"},{"title":"Fix tests","who":"codex"},{"title":"Call dentist","who":"reminder","when":"2026-11-01T16:00:00Z"}]}`, "codex", loc)
	if err != nil || len(items) != 3 || items[2].When != "2026-11-01T09:00:00-07:00" || items[0].ID == "" || items[0].ID == items[1].ID {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, raw := range []string{
		`{"items":[{"title":"x","who":"claude_code"}]}`,
		`{"items":[{"title":"x","who":"draft"}]}`,
		`{"items":[{"title":"x","who":"reminder","when":"tomorrow"}]}`,
		`{"items":[{"title":"x","who":"reminder","surprise":true}]}`,
		`{"items":[{"title":"x","who":"reminder"}]} trailing`,
	} {
		if _, err := decodeBrainDump(raw, "codex", loc); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestMobileBrainDumpParseScopeAndReceipt(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	body := `{"idempotencyKey":"parse-1","text":"one\ntwo"}`
	denied := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodPost, "/tasks/parse", body)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unscoped parse: %d %s", denied.Code, denied.Body.String())
	}
	device := mobileTestDevice(mobileScopeHandoff)
	first := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/parse", body)
	again := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/parse", body)
	if first.Code != http.StatusOK || first.Body.String() != again.Body.String() || !strings.Contains(first.Body.String(), `"title":"one"`) || !strings.Contains(first.Body.String(), `"title":"two"`) {
		t.Fatalf("parse or replay: %d %s / %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
	}
}

func TestMobileBrainDumpStatusCapability(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	response := mobileTestChatCall(bridge, mobileTestDevice(), http.MethodGet, "/status", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"brainDump":true`) {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	raw, err := json.Marshal(mobileStatusView{})
	if err != nil || strings.Contains(string(raw), `"brainDump"`) {
		t.Fatalf("empty status = %s, %v", raw, err)
	}
}

func TestMobileBrainDumpAddRemindersReceipted(t *testing.T) {
	rig := newHandoffTestRig(t)
	svc := newEngagementService(rig.state, nil, engagementDelivery{})
	rig.app.engagement = svc
	svc.prepareSource = rig.app.prepareEngagementRead
	svc.sourceAllowsRead = rig.app.engagementDeliveryAllowsRead
	svc.holdStagedListReminder = rig.app.holdStagedBrainDumpReminder
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	items := []BrainDumpItem{
		{ID: "one", Title: "Call dentist", Who: "reminder", When: time.Now().Add(72 * time.Hour).Format(time.RFC3339)},
		{ID: "two", Title: "Buy tea", Who: "reminder"},
	}
	bodyBytes, err := json.Marshal(map[string]any{"idempotencyKey": "add-one", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	denied := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefRead), http.MethodPost, "/tasks/add", body)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unscoped add: %d %s", denied.Code, denied.Body.String())
	}
	device := mobileTestDevice(mobileScopeHandoff, mobileScopeBriefRead)
	first := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/add", body)
	again := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/add", body)
	if first.Code != http.StatusCreated || again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Fatalf("add/replay: %d %s / %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
	}
	page, err := bridge.mobileTaskList(time.Now())
	if err != nil || len(page.Scheduled) != 2 || page.Counts.NeedsYou != 0 {
		t.Fatalf("page = %+v, err=%v", page, err)
	}
	detailResponse := mobileTestChatCall(bridge, device, http.MethodGet, "/tasks/"+page.Scheduled[0].ID, "")
	detail := mobileTestDecodeThread(t, detailResponse.Body.String())
	if detailResponse.Code != http.StatusOK || detail.Origin == nil || detail.Origin.Kind != handoff.OriginList || len(detail.Events) != 1 || detail.Events[0].Text == "" {
		t.Fatalf("thread = %+v, response=%s", detail, detailResponse.Body.String())
	}
	items[0].Title = "Different"
	changed, _ := json.Marshal(map[string]any{"idempotencyKey": "add-one", "items": items})
	conflict := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/add", string(changed))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed retry: %d %s", conflict.Code, conflict.Body.String())
	}
	stored, err := rig.handoffs.List()
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored = %+v, err=%v", stored, err)
	}
}

func TestBrainDumpDesktopBindingSharesBatch(t *testing.T) {
	rig := newHandoffTestRig(t)
	items := []BrainDumpItem{{ID: "one", Title: "Buy tea", Who: "reminder"}}
	first, err := rig.app.AddBrainDump("desktop-key", items)
	if err != nil || len(first) != 1 {
		t.Fatalf("add = %v, %v", first, err)
	}
	again, err := rig.app.AddBrainDump("desktop-key", items)
	if err != nil || len(again) != 1 || first[0] != again[0] {
		t.Fatalf("retry = %v, %v", again, err)
	}
	entries, err := rig.handoffs.List()
	if err != nil || len(entries) != 1 || entries[0].Source != handoff.SourceDesktop || entries[0].Thread.Origin.Kind != handoff.OriginList {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
}

func TestMobileBrainDumpAddUnmatchedDraftWaitsInRunning(t *testing.T) {
	rig := newHandoffTestRig(t)
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	body := `{"idempotencyKey":"mixed","items":[{"id":"one","title":"Buy tea","who":"reminder"},{"id":"two","title":"Reply to Lena","who":"draft","person":"Lena"}]}`
	device := mobileTestDevice(mobileScopeHandoff)
	response := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/add", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", response.Code, response.Body.String())
	}
	page, err := bridge.mobileTaskList(time.Now())
	if err != nil || len(page.Tasks) != 1 || page.Counts.Running != 1 || len(page.Scheduled) != 1 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	entries, err := rig.handoffs.List()
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
}

func TestBrainDumpLateDraftLinksWithoutSending(t *testing.T) {
	bridge, transport := mobileTestDraftBridge(t)
	drafts, err := bridge.app.mobileDraftTasks(context.Background(), time.Now())
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %+v, %v", drafts, err)
	}
	name := drafts[0].recipientName(bridge.app)
	item := BrainDumpItem{ID: "draft", Title: "Reply to " + name, Who: "draft", Person: name}
	entry := prepareBrainDumpEntry(item, "hf_late", handoff.SourceMobile, time.Now())
	entry.ListItem.BatchID = strings.Repeat("a", 64)
	entries, err := bridge.app.handoffStore.StageNewListBatch([]handoff.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.app.handoffStore.PublishListBatch(entries); err != nil {
		t.Fatal(err)
	}
	page, err := bridge.mobileTaskList(time.Now())
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != entry.ID || page.Tasks[0].Status != "draft_ready" || page.Counts.NeedsYou != 1 || transport.count() != 0 {
		t.Fatalf("page = %+v, sends = %d, err = %v", page, transport.count(), err)
	}
	device := mobileTestDevice(mobileScopeBriefRead, mobileScopeHandoff)
	response := mobileTestChatCall(bridge, device, http.MethodGet, "/tasks/"+entry.ID, "")
	detail := mobileTestDecodeThread(t, response.Body.String())
	if response.Code != http.StatusOK || detail.Draft == nil || !slices.Contains(detail.Actions, "send") || transport.count() != 0 {
		t.Fatalf("detail = %+v, response = %s", detail, response.Body.String())
	}
}

func TestBrainDumpDraftWaitMessageAfterOneDay(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := mobileGoalsNow()
	entry := prepareBrainDumpEntry(BrainDumpItem{ID: "draft", Title: "Reply to Lena", Who: "draft", Person: "Lena"}, "hf_draft", handoff.SourceMobile, now.Add(-25*time.Hour))
	events := rig.app.mobileTaskEvents(context.Background(), entry)
	if len(events) != 2 || events[1].Text != "A draft hasn't appeared yet." {
		t.Fatalf("events = %+v", events)
	}
}

func TestMobileBrainDumpAddUsesExistingCheckedDraftAndReminder(t *testing.T) {
	bridge, transport := mobileTestDraftBridge(t)
	drafts, err := bridge.app.mobileDraftTasks(context.Background(), time.Now())
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %+v, err=%v", drafts, err)
	}
	name := drafts[0].recipientName(bridge.app)
	subject := mailheaders.NormalizeSubject(drafts[0].Card.DraftContent.Subject)
	items := []BrainDumpItem{
		{ID: "one", Title: "Reply to " + name + " about " + subject, Who: "draft", Person: name},
		{ID: "two", Title: "Buy tea", Who: "reminder"},
	}
	body, err := json.Marshal(map[string]any{"idempotencyKey": "mixed-ready", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	device := mobileTestDevice(mobileScopeHandoff, mobileScopeBriefRead)
	response := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/add", string(body))
	if response.Code != http.StatusCreated || transport.count() != 0 {
		t.Fatalf("add = %d %s, simulated sends=%d", response.Code, response.Body.String(), transport.count())
	}
	page, err := bridge.mobileTaskList(time.Now())
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Status != "draft_ready" || len(page.Scheduled) != 1 || page.Scheduled[0].Title != "Buy tea" || page.Counts.NeedsYou != 1 {
		t.Fatalf("page = %+v, err=%v", page, err)
	}
}

func TestBrainDumpFallback(t *testing.T) {
	items := parseBrainDumpResponse(" first \n\nsecond\nthird", "not JSON", "", time.Local)
	if len(items) != 3 {
		t.Fatalf("items=%+v", items)
	}
	for i, title := range []string{"first", "second", "third"} {
		if items[i].Title != title || items[i].Who != "reminder" || items[i].When != "" {
			t.Fatalf("item=%+v", items[i])
		}
	}
	if got := brainDumpFallback(strings.Repeat("task\n", 15)); len(got) != brainDumpLimit {
		t.Fatalf("count=%d", len(got))
	}
}

func TestBrainDumpAddValidation(t *testing.T) {
	valid := []BrainDumpItem{
		{ID: "a", Title: "Reply to Lena", Who: "draft", Person: "Lena"},
		{ID: "b", Title: "Fix tests", Who: "codex"},
		{ID: "c", Title: "Call dentist", Who: "reminder", When: "2026-11-01T09:00:00-07:00"},
		{ID: "d", Title: strings.Repeat("long ", 60), Who: "reminder"},
	}
	if err := validateBrainDumpAdd(valid, "codex"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]BrainDumpItem{
		nil,
		{{ID: "a", Title: "one", Who: "reminder"}, {ID: "a", Title: "two", Who: "reminder"}},
		{{ID: "../a", Title: "one", Who: "reminder"}},
		{{ID: "a", Title: "one", Who: "claude_code"}},
		{{ID: "a", Title: "one", Who: "draft"}},
		{{ID: "a", Title: "one", Who: "reminder", When: "tomorrow"}},
		{{ID: "a", Title: "one\ntwo", Who: "reminder"}},
	} {
		if err := validateBrainDumpAdd(invalid, "codex"); err == nil {
			t.Errorf("accepted %+v", invalid)
		}
	}
}

func TestBrainDumpBatchReservationBindsSelection(t *testing.T) {
	rig := newEngagementTestRig(t)
	items := []BrainDumpItem{{ID: "one", Title: "First", Who: "reminder"}, {ID: "two", Title: "Second", Who: "reminder"}}
	ids, err := reserveBrainDumpBatch(context.Background(), rig.store, "phone:key", items)
	if err != nil || len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("reserve: %v %v", ids, err)
	}
	retry, err := reserveBrainDumpBatch(context.Background(), rig.store, "phone:key", items)
	if err != nil || strings.Join(ids, ",") != strings.Join(retry, ",") {
		t.Fatalf("retry: %v %v", retry, err)
	}
	items[0].Title = "Changed"
	if _, err := reserveBrainDumpBatch(context.Background(), rig.store, "phone:key", items); err == nil {
		t.Fatal("accepted different work under the same key")
	}
	other, err := reserveBrainDumpBatch(context.Background(), rig.store, "other:key", items)
	if err != nil || other[0] == ids[0] {
		t.Fatalf("other request: %v %v", other, err)
	}
}

func TestBrainDumpBatchReservationConcurrentConflict(t *testing.T) {
	rig := newEngagementTestRig(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := map[string]int{}
	for _, title := range []string{"one", "two"} {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(title string) {
				defer wg.Done()
				_, err := reserveBrainDumpBatch(context.Background(), rig.store, "concurrent", []BrainDumpItem{{ID: "a", Title: title, Who: "reminder"}})
				if err == nil {
					mu.Lock()
					accepted[title]++
					mu.Unlock()
				}
			}(title)
		}
	}
	wg.Wait()
	if len(accepted) != 1 || accepted["one"]+accepted["two"] != 4 {
		t.Fatalf("accepted selections = %v", accepted)
	}
}

func TestBrainDumpPreparedEntryPreservesOriginAndLongText(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	item := BrainDumpItem{ID: "one", Title: strings.Repeat("long ", 60), Who: "reminder"}
	entry := prepareBrainDumpEntry(item, "hf_one", handoff.SourceMobile, now)
	if entry.Body != strings.TrimSpace(item.Title) || len(entry.Title) > mobileTaskTitleMax || entry.ListItem == nil || entry.ListItem.Who != item.Who || entry.Thread == nil || entry.Thread.Origin == nil || entry.Thread.Origin.Kind != handoff.OriginList || entry.Thread.Origin.At != now.UnixMilli() {
		t.Fatalf("entry = %+v", entry)
	}
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(entry.ID)
	if err != nil || stored.ListItem == nil || *stored.ListItem != *entry.ListItem || stored.Thread == nil || stored.Thread.Origin.Kind != handoff.OriginList || stored.Body != entry.Body {
		t.Fatalf("stored = %+v, err=%v", stored, err)
	}
}

func TestBrainDumpExplicitTaskIsNotHiddenAsMailDuplicate(t *testing.T) {
	now := time.Now()
	entry := prepareBrainDumpEntry(BrainDumpItem{ID: "one", Title: "Reply about the appendix", Who: "codex"}, "hf_one", handoff.SourceMobile, now)
	subjects := []string{mobileGoalFold(entry.Title)}
	if _, visible := mobileTaskDetail(entry, subjects); !visible {
		t.Fatal("explicit list task was hidden in its thread")
	}
	page := mobileTaskPage([]handoff.Entry{entry}, subjects, now)
	if len(page.Tasks) != 1 || page.Tasks[0].ID != entry.ID {
		t.Fatalf("explicit list task was hidden: %+v", page)
	}
	entry.Thread = nil
	if _, visible := mobileTaskDetail(entry, subjects); visible {
		t.Fatal("derived duplicate should remain hidden")
	}
}

func TestBrainDumpRemindersProjectAsScheduled(t *testing.T) {
	now := time.Now()
	items := []BrainDumpItem{{ID: "one", Title: "Call the dentist", Who: "reminder", When: now.Add(72 * time.Hour).Format(time.RFC3339)}, {ID: "two", Title: "Buy tea", Who: "reminder"}}
	entries := []handoff.Entry{prepareBrainDumpEntry(items[0], "hf_one", handoff.SourceMobile, now), prepareBrainDumpEntry(items[1], "hf_two", handoff.SourceMobile, now)}
	page := mobileTaskPage(entries, nil, now)
	rows := mobileListReminders(entries)
	if len(page.Tasks) != 0 || page.Counts.NeedsYou != 0 || len(rows) != 2 || rows[0].ID != "hf_one" || rows[0].At != items[0].When || rows[1].At != "" {
		t.Fatalf("page=%+v, scheduled=%+v", page, rows)
	}
	if detail, visible := mobileTaskDetail(entries[0], nil); !visible || detail.Group != "scheduled" {
		t.Fatalf("detail=%+v, visible=%v", detail, visible)
	}
}

func TestBrainDumpFirstLineUsesCurrentLanguage(t *testing.T) {
	created := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	due := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		item handoff.ListItem
		lang string
		want string
	}{
		{handoff.ListItem{Who: "draft"}, "en", "Drafting it now. It lands here for you to send."},
		{handoff.ListItem{Who: "draft"}, "zh-CN", "正在起草，写好后放在这里等你发送。"},
		{handoff.ListItem{Who: "claude_code"}, "en", "Claude Code on your computer is on it. You review the result here."},
		{handoff.ListItem{Who: "codex"}, "zh-CN", "你电脑上的 Codex 在做，结果放在这里给你看。"},
		{handoff.ListItem{Who: "reminder", When: due.Format(time.RFC3339)}, "en", "I'll remind you tomorrow at 9:00 AM. This one is yours to do."},
		{handoff.ListItem{Who: "reminder", When: due.Format(time.RFC3339)}, "zh-CN", "我明天上午 9:00 提醒你。这件需要你自己来。"},
		{handoff.ListItem{Who: "reminder"}, "en", "This one is yours to do."},
	} {
		if got := brainDumpFirstLine(&tc.item, tc.lang, created); got != tc.want {
			t.Errorf("%s/%s = %q, want %q", tc.item.Who, tc.lang, got, tc.want)
		}
	}
}

func TestBrainDumpThreadProjectsFirstLine(t *testing.T) {
	rig := newHandoffTestRig(t)
	entry := prepareBrainDumpEntry(BrainDumpItem{ID: "one", Title: "Reply to Lena", Who: "draft", Person: "Lena"}, "hf_one", handoff.SourceMobile, time.Now())
	detail, visible := rig.app.mobileTaskThreadDetail(context.Background(), entry, nil)
	if !visible || detail.Origin == nil || detail.Origin.Kind != handoff.OriginList || len(detail.Events) != 1 || detail.Events[0].Text == "" {
		t.Fatalf("thread = %+v, visible=%v", detail, visible)
	}
}

func TestBrainDumpDraftMatchRequiresOnePersonAndThread(t *testing.T) {
	draft := mobileDraftTask{TaskID: "draft.one", Inbound: &storage.MailMessageRow{SenderName: "Lena"},
		Card: mainChatProactive{DraftContent: &mainChatProactiveDraft{Subject: "Re: Appendix"}}}
	item := BrainDumpItem{Title: "Reply to Lena about the appendix", Who: "draft", Person: "Lena"}
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft}); match == nil || match.TaskID != draft.TaskID {
		t.Fatalf("match = %+v", match)
	}
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft, draft}); match != nil {
		t.Fatal("ambiguous threads matched")
	}
	item.Person = "Dan"
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft}); match != nil {
		t.Fatal("wrong person matched")
	}
	item.Person, item.Title = "Lena", "Reply to Lena about the schedule"
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft}); match != nil {
		t.Fatal("wrong subject matched")
	}
	item.Title = "Reply to Lena about appendixes"
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft}); match != nil {
		t.Fatal("a subject inside another word matched")
	}
	item.Person, item.Title = "Lena Other", "Reply to Lena about the appendix"
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft}); match != nil {
		t.Fatal("a different full name matched")
	}
	item.Person = "Lena"
	item.Title = "Reply to Lena about the appendix"
	chinese := draft
	chinese.Card.DraftContent = &mainChatProactiveDraft{Subject: "回复：附录"}
	item.Title = "回复 Lena 关于附录的事"
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{chinese}); match == nil {
		t.Fatal("the named Chinese subject did not match")
	}
	item.Title = "Reply to Lena about the appendix"
	draft.Inbound = nil
	if match := matchBrainDumpDraft(nil, item, []mobileDraftTask{draft}); match != nil {
		t.Fatal("draft without a known Mail thread matched")
	}
}

func TestBrainDumpStageLinksOnlyCheckedReadyDraft(t *testing.T) {
	bridge, transport := mobileTestDraftBridge(t)
	now := time.Now()
	drafts, err := bridge.app.mobileDraftTasks(context.Background(), now)
	if err != nil || len(drafts) != 1 || drafts[0].Inbound == nil {
		t.Fatalf("drafts = %+v, err=%v", drafts, err)
	}
	name := drafts[0].recipientName(bridge.app)
	subject := mailheaders.NormalizeSubject(drafts[0].Card.DraftContent.Subject)
	item := BrainDumpItem{ID: "draft-one", Title: "Reply to " + name + " about " + subject, Who: "draft", Person: name}
	entries, err := bridge.app.persistBrainDumpSelection(context.Background(), "phone:draft", []BrainDumpItem{item}, handoff.SourceMobile, now)
	if err != nil || len(entries) != 1 || entries[0].ListItem == nil || entries[0].ListItem.DraftTaskID != drafts[0].TaskID {
		t.Fatalf("entries = %+v, err=%v", entries, err)
	}
	if transport.count() != 0 {
		t.Fatal("staging a draft sent a message")
	}
	if err := bridge.app.handoffStore.PublishListBatch(entries); err != nil {
		t.Fatal(err)
	}
	page, err := bridge.mobileTaskList(now)
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != entries[0].ID || page.Tasks[0].Status != "draft_ready" || page.Counts.NeedsYou != 1 {
		t.Fatalf("ready page = %+v, err=%v", page, err)
	}
	device := mobileTestDevice(mobileScopeBriefRead, mobileScopeHandoff)
	response := mobileTestChatCall(bridge, device, http.MethodGet, "/tasks/"+entries[0].ID, "")
	detail := mobileTestDecodeThread(t, response.Body.String())
	if response.Code != http.StatusOK || detail.Origin == nil || detail.Origin.Kind != handoff.OriginList || detail.Draft == nil || !slices.Contains(detail.Actions, "send") || transport.count() != 0 {
		t.Fatalf("ready detail = %+v, response=%s", detail, response.Body.String())
	}
	bodyBytes, _ := json.Marshal(map[string]string{"idempotencyKey": "send-linked", "action": "send", "draftRevision": detail.Draft.Revision})
	denied := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/"+entries[0].ID+"/actions", string(bodyBytes))
	if denied.Code != http.StatusForbidden || transport.count() != 0 {
		t.Fatalf("send without chat scope: %d %s", denied.Code, denied.Body.String())
	}
	device.Scopes = append(device.Scopes, mobileScopeChatWrite)
	sent := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/"+entries[0].ID+"/actions", string(bodyBytes))
	replay := mobileTestChatCall(bridge, device, http.MethodPost, "/tasks/"+entries[0].ID+"/actions", string(bodyBytes))
	completed := mobileTestDecodeThread(t, sent.Body.String())
	if sent.Code != http.StatusOK || replay.Code != http.StatusOK || replay.Body.String() != sent.Body.String() || transport.count() != 1 || completed.Outcome == nil || completed.Outcome.Kind != "replied" || completed.Group != mobileTaskGroupDone {
		t.Fatalf("simulated send/replay: %d %s / %d %s, calls=%d", sent.Code, sent.Body.String(), replay.Code, replay.Body.String(), transport.count())
	}
}

func TestBrainDumpPersistSelectionRetriesWithoutOverwriting(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := time.Now()
	items := []BrainDumpItem{{ID: "one", Title: "First", Who: "reminder"}, {ID: "two", Title: "Second", Who: "reminder"}}
	saved, err := rig.app.persistBrainDumpSelection(context.Background(), "phone:batch", items, handoff.SourceMobile, now)
	if err != nil || len(saved) != 2 {
		t.Fatalf("save = %+v, err=%v", saved, err)
	}
	if items, err := rig.handoffs.List(); err != nil || len(items) != 0 {
		t.Fatalf("staged selection became visible: %+v, err=%v", items, err)
	}
	if err := rig.handoffs.PublishListBatch(saved); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.handoffs.MutateIfStatus(saved[0].ID, []handoff.Status{handoff.StatusActive}, func(e *handoff.Entry) { e.Title = "Changed by you" }); err != nil {
		t.Fatal(err)
	}
	again, err := rig.app.persistBrainDumpSelection(context.Background(), "phone:batch", items, handoff.SourceMobile, now.Add(time.Minute))
	if err != nil || len(again) != 2 || again[0].ID != saved[0].ID || again[0].Title != "Changed by you" {
		t.Fatalf("retry = %+v, err=%v", again, err)
	}
	items[0].Title = "Different request"
	if _, err := rig.app.persistBrainDumpSelection(context.Background(), "phone:batch", items, handoff.SourceMobile, now); err == nil {
		t.Fatal("accepted different selection under the same key")
	}
	stored, err := rig.handoffs.List()
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored = %+v, err=%v", stored, err)
	}
}

func TestBrainDumpPersistRejectsPastTimeButAllowsRetryAfterDue(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := time.Now().Truncate(time.Second)
	items := []BrainDumpItem{{ID: "one", Title: "Call", Who: "reminder", When: now.Add(time.Hour).Format(time.RFC3339)}}
	first, err := rig.app.persistBrainDumpSelection(context.Background(), "phone:timed", items, handoff.SourceMobile, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := rig.app.persistBrainDumpSelection(context.Background(), "phone:timed", items, handoff.SourceMobile, now.Add(2*time.Hour))
	if err != nil || len(again) != 1 || again[0].ID != first[0].ID {
		t.Fatalf("late retry = %+v, err=%v", again, err)
	}
	if _, err := rig.app.persistBrainDumpSelection(context.Background(), "phone:new", items, handoff.SourceMobile, now.Add(2*time.Hour)); err == nil {
		t.Fatal("a new request scheduled a past time")
	}
}

func TestBrainDumpReminderUsesDurableSchedule(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc}
	at := rig.clock.Add(time.Hour)
	item := BrainDumpItem{ID: "one", Title: "Call the dentist", Who: "reminder", When: at.Format(time.RFC3339)}
	id, err := app.scheduleBrainDumpReminder(context.Background(), "task-one", item)
	if err != nil || id == "" {
		t.Fatalf("schedule: %q %v", id, err)
	}
	again, err := app.scheduleBrainDumpReminder(context.Background(), "task-one", item)
	if err != nil || again != id {
		t.Fatalf("retry: %q %v", again, err)
	}
	row, err := rig.store.GetEngagementMoment(context.Background(), id)
	if err != nil || row == nil || row.State != storage.EngagementStateScheduled || row.HoldUntil != at.UnixMilli() {
		t.Fatalf("stored reminder: %+v %v", row, err)
	}
	rig.svc.Pass(context.Background())
	if len(rig.pushed) != 0 || len(rig.desktop) != 0 {
		t.Fatal("reminder delivered before its time")
	}
	rig.clock = at
	rig.svc.Pass(context.Background())
	if len(rig.pushed) != 1 || len(rig.desktop) != 1 {
		t.Fatalf("due reminder: push=%d desktop=%d", len(rig.pushed), len(rig.desktop))
	}
	if retry, err := app.scheduleBrainDumpReminder(context.Background(), "task-one", item); err != nil || retry != id {
		t.Fatalf("retry after delivery: %q %v", retry, err)
	}
	rig.svc.Pass(context.Background())
	if len(rig.pushed) != 1 || len(rig.desktop) != 1 {
		t.Fatal("retry redelivered the reminder")
	}
	if id, err := app.scheduleBrainDumpReminder(context.Background(), "untimed", BrainDumpItem{Title: "Own task"}); err != nil || id != "" {
		t.Fatalf("untimed: %q %v", id, err)
	}
}

func TestBrainDumpReminderWaitsForBatchAndRespectsCompletion(t *testing.T) {
	for _, finishBeforeDue := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%t", finishBeforeDue), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			rig := newEngagementTestRig(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app := &App{store: rig.store, handoffStore: store, engagement: rig.svc}
			rig.svc.prepareSource = app.prepareEngagementRead
			rig.svc.sourceAllowsRead = app.engagementDeliveryAllowsRead
			rig.svc.holdStagedListReminder = app.holdStagedBrainDumpReminder
			due := rig.clock.Add(time.Hour)
			item := BrainDumpItem{ID: "one", Title: "Call the dentist", Who: "reminder", When: due.Format(time.RFC3339)}
			entries, err := app.stageBrainDumpSelection(context.Background(), "phone:hold", []BrainDumpItem{item}, handoff.SourceMobile, rig.clock)
			if err != nil {
				t.Fatal(err)
			}
			id, err := app.scheduleBrainDumpReminder(context.Background(), entries[0].ID, item)
			if err != nil {
				t.Fatal(err)
			}
			rig.clock = due
			rig.svc.Pass(context.Background())
			row, err := rig.store.GetEngagementMoment(context.Background(), id)
			if err != nil || row == nil || row.State != storage.EngagementStateScheduled || len(rig.pushed) != 0 || len(rig.desktop) != 0 {
				t.Fatalf("unpublished reminder = %+v, err=%v", row, err)
			}
			if err := store.PublishListBatch(entries); err != nil {
				t.Fatal(err)
			}
			if finishBeforeDue {
				if _, err := store.MutateIfStatus(entries[0].ID, []handoff.Status{handoff.StatusActive}, func(e *handoff.Entry) {
					e.Status = handoff.StatusCompleted
					e.Thread.Outcome = &handoff.Outcome{Kind: handoff.OutcomeMarkedDone, By: handoff.OutcomeByYou}
				}); err != nil {
					t.Fatal(err)
				}
			}
			rig.clock = due.Add(engagementRetryDelay)
			rig.svc.Pass(context.Background())
			if finishBeforeDue {
				if len(rig.pushed) != 0 || len(rig.desktop) != 0 {
					t.Fatal("completed task was reminded")
				}
			} else if len(rig.pushed) != 1 || len(rig.desktop) != 1 {
				t.Fatalf("published reminder: push=%d desktop=%d", len(rig.pushed), len(rig.desktop))
			}
		})
	}
}
