package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// cua_driver_permissions.go exposes Wails-bound helpers for inspecting
// and triggering the macOS TCC grants cua-driver needs (Accessibility +
// Screen Recording). We deliberately do NOT auto-probe at app startup —
// that would spawn the daemon eagerly, which contradicts the lazy-start
// design and would surface a TCC prompt before the user has any reason
// to expect one. The frontend calls these when the user is about to
// invoke computer-use for the first time.

// CuaPermissionStatus is the cooked form of `check_permissions`'s reply.
// We expose three booleans so the frontend can render targeted UX
// (granted → fall through, denied → "open System Settings" button,
// undetermined → "request permission" button) without re-parsing the
// upstream response shape.
type CuaPermissionStatus struct {
	Accessibility   bool   `json:"accessibility"`
	ScreenRecording bool   `json:"screenRecording"`
	OK              bool   `json:"ok"` // both grants present
	Raw             string `json:"raw"`
}

// cuaCheckPermissionsPayload mirrors the daemon's CallTool.Result envelope:
// `result.payload.content[].text` carries the human-readable status.
// cua-driver doesn't fill structuredContent for this tool, so text is
// what we get; the format is stable enough to parse with simple
// substring checks.
type cuaCheckPermissionsPayload struct {
	Kind    string `json:"kind"`
	Payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

// RequestCuaDriverPermissions surfaces the macOS Accessibility / Screen
// Recording dialogs for any missing grants and returns the resulting
// state. Apple's request APIs are no-ops when a grant is already
// active, so this is safe to call even when both are already granted —
// it'll just report current state.
//
// Important: the dialog that fires is **Yansu's**, not the bundled
// CuaDriver's. The daemon runs as a child of Yansu via exec.Command,
// so macOS treats Yansu as the responsible process for TCC and any
// prompt resolves against Yansu's bundle id. activitymon would have
// triggered the same prompt on its first call regardless; this binding
// just lets the renderer ask explicitly before the LLM does.
func (a *App) RequestCuaDriverPermissions() (*CuaPermissionStatus, error) {
	return a.callCheckPermissions(true)
}

func (a *App) callCheckPermissions(prompt bool) (*CuaPermissionStatus, error) {
	if a.cuaDriver == nil {
		return nil, fmt.Errorf("cua-driver not available on this platform")
	}

	// Permission prompts can take an arbitrarily long time — the user
	// has to walk to System Settings, hit Privacy & Security, etc. — so
	// when prompting we extend the deadline well past the standard
	// 30-second tool ceiling. The non-prompting read-only path stays
	// snappy.
	timeout := 30 * time.Second
	if prompt {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := a.cuaDriver.Call(ctx, "check_permissions", map[string]any{"prompt": prompt})
	if err != nil {
		return nil, fmt.Errorf("check_permissions: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("check_permissions returned error: %s", resp.ErrorMessage())
	}

	var payload cuaCheckPermissionsPayload
	if err := json.Unmarshal(resp.Result, &payload); err != nil {
		return nil, fmt.Errorf("decode check_permissions payload: %w", err)
	}

	var raw string
	for _, block := range payload.Payload.Content {
		if block.Type == "text" {
			raw = block.Text
			break
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("check_permissions returned no text content")
	}

	status := parseCuaPermissionStatus(raw)
	status.Raw = raw
	return status, nil
}

// parseCuaPermissionStatus extracts grant booleans from cua-driver's
// human-readable status text. Format observed in the wild:
//
//	✅ Accessibility: granted.
//	✅ Screen Recording: granted.
//
// Denied / undetermined produce the same key but with different
// suffixes (denied / not granted / not-determined). We treat anything
// other than a clean "granted" (without a preceding "not ") as
// not-granted so the frontend errs on the side of prompting the user.
func parseCuaPermissionStatus(text string) *CuaPermissionStatus {
	status := &CuaPermissionStatus{}
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		granted := lineSaysGranted(lower)
		switch {
		case strings.Contains(lower, "accessibility"):
			status.Accessibility = granted
		case strings.Contains(lower, "screen recording"):
			status.ScreenRecording = granted
		}
	}
	status.OK = status.Accessibility && status.ScreenRecording
	return status
}

// lineSaysGranted is true only for an unambiguous "granted" — "not
// granted", "denied", "not-determined" all return false. We can't just
// substring-match "granted" because "not granted" contains it.
func lineSaysGranted(lowerLine string) bool {
	if strings.Contains(lowerLine, "not granted") {
		return false
	}
	if strings.Contains(lowerLine, "denied") {
		return false
	}
	if strings.Contains(lowerLine, "not-determined") || strings.Contains(lowerLine, "not determined") {
		return false
	}
	return strings.Contains(lowerLine, "granted")
}
