package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cua_policy.go encodes the no-foreground contract Yansu enforces on
// behalf of the bundled cua-driver. The contract used to require
// element_index for every click because trycua/cua's pixel fallback
// (.cghidEventTap) forced the target window frontmost. The current
// vendored daemon prepares process-level background activation and
// posts input directly to a resolved pid/window. The result is a
// click/key/type path that lands inside the target window without
// WindowServer activating its owner. So pixel clicks are now SAFE and
// we no longer reject them.
//
// What we still gate:
//   - Legacy `hotkey` / `press` calls for combos historically known to
//     steal focus or mutate the wrong target. New flows should prefer
//     `press_key`, which the daemon can run through process-level
//     background activation before posting to the target pid.
//
// What we now allow that we previously denied:
//   - click {pid, x, y} without element_index → background pid/window
//     route, safe.
//   - click {pid, ref:"v*"} → visual OCR ref (alma's VisualIndex),
//     safe.
//   - drag {pid, from_x, from_y, to_x, to_y} → alma routes drag
//     through the same background pid/window route. No focus steal.

// cuaPolicyDenial inspects a parsed daemon request and returns a
// non-empty denial string when it would violate the no-foreground
// contract. Empty string means "allowed". The denial text is the
// LLM-visible error: lead with the rule, explain why, end with a
// concrete compliant alternative so the model self-corrects on the
// next turn.
func cuaPolicyDenial(method, name string, rawArgs json.RawMessage) string {
	// Only "call" requests carry a tool name we need to validate.
	// `list-tools`, `describe`, `shutdown`, etc. pass through.
	if method != "" && method != "call" {
		return ""
	}
	args := map[string]any{}
	if len(rawArgs) > 0 {
		// A malformed args blob is the daemon's problem to surface,
		// not ours. Forward it and let the daemon return its native
		// schema error so the model sees one consistent error format.
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ""
		}
	}

	switch name {
	case "click", "drag", "type", "type_text", "set_value", "scroll", "press_key", "screenshot", "get_window_state", "launch_app", "list_apps", "list_windows", "check_permissions", "open_url":
		// All of these now have a focus-safe route through the
		// alma daemon (AX path, background pid/window delivery, or
		// LaunchServices for open_url). No additional gating needed at this layer —
		// the daemon itself enforces TCC and refuses what it can't
		// do safely.

	case "hotkey", "press":
		if reason := cuaFocusStealingHotkeyReason(args); reason != "" {
			return "yansu policy: " + reason + " Use `launch_app {bundle, urls:[...]}` to open URLs without focus-steal; for tab/window switching, ask the user."
		}
	}
	return ""
}

// cuaFocusStealingHotkeyReason returns the reason a hotkey combo would
// pull focus, or "" when the combo is fine to forward. The blocklist
// is narrow: only combos that the OS or the app interprets as "show
// me this window". cmd+S, cmd+C, cmd+V etc. are fine — they run
// inside whichever app currently owns the AX context.
func cuaFocusStealingHotkeyReason(args map[string]any) string {
	combo := normaliseHotkeyCombo(args)
	if combo == nil {
		return ""
	}
	hasMod := combo["cmd"] || combo["command"]
	if !hasMod {
		return ""
	}

	// cmd+L: every major browser focuses the omnibox/address bar.
	// The browser activates itself in response so the user can type
	// — no auth-signed posting trick gets around that.
	if combo["l"] {
		return "cmd+L (omnibox/address-bar focus) is forbidden — browsers activate themselves in response to it, defeating no-foreground delivery."
	}
	// cmd+1..cmd+9: browser tab switch / dock app switch — both pull
	// focus to the window that becomes the new selection.
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		if combo[n] {
			return fmt.Sprintf("cmd+%s (browser tab / window switch) is forbidden — switching the active tab pulls focus to the browser window.", n)
		}
	}
	// cmd+tab / cmd+`: app and window cyclers built into macOS
	// itself. The OS owns these; the daemon can't post them without
	// focus-steal.
	if combo["tab"] {
		return "cmd+Tab (app switcher) is forbidden — it's owned by the WindowServer, not the target app, so no-foreground delivery is impossible."
	}
	if combo["`"] {
		return "cmd+` (window cycler) is forbidden — same reason as cmd+Tab; the WindowServer activates the next window."
	}
	return ""
}

// normaliseHotkeyCombo accepts both the legacy {keys:[...]} shape and
// the alma-shape {key:..., modifiers:[...]} and returns a flat
// lowercased lookup map. nil when neither shape is present.
func normaliseHotkeyCombo(args map[string]any) map[string]bool {
	combo := map[string]bool{}
	if keys, ok := args["keys"].([]any); ok {
		for _, k := range keys {
			if s, ok := k.(string); ok {
				combo[strings.ToLower(strings.TrimSpace(s))] = true
			}
		}
		if len(combo) > 0 {
			return combo
		}
	}
	if mods, ok := args["modifiers"].([]any); ok {
		for _, m := range mods {
			if s, ok := m.(string); ok {
				combo[strings.ToLower(strings.TrimSpace(s))] = true
			}
		}
	}
	if k, ok := args["key"].(string); ok {
		combo[strings.ToLower(strings.TrimSpace(k))] = true
	}
	if len(combo) == 0 {
		return nil
	}
	return combo
}
