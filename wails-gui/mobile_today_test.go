package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestMobileTodayNoticeMetadataUsesEngagementIdentity(t *testing.T) {
	bridge, _ := mobileMainChatWorkBridge(t)
	app := bridge.app
	now := time.Now().UTC().Truncate(time.Second)
	cardID := "card-for-work"
	id := mainChatEngagementMomentID(cardID)
	_, err := app.store.CreateEngagementMoment(context.Background(), storage.EngagementMomentRow{
		ID: id, Kind: "work_blocked", DedupeKey: "work_blocked:test", CreatedAt: now.Add(-time.Hour).UnixMilli(),
		DueAt: now.Add(time.Hour).UnixMilli(), ExpiresAt: now.Add(2 * time.Hour).UnixMilli(), Level: "deadline",
	})
	if err != nil {
		t.Fatal(err)
	}
	card := mobileChatItemView{Proactive: &mainChatProactive{MomentID: cardID}}
	got, err := mobileTodayNeedNotice(context.Background(), app, mobileTodayNeed{ID: cardID, Card: &card}, mobileTimestamp(now), now)
	if err != nil || got.NoticeID != id || got.DestinationKind != "chat" || got.DestinationID != cardID || got.CreatedAt != mobileTimestamp(now.Add(-time.Hour)) || got.ExpiresAt != mobileTimestamp(now.Add(2*time.Hour)) || got.Kind != "work_blocked" || got.Priority != "time-sensitive" {
		t.Fatalf("notice = %+v, err %v", got, err)
	}
	task := MobileTaskView{ID: "handoff", CreatedAt: mobileTimestamp(now)}
	fallback, err := mobileTodayNeedNotice(context.Background(), app, mobileTodayNeed{ID: task.ID, Task: &task}, task.CreatedAt, now)
	if err != nil || fallback.NoticeID != "task:handoff" || fallback.DestinationKind != "task" || fallback.DestinationID != task.ID || fallback.CreatedAt != task.CreatedAt || fallback.Kind != "work_blocked" || fallback.Priority != "active" {
		t.Fatalf("task notice = %+v, err %v", fallback, err)
	}
}

func TestMobileTodayNoticePacingUsesCurrentDay(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc := newEngagementService(nil, nil, engagementDelivery{})
	svc.state.Day = now.Format("2006-01-02")
	svc.state.InterruptsToday = 3
	svc.state.LastQuietBreak = now.Add(-time.Hour)
	got := mobileTodayPacing(svc, now)
	if got == nil || got.ActiveToday != 3 || got.LastQuietBreak != mobileTimestamp(now.Add(-time.Hour)) {
		t.Fatalf("pacing = %+v", got)
	}
	if got = mobileTodayPacing(svc, now.AddDate(0, 0, 1)); got.ActiveToday != 0 {
		t.Fatalf("new day pacing = %+v", got)
	}
}

func TestMobileTodayTopicsRequireChatItems(t *testing.T) {
	topics := []TopicView{{ID: "empty", Name: "Empty"}, {ID: "active", Name: "Active"}}
	var reads []string
	got := mobileTodayTopicsWithChat(topics, func(id string) bool {
		reads = append(reads, id)
		return id == "active"
	})
	if len(reads) != 2 || len(got) != 1 || got[0].ID != "active" {
		t.Fatalf("reads=%v topics=%+v", reads, got)
	}
}

func TestMobileTodayFreeRanges(t *testing.T) {
	shape := TodayDayShape{RangeStart: "2026-10-27T08:00:00Z", RangeEnd: "2026-10-27T19:00:00Z", CalendarConnected: true,
		Events: []TodayDayShapeEvent{
			{Start: "2026-10-27T11:00:00Z", End: "2026-10-27T12:00:00Z"},
			{Start: "2026-10-27T11:30:00Z", End: "2026-10-27T13:00:00Z"},
			{Start: "2026-10-27T13:30:00Z", End: "2026-10-27T14:00:00Z"},
		}, Hold: &TodayDayShapeHold{Start: "2026-10-27T15:00:00Z", End: "2026-10-27T16:00:00Z"}}
	now, _ := time.Parse(time.RFC3339, "2026-10-27T10:00:00Z")
	got := mobileTodayFreeRanges(shape, now)
	want := []mobileTodayRange{{Start: "2026-10-27T10:00:00Z", End: "2026-10-27T11:00:00Z"},
		{Start: "2026-10-27T14:00:00Z", End: "2026-10-27T15:00:00Z"},
		{Start: "2026-10-27T16:00:00Z", End: "2026-10-27T19:00:00Z"}}
	if len(got) != len(want) {
		t.Fatalf("free = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("free = %+v, want %+v", got, want)
		}
	}
	day := mobileTodayDayFromShape(shape, now)
	if day.Next == nil || day.Next.Start != shape.Events[0].Start {
		t.Fatalf("next = %+v", day.Next)
	}
}

