package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func newDebugTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.New(filepath.Join(dir, "debug.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &App{store: st}
}

// ── ClassifyActivityEvents ──

func TestClassifyActivityEvents_EmptyReturnsEmpty(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ClassifyActivityEvents(nil, false)
	if got == nil || len(got) != 0 {
		t.Errorf("expected non-nil empty slice, got %v", got)
	}
}

func TestClassifyActivityEvents_AssignsContentModePerKind(t *testing.T) {
	a := newDebugTestApp(t)
	events := []activitymon.ActivityEvent{
		{Kind: activitymon.EventClick, AppName: "VSCode"},
		{Kind: activitymon.EventKeyboard, AppName: "VSCode"},
		{Kind: activitymon.EventFocus, AppName: "VSCode"},
		{Kind: activitymon.EventAppSwitch, AppName: "Slack"},
		{Kind: activitymon.EventSpeech, SpeechSource: "microphone", AppName: "VSCode"},
		{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppName: "Spotify", AppBundleID: "com.spotify.client"},
	}
	got := a.ClassifyActivityEvents(events, false)
	if len(got) != len(events) {
		t.Fatalf("len = %d, want %d", len(got), len(events))
	}
	want := []string{"performed", "performed", "performed", "performed", "performed", "observed"}
	for i, w := range want {
		if got[i].ContentMode != w {
			t.Errorf("[%d] ContentMode = %q, want %q", i, got[i].ContentMode, w)
		}
		if got[i].Index != i {
			t.Errorf("[%d] Index = %d, want %d", i, got[i].Index, i)
		}
		if got[i].Reasoning == "" {
			t.Errorf("[%d] Reasoning empty", i)
		}
	}
}

func TestClassifyActivityEvents_SystemAudioInMeetingIsReceived(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ClassifyActivityEvents([]activitymon.ActivityEvent{
		{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppName: "Zoom", AppBundleID: "us.zoom.xos", SpeechText: "Shipped Friday?"},
	}, true)
	if got[0].ContentMode != "received" {
		t.Errorf("ContentMode = %q, want received", got[0].ContentMode)
	}
	if !strings.Contains(got[0].Reasoning, "meeting") {
		t.Errorf("reasoning missing 'meeting': %q", got[0].Reasoning)
	}
	if got[0].SpeechHints.Source != "system_audio" {
		t.Errorf("SpeechHints.Source = %q, want system_audio", got[0].SpeechHints.Source)
	}
	if !got[0].SpeechHints.MeetingContext {
		t.Errorf("SpeechHints.MeetingContext should be true")
	}
	if !got[0].SpeechHints.NonUserSpeech {
		t.Errorf("SpeechHints.NonUserSpeech should be true for system_audio")
	}
}

func TestClassifyActivityEvents_BrowserVideoTitleFlagged(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ClassifyActivityEvents([]activitymon.ActivityEvent{
		{
			Kind:         activitymon.EventSpeech,
			SpeechSource: "microphone",
			AppName:      "Google Chrome",
			AppBundleID:  "com.google.Chrome",
			WindowTitle:  "Funny cats compilation — YouTube",
			SpeechText:   "ha ha ha",
		},
	}, false)
	if got[0].ContentMode != "observed" {
		t.Errorf("ContentMode = %q, want observed", got[0].ContentMode)
	}
	if !got[0].SpeechHints.BrowserMedia {
		t.Errorf("BrowserMedia flag not set")
	}
	if !strings.Contains(got[0].Reasoning, "browser") {
		t.Errorf("reasoning missing 'browser': %q", got[0].Reasoning)
	}
}

func TestClassifyActivityEvents_MediaAppFlagged(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ClassifyActivityEvents([]activitymon.ActivityEvent{
		{
			Kind:         activitymon.EventSpeech,
			SpeechSource: "microphone",
			AppName:      "Spotify",
			AppBundleID:  "com.spotify.client",
			SpeechText:   "lyric line",
		},
	}, false)
	if !got[0].SpeechHints.MediaApp {
		t.Errorf("MediaApp flag not set")
	}
	if got[0].ContentMode != "observed" {
		t.Errorf("ContentMode = %q, want observed", got[0].ContentMode)
	}
}

func TestClassifyActivityEvents_WeChatVideoCallFlagged(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ClassifyActivityEvents([]activitymon.ActivityEvent{
		{
			Kind:         activitymon.EventSpeech,
			SpeechSource: "microphone",
			AppBundleID:  "com.tencent.xinWeChat",
			WindowTitle:  "视频通话",
		},
	}, false)
	if got[0].ContentMode != "observed" {
		t.Errorf("ContentMode = %q, want observed", got[0].ContentMode)
	}
	if !strings.Contains(got[0].Reasoning, "WeChat") {
		t.Errorf("reasoning missing 'WeChat': %q", got[0].Reasoning)
	}
}

func TestClassifyActivityEvents_PlainMicSpeechReasoningMentionsApp(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ClassifyActivityEvents([]activitymon.ActivityEvent{
		{
			Kind:         activitymon.EventSpeech,
			SpeechSource: "microphone",
			AppName:      "Visual Studio Code",
			AppBundleID:  "com.microsoft.VSCode",
			SpeechText:   "let me think about this",
		},
	}, false)
	if got[0].ContentMode != "performed" {
		t.Errorf("ContentMode = %q, want performed", got[0].ContentMode)
	}
	if !strings.Contains(got[0].Reasoning, "Visual Studio Code") {
		t.Errorf("reasoning missing app name: %q", got[0].Reasoning)
	}
}

// ── ResolveChatSender ──

func TestResolveChatSender_NilStoreReturnsUnknown(t *testing.T) {
	a := &App{}
	got := a.ResolveChatSender("wechat", "张三")
	if got.Kind != "unknown" {
		t.Errorf("Kind = %q, want unknown", got.Kind)
	}
	if got.Resolved {
		t.Errorf("Resolved should be false")
	}
	if !strings.HasPrefix(got.Label, "[Unknown:") {
		t.Errorf("Label = %q, want [Unknown:...]", got.Label)
	}
}

func TestResolveChatSender_EmptySenderReturnsUnknown(t *testing.T) {
	a := newDebugTestApp(t)
	got := a.ResolveChatSender("wechat", "   ")
	if got.Resolved {
		t.Errorf("Resolved should be false for empty sender")
	}
	if got.Label != "[Unknown: —]" {
		t.Errorf("Label = %q, want [Unknown: —]", got.Label)
	}
}

func TestResolveChatSender_SelfReturnsYouLabel(t *testing.T) {
	a := newDebugTestApp(t)
	self, err := a.store.EnsureSelf(context.Background(), "Me", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	if _, err := a.store.BindAppHandle(context.Background(), self.ID, "wechat", "wxid_me", "me"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	got := a.ResolveChatSender("wechat", "me")
	if got.Kind != "self" {
		t.Errorf("Kind = %q, want self", got.Kind)
	}
	if got.PrincipalID != self.ID {
		t.Errorf("PrincipalID = %q, want %q", got.PrincipalID, self.ID)
	}
	if got.Label != "[YOU]" {
		t.Errorf("Label = %q, want [YOU]", got.Label)
	}
	if got.AttributionMethod == "" {
		t.Errorf("AttributionMethod should be set")
	}
}

func TestResolveChatSender_ContactWithRelationshipFormatsLabel(t *testing.T) {
	a := newDebugTestApp(t)
	c, err := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind:         storage.PrincipalKindContact,
		DisplayName:  "Zhang San",
		Relationship: "friend",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := a.store.BindAppHandle(context.Background(), c.ID, "wechat", "", "张三"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	got := a.ResolveChatSender("wechat", "张三")
	if got.Kind != "contact" {
		t.Errorf("Kind = %q, want contact", got.Kind)
	}
	if got.DisplayName != "Zhang San" {
		t.Errorf("DisplayName = %q, want Zhang San", got.DisplayName)
	}
	if got.Relationship != "friend" {
		t.Errorf("Relationship = %q, want friend", got.Relationship)
	}
	if got.Label != "[Contact: Zhang San, friend]" {
		t.Errorf("Label = %q, want [Contact: Zhang San, friend]", got.Label)
	}
}

func TestResolveChatSender_ReadOnlyDoesNotCreateAnonymousBucket(t *testing.T) {
	a := newDebugTestApp(t)
	// Sender does not exist anywhere; debug call should leave the principals
	// table untouched (autoBucket disabled).
	before, _ := a.store.ListPrincipals(context.Background())
	got := a.ResolveChatSender("telegram", "@stranger")
	if got.Resolved {
		t.Errorf("Resolved should be false")
	}
	if got.Kind != "unknown" {
		t.Errorf("Kind = %q, want unknown", got.Kind)
	}
	after, _ := a.store.ListPrincipals(context.Background())
	if len(after) != len(before) {
		t.Errorf("principals table mutated: before=%d after=%d", len(before), len(after))
	}
}

func TestResolveChatSender_CaseInsensitiveAppNormalization(t *testing.T) {
	a := newDebugTestApp(t)
	c, _ := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "C",
	})
	_, _ = a.store.BindAppHandle(context.Background(), c.ID, "telegram", "tg-1", "@c")

	got := a.ResolveChatSender("TELEGRAM", "@c")
	if got.Kind != "contact" {
		t.Errorf("uppercase app should still resolve: got %+v", got)
	}
}

// ── Browser-media helper ──

func TestIsBrowserMediaTitle(t *testing.T) {
	cases := []struct {
		bundle, title string
		want          bool
	}{
		{"com.google.Chrome", "Funny cats — YouTube", true},
		{"com.apple.Safari", "B站直播 哔哩哔哩", true},
		{"company.thebrowser.Browser", "Spotify Web Player", false}, // arc match passes bundle check; title doesn't hit hint list
		{"com.microsoft.VSCode", "Funny cats — YouTube", false},     // editor bundle, not a browser
		{"", "Anything", false},
		{"com.google.Chrome", "", false},
	}
	for _, c := range cases {
		got := isBrowserMediaTitle(c.bundle, c.title)
		if got != c.want {
			t.Errorf("isBrowserMediaTitle(%q, %q) = %v, want %v", c.bundle, c.title, got, c.want)
		}
	}
}
