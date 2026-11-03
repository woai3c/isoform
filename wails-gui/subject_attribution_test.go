package main

import (
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestEventContentMode(t *testing.T) {
	tests := []struct {
		name      string
		evt       activitymon.ActivityEvent
		inMeeting bool
		want      ContentMode
	}{
		{
			name: "keyboard_event_is_performed",
			evt: activitymon.ActivityEvent{
				Kind:        activitymon.EventKeyboard,
				AppBundleID: "com.microsoft.VSCode",
				KeyChars:    []string{"h", "e", "l", "l", "o"},
			},
			want: ContentModePerformed,
		},
		{
			name: "click_event_is_performed_even_in_media_app",
			evt: activitymon.ActivityEvent{
				Kind:        activitymon.EventClick,
				AppBundleID: "tv.danmaku.bili.client",
			},
			want: ContentModePerformed,
		},
		{
			name: "focus_event_is_performed",
			evt: activitymon.ActivityEvent{
				Kind:        activitymon.EventFocus,
				AppBundleID: "com.apple.Safari",
				FocusRole:   "AXTextField",
			},
			want: ContentModePerformed,
		},
		{
			name: "app_switch_is_performed",
			evt: activitymon.ActivityEvent{
				Kind:        activitymon.EventAppSwitch,
				AppBundleID: "com.tdesktop.Telegram",
			},
			want: ContentModePerformed,
		},
		{
			name: "system_audio_in_meeting_is_received",
			evt: activitymon.ActivityEvent{
				Kind:         activitymon.EventSpeech,
				SpeechSource: "system_audio",
				SpeechText:   "We should ship Friday.",
				AppBundleID:  "us.zoom.xos",
			},
			inMeeting: true,
			want:      ContentModeReceived,
		},
		{
			name: "system_audio_outside_meeting_is_observed",
			evt: activitymon.ActivityEvent{
				Kind:         activitymon.EventSpeech,
				SpeechSource: "system_audio",
				SpeechText:   "Some video soundtrack",
				AppBundleID:  "tv.danmaku.bili.client",
			},
			want: ContentModeObserved,
		},
		{
			name: "microphone_in_media_app_is_observed_outside_meeting",
			evt: activitymon.ActivityEvent{
				Kind:         activitymon.EventSpeech,
				SpeechSource: "microphone",
				SpeechText:   "narration leaking from speakers",
				AppBundleID:  "com.spotify.client",
			},
			want: ContentModeObserved,
		},
		{
			name: "microphone_in_browser_with_youtube_title_is_observed",
			evt: activitymon.ActivityEvent{
				Kind:         activitymon.EventSpeech,
				SpeechSource: "microphone",
				SpeechText:   "background podcast voice",
				AppBundleID:  "com.google.Chrome",
				WindowTitle:  "Long video — YouTube",
			},
			want: ContentModeObserved,
		},
		{
			name: "microphone_in_editor_is_performed",
			evt: activitymon.ActivityEvent{
				Kind:         activitymon.EventSpeech,
				SpeechSource: "microphone",
				SpeechText:   "let me think about this function",
				AppBundleID:  "com.microsoft.VSCode",
			},
			want: ContentModePerformed,
		},
		{
			name: "wechat_video_call_window_is_observed_outside_meeting",
			evt: activitymon.ActivityEvent{
				Kind:         activitymon.EventSpeech,
				SpeechSource: "microphone",
				SpeechText:   "voice during wechat video call",
				AppBundleID:  "com.tencent.xinWeChat",
				WindowTitle:  "视频通话",
			},
			want: ContentModeObserved,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EventContentMode(tt.evt, tt.inMeeting); got != tt.want {
				t.Errorf("EventContentMode = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestProfileGroup_DominantSelection(t *testing.T) {
	mkSession := func(meeting bool) ActivitySessionRow {
		return ActivitySessionRow{ID: "s", IsMeeting: meeting, AppNames: `["X"]`}
	}
	type ev = activitymon.ActivityEvent

	cases := []struct {
		name         string
		isMeeting    bool
		events       []ev
		wantDominant ContentMode
	}{
		{
			name:      "all_keyboard_is_performed",
			isMeeting: false,
			events: []ev{
				{Kind: activitymon.EventKeyboard, AppBundleID: "com.microsoft.VSCode"},
				{Kind: activitymon.EventKeyboard, AppBundleID: "com.microsoft.VSCode"},
				{Kind: activitymon.EventKeyboard, AppBundleID: "com.microsoft.VSCode"},
			},
			wantDominant: ContentModePerformed,
		},
		{
			name:      "mostly_youtube_speech_is_observed",
			isMeeting: false,
			events: []ev{
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "com.google.Chrome", WindowTitle: "X — YouTube"},
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "com.google.Chrome", WindowTitle: "X — YouTube"},
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "com.google.Chrome", WindowTitle: "X — YouTube"},
				{Kind: activitymon.EventClick, AppBundleID: "com.google.Chrome"},
			},
			wantDominant: ContentModeObserved,
		},
		{
			name:      "zoom_meeting_remote_audio_is_received",
			isMeeting: true,
			events: []ev{
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "us.zoom.xos"},
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "us.zoom.xos"},
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "us.zoom.xos"},
			},
			wantDominant: ContentModeReceived,
		},
		{
			name:         "empty_events_is_unknown",
			isMeeting:    false,
			events:       nil,
			wantDominant: ContentModeUnknown,
		},
		{
			name:      "tie_breaks_toward_observed_over_performed",
			isMeeting: false,
			events: []ev{
				{Kind: activitymon.EventKeyboard, AppBundleID: "com.microsoft.VSCode"},
				{Kind: activitymon.EventSpeech, SpeechSource: "system_audio", AppBundleID: "com.spotify.client"},
			},
			wantDominant: ContentModeObserved,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := sessionGroup{Sessions: []ActivitySessionRow{mkSession(tc.isMeeting)}}
			p := ProfileGroup(group, tc.events)
			if p.Dominant != tc.wantDominant {
				t.Errorf("Dominant = %s, want %s (profile=%+v)", p.Dominant, tc.wantDominant, p)
			}
		})
	}
}