func TestMobileTodayCatchUpRecognizesMissedEvening(t *testing.T) {
	made, _ := time.Parse(time.RFC3339, "2026-10-27T06:10:00-07:00")
	if !mobileTodayCatchUp(made, "2026-10-26T19:30:00-07:00", true) ||
		!mobileTodayCatchUp(made, "2026-10-26T19:30:00-07:00", false) {
		t.Fatal("an overnight catch-up should be labelled as made late")
	}
	onTime, _ := time.Parse(time.RFC3339, "2026-10-26T19:31:30-07:00")
	if mobileTodayCatchUp(onTime, "2026-10-26T19:30:00-07:00", true) {
		t.Fatal("normal generation time is not a missed slot")
	}
}

func TestMobileTodayBriefRefreshReplaysReceipt(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	source := &mobileTestBriefSource{}
	bridge.app.mobileBriefSvc = source
	device := mobileTestDevice(mobileScopeBriefRefresh)
	first := mobileTestChatCall(bridge, device, http.MethodPost, "/brief/refresh", `{"idempotencyKey":"refresh-1"}`)
	second := mobileTestChatCall(bridge, device, http.MethodPost, "/brief/refresh", `{"idempotencyKey":"refresh-1"}`)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() || source.refreshes != 1 {
		t.Fatalf("first=%d %s second=%d %s calls=%d", first.Code, first.Body.String(), second.Code, second.Body.String(), source.refreshes)
	}
}

