package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func TestMainChatMeetingPrepSentenceEnglishAndChinese(t *testing.T) {
	start := time.Date(2026, 10, 26, 14, 5, 0, 0, time.UTC)
	item := mainChatProactive{Kind: "meeting_prep", Localized: map[string]mainChatProactiveCopy{
		"en":    {Headline: "A meeting is coming up."},
		"zh-CN": {Headline: "会议即将开始。"},
	}}
	populateMainChatEventCopy(&item, map[string]string{
		"event_title":   "Design review",
		"event_start":   "2026-10-26T14:05:00Z",
		"event_minutes": "15",
	})
	if got := item.Localized["en"]; got.Headline != "Design review starts at "+computerClock(start.Local(), "en")+"." || got.Body != "" {
		t.Fatalf("English copy = %+v", got)
	}
	if got := item.Localized["zh-CN"]; got.Headline != "Design review"+computerClock(start.Local(), "zh-CN")+"开始。" || got.Body != "" {
		t.Fatalf("Chinese copy = %+v", got)
	}
	populateMainChatEventCopy(&item, map[string]string{
		"event_title": "Design review", "event_start": "2026-10-26T14:05:00Z", "event_people": "Dana",
	})
	if got := item.Localized["en"].Headline; got != "Design review with Dana starts at "+computerClock(start.Local(), "en")+"." {
		t.Fatalf("English copy with someone = %q", got)
	}
	if got := item.Localized["zh-CN"].Headline; got != "和 Dana 的Design review"+computerClock(start.Local(), "zh-CN")+"开始。" {
		t.Fatalf("Chinese copy with someone = %q", got)
	}
}

func TestMainChatPrivateMeetingAndConflictSentences(t *testing.T) {
	meeting := mainChatProactive{Kind: "meeting_prep", Localized: map[string]mainChatProactiveCopy{
		"en": {}, "zh-CN": {},
	}}
	populateMainChatEventCopy(&meeting, map[string]string{
		"event_title": "Private planning", "event_start": "2026-10-26T14:05:00Z", "event_minutes": "15",
		"event_private": "true", "event_people": "Dana", "event_place": "Office",
	})
	// Isoform doesn't know a private meeting's title: no meeting sentence.
	if got := meeting.Localized["en"]; got.Headline != "" || got.Body != "" {
		t.Fatalf("private English copy = %+v", got)
	}
	if got := mainChatProactiveEventFromParams(map[string]string{
		"event_title": "   ", "event_start": "2026-10-26T14:05:00Z", "event_people": "Dana",
	}, "event_"); got == nil || !got.Private || got.Title != "" || got.People != "" {
		t.Fatalf("restricted event payload = %+v", got)
	}
	if got := mainChatProactiveEventFromParams(map[string]string{
		"event_title": "Invoice for $500", "event_start": "2026-10-26T14:05:00Z",
	}, "event_"); got == nil || !got.Private || got.Title != "" {
		t.Fatalf("restricted event title = %+v", got)
	}

	for _, tc := range []struct {
		name                        string
		firstPrivate, secondPrivate bool
		wantEN, wantZH              string
	}{
		{"both named", false, false, "Design and Planning.", "“Design”和“Planning”。"},
		{"one private", true, false, "Planning and a private event.", "“Planning”和一个私人日程。"},
		{"both private", true, true, "Two private events.", "两个私人日程。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := mainChatProactive{Kind: "calendar_conflict", Events: []mainChatProactiveEvent{
				{Title: "Design", Private: tc.firstPrivate},
				{Title: "Planning", Private: tc.secondPrivate},
			}, Localized: map[string]mainChatProactiveCopy{"en": {}, "zh-CN": {}}}
			populateMainChatEventCopy(&item, map[string]string{"event_type": "overlap", "event_overlap_at": "2026-10-26T14:05:00Z"})
			if got := item.Localized["en"]; got.Headline != "Two meetings overlap at 7:05 AM." || got.Body != tc.wantEN {
				t.Fatalf("English conflict copy = %+v", got)
			}
			if got := item.Localized["zh-CN"]; got.Headline != "上午7:05 有两个会议冲突。" || got.Body != tc.wantZH {
				t.Fatalf("Chinese conflict copy = %+v", got)
			}
		})
	}
}

func TestThreadOutcomePlaceUsesBundleFromNearestRunEvent(t *testing.T) {
	app, bundleID := threadOutcomeRunPlace(ActivityTimelineRunRow{
		AppKey: "app:Safari",
	}, []ActivityTimelineEvent{
		{ActivityEvent: activitymon.ActivityEvent{AppName: "Safari", AppBundleID: "com.apple.Safari", Timestamp: 1000}},
		{ActivityEvent: activitymon.ActivityEvent{AppName: "Browser", AppBundleID: "com.example.browser", Timestamp: 2000}},
	}, 1900)
	if app != "Browser" || bundleID != "com.example.browser" {
		t.Fatalf("run place = %q %q, want nearest event app and bundle id", app, bundleID)
	}
}

