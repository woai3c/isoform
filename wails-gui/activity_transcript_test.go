package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func TestPersistTranscriptFirstMessage_SkipsMissingChatSession(t *testing.T) {
	app := newBindingsTestApp(t)

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	app.persistTranscriptFirstMessage(
		context.Background(),
		"missing-chat-session",
		"Zoom",
		"## Transcript\n\n- Hello",
		1,
		time.Now().Add(-time.Minute),
		"1m 0s",
	)

	if logs.Len() != 0 {
		t.Fatalf("missing chat session should be skipped quietly, got logs: %s", logs.String())
	}
}

func speech(tsMs int64, text string) activitymon.ActivityEvent {
	return activitymon.ActivityEvent{
		Kind:       activitymon.EventSpeech,
		Timestamp:  tsMs,
		SpeechText: text,
	}
}

func TestDedupSpeechEvents_CollapsesPairsKeepingBetter(t *testing.T) {
	// Based on the real-world screenshot: mic + system-audio produce
	// two near-duplicates one second apart, the second being cleaner.
	events := []activitymon.ActivityEvent{
		speech(12_54_09_000, "the first one a more of a code modernization project was"),
		speech(12_54_10_000, "The first one, more of a code modernization project with FM"),
		speech(12_54_19_000, "consuming the engineering team's bandwidth."),
		speech(12_54_20_000, "It's consuming the engineering team's bandwidth."),
		speech(12_54_24_000, "That's a public safety software company. They develop a platform that"),
		speech(12_54_25_000, "that's a public safety software company. They develop a platform that police chiefs"),
	}

	got := dedupSpeechEvents(events)
	if len(got) != 3 {
		t.Fatalf("dedup should collapse 6 events to 3, got %d: %+v", len(got), got)
	}

	// Each kept event should be the longer/cleaner of its pair.
	if !strings.Contains(got[0].SpeechText, "with FM") {
		t.Errorf("first kept: want cleaner 'with FM' version, got %q", got[0].SpeechText)
	}
	if !strings.HasPrefix(got[1].SpeechText, "It's consuming") {
		t.Errorf("second kept: want 'It's consuming' version, got %q", got[1].SpeechText)
	}
	if !strings.Contains(got[2].SpeechText, "police chiefs") {
		t.Errorf("third kept: want longer 'police chiefs' version, got %q", got[2].SpeechText)
	}
}

func TestDedupSpeechEvents_KeepsUnrelatedUtterances(t *testing.T) {
	// Three utterances in a row, none similar — no collapse.
	events := []activitymon.ActivityEvent{
		speech(1000, "Hey everyone, thanks for joining."),
		speech(3000, "Let's start with the standup."),
		speech(6000, "I finished the auth refactor yesterday."),
	}
	got := dedupSpeechEvents(events)
	if len(got) != 3 {
		t.Fatalf("unrelated utterances should not dedup, got %d", len(got))
	}
}

func TestDedupSpeechEvents_WindowBoundary(t *testing.T) {
	// Similar text, but >6 seconds apart → treated as genuine repetition,
	// not a dedup candidate.
	events := []activitymon.ActivityEvent{
		speech(0, "I said we should ship it today."),
		speech(10_000, "I said we should ship it today."),
	}
	got := dedupSpeechEvents(events)
	if len(got) != 2 {
		t.Errorf("repetition outside window should be preserved, got %d", len(got))
	}
}

func TestDedupSpeechEvents_FiltersNonSpeechAndBlank(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{Kind: activitymon.EventAppSwitch, Timestamp: 1000},
		speech(2000, "   "),
		speech(3000, "real content here"),
		{Kind: activitymon.EventAppSwitch, Timestamp: 3500},
		speech(4000, "more real content"),
	}
	got := dedupSpeechEvents(events)
	if len(got) != 2 {
		t.Fatalf("non-speech and blank events should be filtered, got %d: %+v", len(got), got)
	}
	if got[0].SpeechText != "real content here" || got[1].SpeechText != "more real content" {
		t.Errorf("unexpected events kept: %+v", got)
	}
}