func TestProfileGroup_MediaAppFlag(t *testing.T) {
	group := sessionGroup{Sessions: []ActivitySessionRow{{ID: "s"}}}
	events := []activitymon.ActivityEvent{
		{Kind: activitymon.EventClick, AppBundleID: "com.spotify.client", AppName: "Spotify"},
		{Kind: activitymon.EventClick, AppBundleID: "com.spotify.client", AppName: "Spotify"},
	}
	p := ProfileGroup(group, events)
	if !p.HasMediaPlaybackApp {
		t.Errorf("HasMediaPlaybackApp = false, want true (Spotify is in mediaPlaybackBundleIDs)")
	}
	if p.PrimaryAppName != "Spotify" {
		t.Errorf("PrimaryAppName = %q, want Spotify", p.PrimaryAppName)
	}
}

func TestValidateMemoryDimension(t *testing.T) {
	cases := []struct {
		name       string
		dominant   ContentMode
		dimension  string
		wantAction MemoryGateAction
	}{
		{"performed_user_insight_allowed", ContentModePerformed, "user_insight", MemoryGateAllow},
		{"observed_user_insight_coerced", ContentModeObserved, "user_insight", MemoryGateCoerceToHighlight},
		{"observed_deep_user_model_coerced", ContentModeObserved, "deep_user_model", MemoryGateCoerceToHighlight},
		{"observed_agentic_coerced", ContentModeObserved, "agentic_memory", MemoryGateCoerceToHighlight},
		{"observed_topic_highlight_allowed", ContentModeObserved, "topic_highlight", MemoryGateAllow},
		{"ambient_anything_skipped", ContentModeAmbient, "user_insight", MemoryGateSkip},
		{"ambient_topic_highlight_skipped", ContentModeAmbient, "topic_highlight", MemoryGateSkip},
		{"received_deep_user_model_skipped", ContentModeReceived, "deep_user_model", MemoryGateSkip},
		{"received_user_insight_coerced", ContentModeReceived, "user_insight", MemoryGateCoerceToHighlight},
		{"received_agentic_memory_coerced", ContentModeReceived, "agentic_memory", MemoryGateCoerceToHighlight},
		{"received_topic_highlight_allowed", ContentModeReceived, "topic_highlight", MemoryGateAllow},
		{"unknown_anything_allowed", ContentModeUnknown, "user_insight", MemoryGateAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := SessionContentProfile{Dominant: tc.dominant, Total: 1}
			got, _ := ValidateMemoryDimension(profile, tc.dimension)
			if got != tc.wantAction {
				t.Errorf("ValidateMemoryDimension dominant=%s dim=%q = %s, want %s",
					tc.dominant, tc.dimension, got, tc.wantAction)
			}
		})
	}
}

