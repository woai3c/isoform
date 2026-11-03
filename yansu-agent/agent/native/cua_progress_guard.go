package native

import (
	"encoding/json"
	"regexp"
	"strings"
)

// cua_progress_guard.go detects "no-progress" loops in Computer Use
// (cua_driver) sessions: the agent fires action after action — clicking a
// nav button, retyping a URL — but the target window's title and URL never
// change, i.e. the navigation is silently not committing. This is the
// failure mode behind slow, screenshot-piling CUA runs that the existing
// input-hash loop guard (CheckForLoop) cannot catch: each retry uses a
// *different* ref/coord (refs are regenerated on every snapshot), so the
// inputs differ even though the resulting UI state is identical.
//
// Unlike CheckForLoop (which runs BEFORE execution on tu.Input), this guard
// runs AFTER execution on the tool RESULT — it needs the window/URL the
// snapshot reported. It mirrors RecordSearchNarrowingResult's
// "update cross-turn state from the result block" pattern.

// CuaNoProgressThreshold is how many consecutive action→snapshot rounds may
// leave the window title + URL unchanged before we nudge. 2 means: the agent
// took an action and the next snapshot was identical, then took another
// action and the snapshot was still identical — enough signal that the path
// is not committing.
const CuaNoProgressThreshold = 2

var (
	cuaWindowTitleRe = regexp.MustCompile(`(?i)window:\s*"([^"]*)"`)
	cuaURLRe         = regexp.MustCompile(`(?i)\burl:\s*(\S+)`)
)

// CuaProgressState tracks, across turns, whether successive cua_driver
// snapshots are advancing. It is a single struct (not keyed) because a CUA
// task is one focused background-driving session; switching to a genuinely
// different window changes the fingerprint and resets the stuck counter,
// which is the correct "that was progress" signal.
type CuaProgressState struct {
	LastSnapshotFingerprint string
	ActionSinceSnapshot     bool // an action sub-tool ran since the last snapshot
	StuckRounds             int  // consecutive action→snapshot rounds with no change
	Nudged                  bool // a no-progress nudge was already injected for the current stuck streak
}

// cuaActionSubTools are the cua_driver sub-tools EXPECTED to change the
// window/URL when they work (navigation / interaction). scroll is
// deliberately excluded: scrolling a list legitimately leaves title+URL
// unchanged, so counting it would false-positive on long-list reads.
var cuaActionSubTools = map[string]bool{
	"click":                    true,
	"double_click":             true,
	"right_click":              true,
	"type_text":                true,
	"press_key":                true,
	"hotkey":                   true,
	"set_value":                true,
	"drag":                     true,
	"launch_app":               true,
	"perform_secondary_action": true,
}

// RecordCuaProgress updates the no-progress state from one cua_driver call's
// result and returns the current stuck-round count (0 when not applicable or
// when progress was observed). Only cua_driver calls participate; everything
// else is a no-op that leaves the state untouched.
func RecordCuaProgress(state *CuaProgressState, toolName, rawInput string, block ContentBlock) int {
	if state == nil || !strings.EqualFold(toolName, "cua_driver") {
		return 0
	}
	sub := cuaSubToolName(rawInput)
	if cuaActionSubTools[sub] {
		// An action ran. Remember it so the next snapshot can tell whether
		// the action moved anything. The action's own result rarely carries
		// a window/URL, so we don't fingerprint it here.
		state.ActionSinceSnapshot = true
		return 0
	}

	content, _ := block.Content.(string)
	fp := cuaStateFingerprint(content)
	if fp == "" {
		// Not a state snapshot (list_apps, list_windows, screenshot, a short
		// ack). Nothing to compare; leave counters as-is.
		return 0
	}

	// This is a snapshot. Treat "unchanged" as no-progress only if an action
	// actually happened since the last snapshot — two reads of a static
	// window are not a stuck loop.
	if fp == state.LastSnapshotFingerprint && state.ActionSinceSnapshot {
		state.StuckRounds++
	} else if fp != state.LastSnapshotFingerprint {
		// Real progress: reset the stuck counter and re-arm the nudge so a
		// later, genuinely new stall can be flagged again.
		state.StuckRounds = 0
		state.LastSnapshotFingerprint = fp
		state.Nudged = false
	}
	state.ActionSinceSnapshot = false
	return state.StuckRounds
}

func CuaNoProgressNudgeText(count int) string {
	return "Notice: your last " + itoa(count) + " Computer Use action→snapshot rounds left the " +
		"window title and URL unchanged — the click/navigation is not committing. STOP repeating " +
		"that path. Switch approach: drive the app's own search box or address-bar query syntax to " +
		"jump straight to the target view (e.g. Gmail \"after:2026/6/1\" / \"newer_than:1d\" / " +
		"\"is:unread\"), try a keyboard shortcut, or explain the blocker to the user. Do not keep " +
		"clicking the same non-committing target — it wastes turns and piles up screenshots."
}

// cuaStateFingerprint extracts a stable progress signal from a cua_driver
// snapshot result. Returns "" for non-snapshot results so they're ignored
// rather than mis-counted.
//
// yansu's cua_driver returns get_app_state/get_window_state as the daemon's
// JSON dict (snapToDict): {"window":{"title":…,"live_title":…},"elements":[…]}.
// We fingerprint window.live_title (preferred — updates instantly on
// Electron/CEF navigation; falls back to AX title) PLUS the element count. The
// element count is the AX-content signal that lets the guard work on native
// apps with no URL and on web views whose title doesn't change between views:
// a committing action that changes what's on screen changes the element set,
// while a non-committing retry leaves both title and count identical. It is
// deliberately coarse (a count, not a hash of the whole tree) so benign
// per-snapshot churn — timestamps, blinking carets — doesn't read as progress
// and silence the guard.
//
// The "Window:\"…\"" / "URL:" regex path below is Codex's text surface, not
// ours; it's kept as a fallback for any text-formatted result.
func cuaStateFingerprint(content string) string {
	if content == "" {
		return ""
	}

	// The first cua_driver result of a run is prefixed with an inventory
	// (header + list_apps JSON + separator) by MaybeInjectInventory. Parse the
	// part after the last separator so we fingerprint the real snapshot JSON,
	// not the injected inventory.
	jsonPart := content
	if i := strings.LastIndex(content, cuaInventoryPreludeSeparator); i >= 0 {
		jsonPart = content[i+len(cuaInventoryPreludeSeparator):]
	}

	var parsed struct {
		Window struct {
			Title     string `json:"title"`
			LiveTitle string `json:"live_title"`
		} `json:"window"`
		Elements []json.RawMessage `json:"elements"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonPart)), &parsed); err == nil {
		title := strings.TrimSpace(parsed.Window.LiveTitle)
		if title == "" {
			title = strings.TrimSpace(parsed.Window.Title)
		}
		if title != "" || len(parsed.Elements) > 0 {
			return "t=" + title + "|n=" + itoa(len(parsed.Elements))
		}
	}

	// Fallback: Codex-style text snapshot ("Window: …" / "URL: …").
	var parts []string
	if m := cuaWindowTitleRe.FindStringSubmatch(content); m != nil {
		parts = append(parts, "w="+strings.TrimSpace(m[1]))
	}
	for _, m := range cuaURLRe.FindAllStringSubmatch(content, -1) {
		parts = append(parts, "u="+strings.TrimSpace(m[1]))
	}
	return strings.Join(parts, "|")
}

func cuaSubToolName(rawInput string) string {
	if rawInput == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(rawInput), &m); err != nil {
		return ""
	}
	name, _ := m["name"].(string)
	return strings.TrimSpace(name)
}
