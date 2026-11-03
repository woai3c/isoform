package main

import (
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestScrubPrivateWindowEvent(t *testing.T) {
	key := activitymon.ActivityEvent{
		Kind: activitymon.EventKeyboard, AppBundleID: "com.google.Chrome", AppName: "Google Chrome",
		WindowTitle: "Bank - Google Chrome (Incognito)", KeyChars: []string{"p", "w"}, TypedText: "secret",
	}
	if _, keep := scrubPrivateWindowEvent(key, true); keep {
		t.Fatal("a keystroke in a private window must not be stored")
	}
	if got, keep := scrubPrivateWindowEvent(key, false); !keep || got.TypedText != "secret" {
		t.Fatal("outside a private window the event is unchanged")
	}

	click := activitymon.ActivityEvent{
		Timestamp: 42, Kind: activitymon.EventClick, AppBundleID: "com.google.Chrome", AppName: "Google Chrome",
		WindowTitle: "Bank - Google Chrome (Incognito)", ClickX: 10, ClickY: 20, ClickRole: "AXButton", ClickLabel: "Transfer $5,000",
	}
	got, keep := scrubPrivateWindowEvent(click, true)
	if !keep || got.WindowTitle != "" || got.ClickLabel != "" || got.AppName != "Google Chrome" || got.ClickRole != "AXButton" || got.Timestamp != 42 {
		t.Fatalf("private click kept %+v", got)
	}
	focus := activitymon.ActivityEvent{Kind: activitymon.EventFocus, WindowTitle: "Private Browsing", FocusRole: "AXTextField", FocusLabel: "Search or enter address"}
	if got, _ := scrubPrivateWindowEvent(focus, true); got.FocusLabel != "" || got.WindowTitle != "" {
		t.Fatalf("private focus kept %+v", got)
	}
	switched := activitymon.ActivityEvent{Kind: activitymon.EventAppSwitch, AppName: "Safari", WindowTitle: "Private Browsing - Health portal"}
	if got, keep := scrubPrivateWindowEvent(switched, true); !keep || got.WindowTitle != "" || got.AppName != "Safari" {
		t.Fatalf("private app switch kept %+v", got)
	}
}

func TestPrivateWindowEventLocked(t *testing.T) {
	am := &activityMonitorState{
		lastAppBundleID:         "com.google.Chrome",
		lastWindowTitle:         "Docs - Google Chrome (Incognito)",
		lastWindowTitleBundleID: "com.google.Chrome",
	}
	cases := []struct {
		name string
		evt  activitymon.ActivityEvent
		want bool
	}{
		{"own private title", activitymon.ActivityEvent{WindowTitle: "Mail - InPrivate"}, true},
		{"own normal title", activitymon.ActivityEvent{AppBundleID: "com.google.Chrome", WindowTitle: "Docs - Google Chrome"}, false},
		{"no title, same app as the private window", activitymon.ActivityEvent{AppBundleID: "com.google.Chrome"}, true},
		{"no title, another app", activitymon.ActivityEvent{AppBundleID: "com.apple.Notes"}, false},
		{"no title, a browser whose window is not known yet", activitymon.ActivityEvent{AppBundleID: "com.apple.Safari"}, true},
		{"chinese incognito", activitymon.ActivityEvent{WindowTitle: "新标签页 - 无痕模式"}, true},
	}
	for _, tc := range cases {
		if got := privateWindowEventLocked(am, tc.evt); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTypedTextIsNeverKeptForAPrivateWindow(t *testing.T) {
	a := &App{}
	am := &activityMonitorState{}
	am.typedSegmentOpen = true
	am.typedSegmentBaseline = "hello"
	am.typedSegmentValid = true
	am.typedSegmentBundleID = "com.google.Chrome"
	am.typedSegmentWindowTitle = "Bank - Google Chrome (Incognito)"
	// With no buffer, an attach attempt would panic; a private segment must
	// return before it.
	a.closeTypedSegmentLocked(am, "hello my password", true)
	if am.typedSegmentOpen {
		t.Fatal("the segment should be closed")
	}
}

func TestLastWindowTitleNeverCarriesToAnotherApp(t *testing.T) {
	am := &activityMonitorState{}
	trackWindowTitleLocked(am, activitymon.ActivityEvent{AppBundleID: "com.google.Chrome", WindowTitle: "Bank - Google Chrome (Incognito)"})
	if !privateFrontLocked(am) {
		t.Fatal("the private title was not tracked")
	}
	// An untitled event from Notes clears it, so Notes' keystrokes are not
	// dropped for Chrome's window.
	notes := activitymon.ActivityEvent{AppBundleID: "com.apple.Notes", Kind: activitymon.EventKeyboard}
	trackWindowTitleLocked(am, notes)
	if privateWindowEventLocked(am, notes) || privateFrontLocked(am) {
		t.Fatal("Chrome's private title carried over to Notes")
	}
	// Back to a normal titled window: not private.
	trackWindowTitleLocked(am, activitymon.ActivityEvent{AppBundleID: "com.google.Chrome", WindowTitle: "Docs - Google Chrome"})
	if privateFrontLocked(am) {
		t.Fatal("a normal window is not private")
	}
}

func TestPrivateMarkerPrefixesDiscardCapturedText(t *testing.T) {
	for _, title := range []string{"Private Browsing - Health portal", "Private Browsing — Health portal", "InPrivate - Health portal", "InPrivate — Health portal", "Page — 无痕浏览"} {
		t.Run(title, func(t *testing.T) {
			am := &activityMonitorState{}
			event := activitymon.ActivityEvent{Kind: activitymon.EventKeyboard, AppBundleID: "com.apple.Safari", WindowTitle: title, TypedText: "private text"}
			if _, keep := scrubPrivateWindowEvent(event, privateWindowEventLocked(am, event)); keep {
				t.Fatal("private keyboard event survived the capture gate")
			}
			am.typedSegmentOpen = true
			am.typedSegmentBaseline = "hello"
			am.typedSegmentValid = true
			am.typedSegmentBundleID = event.AppBundleID
			am.typedSegmentWindowTitle = title
			// A nil buffer catches any attempt to persist this private delta.
			(&App{}).closeTypedSegmentLocked(am, "hello private text", true)
			if am.typedSegmentOpen {
				t.Fatal("private typed segment stayed open")
			}
		})
	}
}