func TestValidateMemoryDimension_MediaAppOverridesPerformed(t *testing.T) {
	// Even when per-event Dominant comes back performed (the user
	// clicked play, scrolled the video), a session that touched a
	// known media-playback app must not produce stable personal-
	// trait memories.
	profile := SessionContentProfile{
		Dominant:            ContentModePerformed,
		HasMediaPlaybackApp: true,
		Total:               5,
	}
	for _, dim := range []string{"user_insight", "deep_user_model", "agentic_memory"} {
		got, reason := ValidateMemoryDimension(profile, dim)
		if got != MemoryGateCoerceToHighlight {
			t.Errorf("media app + performed + dim=%q = %s, want %s (reason=%q)",
				dim, got, MemoryGateCoerceToHighlight, reason)
		}
	}
	// topic_highlight unaffected even with media app present.
	if got, _ := ValidateMemoryDimension(profile, "topic_highlight"); got != MemoryGateAllow {
		t.Errorf("media app + topic_highlight = %s, want allow", got)
	}
}

func TestValidateMemoryDimension_NoMediaAppLeavesPerformedAlone(t *testing.T) {
	profile := SessionContentProfile{
		Dominant:            ContentModePerformed,
		HasMediaPlaybackApp: false,
		Total:               5,
	}
	if got, _ := ValidateMemoryDimension(profile, "user_insight"); got != MemoryGateAllow {
		t.Errorf("performed + no media + user_insight = %s, want allow", got)
	}
}

// ── ConnectorAttributionProfile + ValidateConnectorMemoryDimension ──

func TestParseConnectorAttributionProfile(t *testing.T) {
	in := `[SELF] yetone: hey
[OTHER] alice: hi back
[OTHER] bob: yo
[SELF] yetone: ok thanks
[OTHER] alice: anytime
[OTHER] bob: 👍
[OTHER] alice: cya
`
	p := ParseConnectorAttributionProfile(in)
	if p.SelfMsgCount != 2 {
		t.Errorf("SelfMsgCount = %d, want 2", p.SelfMsgCount)
	}
	if p.OtherMsgCount != 5 {
		t.Errorf("OtherMsgCount = %d, want 5", p.OtherMsgCount)
	}
	if !p.HeavilyOther() {
		t.Errorf("HeavilyOther should be true for self=2 other=5 (>2× self AND ≥4 total)")
	}
	if p.NoSelf() {
		t.Errorf("NoSelf should be false when there's at least one [SELF]")
	}
}

