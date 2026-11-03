package native

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// Loop-guard thresholds. The soft threshold injects a synthetic
// tool_result without executing the tool — the model sees a "[loop-guard]"
// notice as if the tool returned it and (usually) pivots on the next
// turn. The hard threshold abandons softness and asks the model to
// stop calling the tool entirely via a strong text nudge.
//
// 3 / 5 mirrors the values x-code-cli landed on after empirical tuning:
// 3 is enough to catch the [A,B,A] alternation pattern that broke a
// "consecutive only" guard, while still tolerating two genuine retries
// of a transient failure (rate limit, dead link). 5 is the hard cap.
//
// Detection uses a rolling window (not cumulative counts) so that
// identical calls far apart — separated by many distinct calls — do not
// accumulate toward the threshold. This prevents false positives in
// long-running tasks where the same tool+args may legitimately recur
// after substantial intervening work.
const (
	// SoftLoopThreshold: at this many matching calls within the rolling
	// window, the next call is short-circuited with a synthetic result.
	// The tool is NOT executed.
	SoftLoopThreshold = 3
	// HardLoopThreshold: at this many matching calls within the rolling
	// window, the loop appends a stop-nudge text block telling the model
	// to stop calling that tool and write a final answer.
	HardLoopThreshold = 5
	// LoopWindowSize is the number of recent tool calls scanned for
	// duplicates. Only the last LoopWindowSize entries are considered
	// when counting matches. 8 matches x-code-cli's empirical tuning:
	// large enough to catch [A,B,A,B,A] alternation within the default
	// 3/5 thresholds, small enough that genuinely distant retries
	// (separated by many unrelated calls) fall out of the window.
	LoopWindowSize = 8
)

// LoopGuardEntry is a single recorded tool call in the rolling window.
type LoopGuardEntry struct {
	ToolName string
	Hash     string
}

// LoopGuardWindow holds the rolling window of recent tool calls used by
// the loop guard. Create with &LoopGuardWindow{} — zero value is ready
// to use.
type LoopGuardWindow struct {
	Entries []LoopGuardEntry
}

// loopGuardExemptTools lists tools whose contract is "call repeatedly
// with identical arguments to drain new state". For these tools
// byte-identical retries are the intended usage pattern, not a stuck
// pattern — counting them toward the loop guard would cause the soft
// short-circuit to silence healthy long-running tasks (long builds,
// test runs, server-startup waits) that legitimately need 5–30s-interval
// polling.
//
// Membership rule: only add a tool here if its design explicitly
// returns "since-last-call" deltas given an identical key argument.
// BashOutput qualifies (returns incremental stdout+stderr per shell_id);
// Read/Grep/Glob do NOT — they return full state, so 3 identical calls
// in a row really is a stuck pattern.
var loopGuardExemptTools = map[string]bool{
	"BashOutput": true,
}

// loopGuardSubToolExempt holds predicates for "wrapper" tools that
// proxy many upstream tools under a single outer name. The predicate
// inspects the parsed input and returns true when this specific call
// should bypass loop-guard counting.
//
// The canonical case is cua_driver: it routes 25+ upstream CUA tools
// through a single outer tool name, with `name` in the input selecting
// the upstream tool. Read-only ones (get_app_state, screenshot, etc.)
// are designed for identical-input polling — the agent's "act → observe
// → act → observe" feedback loop calls them with the same {bundle:..}
// after every action. Counting these towards the loop guard short-
// circuits the very feedback loop CUA needs.
//
// Write ones (click, press_key, ...) are NOT exempted here — they
// route through loopGuardSubToolThresholds for an elevated (but still
// finite) retry budget instead.
var loopGuardSubToolExempt = map[string]func(input any) bool{
	"cua_driver": isCuaDriverReadOnly,
}

// loopGuardSubToolThresholds lets a wrapper tool report custom
// (soft, hard) thresholds based on its input. Returns ok=false when
// defaults (SoftLoopThreshold/HardLoopThreshold) should apply.
//
// cua_driver writes (click, press_key, type_text, ...) get a slightly
// looser budget because real GUI legitimately needs a 2nd or 3rd
// attempt when the target window's renderer is still settling — e.g.
// Slack/Electron cmd+K right after the window comes to the foreground
// often needs one wake-up cycle. Default 3/5 was tuned for tool calls
// that hit a deterministic API; GUI dispatch has more genuine variance.
var loopGuardSubToolThresholds = map[string]func(input any) (soft, hard int, ok bool){
	"cua_driver": cuaDriverWriteThresholds,
}

// cuaDriverReadOnlySubTools mirrors the read-only branch of
// cua-driver's Daemon.dispatch in wails-gui/cua-driver-src/src/Daemon.swift.
// Keep in sync when the daemon adds or renames read-only commands.
var cuaDriverReadOnlySubTools = map[string]bool{
	"ping":              true,
	"permissions":       true,
	"check_permissions": true,
	"apps":              true,
	"list_apps":         true,
	"windows":           true,
	"list_windows":      true,
	"snap":              true,
	"get_app_state":     true,
	"get_window_state":  true,
	"shot":              true,
	"screenshot":        true,
	"pip_status":        true,
	"mcp_status":        true,
	"stealth_stats":     true,
	"list_tools":        true,
}

