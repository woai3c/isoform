package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cua_translator.go bridges two wire protocols that the codebase calls
// the same thing ("cua-driver"):
//
//  - Legacy: trycua/cua's daemon — {method:"call",name,args} ↔
//    {ok,result,error}. The existing yansu-agent tool, the
//    /api/cua/call HTTP handler, and the SKILL.md hub all speak this
//    shape.
//
//  - Native: the alma-derived helper now bundled at
//    cua-driver-bundle/CuaDriver.app — NDJSON requests
//    {id,cmd,args} with responses {id,ok,data,error:{code,message,
//    hint}}. The Swift sources live in wails-gui/cua-driver-src/.
//
// The proxy holds a translator instance and rewrites both directions
// so call sites stay on the legacy shape while gaining alma's
// CEF/Electron-safe background activation path, AX-wakeup snap,
// visual OCR refs (v*), and intent map for free.
//
// State: we cache the most recent snap per pid so a legacy caller
// that hands us element_index can be translated to a ref before
// forwarding. The cache is bounded (one entry per pid; older snaps
// for the same pid replace) and stored only as JSON-shape maps so we
// don't grow a Go type per AX node.

// translatorMaxCachedPids bounds the snap cache. Worst case a long-
// running Yansu instance accumulates one entry per pid it has ever
// snapped; in practice the LLM rarely targets more than a few. We
// hard-cap to avoid a slow leak on pathological sessions. When the
// cap is hit we drop the oldest entries — translation just falls
// back to a "stale ref" error, which the LLM is already trained to
// handle by re-snapping.
const translatorMaxCachedPids = 64

// cuaTranslator wraps the alma daemon, presenting a cua-driver-shaped
// surface to callers. It is safe for concurrent use.
type cuaTranslator struct {
	mu sync.Mutex
	// snapCache[pid] holds the most recent ordered element list from
	// snap/get_app_state for that pid. Position N corresponds to the
	// legacy element_index N. Each element is the raw alma element map
	// (ref + role + bounds + actions + ...).
	snapCache map[int]*cachedSnap
	// snapOrder lets us pick the oldest cache entry to evict when we
	// hit translatorMaxCachedPids. Append-only with linear scan; the
	// cap is small enough (64) that this isn't worth a heap.
	snapOrder []int
	// nextID seeds the synthetic request id we hand the alma daemon.
	// alma requires `id` to be present and unique within a connection
	// (one request per connection in practice, but the field is still
	// required by Protocol.swift).
	nextID uint64
}

type cachedSnap struct {
	elements []map[string]any
	windowID int64 // CGWindowID resolved from the snap response, if any
}

func newCuaTranslator() *cuaTranslator {
	return &cuaTranslator{snapCache: make(map[int]*cachedSnap)}
}

