package main

import (
	"strings"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

// Typed-text segments recover the text a user actually committed during a
// keyboard burst — including IME-composed CJK that the CGEventTap can never
// see — by diffing focused-field AX snapshots taken at the segment's edges.
//
// Every keydown carries a focused-field snapshot captured on the event-tap
// thread (ActivityEvent.FieldText). A segment opens at a burst-start keydown
// (its snapshot is the baseline), tracks the latest snapshot as the user
// types, and closes at the first of:
//   - a commit keydown (Return / keypad Enter) — its pre-delivery snapshot
//     still holds text the app is about to clear on send
//   - a click — its pre-click snapshot (send buttons fire the tap before
//     delivery, so the message text is still present)
//   - a typing pause (the snapshot trigger timer) — uses the latest stored
//     keydown snapshot
//   - an app switch / focus change — closed without attaching (the field
//     that held the segment's baseline is gone)
//
// Closing diffs baseline → final and appends the inserted text to the burst's
// merged keyboard event in the ring buffer as TypedText. Crucially, all field
// reads happen on the event-tap thread at capture time; closing is pure Go
// over already-captured snapshots and never calls AppKit/AX/TIS off-thread.
//
// Known limits, accepted for v1: a candidate-window mouse click commits the
// composition only after delivery, so its pre-click snapshot may still show
// marked pinyin (keyboard candidate selection via Space/Enter is unaffected),
// and tail-capped snapshots of very long fields can misalign the diff
// (bounded by typedTextMaxRunes).

const (
	kcReturn      = 36 // kVK_Return
	kcKeypadEnter = 76 // kVK_ANSI_KeypadEnter

	// typedTextMaxRunes caps the TypedText attached per segment close.
	typedTextMaxRunes = 2000
)

// insertedTextDelta returns the text inserted between two snapshots of the
// same text field: trim the rune-wise common prefix and suffix, return what
// remains of after. Deletions and pure cursor movement yield "".
func insertedTextDelta(before, after string) string {
	if after == "" || before == after {
		return ""
	}
	b := []rune(before)
	a := []rune(after)
	prefix := 0
	for prefix < len(b) && prefix < len(a) && b[prefix] == a[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(b)-prefix && suffix < len(a)-prefix &&
		b[len(b)-1-suffix] == a[len(a)-1-suffix] {
		suffix++
	}
	return strings.TrimSpace(string(a[prefix : len(a)-suffix]))
}

// trackTypedSegmentKeyLocked advances the typed-text segment for one keydown.
// Caller holds am.mu.
func (a *App) trackTypedSegmentKeyLocked(am *activityMonitorState, evt activitymon.ActivityEvent) {
	isCommit := evt.KeyCode == kcReturn || evt.KeyCode == kcKeypadEnter
	if !am.typedSegmentOpen {
		if isCommit {
			return
		}
		am.typedSegmentOpen = true
		am.typedSegmentBaseline = evt.FieldText
		am.typedSegmentValid = evt.FieldTextValid
		am.typedSegmentLatest = evt.FieldText
		am.typedSegmentLatestValid = evt.FieldTextValid
		am.typedSegmentBundleID = strings.TrimSpace(evt.AppBundleID)
		am.typedSegmentAppName = strings.TrimSpace(evt.AppName)
		am.typedSegmentWindowTitle = strings.TrimSpace(evt.WindowTitle)
		return
	}
	if typedTextDestinationChanged(am, evt) {
		// The destination changed under the burst without a close trigger
		// landing first. The stale baseline belongs to another surface, so it
		// cannot be attached to the new destination.
		am.typedSegmentOpen = false
		a.trackTypedSegmentKeyLocked(am, evt)
		return
	}
	// Track the most recent valid snapshot so a typing-pause close has the
	// current field contents without an off-thread read.
	if evt.FieldTextValid {
		am.typedSegmentLatest = evt.FieldText
		am.typedSegmentLatestValid = true
	}
	if isCommit {
		a.closeTypedSegmentLocked(am, evt.FieldText, evt.FieldTextValid)
	}
}

// closeTypedSegmentLocked resolves the open segment against the final field
// snapshot and attaches the inserted text to the burst's merged keyboard
// event. Passing finalValid=false drops the segment without attaching.
// Caller holds am.mu.
func (a *App) closeTypedSegmentLocked(am *activityMonitorState, finalText string, finalValid bool) {
	open := am.typedSegmentOpen
	baseline := am.typedSegmentBaseline
	baseValid := am.typedSegmentValid
	bundleID := am.typedSegmentBundleID
	appName := am.typedSegmentAppName
	windowTitle := am.typedSegmentWindowTitle
	am.typedSegmentOpen = false
	am.typedSegmentBaseline = ""
	am.typedSegmentValid = false
	am.typedSegmentLatest = ""
	am.typedSegmentLatestValid = false
	am.typedSegmentBundleID = ""
	am.typedSegmentAppName = ""
	am.typedSegmentWindowTitle = ""

	if !open || !baseValid || !finalValid {
		return
	}
	if _, sensitive := pii.IsSensitiveApp(bundleID); sensitive {
		return
	}
	// Nothing typed in a private or incognito window is kept.
	if activitymon.IsPrivateBrowsing(windowTitle) {
		return
	}
	delta := insertedTextDelta(baseline, finalText)
	if delta == "" {
		return
	}
	delta, _ = pii.RedactText(delta)
	delta = truncateRunes(strings.TrimSpace(delta), typedTextMaxRunes)
	if delta == "" {
		return
	}
	am.buf.AmendLastKeyboard(bundleID, func(evt *activitymon.ActivityEvent) {
		if evt.TypedText != "" {
			evt.TypedText += "\n"
		}
		evt.TypedText += delta
		evt.TypedTextCaptureMethod = activitymon.TypedTextCaptureMethodAXFieldDiff
		if appName != "" {
			evt.AppName = appName
		}
		if windowTitle != "" {
			evt.WindowTitle = windowTitle
		}
	})
}

func typedTextDestinationChanged(
	am *activityMonitorState,
	evt activitymon.ActivityEvent,
) bool {
	if strings.TrimSpace(evt.AppBundleID) != am.typedSegmentBundleID {
		return true
	}
	appName := strings.TrimSpace(evt.AppName)
	if appName != "" && am.typedSegmentAppName != "" &&
		appName != am.typedSegmentAppName {
		return true
	}
	windowTitle := strings.TrimSpace(evt.WindowTitle)
	return windowTitle != am.typedSegmentWindowTitle
}

// flushTypedSegmentLocked closes the open typed segment using the latest
// keydown snapshot captured on the event-tap thread (typing-pause path). It is
// pure Go over already-captured data — no AppKit/AX/TIS call — so it is safe
// to run under am.mu from any goroutine. Caller holds am.mu.
func (a *App) flushTypedSegmentLocked(am *activityMonitorState) {
	if !am.typedSegmentOpen {
		return
	}
	a.closeTypedSegmentLocked(am, am.typedSegmentLatest, am.typedSegmentLatestValid)
}
