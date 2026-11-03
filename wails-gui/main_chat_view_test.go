package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func mainChatViewPayload(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func mainChatViewCard(t *testing.T, id, kind, state string, at time.Time) storage.StreamItemRow {
	return storage.StreamItemRow{ID: id, Kind: "system", ToolName: mainChatProactiveTool, CreatedAt: at.UnixMilli(), ToolInput: mainChatViewPayload(t, mainChatProactive{MomentID: id, Kind: kind, State: state})}
}
func TestMainChatRowClass(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		row  storage.StreamItemRow
		want MainChatRowClass
	}{
		{"user", storage.StreamItemRow{Kind: "user"}, MainChatRowConversation},
		{"answer", storage.StreamItemRow{Kind: "assistant", Content: "Answer"}, MainChatRowConversation},
		{"empty assistant", storage.StreamItemRow{Kind: "assistant"}, MainChatRowOther},
		{"thinking", storage.StreamItemRow{Kind: "thinking", Content: "Thought"}, MainChatRowOther},
		{"tool", storage.StreamItemRow{Kind: "tool_use", Content: "Tool"}, MainChatRowOther},
		{"result", storage.StreamItemRow{Kind: "tool_result", Content: "Result"}, MainChatRowOther},
		{"status", storage.StreamItemRow{Kind: "system", ToolName: mainChatStatusTool}, MainChatRowRoutine},
		{"brief", mainChatViewCard(t, "brief", "brief_ready", "pending", now), MainChatRowRoutine},
		{"old capture", mainChatViewCard(t, "capture", "capture_paused", "pending", now.Add(-2*time.Hour)), MainChatRowRoutine},
		{"work", mainChatViewCard(t, "work", "work_finished", "pending", now), MainChatRowFinished},
		{"permission", mainChatViewCard(t, "permission", "decision_due", "pending", now), MainChatRowWaiting},
		{"opened", mainChatViewCard(t, "opened", "waiting_on_reply", "opened", now), MainChatRowWaiting},
		{"empty digest", mainChatViewCard(t, "digest", "digest", "pending", now), MainChatRowOther},
	}
	for _, state := range []string{"acted", "dismissed", "deferred", "expired", "muted", ""} {
		cases = append(cases, struct {
			name string
			row  storage.StreamItemRow
			want MainChatRowClass
		}{state, mainChatViewCard(t, state, "waiting_on_reply", state, now), MainChatRowOther})
	}
	for _, tool := range []string{"main_chat_setup", "main_chat_setup_record", "today_chat_context_status", "main_chat_texted_reply", "upcoming_confirmation", "notice", "error"} {
		cases = append(cases, struct {
			name string
			row  storage.StreamItemRow
			want MainChatRowClass
		}{tool, storage.StreamItemRow{Kind: "system", ToolName: tool}, MainChatRowOther})
	}
	for _, status := range []string{"created", "changed", "removed", "ok", "error"} {
		routine := status != "ok" && status != "error"
		want := MainChatRowFinished
		if status != "ok" && status != "error" {
			want = MainChatRowRoutine
		}
		cases = append(cases, struct {
			name string
			row  storage.StreamItemRow
			want MainChatRowClass
		}{status, storage.StreamItemRow{Kind: "system", ToolName: mainChatReportTool, ToolInput: mainChatViewPayload(t, mainChatReport{Source: mainChatSourceSchedule, Status: status, Routine: &routine})}, want})
	}
	cases = append(cases, struct {
		name string
		row  storage.StreamItemRow
		want MainChatRowClass
	}{"expired clock", storage.StreamItemRow{Kind: "system", ToolName: mainChatProactiveTool, ToolInput: mainChatViewPayload(t, mainChatProactive{Kind: "decision_due", State: "pending", ExpiresAt: now.UnixMilli()})}, MainChatRowOther})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mainChatRowClass(tc.row, now); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
func TestMainChatOverviewWaitingPagesAndHideRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yansu.db")
	store, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &App{store: store}
	ctx := t.Context()
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	rows := []storage.StreamItemRow{
		{ID: "user-today", Kind: "user", Content: "Question", CreatedAt: today.UnixMilli()},
		{ID: "answer", Kind: "assistant", Content: "Answer", CreatedAt: today.UnixMilli()},
		{ID: "user-yesterday", Kind: "user", Content: "Question", CreatedAt: yesterday.UnixMilli()},
		mainChatViewCard(t, "wait-yesterday", "decision_due", "opened", yesterday),
		mainChatViewCard(t, "wait-a", "waiting_on_reply", "pending", today), mainChatViewCard(t, "wait-b", "waiting_on_reply", "pending", today),
		mainChatViewCard(t, "dismissed", "waiting_on_reply", "dismissed", today),
		mainChatViewCard(t, "routine", "capture_paused", "pending", today),
		mainChatViewCard(t, "finished", "work_finished", "acted", today), mainChatViewCard(t, "old-finished", "work_finished", "acted", yesterday),
		{ID: "status", Kind: "system", ToolName: mainChatStatusTool, CreatedAt: today.UnixMilli(), ToolInput: mainChatViewPayload(t, mainChatStatus{Entries: []mainChatStatusEntry{{ID: "one"}, {ID: "two"}, {ID: "three"}}})},
		{ID: "old-status", Kind: "system", ToolName: mainChatStatusTool, CreatedAt: yesterday.UnixMilli(), ToolInput: mainChatViewPayload(t, mainChatStatus{Entries: []mainChatStatusEntry{{ID: "old"}}})},
		{ID: "schedule", Kind: "system", ToolName: mainChatReportTool, CreatedAt: today.UnixMilli(), ToolInput: mainChatViewPayload(t, mainChatReport{Source: mainChatSourceSchedule, Status: "created"})},
	}
	for _, row := range rows {
		if _, err = a.store.AppendStreamItem(ctx, main.LocalID, row); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := a.mainChatOverview(ctx, today)
	if err != nil || overview.Conversations != 1 || overview.Waiting != 3 || overview.Finished != 1 || overview.Routine != 5 || overview.HideRoutine || !reflect.DeepEqual(overview.WaitingItemIDs, []string{"wait-b", "wait-a", "wait-yesterday"}) {
		t.Fatalf("overview %+v %v", overview, err)
	}
	page, err := a.ListMainChatWaitingItems("", 2)
	if err != nil || !page.HasMore || len(page.Items) != 2 || page.Items[0].ID != "wait-a" || page.Items[1].ID != "wait-b" {
		t.Fatalf("page %+v %v", page, err)
	}
	next, err := a.ListMainChatWaitingItems(page.NextCursor, 2)
	if err != nil || next.HasMore || len(next.Items) != 1 || next.Items[0].ID != "wait-yesterday" || next.Items[0].RowClass != "waiting" || next.Items[0].LocalDay != yesterday.Format("2006-01-02") {
		t.Fatalf("next %+v %v", next, err)
	}
	var invalid *MainChatWaitingCursorError
	if _, err = a.ListMainChatWaitingItems("bad", 2); !errors.As(err, &invalid) {
		t.Fatalf("invalid %v", err)
	}
	if err = a.SetMainChatHideRoutine(true); err != nil {
		t.Fatal(err)
	}
	before, err := a.store.LoadStreamItems(ctx, main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	// Reopen the actual file to prove the preference is not process state.
	reopened, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := &App{store: reopened}
	overview, err = restarted.mainChatOverview(ctx, today)
	if err != nil || !overview.HideRoutine || overview.Waiting != 3 {
		t.Fatalf("restart %+v %v", overview, err)
	}
	after, err := a.store.LoadStreamItems(ctx, main.LocalID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("view mutated history")
	}
}

func TestMainChatWaitingReplyNotNowAndLegacyHeadlines(t *testing.T) {
	a := mainChatTestApp(t)
	id := a.postProactiveItem(mainChatProactive{MomentID: "reply", Kind: "waiting_on_reply", State: "pending", Headline: "Someone is waiting on your reply.", Body: "Someone is waiting on your reply.", ActionIDs: []string{"open", "dismiss"}}, "reply")
	if id == "" {
		t.Fatal("no card")
	}
	state, err := a.ResolveMainChatProactiveAction("reply", "not_now")
	if err != nil || state != "deferred" {
		t.Fatalf("state %s %v", state, err)
	}
	row, err := a.store.LoadStreamItem(t.Context(), id)
	if err != nil || mainChatRowClass(*row, time.Now()) != MainChatRowOther {
		t.Fatalf("still waiting: %+v %v", row, err)
	}
	for _, lang := range engagement.Languages {
		catalog, _ := engagement.Copy(engagement.KindCapturePaused, lang)
		card := mainChatProactive{MomentID: "capture", Kind: "capture_paused", Headline: catalog.Actions["open_settings"], Body: catalog.Actions["open_settings"], Localized: map[string]mainChatProactiveCopy{}}
		for _, locale := range engagement.Languages {
			copy, _ := engagement.Copy(engagement.KindCapturePaused, locale)
			card.Localized[locale] = mainChatProactiveCopy{Headline: copy.Actions["open_settings"], Body: copy.Actions["open_settings"]}
		}
		var got mainChatProactive
		if err := json.Unmarshal([]byte(localizedMainChatToolInput(mainChatProactiveTool, mainChatViewPayload(t, card))), &got); err != nil {
			t.Fatal(err)
		}
		for _, locale := range engagement.Languages {
			copy, _ := engagement.Copy(engagement.KindCapturePaused, locale)
			if got.Localized[locale].Headline != copy.PushBody || got.Localized[locale].Body != copy.PushBody {
				t.Fatalf("%s repair %+v", locale, got)
			}
		}
		english, _ := engagement.Copy(engagement.KindCapturePaused, "en")
		if got.Headline != english.PushBody || got.Body != english.PushBody {
			t.Fatalf("legacy card copy %+v", got)
		}
		entry := mainChatStatusEntry{Kind: "capture_paused", Text: catalog.Actions["open_settings"], Localized: map[string]string{lang: catalog.Actions["open_settings"]}}
		populateMainChatStatusCopy(&entry)
		if entry.Text != english.PushBody || entry.Localized[lang] != catalog.PushBody {
			t.Fatalf("status %+v", entry)
		}
	}
}

func TestMainChatViewLegacyExpiryAndOtherSessions(t *testing.T) {
	a := mainChatTestApp(t)
	ctx := t.Context()
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	moment, err := a.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{Kind: "waiting_on_reply", DedupeKey: "expired-legacy", ExpiresAt: now.Add(-time.Minute).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	row := mainChatViewCard(t, moment.ID, "waiting_on_reply", "pending", now)
	if _, err = a.store.AppendStreamItem(ctx, main.LocalID, row); err != nil {
		t.Fatal(err)
	}
	overview, err := a.GetMainChatOverview()
	if err != nil || overview.Waiting != 0 {
		t.Fatalf("expired overview %+v %v", overview, err)
	}
	page, err := a.ListMainChatWaitingItems("", 50)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("expired page %+v %v", page, err)
	}
	if item := mainChatRowPageItem(t, a, main.LocalID, row.ID); item.RowClass != "other" {
		t.Fatalf("legacy expiry %+v", item)
	}
	if err = a.store.SaveSession(ctx, storage.SessionRow{LocalID: "side", ProjectID: main.ProjectID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.AppendStreamItem(ctx, "side", storage.StreamItemRow{ID: "side-user", Kind: "user", Content: "Words"}); err != nil {
		t.Fatal(err)
	}
	side, err := a.LoadStreamItemsPage("side", -1, 50)
	if err != nil || len(side.Items) != 1 {
		t.Fatalf("side %+v %v", side, err)
	}
	data := mainChatViewPayload(t, side.Items[0])
	for _, field := range []string{"rowClass", "topicIds", "localDay"} {
		if strings.Contains(data, field) {
			t.Fatalf("side has %s", field)
		}
	}
	saved, err := a.store.LoadStreamItem(ctx, row.ID)
	if err != nil || saved.ToolInput != row.ToolInput {
		t.Fatal("expiry read rewrote card")
	}
}
func TestMainChatOverviewEmptyReadDoesNotCreateSession(t *testing.T) {
	a := mainChatTestApp(t)
	if err := a.SetMainChatHideRoutine(true); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetMainChatOverview()
	if err != nil || got.SessionID != "" || !got.HideRoutine {
		t.Fatalf("empty %+v %v", got, err)
	}
	page, err := a.ListMainChatWaitingItems("", 50)
	if err != nil || page.SessionID != "" || len(page.Items) != 0 {
		t.Fatalf("empty waiting %+v %v", page, err)
	}
	if _, err = a.store.LoadMainSession(t.Context()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("read created main session")
	}
}
