package main

import (
	"strings"
	"testing"
	"time"

	"wails-gui/internal/activitybuf"
	"wails-gui/internal/platform/activitymon"
)

func TestInsertedTextDelta(t *testing.T) {
	cases := []struct {
		name   string
		before string
		after  string
		want   string
	}{
		{"append to empty field", "", "也不输上去了", "也不输上去了"},
		{"append to existing text", "你好", "你好，也不输上去了", "，也不输上去了"},
		{"ime marked text resolved", "ye bushu", "也不输", "也不输"},
		{"deletion only", "hello world", "hello", ""},
		{"identical", "same", "same", ""},
		{"cleared field (send)", "message text", "", ""},
		{"middle insert", "step3.7 up", "step3.7 not up", "not"},
		{"ascii append", "step3", "step3.7", ".7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := insertedTextDelta(tc.before, tc.after); got != tc.want {
				t.Errorf("insertedTextDelta(%q, %q) = %q, want %q", tc.before, tc.after, got, tc.want)
			}
		})
	}
}

func TestInsertedTextDeltaRuneSafety(t *testing.T) {
	before := strings.Repeat("中", 10)
	after := before + "新增的字"
	if got := insertedTextDelta(before, after); got != "新增的字" {
		t.Errorf("CJK append delta = %q", got)
	}
}

func TestCloseTypedSegmentMarksAXFieldDiffCapture(t *testing.T) {
	buffer := activitybuf.NewRingBuffer(8, time.Minute)
	am := &activityMonitorState{buf: buffer}
	app := &App{}
	now := time.Now().UnixMilli()
	start := activitymon.ActivityEvent{
		Timestamp:      now,
		Kind:           activitymon.EventKeyboard,
		AppBundleID:    "com.apple.mail",
		AppName:        "Mail",
		WindowTitle:    "Customer update",
		KeyChars:       []string{"H"},
		FieldText:      "",
		FieldTextValid: true,
	}
	buffer.Push(start)
	app.trackTypedSegmentKeyLocked(am, start)
	commit := start
	commit.Timestamp = now + 100
	commit.KeyCode = kcReturn
	commit.KeyChars = []string{"Return"}
	commit.FieldText = "Hello customer"
	buffer.Push(commit)
	app.trackTypedSegmentKeyLocked(am, commit)

	events := buffer.Snapshot()
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	if events[0].TypedText != "Hello customer" ||
		events[0].TypedTextCaptureMethod !=
			activitymon.TypedTextCaptureMethodAXFieldDiff {
		t.Fatalf("captured event=%+v", events[0])
	}
}

func TestTypedSegmentDropsCrossWindowBaseline(t *testing.T) {
	buffer := activitybuf.NewRingBuffer(8, time.Minute)
	am := &activityMonitorState{buf: buffer}
	app := &App{}
	now := time.Now().UnixMilli()
	first := activitymon.ActivityEvent{
		Timestamp:      now,
		Kind:           activitymon.EventKeyboard,
		AppBundleID:    "com.apple.mail",
		AppName:        "Mail",
		WindowTitle:    "First draft",
		KeyChars:       []string{"a"},
		FieldText:      "first",
		FieldTextValid: true,
	}
	buffer.Push(first)
	app.trackTypedSegmentKeyLocked(am, first)
	second := first
	second.Timestamp = now + 2_100
	second.WindowTitle = "Second draft"
	second.KeyChars = []string{"b"}
	second.FieldText = "second"
	buffer.Push(second)
	app.trackTypedSegmentKeyLocked(am, second)
	commit := second
	commit.Timestamp = now + 2_200
	commit.KeyCode = kcReturn
	commit.KeyChars = []string{"Return"}
	commit.FieldText = "second result"
	buffer.Push(commit)
	app.trackTypedSegmentKeyLocked(am, commit)

	events := buffer.Snapshot()
	if len(events) != 2 {
		t.Fatalf("events=%+v", events)
	}
	if events[0].TypedText != "" || events[0].TypedTextCaptureMethod != "" {
		t.Fatalf("first destination received text: %+v", events[0])
	}
	if events[1].TypedText != "result" ||
		events[1].WindowTitle != "Second draft" ||
		events[1].TypedTextCaptureMethod !=
			activitymon.TypedTextCaptureMethodAXFieldDiff {
		t.Fatalf("second destination=%+v", events[1])
	}
}

func TestProtectedActivityScrubClearsTextCaptureProvenance(t *testing.T) {
	event := activitymon.ActivityEvent{
		AppBundleID:            "com.1password.desktop",
		WindowTitle:            "Vault",
		ClickLabel:             "Copy password",
		FocusLabel:             "Password",
		KeyChar:                "x",
		KeyChars:               []string{"x"},
		TypedText:              "secret",
		TypedTextCaptureMethod: activitymon.TypedTextCaptureMethodAXFieldDiff,
		SpeechText:             "secret",
	}
	if !scrubProtectedActivityEvent(&event) {
		t.Fatal("protected event was not recognized")
	}
	if event.WindowTitle != "" || event.ClickLabel != "" ||
		event.FocusLabel != "" || event.KeyChar != "" ||
		len(event.KeyChars) != 0 || event.TypedText != "" ||
		event.TypedTextCaptureMethod != "" || event.SpeechText != "" {
		t.Fatalf("protected event retained content: %+v", event)
	}
}