func TestParseConnectorAttributionProfile_NoSelf(t *testing.T) {
	in := `[OTHER] alice: hi
[OTHER] bob: yo
[OTHER] alice: cya
`
	p := ParseConnectorAttributionProfile(in)
	if p.SelfMsgCount != 0 || p.OtherMsgCount != 3 {
		t.Fatalf("counts = %+v", p)
	}
	if !p.NoSelf() {
		t.Errorf("NoSelf should be true when zero [SELF] lines and some [OTHER]")
	}
	// 3 OTHER messages with zero SELF: HeavilyOther's threshold is
	// now ≥3 other AND other > 2*self, so this qualifies. The gate
	// itself short-circuits on NoSelf first (more severe), but the
	// helper still reports the heavy-skew signal accurately.
	if !p.HeavilyOther() {
		t.Errorf("HeavilyOther should be true at threshold ≥3 other when self=0")
	}
}

func TestParseConnectorAttributionProfile_TwoOtherNotHeavy(t *testing.T) {
	in := `[OTHER] alice: hi
[OTHER] bob: yo
`
	p := ParseConnectorAttributionProfile(in)
	if p.HeavilyOther() {
		t.Errorf("HeavilyOther should be false below ≥3 floor; got other=%d", p.OtherMsgCount)
	}
}

func TestParseConnectorAttributionProfile_IgnoresUntaggedLines(t *testing.T) {
	in := `# header
[SELF] yetone: hi
random untagged stuff
[OTHER] alice: yo
`
	p := ParseConnectorAttributionProfile(in)
	if p.SelfMsgCount != 1 || p.OtherMsgCount != 1 {
		t.Errorf("counts = %+v, want self=1 other=1", p)
	}
}

func TestValidateConnectorMemoryDimension(t *testing.T) {
	cases := []struct {
		name        string
		self, other int
		dimension   string
		wantAction  MemoryGateAction
	}{
		{"no_self_user_insight_skipped", 0, 3, "user_insight", MemoryGateSkip},
		{"no_self_deep_skipped", 0, 5, "deep_user_model", MemoryGateSkip},
		{"no_self_agentic_skipped", 0, 5, "agentic_memory", MemoryGateSkip},
		{"no_self_topic_highlight_allowed", 0, 5, "topic_highlight", MemoryGateAllow},

		{"heavily_other_user_insight_coerced", 1, 5, "user_insight", MemoryGateCoerceToHighlight},
		{"heavily_other_deep_coerced", 2, 7, "deep_user_model", MemoryGateCoerceToHighlight},
		{"heavily_other_topic_highlight_allowed", 1, 8, "topic_highlight", MemoryGateAllow},

		// Below ≥4 threshold — even 0:3 isn't "heavy" but IS NoSelf
		// which catches it via the stricter rule.
		{"small_burst_balanced_allowed", 2, 1, "user_insight", MemoryGateAllow},
		{"small_burst_self_only_allowed", 3, 0, "user_insight", MemoryGateAllow},

		// Equal counts at ≥4 — not heavy.
		{"balanced_large_allowed", 4, 4, "user_insight", MemoryGateAllow},

		// 2x exact ratio is NOT heavy (must be strictly >).
		{"exactly_2x_other_allowed", 3, 6, "user_insight", MemoryGateAllow},
		{"just_over_2x_coerced", 3, 7, "user_insight", MemoryGateCoerceToHighlight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ConnectorAttributionProfile{SelfMsgCount: c.self, OtherMsgCount: c.other}
			got, reason := ValidateConnectorMemoryDimension(p, c.dimension)
			if got != c.wantAction {
				t.Errorf("ValidateConnectorMemoryDimension(self=%d other=%d dim=%q) = %s, want %s (reason=%q)",
					c.self, c.other, c.dimension, got, c.wantAction, reason)
			}
		})
	}
}

