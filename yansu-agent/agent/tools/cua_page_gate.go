package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yansu-agent/agent/authority"
)

// An unattended run that may read a browser may read only the pages it was
// allowed: an app grant covers the window, not whichever tab happens to be in
// front of it or the browser's own chrome. So for an unattended run:
//
//   - every snapshot asks the daemon for its elements' addresses (a one-use
//     capability from the host app, CuaSourceLocatorCapability), since only
//     the web areas' addresses say which pages a window shows;
//   - a snapshot of a browser window is returned only when every web area in
//     it has an address that passes "computer.read.page" (an approved origin);
//     a web area without an address (or one too long for the daemon to send)
//     fails the check;
//   - what is returned is only what lies inside those web areas: tab strip,
//     address bar, bookmarks, side panels, menus and the window screenshot
//     never pass, and a screenshot file the daemon wrote is deleted;
//   - a plain screenshot (screenshot or shot) is refused and its file
//     deleted: it names no app, so it cannot be checked;
//   - a snapshot whose app the daemon does not name is refused.
//
// The checks key on the daemon command (get_window_state and its aliases
// get_app_state and snap) and on the app the snapshot names, never on the
// arguments. Attended runs are not gated: the user is there.

// CuaSourceLocatorCapability is set by the host app: it returns a one-use
// capability the cua-driver proxy exchanges for element addresses in one
// get_window_state snapshot.
var CuaSourceLocatorCapability func() (string, error)

// cuaBrowserBundles are the apps whose windows show web pages.
var cuaBrowserBundles = map[string]bool{
	"com.google.Chrome": true, "com.google.Chrome.beta": true, "com.google.Chrome.canary": true,
	"org.chromium.Chromium": true, "com.apple.Safari": true, "com.apple.SafariTechnologyPreview": true,
	"company.thebrowser.Browser": true, "com.microsoft.edgemac": true, "com.brave.Browser": true,
	"org.mozilla.firefox": true, "com.operasoftware.Opera": true, "com.vivaldi.Vivaldi": true,
}

var (
	cuaSnapshotCommands = map[string]bool{"get_window_state": true, "get_app_state": true, "snap": true}
	cuaShotCommands     = map[string]bool{"screenshot": true, "shot": true}
	// cuaPassCommands open a page or an app, or report on the driver, and
	// hand back only an acknowledgement. With the snapshot commands they are
	// all a run on its own may send: actions (clicks, typing, keys, drags,
	// scrolling) and every other read (screenshots, window and app lists,
	// whose titles name other tabs; OCR lenses; anything new) are refused
	// before they reach the daemon.
	cuaPassCommands = map[string]bool{
		"open_url": true, "launch_app": true, "check_permissions": true, "permissions": true,
		"list_tools": true, "list-tools": true, "describe": true, "ping": true,
	}
)

// CuaReadPageCapability is the capability a page read is checked against.
const CuaReadPageCapability = "computer.read.page"

const (
	cuaPageGateRefusal       = "Not returned: this run may read only the pages it was allowed, and the browser window shows another page or does not say which page it shows. Open an allowed page with open_url, then read it with get_window_state."
	cuaShotRefusal           = "Not returned: a run on its own reads windows with get_window_state, never as a plain screenshot."
	cuaUnknownAppRefusal     = "Not returned: the snapshot does not say which app it shows."
	cuaUnknownCommandRefusal = "Not returned: a run on its own may read only checked window snapshots."
	cuaPartialPageNote       = "Only what was in view of this page was read: content scrolled above or below it may be missing. Say in the result which part you saw."
)

var errCuaNoAuthority = errors.New("cua_driver needs the run's authority to act on the computer")

// cuaUnattendedRefusal refuses, before it is sent, a command a run on its
// own may not use. Authority refuses these too; this holds when a grant is
// wider than it should be.
func cuaUnattendedRefusal(name string) error {
	return fmt.Errorf("a run on its own may not use %s: it may open pages and apps and read window snapshots only", name)
}

// cuaUnattended reports whether a call belongs to a run nobody is watching.
func cuaUnattended(call ToolCall) bool {
	return call.Authority != nil && call.Authority.Context.Unattended
}

// prepareCuaCall refuses a call without the run's authority (it would skip
// every check), and asks for element addresses on an unattended snapshot. It
// returns the arguments to send: a copy when it adds the one-use token, so
// the token never lands in the call's stored input.
func prepareCuaCall(call ToolCall, name string, args any) (any, error) {
	if call.Authority == nil || (call.Authority.Context.Unattended && call.Authority.Evaluator == nil) {
		return args, errCuaNoAuthority
	}
	if cuaUnattended(call) && !cuaSnapshotCommands[name] && !cuaPassCommands[name] {
		return args, cuaUnattendedRefusal(name)
	}
	if !cuaUnattended(call) || name != "get_window_state" || CuaSourceLocatorCapability == nil {
		return args, nil
	}
	m, ok := args.(map[string]any)
	if !ok {
		return args, nil
	}
	token, err := CuaSourceLocatorCapability()
	if err != nil || strings.TrimSpace(token) == "" {
		// No addresses: a browser snapshot then fails the page check.
		return args, nil
	}
	sent := make(map[string]any, len(m)+2)
	for k, v := range m {
		sent[k] = v
	}
	sent["include_element_urls"] = true
	sent["source_locator_capability"] = token
	return sent, nil
}