// translateRequest rewrites an incoming cua-driver-shape request to
// the alma wire shape. Returns the new line bytes (ready to write
// straight to the daemon, including trailing newline) plus the
// original tool name so the caller can route the response back
// through the right translator.
//
// raw is the entire request line as it arrived from the caller.
// Translation is best-effort: if we can't parse the legacy shape,
// we hand the bytes back untouched and let the daemon surface a
// schema error. Better one error than two layers of confusion for
// the model.
func (t *cuaTranslator) translateRequest(raw []byte) ([]byte, string, error) {
	var head struct {
		Method string          `json:"method"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return raw, "", nil
	}
	// Non-"call" methods (list-tools, describe, shutdown, ping) take
	// a fast path below. Empty Method counts as "call" — matches the
	// legacy proxy behaviour.
	method := head.Method
	if method == "" {
		method = "call"
	}

	switch method {
	case "list-tools":
		// alma has no list-tools; we synthesize one so the LLM's
		// discovery flow stays unchanged. The list mirrors the cua-
		// driver surface the SKILL.md teaches.
		return nil, "list-tools", errSynthesizedListTools

	case "describe":
		// Cua-driver-style {method:"describe", name:"<tool>"} — there's
		// no alma equivalent. Fall through to a stub response handled
		// by the proxy, similar to list-tools.
		return nil, "describe", errSynthesizedDescribe

	case "shutdown":
		out := map[string]any{
			"id":  t.allocID(),
			"cmd": "shutdown",
		}
		return marshalLineWithNewline(out), "shutdown", nil

	case "call":
		// fall through to the main handler below.
	default:
		// Unknown method — pass the raw bytes through so the daemon
		// can return its own error.
		return raw, head.Name, nil
	}

	if head.Name == "" {
		return raw, "", nil
	}

	mappedName, ok := cuaToolNameMap[head.Name]
	if !ok {
		mappedName = head.Name
	}

	args := map[string]any{}
	if len(head.Args) > 0 {
		_ = json.Unmarshal(head.Args, &args)
	}

	// Image paths are the daemon's to write, so the model never chooses
	// where: a screenshot always lands in this user's temp folder.
	pinCuaImagePaths(args)

	// Per-tool arg rewriting. Anything we don't recognise passes the
	// args through verbatim — alma's schema is a superset of cua-
	// driver's for most tools.
	switch head.Name {
	case "click":
		t.rewriteClickArgs(args)
	case "type":
		t.rewriteRefArgs(args, "type")
	case "set_value":
		t.rewriteRefArgs(args, "set_value")
	case "scroll":
		// scroll has two shapes: ref-based (cua does {pid, window_id,
		// element_index, direction}) or pixel-based. Reshape if we
		// have an element_index.
		if _, has := args["element_index"]; has {
			t.rewriteRefArgs(args, "scroll")
		}
	case "press_key":
		t.rewriteRefArgs(args, "press_key")
	case "hotkey":
		// cua: {pid, keys:[...]}; alma's press_key takes a single key
		// + modifiers. Translate "cmd+l" style into alma's shape.
		t.rewriteHotkeyArgs(args)
	case "get_window_state":
		t.rewriteSnapArgs(args)
	case "screenshot":
		// alma's `shot` mirrors screenshot args (pid, window_id).
		// Nothing to rewrite.
	case "type_text":
		// Alma can address a concrete text field by ref. Legacy callers may
		// still send element_index from get_window_state, so translate it the
		// same way click/type/set_value do.
		t.rewriteRefArgs(args, "type_text")
	case "drag":
		// alma's drag accepts the same {pid, from_x, from_y, to_x,
		// to_y} shape. Nothing to rewrite.
	}

	out := map[string]any{
		"id":   t.allocID(),
		"cmd":  mappedName,
		"args": args,
	}
	return marshalLineWithNewline(out), head.Name, nil
}

// translateResponse rewrites an alma daemon response into the legacy
// cua-driver envelope. toolName is the *original* (pre-translation)
// tool name from translateRequest, so per-tool response reshaping
// (e.g. attaching element_index to snap elements) can target the
// right shape.
func (t *cuaTranslator) translateResponse(toolName string, raw []byte) []byte {
	var resp struct {
		ID    string          `json:"id"`
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return raw
	}

	if !resp.OK {
		msg := ""
		if resp.Error != nil {
			msg = resp.Error.Message
			if resp.Error.Hint != "" {
				msg = msg + " (" + resp.Error.Hint + ")"
			}
		}
		out := map[string]any{
			"ok":    false,
			"error": msg,
		}
		return marshalLineWithNewline(out)
	}

	// Per-tool data reshape on the success path. Most pass data
	// through to the legacy "result" key; snap/get_window_state
	// additionally injects element_index into each element so
	// existing element_index-based clickers keep working.
	var result any = json.RawMessage(resp.Data)
	switch toolName {
	case "get_window_state":
		result = t.reshapeSnapResponse(resp.Data)
	case "list_apps", "list_windows":
		// Alma already returns the same arrays; nothing to do.
	}

	out := map[string]any{
		"ok":     true,
		"result": result,
	}
	return marshalLineWithNewline(out)
}

// rewriteClickArgs maps a cua-driver click payload to alma's. Three
// addressing modes are supported:
//   - element_index (legacy AX): looked up against snapCache[pid],
//     becomes alma `ref:"aN"`.
//   - x/y + pid (legacy raw coord — was rejected by policy, now
//     allowed because daemon-level background activation is focus-safe):
//     passes through as alma's pixel click.
//   - ref:"v*"/"a*" (already alma-shaped): passes through.
func (t *cuaTranslator) rewriteClickArgs(args map[string]any) {
	if _, hasRef := args["ref"]; hasRef {
		// Caller already speaks alma-shape (visual ref or AX ref).
		// Strip stale legacy fields that alma doesn't understand.
		delete(args, "window_id")
		delete(args, "element_index")
		return
	}
	idx, hasIdx := lookupIntArg(args, "element_index")
	pid, hasPid := lookupIntArg(args, "pid")
	if hasIdx && hasPid {
		if ref := t.refForElementIndex(pid, idx); ref != "" {
			args["ref"] = ref
			delete(args, "window_id")
			delete(args, "element_index")
			return
		}
		// Cache miss — leave element_index in place so alma returns
		// a clear "no element known for ref" once we forward (we
		// can't fabricate a ref). Surface a more useful hint by
		// storing a poison ref the daemon will reject.
		args["ref"] = fmt.Sprintf("__yansu_unknown_%d_%d", pid, idx)
		delete(args, "window_id")
		delete(args, "element_index")
		return
	}
	// Pixel-mode: drop window_id (alma resolves the window itself
	// from the click point), keep pid + x + y.
	delete(args, "window_id")
}

// rewriteRefArgs handles the cua-shape→alma-shape translation for any
// tool that targets an AX node by element_index. label is purely for
// debugging.
func (t *cuaTranslator) rewriteRefArgs(args map[string]any, _ string) {
	if _, hasRef := args["ref"]; hasRef {
		delete(args, "window_id")
		delete(args, "element_index")
		return
	}
	idx, hasIdx := lookupIntArg(args, "element_index")
	pid, hasPid := lookupIntArg(args, "pid")
	if hasIdx && hasPid {
		if ref := t.refForElementIndex(pid, idx); ref != "" {
			args["ref"] = ref
		} else {
			args["ref"] = fmt.Sprintf("__yansu_unknown_%d_%d", pid, idx)
		}
		delete(args, "window_id")
		delete(args, "element_index")
	}
}

// rewriteHotkeyArgs maps cua's {pid, keys:[...]} hotkey into alma's
// press_key {pid, key, modifiers:[...]}. The convention from the
// legacy SKILL.md is that the last key in `keys` is the action key
// and any cmd/shift/alt/ctrl entries are modifiers.
func (t *cuaTranslator) rewriteHotkeyArgs(args map[string]any) {
	keys, ok := args["keys"].([]any)
	if !ok || len(keys) == 0 {
		return
	}
	mods := make([]string, 0, len(keys)-1)
	var actionKey string
	for _, k := range keys {
		s, ok := k.(string)
		if !ok {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(s))
		switch lower {
		case "cmd", "command", "shift", "alt", "option", "ctrl", "control", "fn":
			mods = append(mods, lower)
		default:
			actionKey = lower
		}
	}
	if actionKey != "" {
		args["key"] = actionKey
	}
	if len(mods) > 0 {
		// alma's press_key reads `modifiers` per Daemon.swift; double-
		// check via grep before relying on this shape.
		args["modifiers"] = stringSliceToAny(mods)
	}
	delete(args, "keys")
}

// rewriteSnapArgs maps cua's get_window_state args onto alma's
// get_app_state args. Both accept {pid|bundle, window_id, depth}.
// alma additionally honours `wakeup_seconds`; we set a sensible
// default so AX-wakes-up-late apps (Electron/CEF) settle before the
// LLM sees an empty tree.
func (t *cuaTranslator) rewriteSnapArgs(args map[string]any) {
	if _, has := args["wakeup_seconds"]; !has {
		args["wakeup_seconds"] = 1.5
	}
	if _, has := args["include_screenshot"]; !has {
		// Default to AX-first. Screenshots are opt-in because minimized
		// windows can be read through AX without unminimizing, while a
		// mandatory screenshot forces extra work and used to pop windows
		// out of the Dock.
		args["include_screenshot"] = false
	}
	if _, has := args["interactive_only"]; !has {
		// Default flipped from true → false on 2026-05 to match the
		// upstream cua-driver Daemon.swift change for get_app_state.
		// The previous true default was inherited from cua-driver's
		// automation-first design but actively SUPPRESSED text content
		// (every AXStaticText node — chat messages, email bodies, code
		// lines — was filtered out before serialization). For our
		// read-state use case (the dominant one through the
		// cua_driver agent tool: "what's in app X right now"),
		// returning a snapshot without text was misleading: ax_status
		// came back "populated" because window chrome existed, but
		// the agent saw no content and concluded the AX tree was
		// empty for the target app. Callers that genuinely want the
		// lean "find a clickable target" payload should pass
		// interactive_only=true explicitly. Note: this rewrite must
		// stay in sync with Daemon.swift's `case "get_app_state"`
		// default — the translator runs first, so if either side
		// defaults to true the read-state semantics break.
		args["interactive_only"] = false
	}
}

// reshapeSnapResponse turns alma's snap data into the legacy cua-
// driver shape AND populates the translator cache so subsequent
// element_index-addressed actions can resolve to a ref.
func (t *cuaTranslator) reshapeSnapResponse(data json.RawMessage) any {
	var snap struct {
		SnapshotID string `json:"snapshot_id"`
		AxStatus   string `json:"ax_status"`
		App        struct {
			Pid    int    `json:"pid"`
			Bundle string `json:"bundle"`
			Name   string `json:"name"`
		} `json:"app"`
		Window struct {
			ID    *int64 `json:"id"`
			Title string `json:"title"`
		} `json:"window"`
		Elements []map[string]any `json:"elements"`
		// alma's daemon emits this as `visual_refs` (see
		// cua-driver-src/src/Daemon.swift's out["visual_refs"]); the
		// legacy translator output and bundled skill both expose it
		// under the older name `visual_index`, so we re-key it on
		// the way out. Without matching daemon's `visual_refs` here
		// the field deserialized as nil and the OCR fallback was
		// invisible to the agent.
		VisualIndex []map[string]any `json:"visual_refs"`
		Screenshot  map[string]any   `json:"screenshot"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		// Fall through with the raw blob if alma's shape ever drifts.
		return json.RawMessage(data)
	}

	// Inject element_index per array position for legacy callers.
	for i := range snap.Elements {
		snap.Elements[i]["element_index"] = i
	}

	if snap.App.Pid > 0 {
		entry := &cachedSnap{
			elements: append([]map[string]any(nil), snap.Elements...),
		}
		if snap.Window.ID != nil {
			entry.windowID = *snap.Window.ID
		}
		t.recordSnap(snap.App.Pid, entry)
	}

	out := map[string]any{
		"snapshot_id":  snap.SnapshotID,
		"ax_status":    snap.AxStatus,
		"app":          snap.App,
		"window":       snap.Window,
		"ui_elements":  snap.Elements,
		"visual_index": snap.VisualIndex,
	}
	if snap.Screenshot != nil {
		out["screenshot"] = snap.Screenshot
	}
	return out
}