func TestPerspectiveInstruction_ObservedRefusesFirstPerson(t *testing.T) {
	profile := SessionContentProfile{
		Dominant:       ContentModeObserved,
		ObservedCount:  10,
		Total:          12,
		PrimaryAppName: "YouTube",
	}
	got := profile.PerspectiveInstruction("Alice")
	// Should NOT contain the standard first-person directive heading.
	if strings.Contains(got, "every memory point MUST be written in FIRST PERSON") {
		t.Errorf("observed profile should not emit first-person directive; got=\n%s", got)
	}
	// Should explicitly forbid user-attribution phrasing.
	for _, want := range []string{"CONSUMING", "DO NOT", "topic_highlight"} {
		if !strings.Contains(got, want) {
			t.Errorf("observed instruction missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestPerspectiveInstruction_PerformedUsesFirstPerson(t *testing.T) {
	profile := SessionContentProfile{
		Dominant:       ContentModePerformed,
		PerformedCount: 50,
		Total:          50,
	}
	got := profile.PerspectiveInstruction("Alice")
	if !strings.Contains(got, "FIRST PERSON") {
		t.Errorf("performed profile should emit first-person directive; got=\n%s", got)
	}
}

func TestProfileSummaryBlock_EmptyForZeroEvents(t *testing.T) {
	p := SessionContentProfile{}
	if got := p.ProfileSummaryBlock(); got != "" {
		t.Errorf("zero-event profile should produce empty block; got %q", got)
	}
}

func TestProfileSummaryBlock_IncludesCounts(t *testing.T) {
	p := SessionContentProfile{
		PerformedCount: 5,
		ObservedCount:  3,
		Total:          8,
		Dominant:       ContentModePerformed,
		PrimaryAppName: "VSCode",
	}
	got := p.ProfileSummaryBlock()
	for _, want := range []string{"performed=5", "observed=3", "total=8", "VSCode", "performed"} {
		if !strings.Contains(got, want) {
			t.Errorf("ProfileSummaryBlock missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// ── ChatSenderStats ──

func TestChatSenderStats_TotalAndNonSelf(t *testing.T) {
	s := ChatSenderStats{SelfMsgCount: 2, ContactMsgCount: 5, AnonymousMsgCount: 1, UnknownMsgCount: 3}
	if s.Total() != 11 {
		t.Errorf("Total = %d, want 11", s.Total())
	}
	if s.NonSelfTotal() != 9 {
		t.Errorf("NonSelfTotal = %d, want 9", s.NonSelfTotal())
	}
}

func TestChatSenderStats_HeavilyNonSelf(t *testing.T) {
	cases := []struct {
		name      string
		stats     ChatSenderStats
		wantHeavy bool
	}{
		{"all_self", ChatSenderStats{SelfMsgCount: 10}, false},
		{"all_contact_above_threshold", ChatSenderStats{ContactMsgCount: 5}, true},
		{"tied_with_self", ChatSenderStats{SelfMsgCount: 3, ContactMsgCount: 3}, false},
		{"non_self_just_above_2x", ChatSenderStats{SelfMsgCount: 2, ContactMsgCount: 5}, true},
		{"non_self_exactly_2x_does_not_qualify", ChatSenderStats{SelfMsgCount: 2, ContactMsgCount: 4}, false}, // strict >
		// 3-message floor (was 4). Three non-self messages with zero
		// self IS now considered heavy — strictly greater than 2*0 = 0.
		{"three_msg_burst_no_self_qualifies", ChatSenderStats{ContactMsgCount: 3}, true},
		{"two_msg_burst_below_floor", ChatSenderStats{ContactMsgCount: 2}, false},
		{"mixed_meets_threshold", ChatSenderStats{SelfMsgCount: 1, ContactMsgCount: 2, AnonymousMsgCount: 1, UnknownMsgCount: 1}, true},
		{"empty_session", ChatSenderStats{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.stats.ChatHeavilyNonSelf(); got != tc.wantHeavy {
				t.Errorf("ChatHeavilyNonSelf = %v, want %v (stats=%+v)", got, tc.wantHeavy, tc.stats)
			}
		})
	}
}

// ── PerspectiveInstruction chat addendum ──

func TestPerspectiveInstruction_AddsChatAddendumWhenHeavyNonSelf(t *testing.T) {
	p := SessionContentProfile{
		Dominant:       ContentModePerformed,
		PerformedCount: 10,
		Total:          10,
		ChatSenders:    ChatSenderStats{SelfMsgCount: 1, ContactMsgCount: 8},
	}
	got := p.PerspectiveInstruction("Yetone")
	for _, want := range []string{"CHAT SENDER NOTICE", "[YOU]", "[Contact:", "topic_highlight"} {
		if !strings.Contains(got, want) {
			t.Errorf("addendum missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestPerspectiveInstruction_NoChatAddendumWhenBalanced(t *testing.T) {
	p := SessionContentProfile{
		Dominant:       ContentModePerformed,
		PerformedCount: 10,
		Total:          10,
		ChatSenders:    ChatSenderStats{SelfMsgCount: 5, ContactMsgCount: 5}, // not heavily non-self
	}
	got := p.PerspectiveInstruction("Yetone")
	if strings.Contains(got, "CHAT SENDER NOTICE") {
		t.Errorf("balanced chat should not get addendum\n--- got ---\n%s", got)
	}
}

// ── ValidateMemoryDimension chat gate ──

func TestValidateMemoryDimension_ChatHeavyNonSelfCoerces(t *testing.T) {
	profile := SessionContentProfile{
		Dominant:    ContentModePerformed,
		ChatSenders: ChatSenderStats{SelfMsgCount: 1, ContactMsgCount: 8},
		Total:       1,
	}
	got, reason := ValidateMemoryDimension(profile, "user_insight")
	if got != MemoryGateCoerceToHighlight {
		t.Errorf("got %s, want coerce_to_topic_highlight; reason=%s", got, reason)
	}
	if !strings.Contains(reason, "chat content dominated by non-self") {
		t.Errorf("reason missing chat context: %s", reason)
	}
}

func TestValidateMemoryDimension_ChatHeavyAllowsTopicHighlight(t *testing.T) {
	profile := SessionContentProfile{
		Dominant:    ContentModePerformed,
		ChatSenders: ChatSenderStats{SelfMsgCount: 1, ContactMsgCount: 8},
		Total:       1,
	}
	got, _ := ValidateMemoryDimension(profile, "topic_highlight")
	if got != MemoryGateAllow {
		t.Errorf("topic_highlight should pass chat gate, got %s", got)
	}
}

func TestValidateMemoryDimension_BalancedChatDoesNotTriggerGate(t *testing.T) {
	profile := SessionContentProfile{
		Dominant:    ContentModePerformed,
		ChatSenders: ChatSenderStats{SelfMsgCount: 5, ContactMsgCount: 5}, // not heavy
		Total:       1,
	}
	got, _ := ValidateMemoryDimension(profile, "user_insight")
	if got != MemoryGateAllow {
		t.Errorf("balanced chat should not gate, got %s", got)
	}
}

func TestProfileSummaryBlock_IncludesChatStats(t *testing.T) {
	p := SessionContentProfile{
		PerformedCount: 5,
		Total:          5,
		Dominant:       ContentModePerformed,
		ChatSenders:    ChatSenderStats{SelfMsgCount: 2, ContactMsgCount: 6, AnonymousMsgCount: 1},
	}
	got := p.ProfileSummaryBlock()
	for _, want := range []string{"self=2", "contact=6", "anonymous=1", "WARNING"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestProfileSummaryBlock_ChatOnly(t *testing.T) {
	p := SessionContentProfile{
		ChatSenders: ChatSenderStats{SelfMsgCount: 1, ContactMsgCount: 1},
	}
	got := p.ProfileSummaryBlock()
	if !strings.Contains(got, "Chat messages by resolved sender") {
		t.Errorf("chat-only profile missing chat line\n--- got ---\n%s", got)
	}
}