// cuaDataKeys are the parts of a response that carry what a window shows.
var cuaDataKeys = []string{"elements", "ui_elements", "visual_index", "visual_refs", "screenshot"}

// gateUnattendedCuaResult replaces an unattended run's result when it must
// not be returned as it is: refused, or cut down to the approved pages.
func gateUnattendedCuaResult(ctx context.Context, call ToolCall, name string, result json.RawMessage) (ToolResult, bool) {
	if !cuaUnattended(call) {
		return ToolResult{}, false
	}
	if cuaShotCommands[name] {
		var shot struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(result, &shot)
		removeCuaShot(shot.Path)
		return ToolResult{Content: cuaShotRefusal, IsError: true}, true
	}
	if !cuaSnapshotCommands[name] {
		// Commands that only act or report on the driver pass, unless what
		// they hand back carries a window's content; every other command's
		// result is withheld from a run on its own.
		var other map[string]any
		_ = json.Unmarshal(result, &other)
		for _, key := range cuaDataKeys {
			if _, ok := other[key]; ok {
				if shot, ok := other["screenshot"].(map[string]any); ok {
					path, _ := shot["path"].(string)
					removeCuaShot(path)
				}
				return ToolResult{Content: cuaUnknownAppRefusal, IsError: true}, true
			}
		}
		if cuaPassCommands[name] {
			return ToolResult{}, false
		}
		return ToolResult{Content: cuaUnknownCommandRefusal, IsError: true}, true
	}
	var snap map[string]any
	if err := json.Unmarshal(result, &snap); err != nil {
		return ToolResult{Content: cuaUnknownAppRefusal, IsError: true}, true
	}
	removeShot := func() {
		if shot, ok := snap["screenshot"].(map[string]any); ok {
			path, _ := shot["path"].(string)
			removeCuaShot(path)
		}
	}
	app, _ := snap["app"].(map[string]any)
	bundle, _ := app["bundle"].(string)
	bundle = strings.TrimSpace(bundle)
	if bundle == "" {
		removeShot()
		return ToolResult{Content: cuaUnknownAppRefusal, IsError: true}, true
	}
	refuse := func() (ToolResult, bool) {
		removeShot()
		return ToolResult{Content: cuaPageGateRefusal, IsError: true}, true
	}
	// One element list, as an array; two, or one of another shape, is not
	// a snapshot the check can trust.
	_, hasUI := snap["ui_elements"]
	_, hasRaw := snap["elements"]
	if hasUI && hasRaw {
		return refuse()
	}
	key := "ui_elements"
	if hasRaw {
		key = "elements"
	}
	elements, ok := snap[key].([]any)
	if !ok {
		return refuse()
	}
	var webAreas []int
	for i, raw := range elements {
		el, _ := raw.(map[string]any)
		if cuaIsWebArea(el) {
			webAreas = append(webAreas, i)
		}
	}
	// Any window with web content (a browser, or an app built on one) is
	// page-checked; a browser window must show a page; other apps' windows
	// are their app grant's alone.
	if len(webAreas) == 0 {
		if cuaBrowserBundles[bundle] {
			return refuse()
		}
		return ToolResult{}, false
	}
	approved := map[int]bool{}
	var areas []cuaRect
	for _, i := range webAreas {
		el, _ := elements[i].(map[string]any)
		page, _ := el["url"].(string)
		if strings.TrimSpace(page) == "" {
			return refuse()
		}
		evaluation := call.Authority.Evaluator.Evaluate(ctx, call.Authority.Context, authority.Action{
			Capability: CuaReadPageCapability,
			ToolName:   "cua_driver",
			Operation:  name,
			Effect:     authority.EffectRead,
			Resource:   strings.TrimSpace(page),
			Reversible: true,
		})
		if call.Authority.Record != nil {
			call.Authority.Record(evaluation)
		}
		if evaluation.Decision != authority.DecisionAllow {
			return refuse()
		}
		approved[i] = true
		if rect, ok := cuaElementRect(el); ok {
			areas = append(areas, rect)
		}
	}
	kept, partial, ok := cuaPageElements(elements, approved, areas)
	if !ok {
		return refuse()
	}
	snap[key] = kept
	if partial {
		snap["page_note"] = cuaPartialPageNote
	}
	// OCR refs are in screenshot pixels, not screen points, so they cannot
	// be placed against the page: none are returned. Neither are the
	// window's titles (the live one can name another tab mid-read), the
	// screenshot or the app's menu shortcuts.
	delete(snap, "visual_index")
	delete(snap, "visual_refs")
	removeShot()
	delete(snap, "screenshot")
	delete(snap, "menu_shortcuts")
	if window, ok := snap["window"].(map[string]any); ok {
		delete(window, "title")
		delete(window, "live_title")
	}
	out, err := json.Marshal(snap)
	if err != nil {
		return refuse()
	}
	return ToolResult{Content: string(out)}, true
}