func TestMobileTodayLargeSnapshotParts(t *testing.T) {
	bridge, err := newMobileBridge(&App{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	view := MobileTodayV1{GeneratedAt: mobileTimestamp(time.Now()), Briefs: []mobileTodayBrief{}, NeedsYou: []mobileTodayNeed{}, Topics: []mobileTopicView{}, Day: mobileTodayDay{Free: []mobileTodayRange{}}}
	for i := 0; i < 500; i++ {
		task := MobileTaskView{ID: fmt.Sprintf("task-%d", i), Title: strings.Repeat("x", 200), Group: mobileTaskGroupNeedsYou}
		view.NeedsYou = append(view.NeedsYou, mobileTodayNeed{ID: task.ID, Task: &task})
	}
	view.NeedsYouCount = len(view.NeedsYou)
	parts, err := mobileTodayParts(view)
	if err != nil {
		t.Fatal(err)
	}
	bridge.today.mu.Lock()
	bridge.today.value, bridge.today.version = &view, 7
	bridge.today.briefParts = parts
	bridge.today.mu.Unlock()
	device := mobileTestDevice(mobileScopeBriefRead)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		bridge.handleAuthorized(r, httptest.NewRequest(http.MethodGet, path, nil), device)
		return r
	}
	manifest := request("/mobile/v1/today")
	var m struct {
		Data struct {
			Chunked bool   `json:"chunked"`
			Version uint64 `json:"version"`
			Parts   int    `json:"parts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(manifest.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if manifest.Code != http.StatusOK || !m.Data.Chunked || m.Data.Version != 7 || m.Data.Parts != len(parts) {
		t.Fatalf("manifest: %s", manifest.Body.String())
	}
	var assembled []byte
	for i := 0; i < m.Data.Parts; i++ {
		r := request(fmt.Sprintf("/mobile/v1/today?version=7&part=%d", i))
		var piece struct {
			Data struct {
				Data string `json:"data"`
			} `json:"data"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &piece) != nil {
			t.Fatalf("part %d: %s", i, r.Body.String())
		}
		raw, err := base64.RawURLEncoding.DecodeString(piece.Data.Data)
		if err != nil {
			t.Fatal(err)
		}
		assembled = append(assembled, raw...)
	}
	var decoded MobileTodayV1
	if err := json.Unmarshal(assembled, &decoded); err != nil || len(decoded.NeedsYou) != len(view.NeedsYou) {
		t.Fatalf("assembled: %d, %v", len(decoded.NeedsYou), err)
	}
	if r := request("/mobile/v1/today?version=6&part=0"); r.Code != http.StatusConflict {
		t.Fatalf("old version: %d", r.Code)
	}
}

func TestMobileBriefListIncludesDeliveryLead(t *testing.T) {
	brief := mobileTestBrief()
	brief.Narrative = []MobileBriefPara{{Text: "The plan shipped. The review is tomorrow."}}
	source := &mobileTestBriefSource{brief: &brief}
	bridge, err := newMobileBridge(&App{mobileBriefSvc: source}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	r := httptest.NewRecorder()
	bridge.handleAuthorized(r, httptest.NewRequest(http.MethodGet, "/mobile/v1/briefs", nil), mobileTestDevice(mobileScopeBriefRead))
	var response struct {
		Data []MobileBriefSummary `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if r.Code != http.StatusOK || len(response.Data) != 1 || response.Data[0].Lead != "The plan shipped." {
		t.Fatalf("list = %d %s", r.Code, r.Body.String())
	}
}

func TestMobileTodayScopeAndShape(t *testing.T) {
	brief := mobileTestBrief()
	brief.AsOf = mobileTimestamp(time.Now())
	brief.Narrative = []MobileBriefPara{{Text: "The plan shipped. More tomorrow."}}
	source := &mobileTestBriefSource{brief: &brief}
	bridge, err := newMobileBridge(&App{mobileBriefSvc: source}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	// GET /today answers only from the cache; the loop builds the first snapshot at start.
	waitForMobileTodaySnapshot(t, bridge)
	call := func(device mobileDeviceRecord) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		bridge.handleAuthorized(r, httptest.NewRequest(http.MethodGet, "/mobile/v1/today", nil), device)
		return r
	}
	if r := call(mobileTestDevice(mobileScopeChatRead)); r.Code != http.StatusForbidden {
		t.Fatalf("without brief scope = %d", r.Code)
	}
	r := call(mobileTestDevice(mobileScopeBriefRead))
	var response struct {
		Data MobileTodayV1 `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if r.Code != http.StatusOK || response.Data.LatestBrief == nil || response.Data.LatestBrief.Lead != "The plan shipped." || response.Data.GeneratedAt == "" {
		t.Fatalf("snapshot = %d %s", r.Code, r.Body.String())
	}
	if response.Data.BriefSchedule == nil || response.Data.KeepAwake != bridge.keepAwakeSetting() || response.Data.InstantPush != mobileInstantPushConfigured() {
		t.Fatalf("status fields missing or inconsistent: %+v", response.Data)
	}
}

func TestMobileTodayHidesChatContentWithoutChatScope(t *testing.T) {
	card := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "card"}}
	task := MobileTaskView{ID: "task"}
	original := MobileTodayV1{NeedsYou: []mobileTodayNeed{{ID: "card", Card: &card}, {ID: "task", Task: &task}}, NeedsYouCount: 2, Topics: []mobileTopicView{{ID: "topic"}}}
	filtered := mobileTodayForDevice(original, mobileTestDevice(mobileScopeBriefRead))
	if len(filtered.NeedsYou) != 1 || filtered.NeedsYou[0].ID != "task" || filtered.NeedsYouCount != 1 || len(filtered.Topics) != 0 {
		t.Fatalf("filtered = %+v", filtered)
	}
	if len(original.NeedsYou) != 2 || len(original.Topics) != 1 {
		t.Fatal("filter modified cached snapshot")
	}
}

func TestMobileTodayOnlyLiveCards(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name string
		card mainChatProactive
		want bool
	}{
		{"waiting", mainChatProactive{Kind: "waiting_on_reply", State: "pending"}, true},
		{"resolved", mainChatProactive{Kind: "waiting_on_reply", State: "acted"}, false},
		{"dismissed", mainChatProactive{Kind: "waiting_on_reply", State: "dismissed"}, false},
		{"expired", mainChatProactive{Kind: "waiting_on_reply", State: "pending", ExpiresAt: now.Add(-time.Second).UnixMilli()}, false},
		{"unavailable", mainChatProactive{Kind: "thread_update_unavailable", State: "pending"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := mobileTodayLiveCard(&test.card, now); got != test.want {
				t.Fatalf("live = %t, want %t", got, test.want)
			}
		})
	}
}