// cuaDriverWriteSubTools mirrors cua-driver's mutating actions.
// These get elevated thresholds via cuaDriverWriteThresholds.
//
// Include both daemon-side canonical names and LLM-facing aliases —
// loop_guard runs at the agent layer BEFORE wails-gui/cua_translator.go
// rewrites the legacy names. cuaToolNameMap in cua_translator.go is
// the source of truth for aliases; keep this list in sync. Today
// `hotkey` is the only write alias (maps to `press_key`); the read-side
// aliases (get_window_state, check_permissions, screenshot, list_windows)
// are already covered in cuaDriverReadOnlySubTools.
var cuaDriverWriteSubTools = map[string]bool{
	"click":                    true,
	"type":                     true,
	"type_text":                true,
	"press":                    true,
	"press_key":                true,
	"hotkey":                   true, // alias → press_key (cua_translator.go:598)
	"perform_secondary_action": true,
	"drag":                     true,
	"set_value":                true,
	"scroll":                   true,
	"raise":                    true,
}

// isCuaDriverReadOnly extracts the upstream tool `name` from cua_driver
// input and reports whether it's a read-only call.
func isCuaDriverReadOnly(input any) bool {
	m, ok := input.(map[string]any)
	if !ok {
		return false
	}
	name, _ := m["name"].(string)
	return cuaDriverReadOnlySubTools[name]
}

// cuaDriverWriteThresholds returns elevated thresholds for cua_driver
// write actions. Default 3/5 is too tight for GUI dispatch where one
// genuine wake-up retry is normal; 5/8 keeps the guard armed against
// runaway loops while tolerating real settle latency.
func cuaDriverWriteThresholds(input any) (int, int, bool) {
	m, ok := input.(map[string]any)
	if !ok {
		return 0, 0, false
	}
	name, _ := m["name"].(string)
	if cuaDriverWriteSubTools[name] {
		return 5, 8, true
	}
	return 0, 0, false
}

// parseRawInputForGuard decodes the raw JSON input the loop guard
// receives. Returns nil for empty / unparseable input — callers must
// handle a nil value, which by design means "no input to inspect".
func parseRawInputForGuard(rawInput string) any {
	if strings.TrimSpace(rawInput) == "" {
		return nil
	}
	pr, err := Parse(rawInput, ParseOptions{})
	if err != nil {
		return nil
	}
	return pr.Value
}

// stableJSONString produces a deterministic JSON encoding of any value
// where object keys are sorted lexicographically. Same value tree → same
// bytes regardless of the model's whim about key order. Anthropic
// streams partial_json deltas in whatever order the model emitted, so
// {"a":1,"b":2} on turn 7 and {"b":2,"a":1} on turn 9 are semantically
// identical retries but differ byte-for-byte; without the stable
// encoding the guard misses them.
func stableJSONString(v any) string {
	var b strings.Builder
	stableEncode(&b, v)
	return b.String()
}

func stableEncode(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			stableEncode(b, t[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			stableEncode(b, e)
		}
		b.WriteByte(']')
	default:
		// Numbers, booleans, strings — fall back to encoding/json which
		// handles all the JSON-native scalar types correctly.
		raw, err := json.Marshal(t)
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(raw)
	}
}

