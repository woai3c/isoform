package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestPopulateMainChatProactiveCopyDropsRepeatedBody(t *testing.T) {
	tests := []struct {
		name                  string
		item                  mainChatProactive
		params                map[string]string
		wantBody              string
		wantLocalizedHeadline string
		wantLocalizedBody     string
	}{
		{name: "identical body", item: mainChatProactive{Kind: "brief_ready", Headline: "Your brief is ready.", Body: "Your brief is ready."}, wantBody: ""},
		{name: "trailing period", item: mainChatProactive{Kind: "brief_ready", Headline: "Your brief is ready", Body: "Your brief is ready."}, wantBody: ""},
		{name: "case and spaces", item: mainChatProactive{Kind: "brief_ready", Headline: "Your brief is ready.", Body: "  yOUR bRIEF is ready.  "}, wantBody: ""},
		{name: "different body", item: mainChatProactive{Kind: "brief_ready", Headline: "Your brief is ready.", Body: "The result is ready to review."}, wantBody: "The result is ready to review."},
		{name: "Chinese identical body", item: mainChatProactive{Kind: "brief_ready", Headline: "你的简报已经准备好了。", Body: "你的简报已经准备好了。"}, wantBody: ""},
		{name: "brief ready morning slot", item: mainChatProactive{Kind: "brief_ready", Headline: "Your morning brief is ready.", Body: "Your morning brief is ready."}, params: map[string]string{"slot": "morning"}, wantBody: "", wantLocalizedHeadline: "Your morning brief is ready.", wantLocalizedBody: ""},
		{name: "brief ready unknown slot", item: mainChatProactive{Kind: "brief_ready", Headline: "Your brief is ready.", Body: "Your brief is ready."}, params: map[string]string{"slot": "unknown"}, wantBody: "", wantLocalizedHeadline: "Your brief is ready.", wantLocalizedBody: ""},
		{name: "status entry keeps repeated body", item: mainChatProactive{Kind: "capture_paused", VoiceVersion: "v1", Headline: "Isoform stopped watching for a while.", Body: "Isoform stopped watching for a while."}, wantBody: "Isoform stopped watching for a while."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item := tc.item
			populateMainChatProactiveCopy(&item, tc.params)
			if item.Body != tc.wantBody {
				t.Errorf("body = %q, want %q", item.Body, tc.wantBody)
			}
			if tc.wantLocalizedHeadline != "" {
				localized := item.Localized["en"]
				if localized.Headline != tc.wantLocalizedHeadline || localized.Body != tc.wantLocalizedBody {
					t.Errorf("localized copy = %+v, want headline %q and body %q", localized, tc.wantLocalizedHeadline, tc.wantLocalizedBody)
				}
			}
		})
	}
}

func TestProactiveStoredCopyIncludesBothLanguagesWithoutMailSubjects(t *testing.T) {
	app := mainChatTestApp(t)
	app.postEngagementReport(engagement.Moment{ID: "brief-localized", Kind: engagement.KindBriefReady, SubjectID: "brief", Params: map[string]string{"slot": "morning"}}, engagementText{PushBody: "Your brief is ready.", Title: "Your morning brief is ready."})
	rows := mainChatStructuredRows(t, app, mainChatProactiveTool)
	var payload struct {
		Localized map[string]struct {
			Headline, Body string
			ActionLabels   map[string]string
		}
	}
	if err := json.Unmarshal([]byte(rows[0].ToolInput), &payload); err != nil {
		t.Fatal(err)
	}
	zh := payload.Localized["zh-CN"]
	if zh.Headline != "你的简报已经准备好了。" || zh.Body != "" || zh.ActionLabels["open"] != "播放" {
		t.Fatalf("missing Chinese catalog copy: %+v", payload)
	}
	// The fixture has no persisted brief evidence. Read-time rejection keeps
	// both locales generic rather than trusting the stale slot parameter.
	if payload.Localized["en"].Headline != "Your brief is ready." || payload.Localized["en"].Body != "" {
		t.Fatalf("missing English copy: %+v", payload)
	}
	app.postEngagementReport(engagement.Moment{ID: "mail-localized", Kind: engagement.KindWaitingOnReply, SubjectID: "person", Params: map[string]string{"name": "Dana", "subject": "Secret acquisition"}}, engagementText{PushBody: "Someone is waiting on your reply.", Title: "A reply needs your attention."})
	rows = mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(rows) != 1 {
		t.Fatalf("generic-only mail card reached chat: %+v", rows)
	}
}