func (t *cuaTranslator) recordSnap(pid int, entry *cachedSnap) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, existing := t.snapCache[pid]; !existing {
		t.snapOrder = append(t.snapOrder, pid)
	}
	t.snapCache[pid] = entry
	// Evict oldest if over cap. Pre-existing pids are already in the
	// order list, so the only growth path here is unique pids.
	for len(t.snapCache) > translatorMaxCachedPids && len(t.snapOrder) > 0 {
		oldest := t.snapOrder[0]
		t.snapOrder = t.snapOrder[1:]
		delete(t.snapCache, oldest)
	}
}

func (t *cuaTranslator) refForElementIndex(pid, idx int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.snapCache[pid]
	if !ok {
		return ""
	}
	if idx < 0 || idx >= len(entry.elements) {
		return ""
	}
	ref, _ := entry.elements[idx]["ref"].(string)
	return ref
}

func (t *cuaTranslator) allocID() string {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()
	return fmt.Sprintf("y%d", id)
}

// synthesizedToolList is what we return when the caller invokes
// {method:"list-tools"}. The names + descriptions mirror the cua-
// driver surface the SKILL.md hub teaches; behaviour is preserved
// by the translator above.
var synthesizedToolList = []map[string]any{
	{"name": "launch_app", "description": "Launch a macOS app without bringing it to the foreground."},
	{"name": "list_apps", "description": "List running apps with pid, bundle id, and active state."},
	{"name": "list_windows", "description": "List windows belonging to one app (by bundle or pid)."},
	{"name": "get_window_state", "description": "Snapshot an app's AX tree + screenshot + visual OCR refs. Returns ui_elements with element_index. Pass include_screenshot=false only for AX-only reads."},
	{"name": "click", "description": "Click an AX element by element_index, a visual OCR ref (v*), or raw (x, y) coordinates inside a target pid using background activation (no focus steal)."},
	{"name": "type", "description": "Type into an AX element addressed by element_index. Pass replace=true to overwrite the current value."},
	{"name": "type_text", "description": "Type plain text into an AX element by element_index/ref, or into the currently-focused element inside the target pid."},
	{"name": "press_key", "description": "Send a keystroke (with optional modifiers) to the target pid."},
	{"name": "hotkey", "description": "Send a key combination (e.g. cmd+s). Focus-stealing combos like cmd+L, cmd+Tab, cmd+1..9 are rejected by Isoform's policy."},
	{"name": "drag", "description": "Drag from one screen point to another inside the target pid using background event delivery (no focus steal)."},
	{"name": "scroll", "description": "Scroll inside an AX element by element_index, or at a screen point."},
	{"name": "set_value", "description": "Set an AX element's value directly (e.g. type into a text field without a separate focus step)."},
	{"name": "screenshot", "description": "Capture the target window to a local image path without raising it; falls back to backing-store capture when ScreenCaptureKit cannot capture."},
	{"name": "check_permissions", "description": "Report whether Accessibility and Screen Recording grants are currently in place."},
	{"name": "open_url", "description": "Open a URL via LaunchServices without activating the handling app."},
}

