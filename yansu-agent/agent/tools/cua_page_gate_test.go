package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

// chromeSnapshot is a get_window_state result in the shape the daemon sends
// (AXBridge.swift Element.toDict, Daemon.swift snapToDict; the proxy renames
// "elements" to "ui_elements" and adds element_index): a Chrome window with a
// bank tab in the tab strip, the omnibox, a bookmark, an allowed page with
// numbers above and below the fold, a side panel, and a screenshot.
func chromeSnapshot(t *testing.T, elementsKey, page string, shot string) json.RawMessage {
	t.Helper()
	el := func(index int, role, title, value, url string, x, y, w, h float64) map[string]any {
		out := map[string]any{"ref": "a" + string(rune('0'+index)), "role": role, "enabled": true, "focused": false,
			"actions": []any{}, "children_count": 0, "element_index": index,
			"bounds": map[string]any{"x": x, "y": y, "w": w, "h": h}}
		if title != "" {
			out["title"] = title
		}
		if value != "" {
			out["value"] = value
		}
		if url != "" {
			out["url"] = url
		}
		return out
	}
	elements := []any{
		el(0, "AXWindow", "Downloads • PostHog", "", "", 0, 25, 1200, 875),
		el(1, "AXRadioButton", "Chase - Account balance", "", "", 10, 30, 220, 30),
		el(2, "AXRadioButton", "Downloads • PostHog", "", "", 240, 30, 220, 30),
		el(3, "AXTextField", "Address and search bar", "us.posthog.com/project/1/insights/AbCd1234", "", 120, 62, 800, 28),
		el(4, "AXButton", "Payroll", "", "https://payroll.example.com/", 10, 95, 80, 22),
		el(5, "AXWebArea", "Downloads • PostHog", "", page, 0, 120, 900, 780),
		el(6, "AXStaticText", "", "Downloads", "", 40, 200, 120, 20),
		el(7, "AXStaticText", "", "1,240 (+8% vs last week)", "", 200, 200, 200, 20),
		el(8, "AXStaticText", "", "D7 retention 31%", "", 40, 1400, 200, 20),
		el(9, "AXGroup", "Reading list", "Chase statement ready", "", 905, 120, 290, 600),
		el(10, "AXStaticText", "", "", "", 0, 0, 0, 0),
		el(11, "AXGroup", "Downloads bar", "chase-statement.pdf", "", 0, 902, 900, 20),
	}
	snap := map[string]any{
		"snapshot_id": "s1", "ax_status": "populated",
		"app":            map[string]any{"pid": 501, "bundle": "com.google.Chrome", "name": "Google Chrome"},
		"window":         map[string]any{"id": 77, "title": "Downloads • PostHog", "live_title": "Chase - Account balance"},
		elementsKey:      elements,
		"visual_index":   []any{map[string]any{"ref": "v1", "text": "Chase", "x": 12.0, "y": 32.0, "w": 60.0, "h": 14.0}, map[string]any{"ref": "v2", "text": "1,240", "x": 200.0, "y": 200.0, "w": 60.0, "h": 14.0}},
		"menu_shortcuts": []any{map[string]any{"path": "History > Chase", "combo": "cmd+1"}},
	}
	if shot != "" {
		snap["screenshot"] = map[string]any{"path": shot, "width": 1200, "height": 875}
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// withParents adds what a current daemon sends: each element's parent_index
// (the window holds the browser's chrome and the web area, the web area its
// content), and a find bar drawn over the page, inside its box but not part
// of it.
func withParents(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var snap map[string]any
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	elements := snap[key].([]any)
	parents := []int{-1, 0, 0, 0, 0, 0, 5, 5, 5, 0, 0, 0}
	for i, p := range parents {
		if p >= 0 {
			elements[i].(map[string]any)["parent_index"] = p
		}
	}
	elements = append(elements, map[string]any{"role": "AXTextField", "title": "Find in page", "value": "Chase", "element_index": 12,
		"parent_index": 0, "bounds": map[string]any{"x": 600.0, "y": 130.0, "w": 280.0, "h": 40.0}})
	snap[key] = elements
	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func unattendedCall(grants ...authority.Grant) ToolCall {
	return ToolCall{Authority: &AuthorityRuntime{Evaluator: authority.NewPolicy(grants), Context: authority.Context{Unattended: true}}}
}

func TestUnattendedBrowserSnapshotsReturnOnlyTheApprovedPage(t *testing.T) {
	call := unattendedCall(authority.Grant{ID: "page", Capability: CuaReadPageCapability, ResourcePrefix: "https://us.posthog.com/"})
	for _, key := range []string{"ui_elements", "elements"} {
		shot := filepath.Join(t.TempDir(), "alma-cu-state-1.jpg")
		if err := os.WriteFile(shot, []byte("jpg"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, replaced := gateUnattendedCuaResult(context.Background(), call, "get_window_state",
			chromeSnapshot(t, key, "https://us.posthog.com/project/1/insights/AbCd1234", shot))
		if !replaced || res.IsError {
			t.Fatalf("%s: an approved page must be returned: %+v", key, res)
		}
		for _, leak := range []string{"Chase", "chase-statement", "Address and search bar", "Payroll", "payroll.example.com", "Reading list", "menu_shortcuts", "screenshot", "visual_index", "live_title", "D7 retention"} {
			if strings.Contains(res.Content, leak) {
				t.Fatalf("%s: %q passed the gate: %s", key, leak, res.Content)
			}
		}
		// What the page shows in its box is returned; content scrolled out
		// of it is not (the run has no scrolling either).
		for _, want := range []string{"Downloads", "1,240 (+8% vs last week)"} {
			if !strings.Contains(res.Content, want) {
				t.Fatalf("%s: the page's %q is missing: %s", key, want, res.Content)
			}
		}
		if _, err := os.Stat(shot); !os.IsNotExist(err) {
			t.Fatalf("%s: the window screenshot shows the tab strip and must be deleted", key)
		}
		if !strings.Contains(res.Content, cuaPartialPageNote) {
			t.Fatalf("%s: a page cut to its box says the run saw only part of it: %s", key, res.Content)
		}
	}
}

func TestUnattendedSnapshotsKeepWhatLiesUnderTheApprovedPage(t *testing.T) {
	call := unattendedCall(authority.Grant{ID: "page", Capability: CuaReadPageCapability, ResourcePrefix: "https://us.posthog.com/"})
	for _, key := range []string{"ui_elements", "elements"} {
		raw := withParents(t, chromeSnapshot(t, key, "https://us.posthog.com/project/1/insights/AbCd1234", ""), key)
		res, replaced := gateUnattendedCuaResult(context.Background(), call, "get_window_state", raw)
		if !replaced || res.IsError {
			t.Fatalf("%s: an approved page must be returned: %+v", key, res)
		}
		// The find bar lies inside the page's box but is not part of it.
		for _, leak := range []string{"Chase", "Find in page", "Address and search bar", "Payroll", "Reading list", "chase-statement"} {
			if strings.Contains(res.Content, leak) {
				t.Fatalf("%s: %q passed the gate: %s", key, leak, res.Content)
			}
		}
		// The whole page is kept, scrolled-away content too, so nothing is
		// partial.
		for _, want := range []string{"Downloads", "1,240 (+8% vs last week)", "D7 retention 31%"} {
			if !strings.Contains(res.Content, want) {
				t.Fatalf("%s: the page's %q is missing: %s", key, want, res.Content)
			}
		}
		if strings.Contains(res.Content, "page_note") {
			t.Fatalf("%s: a whole page is not partial: %s", key, res.Content)
		}
	}
	// A parent named after its child, or one that is not an index, is not a
	// tree the check can follow.
	for _, bad := range []any{13.0, "5", -1.0, 2.5} {
		var snap map[string]any
		_ = json.Unmarshal(withParents(t, chromeSnapshot(t, "ui_elements", "https://us.posthog.com/a", ""), "ui_elements"), &snap)
		snap["ui_elements"].([]any)[7].(map[string]any)["parent_index"] = bad
		raw, _ := json.Marshal(snap)
		if res, replaced := gateUnattendedCuaResult(context.Background(), call, "get_window_state", raw); !replaced || !res.IsError {
			t.Fatalf("parent_index %v must be refused: %+v", bad, res)
		}
	}
}

func TestUnattendedWebContentIsCheckedInEveryApp(t *testing.T) {
	call := unattendedCall(authority.Grant{ID: "page", Capability: CuaReadPageCapability, ResourcePrefix: "https://us.posthog.com/"})
	// An app built on a web view shows pages as well; its web areas are
	// checked like a browser's.
	electron := func(page string) json.RawMessage {
		return json.RawMessage(`{"app":{"bundle":"com.tinyspeck.slackmacgap"},"ui_elements":[` +
			`{"role":"AXWindow","title":"Slack","bounds":{"x":0,"y":0,"w":900,"h":700}},` +
			`{"role":"AXWebArea","url":"` + page + `","parent_index":0,"bounds":{"x":0,"y":40,"w":900,"h":660}},` +
			`{"role":"AXStaticText","value":"direct messages","parent_index":1,"bounds":{"x":10,"y":60,"w":200,"h":20}}]}`)
	}
	if res, replaced := gateUnattendedCuaResult(context.Background(), call, "get_window_state", electron("https://app.slack.com/client/T1")); !replaced || !res.IsError {
		t.Fatalf("a web area on a page the run was not allowed must be refused in any app: %+v", res)
	}
	res, replaced := gateUnattendedCuaResult(context.Background(), call, "get_window_state", electron("https://us.posthog.com/a"))
	if !replaced || res.IsError || strings.Contains(res.Content, "Slack") {
		t.Fatalf("an allowed page in any app is cut to the page: %+v", res)
	}
	// Commands whose results the checks do not know are withheld; opening
	// an app or a page and the driver's reports pass.
	for _, name := range []string{"list_windows", "windows", "list_apps", "lens", "intent", "something_new", "click"} {
		if res, replaced := gateUnattendedCuaResult(context.Background(), call, name, json.RawMessage(`{"windows":[{"title":"Chase - Account balance"}]}`)); !replaced || !res.IsError {
			t.Fatalf("%s must be withheld: %+v", name, res)
		}
	}
	for _, name := range []string{"launch_app", "open_url", "check_permissions"} {
		if _, replaced := gateUnattendedCuaResult(context.Background(), call, name, json.RawMessage(`{"opened":true}`)); replaced {
			t.Fatalf("%s's acknowledgement passes", name)
		}
	}
}

func TestUnattendedActionsAreRefusedBeforeTheyAreSent(t *testing.T) {
	call := unattendedCall(authority.Grant{ID: "wide", Capability: "computer.", ResourcePrefix: ""})
	for _, name := range []string{"click", "type", "type_text", "set_value", "scroll", "press", "press_key", "hotkey", "drag",
		"perform_secondary_action", "screenshot", "shot", "list_windows", "list_apps", "lens", "something_new"} {
		if _, err := prepareCuaCall(call, name, map[string]any{"bundle": "com.google.Chrome"}); err == nil {
			t.Fatalf("%s must be refused before it reaches the daemon, whatever the grants", name)
		}
	}
	for _, name := range []string{"get_window_state", "get_app_state", "snap", "open_url", "launch_app", "check_permissions", "list_tools"} {
		if _, err := prepareCuaCall(call, name, map[string]any{"bundle": "com.google.Chrome"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A watched chat acts as the user lets it.
	if _, err := prepareCuaCall(ToolCall{Authority: &AuthorityRuntime{}}, "click", map[string]any{}); err != nil {
		t.Fatalf("attended click: %v", err)
	}
}

func TestWebPageElementsForOtherReaders(t *testing.T) {
	var snap map[string]any
	_ = json.Unmarshal(withParents(t, chromeSnapshot(t, "ui_elements", "https://mail.google.com/mail/u/0/#search/x", ""), "ui_elements"), &snap)
	elements := snap["ui_elements"].([]any)
	// A frame inside the page is part of it, not a page of its own.
	elements = append(elements, map[string]any{"role": "AXWebArea", "url": "about:blank", "parent_index": 5.0,
		"bounds": map[string]any{"x": 40.0, "y": 300.0, "w": 300.0, "h": 100.0}})
	kept, pages, partial, ok := CuaWebPageElements(elements)
	if !ok || partial || len(pages) != 1 || pages[0]["url"] != "https://mail.google.com/mail/u/0/#search/x" {
		t.Fatalf("pages = %+v partial=%v ok=%v", pages, partial, ok)
	}
	raw, _ := json.Marshal(kept)
	for _, leak := range []string{"Chase", "Find in page", "Address and search bar", "Reading list"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("%q kept: %s", leak, raw)
		}
	}
	if !strings.Contains(string(raw), "D7 retention 31%") || !strings.Contains(string(raw), "about:blank") {
		t.Fatalf("the page and its frame are kept: %s", raw)
	}
	if _, _, _, ok := CuaWebPageElements([]any{map[string]any{"role": "AXWindow"}}); ok {
		t.Fatal("a window without a page has no page to keep")
	}
	// Without parents, what is in view is kept and said to be partial.
	_ = json.Unmarshal(chromeSnapshot(t, "ui_elements", "https://mail.google.com/", ""), &snap)
	if kept, _, partial, ok := CuaWebPageElements(snap["ui_elements"].([]any)); !ok || !partial || len(kept) == 0 {
		t.Fatalf("box fallback: kept=%d partial=%v ok=%v", len(kept), partial, ok)
	}
}

func TestUnattendedReadsFailClosed(t *testing.T) {
	call := unattendedCall(authority.Grant{ID: "page", Capability: CuaReadPageCapability, ResourcePrefix: "https://us.posthog.com/"})
	refused := func(name string, result json.RawMessage) bool {
		res, replaced := gateUnattendedCuaResult(context.Background(), call, name, result)
		return replaced && res.IsError
	}
	if !refused("get_window_state", chromeSnapshot(t, "ui_elements", "https://onlinebanking.example.com/accounts", "")) {
		t.Fatal("a bank page in front must be refused")
	}
	if !refused("get_window_state", chromeSnapshot(t, "ui_elements", "", "")) {
		t.Fatal("a web area without an address (none asked for, or over 2,048 characters) must be refused")
	}
	// A child frame on another site beside the approved page.
	var snap map[string]any
	_ = json.Unmarshal(chromeSnapshot(t, "ui_elements", "https://us.posthog.com/a", ""), &snap)
	snap["ui_elements"] = append(snap["ui_elements"].([]any), map[string]any{"role": "AXWebArea", "url": "https://ads.example/frame",
		"bounds": map[string]any{"x": 10.0, "y": 500.0, "w": 300.0, "h": 250.0}})
	raw, _ := json.Marshal(snap)
	if !refused("get_window_state", raw) {
		t.Fatal("a frame on another site must be refused")
	}
	// Two element lists, or one that is not a list, are refused.
	var both map[string]any
	_ = json.Unmarshal(chromeSnapshot(t, "ui_elements", "https://us.posthog.com/a", ""), &both)
	both["elements"] = []any{}
	raw, _ = json.Marshal(both)
	if !refused("get_window_state", raw) {
		t.Fatal("conflicting element lists must be refused")
	}
	if !refused("get_window_state", json.RawMessage(`{"app":{"bundle":"com.google.Chrome"},"ui_elements":{"a":1}}`)) {
		t.Fatal("an element list that is not a list must be refused")
	}
	// A command the checks do not know that hands back a window's content.
	if !refused("lens", json.RawMessage(`{"visual_refs":[{"text":"Chase"}]}`)) {
		t.Fatal("unknown commands that return window content must be refused")
	}
	if _, replaced := gateUnattendedCuaResult(context.Background(), call, "open_url", json.RawMessage(`{"opened":true,"url":"https://us.posthog.com/a"}`)); replaced {
		t.Fatal("a plain acknowledgement passes")
	}
	// The aliases are gated the same way.
	for _, alias := range []string{"get_app_state", "snap"} {
		if !refused(alias, chromeSnapshot(t, "elements", "https://onlinebanking.example.com/", "")) {
			t.Fatalf("%s must be gated", alias)
		}
	}
	// A snapshot that names no app, and plain screenshots.
	if !refused("get_window_state", json.RawMessage(`{"app":{},"ui_elements":[]}`)) {
		t.Fatal("a snapshot of an unknown app must be refused")
	}
	shot := filepath.Join(t.TempDir(), "shot.png")
	_ = os.WriteFile(shot, []byte("png"), 0o600)
	if !refused("shot", json.RawMessage(`{"path":"`+shot+`","width":10,"height":10}`)) {
		t.Fatal("a plain screenshot must be refused")
	}
	if _, err := os.Stat(shot); !os.IsNotExist(err) {
		t.Fatal("a refused screenshot must be deleted")
	}
	// Non-browser apps are governed by their app grant, attended runs by the user.
	if _, replaced := gateUnattendedCuaResult(context.Background(), call, "get_window_state", json.RawMessage(`{"app":{"bundle":"com.apple.Notes"},"ui_elements":[]}`)); replaced {
		t.Fatal("a non-browser snapshot is not the page gate's to change")
	}
	attended := ToolCall{Authority: &AuthorityRuntime{Evaluator: authority.NewPolicy(nil)}}
	if _, replaced := gateUnattendedCuaResult(context.Background(), attended, "get_window_state", chromeSnapshot(t, "ui_elements", "https://onlinebanking.example.com/", "")); replaced {
		t.Fatal("attended reads are the user's")
	}
}

func TestCuaCallsNeedTheRunsAuthorityAndAskForAddresses(t *testing.T) {
	if _, err := prepareCuaCall(ToolCall{}, "get_window_state", map[string]any{}); err == nil {
		t.Fatal("a call without authority skips every check and must be refused")
	}
	if _, err := prepareCuaCall(ToolCall{Authority: &AuthorityRuntime{Context: authority.Context{Unattended: true}}}, "get_window_state", map[string]any{}); err == nil {
		t.Fatal("an unattended call without an evaluator must be refused")
	}
	minted := 0
	CuaSourceLocatorCapability = func() (string, error) { minted++; return "cap-1", nil }
	t.Cleanup(func() { CuaSourceLocatorCapability = nil })
	args := map[string]any{"bundle": "com.google.Chrome"}
	sent, err := prepareCuaCall(unattendedCall(), "get_window_state", args)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := sent.(map[string]any)
	if m["include_element_urls"] != true || m["source_locator_capability"] != "cap-1" || minted != 1 {
		t.Fatalf("an unattended snapshot asks for addresses: %v", m)
	}
	if _, leaked := args["source_locator_capability"]; leaked {
		t.Fatal("the token must not land in the call's own input")
	}
	attended := map[string]any{}
	if sent, err := prepareCuaCall(ToolCall{Authority: &AuthorityRuntime{}}, "get_window_state", attended); err != nil || len(sent.(map[string]any)) != 0 {
		t.Fatalf("an attended snapshot is unchanged: %v %v", sent, err)
	}
}