func TestMobileTodayReadsLiveWaitingAndDeduplicatesTasks(t *testing.T) {
	bridge, _ := mobileMainChatWorkBridge(t)
	app := bridge.app
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	app.mobileBriefSvc = newMobileBriefService(app)
	var duplicate handoff.Entry
	for _, task := range []handoff.Entry{
		{ID: "duplicate", Title: "Duplicate", Source: "manual", Status: handoff.StatusBlocked},
		{ID: "another", Title: "Another", Source: "manual", Status: handoff.StatusActive},
		{ID: "finished", Title: "Finished", Source: "manual", Status: handoff.StatusCompleted},
	} {
		saved, err := app.handoffStore.Save(task)
		if err != nil {
			t.Fatal(err)
		}
		if task.ID == "duplicate" {
			duplicate = saved
		}
	}
	app.postHandoffReport(duplicate)
	view, err := bridge.buildToday()
	if err != nil {
		t.Fatal(err)
	}
	if view.NeedsYouCount != 2 || len(view.NeedsYou) != 2 || view.NeedsYou[0].Card == nil || view.NeedsYou[1].ID != "another" {
		t.Fatalf("needs you = %+v, count %d", view.NeedsYou, view.NeedsYouCount)
	}
	if view.TasksVersion == "" {
		t.Fatal("task change token missing")
	}
	if _, err := app.handoffStore.MutateThread("another", func(thread *handoff.Thread) { thread.OpenedAt = time.Now().UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	updated, err := bridge.buildToday()
	if err != nil || updated.TasksVersion == view.TasksVersion {
		t.Fatalf("task open did not update change token: %q %v", updated.TasksVersion, err)
	}
	entry, err := app.handoffStore.Get("another")
	if err != nil || mobileTaskView(entry).OpenedAt == "" {
		t.Fatalf("opened timestamp missing: %+v %v", entry, err)
	}
}

func TestMobileTaskWaitingListPreservesCardsAndDeduplicatesDraftsAndPermissions(t *testing.T) {
	page := MobileTaskPage{Counts: MobileTaskCounts{NeedsYou: 2}, Tasks: []MobileTaskView{
		{ID: mobileDraftTaskID("reply"), Group: mobileTaskGroupNeedsYou},
		{ID: "work", Group: mobileTaskGroupNeedsYou},
	}}
	draft := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "reply", Kind: "waiting_on_reply"}}
	permission := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "permission", Kind: "decision_due",
		Permission: &mainChatWorkPermission{HandoffID: "work"}}}
	other := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "calendar", Kind: "calendar_conflict"}}
	items, count := mobileTaskWaitingItems(page, []mobileTodayNeed{
		{ID: "reply", Card: &draft}, {ID: "permission", Card: &permission}, {ID: "calendar", Card: &other},
	})
	if count != 3 || len(items) != 3 || items[0].Task == nil || items[0].Task.ID != mobileDraftTaskID("reply") ||
		items[1].Card == nil || items[1].ID != "permission" || items[2].ID != "calendar" {
		t.Fatalf("waiting list count=%d items=%+v", count, items)
	}
}