var errSynthesizedListTools = &translatorSynthetic{tool: "list-tools"}
var errSynthesizedDescribe = &translatorSynthetic{tool: "describe"}

// translatorSynthetic is a sentinel error type the proxy recognises
// and turns into a direct response without dialling the daemon.
type translatorSynthetic struct {
	tool string
}

func (e *translatorSynthetic) Error() string {
	return "translator synthetic response: " + e.tool
}

// synthesizedResponse returns the wire bytes the proxy should send
// back to the caller for a list-tools / describe request without
// involving the daemon. Returns nil for unrecognized tool names.
func (t *cuaTranslator) synthesizedResponse(tool string) []byte {
	switch tool {
	case "list-tools":
		out := map[string]any{
			"ok":     true,
			"result": map[string]any{"tools": synthesizedToolList},
		}
		return marshalLineWithNewline(out)
	case "describe":
		// We don't carry full JSONSchema for every tool here. The
		// SKILL.md hub is the canonical reference; pointing the
		// caller back at it keeps one source of truth.
		out := map[string]any{
			"ok": true,
			"result": map[string]any{
				"message": "Per-tool schemas are documented in the bundled SKILL.md. Call list-tools for the tool name list.",
			},
		}
		return marshalLineWithNewline(out)
	}
	return nil
}