func TestMainChatThreadOutcomeSentenceAndPlace(t *testing.T) {
	item := mainChatProactive{Kind: "decision_due", Localized: map[string]mainChatProactiveCopy{"en": {}, "zh-CN": {}}}
	populateMainChatEventCopy(&item, map[string]string{
		"title": "Project Atlas", "next_step": "send the review", "app": "Editor", "time": "14:05",
	})
	if got := item.Localized["en"]; got.Headline != "Project Atlas." || got.Body != "Next: send the review." {
		t.Fatalf("English thread copy = %+v", got)
	}
	if got := item.Localized["zh-CN"]; got.Headline != "Project Atlas。" || got.Body != "接下来：send the review。" {
		t.Fatalf("Chinese thread copy = %+v", got)
	}
	for _, copy := range item.Localized {
		if strings.Contains(copy.Headline+copy.Body, "next step") || strings.Contains(copy.Headline+copy.Body, "14:05") {
			t.Fatalf("left-off copy kept the template or a second time: %+v", copy)
		}
	}
}

func TestMainChatNoSentenceCardIsNotPostedAndLogsKindOnly(t *testing.T) {
	app := mainChatTestApp(t)
	copy, _ := engagement.Copy(engagement.KindMeetingPrep, "en")
	var output bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(oldOutput)

	_, posted := app.proactiveItem(engagement.Moment{
		ID: "generic-meeting", Kind: engagement.KindMeetingPrep, SubjectID: "event-1",
		Params: map[string]string{},
	}, engagementText{PushBody: copy.PushBody, Title: copy.PushBody})
	if posted {
		t.Fatal("generic-only meeting card was posted")
	}
	if got := output.String(); !strings.Contains(got, "[engagement] card not posted: kind=meeting_prep reason=no_title") {
		t.Fatalf("no-post log = %q", got)
	}
	if strings.Contains(output.String(), "event-1") {
		t.Fatalf("log disclosed card content: %q", output.String())
	}
}

func TestLoadMainChatProactiveItemAndReturnToPlace(t *testing.T) {
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(mainChatProactive{
		MomentID: "moment-place", Kind: "decision_due", Headline: "A next step.", State: "pending",
		Target: mainChatTarget{Kind: "thread", ID: "thread-1"},
		Place:  &mainChatProactivePlace{App: "Editor", BundleID: "com.example.editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	app.appendMainChatItem(context.Background(), main.LocalID, storage.StreamItemRow{
		ID: "proactive-row", Kind: "system", Content: "A next step.", ToolName: mainChatProactiveTool, ToolInput: string(payload),
	}, mainChatSourceEngagement)

	loaded, err := app.LoadMainChatProactiveItem("moment-place")
	if err != nil || loaded == nil || !strings.Contains(loaded.ToolInput, "moment-place") {
		t.Fatalf("loaded proactive item = %+v, %v", loaded, err)
	}
	oldActivate := activateMainChatPlaceApp
	t.Cleanup(func() { activateMainChatPlaceApp = oldActivate })
	var activated string
	activateMainChatPlaceApp = func(bundleID string) bool {
		activated = bundleID
		return true
	}
	ok, err := app.ReturnToMainChatPlace("moment-place")
	if err != nil || !ok || activated != "com.example.editor" {
		t.Fatalf("return to place = %v, %v, activated %q", ok, err, activated)
	}
}

func TestLoadMainChatProactiveItemSelectsDigestChild(t *testing.T) {
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	digest := mainChatProactive{MomentID: "digest", Kind: "digest", State: "pending", Items: []mainChatProactive{
		{MomentID: "first", Kind: "decision_due", Headline: "First item", State: "pending"},
		{MomentID: "second", Kind: "work_blocked", Headline: "Second item", State: "pending"},
	}}
	raw, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	app.appendMainChatItem(context.Background(), main.LocalID, storage.StreamItemRow{ID: "digest-row", Kind: "system", Content: "First item and Second item", ToolName: mainChatProactiveTool, ToolInput: string(raw)}, mainChatSourceEngagement)
	got, err := app.LoadMainChatProactiveItem("second")
	if err != nil || got == nil {
		t.Fatalf("load child: %+v, %v", got, err)
	}
	var card mainChatProactive
	if err := json.Unmarshal([]byte(got.ToolInput), &card); err != nil {
		t.Fatal(err)
	}
	if card.MomentID != "second" || card.Headline == "" || strings.Contains(got.ToolInput+got.Content, "First item") {
		t.Fatalf("wrong child projection: %+v", got)
	}
}