func TestDedupSpeechEvents_PreferPunctuatedWhenSameLength(t *testing.T) {
	// Same length (within 4 chars), one has more punctuation → keep it.
	events := []activitymon.ActivityEvent{
		speech(1000, "got it i ll make sure my pr is merged before then"),
		speech(2000, "Got it. I'll make sure my PR is merged before then."),
	}
	got := dedupSpeechEvents(events)
	if len(got) != 1 {
		t.Fatalf("pair should collapse to 1, got %d", len(got))
	}
	if !strings.Contains(got[0].SpeechText, "I'll") {
		t.Errorf("should keep punctuated version, got %q", got[0].SpeechText)
	}
}

func TestStripTranscriptHeader(t *testing.T) {
	// The frontend overview card renders the metadata on its own, so
	// the body returned by GetTranscriptView must have the file's YAML
	// header + "## Transcript" heading stripped — leaving just the
	// timestamped lines.
	raw := "# Meeting Transcript: Zoom\n\n" +
		"- **Date**: 2026-04-17 20:13\n" +
		"- **Duration**: 15m 10s\n" +
		"- **App**: Zoom\n" +
		"- **Session**: abc123\n\n" +
		"## Transcript\n\n" +
		"[20:13:56] 我们在lowcode的功能实在太多了\n\n" +
		"[20:14:06] 复local的原因。\n"

	body := stripTranscriptHeader(raw)

	if strings.Contains(body, "# Meeting Transcript") {
		t.Error("metadata header should be stripped")
	}
	if strings.Contains(body, "## Transcript") {
		t.Error("transcript heading should be stripped")
	}
	if !strings.Contains(body, "[20:13:56]") || !strings.Contains(body, "复local") {
		t.Errorf("timestamped lines must survive, got: %q", body)
	}
}

func TestSpeakerLabel(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"microphone", "Me"},
		{"system_audio", "Other"},
		{"", ""},
		{"both", ""},    // config value, never appears on a chunk
		{"garbage", ""}, // defensive: unknown source -> no prefix
	}
	for _, c := range cases {
		if got := speakerLabel(c.source); got != c.want {
			t.Errorf("speakerLabel(%q) = %q, want %q", c.source, got, c.want)
		}
	}
}

func TestDetectMeetingInEvents_OnlyTranscriptSupportedPlatforms(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{
			Kind:         activitymon.EventSpeech,
			AppBundleID:  "com.hnc.Discord",
			AppName:      "Discord",
			WindowTitle:  "Discord - Voice Channel",
			SpeechText:   "This should not create a transcript.",
			SpeechInCall: true,
		},
	}

	if gotName, gotMeeting := detectMeetingInEvents(events); gotMeeting || gotName != "" {
		t.Fatalf("detectMeetingInEvents() = (%q, %v), want unsupported meeting to be ignored for transcripts", gotName, gotMeeting)
	}

	events = append(events, activitymon.ActivityEvent{
		Kind:         activitymon.EventSpeech,
		AppBundleID:  "com.microsoft.teams2",
		AppName:      "Microsoft Teams",
		WindowTitle:  "Meeting in Progress",
		SpeechText:   "This should create a transcript.",
		SpeechInCall: true,
	})

	gotName, gotMeeting := detectMeetingInEvents(events)
	if !gotMeeting || gotName != "Microsoft Teams" {
		t.Fatalf("detectMeetingInEvents() = (%q, %v), want Microsoft Teams transcript target", gotName, gotMeeting)
	}
}

func TestDetectMeetingInEvents_UsesCapturedMeetingAppName(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{
			Kind:         activitymon.EventSpeech,
			AppBundleID:  "com.google.Chrome",
			AppName:      "Google Meet",
			WindowTitle:  "repo notes",
			SpeechText:   "Delayed ASR returned after the user switched windows.",
			SpeechInCall: true,
		},
	}

	gotName, gotMeeting := detectMeetingInEvents(events)
	if !gotMeeting || gotName != "Google Meet" {
		t.Fatalf("detectMeetingInEvents() = (%q, %v), want captured Google Meet context", gotName, gotMeeting)
	}
}

func TestDetectMeetingInEventsIgnoresSpeechOutsideCalls(t *testing.T) {
	// Dictating into Claude with Zoom in front: Zoom is a meeting app, but
	// the speech was not captured in a call.
	events := []activitymon.ActivityEvent{{
		Kind:               activitymon.EventSpeech,
		AppBundleID:        "us.zoom.xos",
		AppName:            "zoom.us",
		WindowTitle:        "Zoom Workplace",
		SpeechText:         "Write the backlog entry for text messages and telemetry.",
		SpeechDictationApp: "Claude",
	}}
	if name, meeting := detectMeetingInEvents(events); meeting {
		t.Fatalf("speech outside a call made a meeting: %q", name)
	}
}