// CuaWebPageElements keeps, of a snapshot's element list, its web pages: the
// web areas and what lies under them, as an unattended read keeps its
// approved pages. webAreas are the kept web areas with no web area above
// them, for a caller to check their addresses. partial is true when the
// daemon names no parents and only what was in view could be kept. ok is
// false when the list shows no web page or names parents that cannot be
// followed.
func CuaWebPageElements(elements []any) (kept []any, webAreas []map[string]any, partial bool, ok bool) {
	pages := map[int]bool{}
	var areas []cuaRect
	for i, raw := range elements {
		el, _ := raw.(map[string]any)
		if cuaIsWebArea(el) {
			pages[i] = true
			if rect, ok := cuaElementRect(el); ok {
				areas = append(areas, rect)
			}
		}
	}
	if len(pages) == 0 {
		return nil, nil, false, false
	}
	kept, partial, ok = cuaPageElements(elements, pages, areas)
	if !ok {
		return nil, nil, false, false
	}
	// The outermost pages: a web area under another (a frame) is part of
	// the page around it.
	for i := range elements {
		if !pages[i] {
			continue
		}
		el, _ := elements[i].(map[string]any)
		outer := true
		for p := cuaParentIndex(el); p >= 0; {
			if pages[p] {
				outer = false
				break
			}
			parent, _ := elements[p].(map[string]any)
			p = cuaParentIndex(parent)
		}
		if outer {
			webAreas = append(webAreas, el)
		}
	}
	return kept, webAreas, partial, true
}

func cuaIsWebArea(el map[string]any) bool {
	role, _ := el["role"].(string)
	return strings.Contains(strings.ToLower(role), "webarea")
}

// cuaParentIndex is an element's parent_index, or -1. cuaPageElements has
// checked every one before this is called.
func cuaParentIndex(el map[string]any) int {
	if p, ok := el["parent_index"].(float64); ok {
		return int(p)
	}
	return -1
}

// cuaPageElements keeps what belongs to the approved pages. When the daemon
// names each element's parent (parent_index), that is the approved web areas
// and everything under them, however far it is scrolled, and nothing drawn
// over them (find bar, extension pop-ups). An older daemon names no parents:
// then only what lies wholly inside a page's box is kept, and partial is
// always true, since content scrolled above or below the box cannot be told
// apart from the browser's own bars there.
func cuaPageElements(elements []any, approved map[int]bool, areas []cuaRect) ([]any, bool, bool) {
	parents := make([]int, len(elements))
	withParents := false
	for i, raw := range elements {
		el, _ := raw.(map[string]any)
		parents[i] = -1
		if v, ok := el["parent_index"]; ok {
			p, isNum := v.(float64)
			if !isNum || p < 0 || int(p) >= i || p != float64(int(p)) {
				// A parent must be listed before its child.
				return nil, false, false
			}
			parents[i] = int(p)
			withParents = true
		}
	}
	kept := make([]any, 0, len(elements))
	if withParents {
		under := make([]bool, len(elements))
		for i := range elements {
			under[i] = approved[i] || (parents[i] >= 0 && under[parents[i]])
			if under[i] {
				kept = append(kept, elements[i])
			}
		}
		return kept, false, true
	}
	if len(areas) == 0 {
		return nil, false, false
	}
	for _, raw := range elements {
		el, _ := raw.(map[string]any)
		if rect, ok := cuaElementRect(el); ok && rect.inside(areas) {
			kept = append(kept, el)
		}
	}
	return kept, true, true
}

type cuaRect struct{ x, y, w, h float64 }

// cuaElementRect reads an element's bounds: {"bounds":{x,y,w,h}} or flat
// x, y, w, h (visual refs). A missing or empty box is no box.
func cuaElementRect(el map[string]any) (cuaRect, bool) {
	src := el
	if b, ok := el["bounds"].(map[string]any); ok {
		src = b
	}
	num := func(key string) (float64, bool) {
		v, ok := src[key].(float64)
		return v, ok
	}
	x, ok1 := num("x")
	y, ok2 := num("y")
	w, ok3 := num("w")
	h, ok4 := num("h")
	if !ok1 || !ok2 || !ok3 || !ok4 || w <= 0 || h <= 0 {
		return cuaRect{}, false
	}
	return cuaRect{x, y, w, h}, true
}

// inside reports whether the box lies wholly inside one of the pages' boxes.
func (r cuaRect) inside(areas []cuaRect) bool {
	const slack = 1.0
	for _, a := range areas {
		if r.x >= a.x-slack && r.y >= a.y-slack && r.x+r.w <= a.x+a.w+slack && r.y+r.h <= a.y+a.h+slack {
			return true
		}
	}
	return false
}

// removeCuaShot deletes a screenshot the daemon wrote, only inside the
// temp folders it writes to.
func removeCuaShot(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	clean := filepath.Clean(path)
	for _, root := range []string{os.TempDir(), "/tmp", "/private/tmp"} {
		root = filepath.Clean(root)
		if strings.HasPrefix(clean, root+string(filepath.Separator)) {
			_ = os.Remove(clean)
			return
		}
	}
}