// lookupIntArg pulls a numeric arg out of a request body, accepting
// either a JSON number or a string ("123") so callers that
// stringify ints still resolve.
func lookupIntArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i), true
		}
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func stringSliceToAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func marshalLineWithNewline(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Falls back to a structured envelope so we always emit valid
		// JSON. If marshalling a fixed-shape map fails we have a
		// deeper Go-level bug — surface it via a sane error.
		fallback, _ := json.Marshal(map[string]any{
			"ok":    false,
			"error": "yansu cua-translator: internal marshal error: " + err.Error(),
		})
		return append(fallback, '\n')
	}
	return append(b, '\n')
}

// cuaToolNameMap is the legacy → alma rename table. Tools missing
// from the map pass through under their original name (alma adds new
// surfaces — intent, get_app_state, perform_secondary_action — that
// the legacy SKILL.md doesn't reference but advanced callers may
// reach directly).
var cuaToolNameMap = map[string]string{
	"get_window_state":  "get_app_state",
	"check_permissions": "permissions",
	"screenshot":        "shot",
	"list_windows":      "windows",
	"hotkey":            "press_key",
}

// cuaShotDirName is the folder under this user's temp folder that every
// screenshot the daemon writes on a model's request goes to.
const cuaShotDirName = "isoform-cua-shots"

var cuaShotNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// pinCuaImagePaths rewrites the image paths a call names ("out" for shot,
// "screenshot_path" for get_window_state) into this user's temp folder,
// keeping only a clean file name, so a run can never overwrite a file of
// its choosing. A path that cannot be pinned is dropped and the daemon's
// own default applies, or the call fails for want of one.
func pinCuaImagePaths(args map[string]any) {
	// A snapshot screenshot without a path would land at the daemon's
	// default under /tmp; name one in the pinned folder instead.
	if want, _ := args["include_screenshot"].(bool); want {
		if _, has := args["screenshot_path"]; !has {
			args["screenshot_path"] = fmt.Sprintf("alma-cu-state-%d.jpg", time.Now().UnixNano())
		}
	}
	for _, key := range []string{"out", "screenshot_path", "token_path"} {
		value, present := args[key]
		if !present {
			continue
		}
		// A path of another type is pinned too: the daemon may read it
		// some other way.
		raw, _ := value.(string)
		dir := filepath.Join(os.TempDir(), cuaShotDirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			delete(args, key)
			continue
		}
		name := strings.Trim(cuaShotNameUnsafe.ReplaceAllString(filepath.Base(strings.TrimSpace(raw)), "-"), ".-")
		switch {
		case key == "token_path":
			if name == "" {
				name = fmt.Sprintf("token-%d", time.Now().UnixNano())
			}
			args[key] = filepath.Join(dir, name)
			continue
		case strings.HasSuffix(strings.ToLower(name), ".png"), strings.HasSuffix(strings.ToLower(name), ".jpg"), strings.HasSuffix(strings.ToLower(name), ".jpeg"):
		default:
			name += ".png"
		}
		if name == ".png" || strings.HasPrefix(name, ".") {
			name = fmt.Sprintf("shot-%d.png", time.Now().UnixNano())
		}
		args[key] = filepath.Join(dir, name)
	}
}