func TestDetectMeetingCandidateInEvents_AcceptsHolisticSupportedCandidate(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{
			Kind:                   activitymon.EventSpeech,
			Timestamp:              10_000,
			AppBundleID:            "com.googlecode.iterm2",
			AppName:                "iTerm2",
			WindowTitle:            "notes",
			SpeechText:             "We should review the implementation plan",
			SpeechSource:           "system_audio",
			MeetingCandidate:       true,
			MeetingCandidateApp:    "Microsoft Teams",
			MeetingCandidateReason: "candidate_continuation_audio",
		},
		{
			Kind:                   activitymon.EventSpeech,
			Timestamp:              20_000,
			AppBundleID:            "com.googlecode.iterm2",
			AppName:                "iTerm2",
			WindowTitle:            "notes",
			SpeechText:             "and confirm the next action before shipping",
			SpeechSource:           "microphone",
			MeetingCandidate:       true,
			MeetingCandidateApp:    "Microsoft Teams",
			MeetingCandidateReason: "native_supported_app_with_external_mic",
		},
	}

	gotName, gotMeeting := detectMeetingCandidateInEvents(events)
	if !gotMeeting || gotName != "Microsoft Teams" {
		t.Fatalf("detectMeetingCandidateInEvents() = (%q, %v), want Microsoft Teams candidate", gotName, gotMeeting)
	}
}

func TestDetectMeetingCandidateInEvents_RejectsWeakOrUnknownCandidates(t *testing.T) {
	tests := []struct {
		name   string
		events []activitymon.ActivityEvent
	}{
		{
			name: "unknown platform",
			events: []activitymon.ActivityEvent{{
				Kind:                activitymon.EventSpeech,
				SpeechText:          "This has enough words to look conversational but should not pass.",
				MeetingCandidate:    true,
				MeetingCandidateApp: "Discord",
			}},
		},
		{
			name: "too little speech",
			events: []activitymon.ActivityEvent{{
				Kind:                activitymon.EventSpeech,
				SpeechText:          "Yep",
				MeetingCandidate:    true,
				MeetingCandidateApp: "Zoom",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotName, gotMeeting := detectMeetingCandidateInEvents(tt.events); gotMeeting || gotName != "" {
				t.Fatalf("detectMeetingCandidateInEvents() = (%q, %v), want rejected", gotName, gotMeeting)
			}
		})
	}
}

func TestMeetingSpeechEventsForRecordingEpochRejectsStaleMeetingEvidence(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{
			Timestamp:             1,
			Kind:                  activitymon.EventSpeech,
			AppBundleID:           "com.microsoft.teams2",
			AppName:               "Microsoft Teams",
			SpeechText:            "stale meeting speech",
			SpeechInCall:          true,
			MeetingRecordingEpoch: 4,
		},
		{
			Timestamp:             2,
			Kind:                  activitymon.EventSpeech,
			AppBundleID:           "com.googlecode.iterm2",
			AppName:               "iTerm2",
			SpeechText:            "fresh speech in an unsupported call",
			SpeechInCall:          true,
			MeetingRecordingEpoch: 6,
		},
		{
			Timestamp:             3,
			Kind:                  activitymon.EventSpeech,
			AppBundleID:           "com.anthropic.claudefordesktop",
			AppName:               "Claude",
			SpeechText:            "dictation outside the call",
			SpeechDictationApp:    "Claude",
			MeetingRecordingEpoch: 6,
		},
	}

	// Only the current generation's call speech: dictation in the same
	// session is never part of the call.
	fresh := meetingSpeechEventsForRecordingEpoch(events, 6)
	if len(fresh) != 1 || fresh[0].MeetingRecordingEpoch != 6 || !fresh[0].SpeechInCall {
		t.Fatalf("filtered events = %#v, want only epoch 6 call speech", fresh)
	}
	if name, meeting := detectMeetingInEvents(fresh); meeting || name != "" {
		t.Fatalf("stale evidence classified current epoch as meeting: (%q, %v)", name, meeting)
	}
}