func TestMobileTaskWaitingListDeduplicatesBeyondResponseLimit(t *testing.T) {
	page := MobileTaskPage{Counts: MobileTaskCounts{NeedsYou: mobileTasksGroupLimit + 1}}
	for i := 0; i <= mobileTasksGroupLimit; i++ {
		task := MobileTaskView{ID: fmt.Sprintf("work-%02d", i), Group: mobileTaskGroupNeedsYou}
		page.allWaiting = append(page.allWaiting, task)
		if i < mobileTasksGroupLimit {
			page.Tasks = append(page.Tasks, task)
		}
	}
	card := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "hidden-work", Work: &mainChatHandoffWork{HandoffID: "work-20"}}}
	target := mobileChatItemView{Proactive: &mainChatProactive{MomentID: "hidden-target", Target: mainChatTarget{Kind: mainChatTargetHandoff, ID: "work-20"}}}
	items, count := mobileTaskWaitingItems(page, []mobileTodayNeed{{ID: "hidden-work", Card: &card}, {ID: "hidden-target", Card: &target}})
	if count != mobileTasksGroupLimit+1 || len(items) != mobileTasksGroupLimit {
		t.Fatalf("waiting count=%d rows=%d", count, len(items))
	}
}

func TestMobileTodayMeetingPrepRequiresScopeAndCurrentSource(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	if err := app.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	start := now.Add(2 * time.Minute)
	if start.Day() != now.Day() {
		t.Skip("no upcoming meeting fits in today's remaining minutes")
	}
	person := peopleTestContact(t, app, "Mei Chen")
	peopleTestPurpose(t, app, "Send the launch outline", person.DisplayName, PurposeOwnerUser, time.Time{})
	event := beforeYouTalkEvent("phone-prep-event", person.ID, start, start.Add(time.Hour))
	if err := app.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	bridge := newMobileBriefPrivacyBridge(t, app)
	status := httptest.NewRecorder()
	bridge.handleAuthorized(status, httptest.NewRequest(http.MethodGet, "/mobile/v1/status", nil), mobileTestDevice())
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"todayMeetingPrep":true`) {
		t.Fatalf("meeting capability: %d %s", status.Code, status.Body.String())
	}
	request := func(device mobileDeviceRecord, id string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		bridge.handleAuthorized(rec, httptest.NewRequest(http.MethodGet, "/mobile/v1/today/meeting-prep?id="+id, nil), device)
		return rec
	}
	if got := request(mobileTestDevice(), event.ID); got.Code != http.StatusForbidden {
		t.Fatalf("missing scope: %d %s", got.Code, got.Body.String())
	}
	got := request(mobileTestDevice(mobileScopeBriefRead), event.ID)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Mei Chen") || !strings.Contains(got.Body.String(), event.ID) {
		t.Fatalf("meeting prep: %d %s", got.Code, got.Body.String())
	}
	if got := request(mobileTestDevice(mobileScopeBriefRead), "missing"); got.Code != http.StatusNotFound {
		t.Fatalf("missing meeting: %d %s", got.Code, got.Body.String())
	}
	if err := app.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	if got := request(mobileTestDevice(mobileScopeBriefRead), event.ID); got.Code != http.StatusNotFound {
		t.Fatalf("withdrawn calendar: %d %s", got.Code, got.Body.String())
	}
}

func TestMobileTodayChangedCoalesces(t *testing.T) {
	bridge, err := newMobileBridge(&App{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	type publication struct {
		version uint64
		at      time.Time
	}
	publications := make(chan publication, 3)
	bridge.today.onEvent = func(version uint64) { publications <- publication{version, time.Now()} }
	bridge.startTodayCache()
	set := func(version uint64) {
		bridge.today.mu.Lock()
		bridge.today.value = &MobileTodayV1{GeneratedAt: mobileTimestamp(time.Now())}
		bridge.today.version = version
		bridge.today.mu.Unlock()
		select {
		case bridge.today.changed <- struct{}{}:
		default:
		}
	}
	set(1)
	var first publication
	select {
	case first = <-publications:
	case <-time.After(4 * time.Second):
		t.Fatal("first event not published")
	}
	set(2)
	set(3)
	var second publication
	select {
	case second = <-publications:
		if second.version != 3 || second.at.Sub(first.at) < 1900*time.Millisecond {
			t.Fatalf("events = %+v, %+v", first, second)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("coalesced event not published")
	}
	bridge.wakeTodayPhones()
	bridge.wakeTodayPhones()
	select {
	case wake := <-publications:
		if wake.version != 3 || wake.at.Sub(second.at) < 1900*time.Millisecond {
			t.Fatalf("wake event = %+v after %+v", wake, second)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("wake event not published")
	}
}