// hashToolCall returns a 16-hex-char fingerprint of a (toolName, input)
// pair. Truncated to 16 chars because we compare against a small
// per-run map: collision probability at that length is vanishingly
// small for the volumes we see in a single conversation.
//
// `input` is the raw partial_json bytes accumulated from the stream;
// the function decodes once via tolerant JSON, normalises to stable
// key order, and hashes.
func hashToolCall(toolName, rawInput string) string {
	var v any
	if strings.TrimSpace(rawInput) != "" {
		if pr, err := Parse(rawInput, ParseOptions{}); err == nil {
			v = pr.Value
		}
	}
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte{0})
	h.Write([]byte(stableJSONString(v)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// LoopGuardResult is the verdict from a single CheckForLoop call.
type LoopGuardResult struct {
	// Kind is one of "ok", "soft", "hard".
	Kind string
	// Hash is the precomputed (toolName, input) fingerprint. Returned so
	// the caller can pass it back to RecordToolCall without re-hashing.
	Hash string
	// Count is the number of matching calls within the rolling window
	// INCLUDING the current incoming call. Used in the synthetic message.
	Count int
	// HardThreshold is the hard cap that applied to this call. Wrapper
	// tools can override it per sub-tool, so callers must not assume the
	// package-level HardLoopThreshold value applies universally.
	HardThreshold int
}

// CheckForLoop reports whether the incoming tool call repeats prior
// calls in the rolling window enough to trigger the soft or hard guard.
// Uses a sliding window of the last LoopWindowSize calls (not cumulative
// counts) so that identical calls separated by many distinct calls do
// not accumulate — only recent clustering triggers the guard.
//
// Does NOT mutate the window — call RecordToolCall once you've decided
// to proceed with (or skip) execution.
//
// Tools listed in loopGuardExemptTools always return "ok": their
// contract is built on identical-input polling. Wrapper tools listed in
// loopGuardSubToolExempt get the same treatment for the specific
// upstream calls their predicate flags as read-only. Wrapper tools in
// loopGuardSubToolThresholds use elevated (soft, hard) values for the
// upstream calls their predicate covers, falling back to the package
// defaults otherwise.
func CheckForLoop(w *LoopGuardWindow, toolName, rawInput string) LoopGuardResult {
	hash := hashToolCall(toolName, rawInput)
	if loopGuardExemptTools[toolName] {
		return LoopGuardResult{Kind: "ok", Hash: hash}
	}

	// Sub-tool routing for wrapper tools. We parse the input at most
	// once and reuse it for both exemption and threshold lookup.
	var (
		parsed       any
		parsedLoaded bool
	)
	loadParsed := func() any {
		if !parsedLoaded {
			parsed = parseRawInputForGuard(rawInput)
			parsedLoaded = true
		}
		return parsed
	}

	if pred, ok := loopGuardSubToolExempt[toolName]; ok {
		if pred(loadParsed()) {
			return LoopGuardResult{Kind: "ok", Hash: hash}
		}
	}

	soft, hard := SoftLoopThreshold, HardLoopThreshold
	if thr, ok := loopGuardSubToolThresholds[toolName]; ok {
		if s, h, override := thr(loadParsed()); override {
			soft, hard = s, h
		}
	}

	// Scan the rolling window for prior matches.
	entries := w.Entries
	if len(entries) > LoopWindowSize {
		entries = entries[len(entries)-LoopWindowSize:]
	}
	priorMatches := 0
	for _, e := range entries {
		if e.ToolName == toolName && e.Hash == hash {
			priorMatches++
		}
	}
	// +1 represents the current incoming call.
	total := priorMatches + 1

	switch {
	case total >= hard:
		return LoopGuardResult{Kind: "hard", Hash: hash, Count: total, HardThreshold: hard}
	case total >= soft:
		return LoopGuardResult{Kind: "soft", Hash: hash, Count: total, HardThreshold: hard}
	default:
		return LoopGuardResult{Kind: "ok", Hash: hash, Count: total, HardThreshold: hard}
	}
}

// RecordToolCall appends the tool call to the rolling window. Pass the
// hash returned by CheckForLoop to skip recomputing it; pass an empty
// hash to have the function recompute from rawInput.
//
// Sub-tool-exempt calls (e.g. cua_driver read-only polling) are not
// recorded — they would fill the window with noise and suppress
// detection of genuine write-side loops.
func RecordToolCall(w *LoopGuardWindow, toolName, rawInput, hash string) {
	if loopGuardExemptTools[toolName] {
		return
	}
	if pred, ok := loopGuardSubToolExempt[toolName]; ok {
		if pred(parseRawInputForGuard(rawInput)) {
			return
		}
	}
	if hash == "" {
		hash = hashToolCall(toolName, rawInput)
	}
	w.Entries = append(w.Entries, LoopGuardEntry{ToolName: toolName, Hash: hash})
	// Cap at 2x the window size to bound memory while giving
	// CheckForLoop some history beyond the active comparison window.
	cap := LoopWindowSize * 2
	if len(w.Entries) > cap {
		w.Entries = w.Entries[len(w.Entries)-cap:]
	}
}

// SyntheticLoopBlockResult builds a tool_result content block carrying
// the loop-guard message. The model reads this as if the tool returned
// it and (in practice) reliably pivots away from the failing call.
func SyntheticLoopBlockResult(toolUseID, toolName string, count int) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		IsError:   true,
		Content: "[loop-guard] This exact " + toolName + " call (same arguments) has now been attempted " +
			itoa(count) + " times in this task with the same outcome. " +
			"This invocation was short-circuited; the tool was NOT executed. " +
			"Stop retrying with these arguments — change your approach: alter the arguments meaningfully, " +
			"try a different tool, or write your final answer based on what you already have.",
	}
}

// HardLoopNudgeText builds the strong text-nudge appended to the user
// message when the hard threshold trips. Same shape as the previous
// sameInputNudgeText; centralised here so the soft and hard messages
// stay consistent.
func HardLoopNudgeText(toolName string, count int) string {
	base := "Notice: you have called " + toolName + " with the exact same input " + itoa(count) +
		" times in this task. Retrying with identical arguments will not change the result, even when interleaved with other calls. " +
		"Either call " + toolName + " with meaningfully different arguments, " +
		"or stop calling " + toolName + " and write your final answer based on what you already have."
	if toolName == "WebFetch" {
		return base + " If a fetched page's content did not match your query, treat that URL as dead and move on — do NOT refetch it."
	}
	return base
}

// itoa is strconv.Itoa inlined to avoid the strconv import for one
// callsite — keeps the package's import block tight.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