func TestStripTranscriptLinePrefix(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"timestamp only", "[10:14:33] hello world", "hello world"},
		{"timestamp + me", "[10:14:33] Me: hello world", "hello world"},
		{"timestamp + other", "[10:14:33] Other: yes indeed", "yes indeed"},
		{"me only, no timestamp", "Me: bare line", "bare line"},
		{"no prefix at all", "plain text", "plain text"},
		{"empty", "", ""},
		{"just whitespace", "   ", ""},
		{"body starts with Me-like word", "[10:14:33] Medium rare", "Medium rare"}, // "Medium" not stripped
	}
	for _, c := range cases {
		if got := stripTranscriptLinePrefix(c.in); got != c.want {
			t.Errorf("%s: stripTranscriptLinePrefix(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestBuildTranscriptMarkdown_AddsSpeakerLabels(t *testing.T) {
	session := ActivitySessionRow{
		ID:        "s1",
		StartTime: 1_000,
		EndTime:   61_000,
	}
	events := []activitymon.ActivityEvent{
		{Kind: activitymon.EventSpeech, Timestamp: 10_000, SpeechText: "My question was", SpeechSource: "microphone"},
		{Kind: activitymon.EventSpeech, Timestamp: 20_000, SpeechText: "Here's the answer", SpeechSource: "system_audio"},
		{Kind: activitymon.EventSpeech, Timestamp: 30_000, SpeechText: "Legacy line" /* no source */},
	}
	body, wordCount := buildTranscriptMarkdown("Zoom", session, events)
	if !strings.Contains(body, "Me: My question was") {
		t.Errorf("microphone line missing Me: prefix, body=%q", body)
	}
	if !strings.Contains(body, "Other: Here's the answer") {
		t.Errorf("system_audio line missing Other: prefix, body=%q", body)
	}
	// Legacy event with no source should render unprefixed.
	if !strings.Contains(body, "] Legacy line\n") {
		t.Errorf("legacy line should render without speaker prefix, body=%q", body)
	}
	// Word count should not count the Me:/Other: prefixes.
	// "My question was" (3) + "Here's the answer" (3) + "Legacy line" (2) = 8
	if wordCount != 8 {
		t.Errorf("wordCount = %d, want 8 (speaker prefixes must not count)", wordCount)
	}
}

func TestStripTranscriptHeader_NoHeaderPassesThrough(t *testing.T) {
	// Defensive: if the file doesn't match the expected shape (e.g.
	// user-edited or AI-cleaned to remove the header), fall through
	// and render what's there rather than lose all content.
	raw := "[20:13:56] hello\n[20:14:00] world\n"
	if got := stripTranscriptHeader(raw); got != raw {
		t.Errorf("no-marker input should pass through unchanged, got %q", got)
	}
}

func TestWordOverlapRatio(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"hello world", "hello world", 1.0},
		{"hello world", "world hello", 1.0},
		{"hello world", "goodbye world", 0.5},
		{"a b c d", "a b c d e f g h", 0.5},
		{"", "anything", 0.0},
		{"shared.", "shared!", 1.0}, // punctuation stripped
	}
	for _, c := range cases {
		got := wordOverlapRatio(c.a, c.b)
		if got != c.want {
			t.Errorf("wordOverlapRatio(%q, %q) = %f, want %f", c.a, c.b, got, c.want)
		}
	}
}

func TestWordOverlapRatio_NonAsciiLetters(t *testing.T) {
	// Regression for ASCII-only normalizer that collapsed non-English
	// alphabetic words to the empty string. With unicode.IsLetter the
	// words must survive normalization and still overlap.
	//
	// Note: CJK scripts have no spaces between words, so Fields-based
	// splitting can't meaningfully compare them at the word level —
	// that's a separate feature. These cases only cover scripts where
	// whitespace is the natural word boundary.
	cases := []struct {
		name    string
		a, b    string
		wantMin float64
	}{
		{"accented latin", "Über alles café", "über alles café", 1.0},
		{"spanish inverted", "¿Cómo estás?", "cómo estás", 1.0},
		{"cyrillic", "Привет мир", "привет мир", 1.0},
		{"greek", "Καλημέρα κόσμε", "καλημέρα κόσμε", 1.0},
	}
	for _, c := range cases {
		got := wordOverlapRatio(c.a, c.b)
		if got < c.wantMin {
			t.Errorf("%s: wordOverlapRatio(%q, %q) = %f, want >= %f",
				c.name, c.a, c.b, got, c.wantMin)
		}
	}
}