func TestProactiveLegacyPageUsesGenericLocalizedCopyWithoutSourceReads(t *testing.T) {
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"momentId":"legacy","kind":"brief_ready","headline":"Your brief is ready.","body":"Your morning brief is ready.","actionIds":["open"],"actionLabels":{"open":"Open brief"},"state":"pending","target":{"kind":"today"}}`
	if _, err = app.store.AppendStreamItem(context.Background(), main.LocalID, storage.StreamItemRow{ID: "legacy-copy", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: payload}); err != nil {
		t.Fatal(err)
	}
	page, err := app.LoadStreamItemsPage(main.LocalID, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range page.Items {
		if item.ID == "legacy-copy" {
			found = true
			if !strings.Contains(item.ToolInput, "你的简报已经准备好了。") || !strings.Contains(item.ToolInput, "播放") ||
				strings.Contains(item.ToolInput, "morning") || strings.Contains(item.ToolInput, "早间") {
				t.Fatalf("legacy copy not localized: %s", item.ToolInput)
			}
		}
	}
	if !found {
		t.Fatal("legacy row missing")
	}
	stored, err := app.store.LoadStreamItem(context.Background(), "legacy-copy")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ToolInput != payload {
		t.Fatal("read hydration rewrote history")
	}
}

func TestMainChatStatusStoredAndLegacyCopyLocalizes(t *testing.T) {
	app := mainChatTestApp(t)
	app.postScheduleRunReport(CronRunLifecycleEvent{JobID: "job", RunID: "run", Status: "ok", ResultSummary: "ready"}, "Research", "scheduled")
	rows := mainChatStructuredRows(t, app, mainChatStatusTool)
	if !strings.Contains(rows[0].ToolInput, "Research已生成结果。") {
		t.Fatalf("status translation missing: %s", rows[0].ToolInput)
	}
	// Legacy capture rows cannot prove current source provenance. Delivery
	// replaces their old outage claim with source-free localized catalog copy.
	id := app.postMainChatStatus(mainChatStatusEntry{ID: "capture-legacy", Kind: "capture_paused", Text: "Screen capture has been off for 20 minutes. Turn it back on?"}, time.Now())
	main, _ := app.ensureMainChat(context.Background())
	page, err := app.LoadStreamItemsPage(main.LocalID, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == id {
			if !strings.Contains(item.ToolInput, "Isoform 暂停观察了一会儿。") || strings.Contains(item.ToolInput, "20 minutes") || strings.Contains(item.ToolInput, "20 分钟") {
				t.Fatalf("capture translation missing: %s", item.ToolInput)
			}
		}
	}
}

func TestLegacyChineseGenericStatusGetsEnglishCopy(t *testing.T) {
	entry := mainChatStatusEntry{Kind: "brief_missed", Text: "你的简报没有送达。"}
	populateMainChatStatusCopy(&entry)
	if entry.Localized["en"] != "Your brief didn't come." || entry.Localized["zh-CN"] != "你的简报没有来。" {
		t.Fatalf("legacy language frozen: %+v", entry.Localized)
	}
}

func TestLocalizedProactiveCopyRedactsEveryVariantAndRefreshesDigestCount(t *testing.T) {
	app := mainChatTestApp(t)
	// An unvoiced kind isolates redaction from the source-rejection fallback.
	m := engagement.Moment{ID: "redacted-locales", Kind: engagement.KindGoalCheckIn, SubjectID: "goal", Params: map[string]string{"title": "Use 4111 1111 1111 1111"}}
	item, ok := app.proactiveItem(m, engagementText{PushBody: "A quick check on a goal.", Title: `You set "Use 4111 1111 1111 1111". How is it going?`})
	if !ok {
		t.Fatal("safe moment dropped")
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "4111") {
		t.Fatalf("localized variants exposed card: %s", encoded)
	}
	for _, lang := range engagement.Languages {
		if !strings.Contains(item.Localized[lang].Body, "REDACTED:credit_card") {
			t.Fatalf("%s not redacted: %+v", lang, item)
		}
	}
	app.postProactiveItem(item, "earlier-child")
	fresh := mainChatProactive{MomentID: "fresh", Kind: "brief_ready", Headline: "Your brief is ready.", Body: "Your morning brief is ready.", ActionIDs: []string{"open"}}
	digest := mainChatProactive{MomentID: "digest-partial", Kind: "digest", Items: []mainChatProactive{item, fresh}}
	populateMainChatProactiveCopy(&digest, nil)
	id := app.postProactiveItem(digest, "partial-localized")
	row, err := app.store.LoadStreamItem(context.Background(), id)
	if err != nil || row == nil {
		t.Fatalf("digest not stored: %v", err)
	}
	var stored mainChatProactive
	if err := json.Unmarshal([]byte(row.ToolInput), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Items) != 1 || stored.Localized["en"].Headline != "One thing to catch up on." || stored.Localized["zh-CN"].Headline != "有一件事可以看看。" {
		t.Fatalf("deduped digest locale count stale: %+v", stored)
	}
}
