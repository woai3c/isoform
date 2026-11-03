package native

import "testing"

// TestHashToolCall_Deterministic: same (toolName, input) produces same
// hash regardless of JSON object key order. The whole point of the
// stable encoder.
func TestHashToolCall_Deterministic(t *testing.T) {
	a := hashToolCall("Bash", `{"a":1,"b":2,"c":3}`)
	b := hashToolCall("Bash", `{"c":3,"a":1,"b":2}`)
	if a != b {
		t.Errorf("hash differs across key orders: %s vs %s", a, b)
	}
	c := hashToolCall("Bash", `{"a":1,"b":2}`)
	if a == c {
		t.Errorf("hash same for different inputs (b/c): %s == %s", a, c)
	}
}

// TestHashToolCall_DistinguishesTools: same input, different tool name,
// different hash. Catches the trivial mistake of seeding the hash with
// only the input.
func TestHashToolCall_DistinguishesTools(t *testing.T) {
	a := hashToolCall("Read", `{"path":"/tmp/x"}`)
	b := hashToolCall("Bash", `{"path":"/tmp/x"}`)
	if a == b {
		t.Errorf("hash collides across tool names")
	}
}

// TestCheckForLoop_Thresholds drives the counter from 1 → 5 and asserts
// the verdict at each boundary.
func TestCheckForLoop_Thresholds(t *testing.T) {
	w := &LoopGuardWindow{}
	for i := 1; i <= 5; i++ {
		got := CheckForLoop(w, "Bash", `{"command":"true"}`)
		var want string
		switch {
		case i >= HardLoopThreshold:
			want = "hard"
		case i >= SoftLoopThreshold:
			want = "soft"
		default:
			want = "ok"
		}
		if got.Kind != want {
			t.Errorf("call #%d: kind=%s, want %s (count=%d)", i, got.Kind, want, got.Count)
		}
		RecordToolCall(w, "Bash", `{"command":"true"}`, got.Hash)
	}
}

// TestCheckForLoop_WindowExpiry: identical calls separated by enough
// distinct calls fall out of the rolling window and no longer count.
func TestCheckForLoop_WindowExpiry(t *testing.T) {
	w := &LoopGuardWindow{}
	input := `{"url":"target"}`

	// Record 2 identical WebFetch calls (below soft threshold).
	for i := 0; i < 2; i++ {
		got := CheckForLoop(w, "WebFetch", input)
		RecordToolCall(w, "WebFetch", input, got.Hash)
	}

	// Fill the window with distinct calls to push the old ones out.
	for i := 0; i < LoopWindowSize; i++ {
		distinct := `{"command":"cmd-` + itoa(i) + `"}`
		got := CheckForLoop(w, "Bash", distinct)
		RecordToolCall(w, "Bash", distinct, got.Hash)
	}

	// Now the same WebFetch call should be "ok" — old entries expired.
	got := CheckForLoop(w, "WebFetch", input)
	if got.Kind != "ok" {
		t.Errorf("after window expiry: kind=%s count=%d, want ok/1", got.Kind, got.Count)
	}
	if got.Count != 1 {
		t.Errorf("after window expiry: count=%d, want 1", got.Count)
	}
}

// TestCheckForLoop_AlternatingInputsInWindow: A,B,A,B,A — each A is
// distinct from B, but the A count within the window climbs to 3 on the
// 5th call. That call must trip the soft threshold.
func TestCheckForLoop_AlternatingInputsInWindow(t *testing.T) {
	w := &LoopGuardWindow{}
	pattern := []string{
		`{"url":"a"}`,
		`{"url":"b"}`,
		`{"url":"a"}`,
		`{"url":"b"}`,
		`{"url":"a"}`,
	}
	var lastA LoopGuardResult
	for i, in := range pattern {
		got := CheckForLoop(w, "WebFetch", in)
		RecordToolCall(w, "WebFetch", in, got.Hash)
		if in == `{"url":"a"}` {
			lastA = got
			t.Logf("call #%d (A): kind=%s count=%d", i+1, got.Kind, got.Count)
		}
	}
	if lastA.Kind != "soft" {
		t.Errorf("3rd A call should soft-trip; got kind=%s count=%d", lastA.Kind, lastA.Count)
	}
}

// TestCheckForLoop_ExemptToolBypassesGuard: BashOutput is the polling
// tool and is exempt — even 100 identical calls must not trip the guard.
func TestCheckForLoop_ExemptToolBypassesGuard(t *testing.T) {
	w := &LoopGuardWindow{}
	for i := 0; i < 20; i++ {
		got := CheckForLoop(w, "BashOutput", `{"shell_id":"sh-1"}`)
		if got.Kind != "ok" {
			t.Errorf("BashOutput call #%d tripped guard: kind=%s", i+1, got.Kind)
		}
		RecordToolCall(w, "BashOutput", `{"shell_id":"sh-1"}`, got.Hash)
	}
}

// TestSyntheticLoopBlockResult_Shape: the synthetic result must carry
// the original tool_use_id so Anthropic accepts it as a paired result,
// and IsError=true so the model treats it as a failure signal.
func TestSyntheticLoopBlockResult_Shape(t *testing.T) {
	blk := SyntheticLoopBlockResult("toolu_xyz", "WebFetch", 4)
	if blk.Type != "tool_result" {
		t.Errorf("Type = %s, want tool_result", blk.Type)
	}
	if blk.ToolUseID != "toolu_xyz" {
		t.Errorf("ToolUseID = %s, want toolu_xyz", blk.ToolUseID)
	}
	if !blk.IsError {
		t.Errorf("IsError must be true so the model reads it as a failure")
	}
	body, ok := blk.Content.(string)
	if !ok {
		t.Fatalf("Content type = %T, want string", blk.Content)
	}
	for _, want := range []string{"[loop-guard]", "WebFetch", "4"} {
		if !contains(body, want) {
			t.Errorf("synthetic body missing %q: %s", want, body)
		}
	}
}

// TestStableJSONString_NestedObjects: nested object key sorting works
// recursively — without recursion, {"x":{"a":1,"b":2}} and
// {"x":{"b":2,"a":1}} would hash differently even with the top-level
// sort. Catches an easy implementation regression.
func TestStableJSONString_NestedObjects(t *testing.T) {
	a := stableJSONString(map[string]any{"x": map[string]any{"a": 1, "b": 2}, "y": 3})
	b := stableJSONString(map[string]any{"y": 3, "x": map[string]any{"b": 2, "a": 1}})
	if a != b {
		t.Errorf("nested keys not normalised: %s vs %s", a, b)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestCheckForLoop_CuaDriverReadOnlyExempt: cua_driver wraps many
// upstream tools. Read-only ones (get_app_state, list_apps, ...) are
// designed for identical-input polling between actions — they must not
// trip the loop guard no matter how many times the agent calls them.
func TestCheckForLoop_CuaDriverReadOnlyExempt(t *testing.T) {
	w := &LoopGuardWindow{}
	input := `{"name":"get_app_state","args":{"bundle":"com.tinyspeck.slackmacgap"}}`
	for i := range 20 {
		got := CheckForLoop(w, "cua_driver", input)
		if got.Kind != "ok" {
			t.Errorf("call #%d tripped guard for read-only cua_driver: kind=%s", i+1, got.Kind)
		}
		RecordToolCall(w, "cua_driver", input, got.Hash)
	}
	if len(w.Entries) != 0 {
		t.Errorf("read-only cua_driver calls should not be recorded; entries=%d", len(w.Entries))
	}
}

// TestCheckForLoop_CuaDriverWriteElevatedThresholds: write actions
// (click/press_key/type_text) tolerate one or two genuine settle
// retries that the package default (3/5) would short-circuit. They
// should soft-trip at 5, hard at 8 instead.
func TestCheckForLoop_CuaDriverWriteElevatedThresholds(t *testing.T) {
	w := &LoopGuardWindow{}
	input := `{"name":"press_key","args":{"bundle":"com.tinyspeck.slackmacgap","key":"cmd+k"}}`

	cases := []struct {
		callNo int
		want   string
	}{
		{1, "ok"}, {2, "ok"}, {3, "ok"}, {4, "ok"},
		{5, "soft"}, {6, "soft"}, {7, "soft"},
		{8, "hard"},
	}
	for _, c := range cases {
		got := CheckForLoop(w, "cua_driver", input)
		if got.Kind != c.want {
			t.Errorf("call #%d: kind=%s, want %s (count=%d)", c.callNo, got.Kind, c.want, got.Count)
		}
		RecordToolCall(w, "cua_driver", input, got.Hash)
	}
}

// TestCheckForLoop_CuaDriverUnknownSubToolUsesDefaults: an unrecognised
// cua_driver sub-tool (neither read-only nor write-listed) falls back
// to the package default 3/5. Catches accidental over-broadening of
// the exemption.
func TestCheckForLoop_CuaDriverUnknownSubToolUsesDefaults(t *testing.T) {
	w := &LoopGuardWindow{}
	input := `{"name":"made_up_subtool","args":{}}`

	for i := 1; i <= 5; i++ {
		got := CheckForLoop(w, "cua_driver", input)
		var want string
		switch {
		case i >= HardLoopThreshold:
			want = "hard"
		case i >= SoftLoopThreshold:
			want = "soft"
		default:
			want = "ok"
		}
		if got.Kind != want {
			t.Errorf("unknown sub-tool call #%d: kind=%s, want %s", i, got.Kind, want)
		}
		RecordToolCall(w, "cua_driver", input, got.Hash)
	}
}

// TestCheckForLoop_CuaDriverWriteCountedPerHash: counts are scoped by
// (toolName, hash). Same outer tool name + same upstream sub-tool name
// but DIFFERENT args produce distinct hashes — so soft-tripping one
// must NOT pollute the other's counter. Drive cmdK to its soft
// threshold (5), then verify cmdF is still on its first call.
func TestCheckForLoop_CuaDriverWriteCountedPerHash(t *testing.T) {
	w := &LoopGuardWindow{}
	cmdK := `{"name":"press_key","args":{"key":"cmd+k","bundle":"X"}}`
	cmdF := `{"name":"press_key","args":{"key":"cmd+f","bundle":"X"}}`

	// Fire cmdK 5 times. First 4 stay "ok", 5th trips soft (elevated
	// threshold for cua_driver writes is 5).
	for i := 1; i <= 5; i++ {
		got := CheckForLoop(w, "cua_driver", cmdK)
		want := "ok"
		if i >= 5 {
			want = "soft"
		}
		if got.Kind != want {
			t.Errorf("cmdK call #%d: kind=%s count=%d, want %s", i, got.Kind, got.Count, want)
		}
		RecordToolCall(w, "cua_driver", cmdK, got.Hash)
	}

	// cmdF is a fresh hash. Even though cmdK is soft-tripped, cmdF's
	// counter must be untouched — its first call returns ok with
	// count=1. This is the property the test is asserting.
	got := CheckForLoop(w, "cua_driver", cmdF)
	if got.Kind != "ok" || got.Count != 1 {
		t.Errorf("cmdF first call after cmdK soft-tripped: kind=%s count=%d, want ok/1",
			got.Kind, got.Count)
	}
}

// TestCheckForLoop_CuaDriverHotkeyAliasTreatedAsWrite: the LLM-facing
// `hotkey` name is an alias for `press_key` in cua_translator.go.
// loop_guard runs BEFORE the translator rewrites names, so it must
// apply the elevated write threshold to `hotkey` the same way it does
// for `press_key`. Without this, calling cua_driver({name:"hotkey",...})
// would prematurely trip the default 3/5 budget on the 3rd retry.
func TestCheckForLoop_CuaDriverHotkeyAliasTreatedAsWrite(t *testing.T) {
	w := &LoopGuardWindow{}
	input := `{"name":"hotkey","args":{"bundle":"com.tinyspeck.slackmacgap","keys":["cmd","k"]}}`

	// Default thresholds would trip at 3/5. Elevated write thresholds
	// trip at 5/8. Assert the elevated boundary on call #4 staying ok
	// — that's the discriminator between the two threshold sets.
	for i := 1; i <= 4; i++ {
		got := CheckForLoop(w, "cua_driver", input)
		if got.Kind != "ok" {
			t.Errorf("hotkey call #%d: kind=%s, want ok (write threshold not applied?)", i, got.Kind)
		}
		RecordToolCall(w, "cua_driver", input, got.Hash)
	}
	// 5th call must soft-trip under the elevated 5/8 thresholds.
	got := CheckForLoop(w, "cua_driver", input)
	if got.Kind != "soft" {
		t.Errorf("hotkey 5th call: kind=%s, want soft", got.Kind)
	}
}

// TestCheckForLoop_NonCuaDriverUnaffected: tools that aren't wrapper
// tools (no sub-tool predicate registered) behave exactly as before —
// 3/5 thresholds, no input parsing. Catches an accidental broadening
// of the new code path to all tools.
func TestCheckForLoop_NonCuaDriverUnaffected(t *testing.T) {
	w := &LoopGuardWindow{}
	// Use cua_driver-shaped input but a non-cua tool name. The
	// sub-tool predicate must not run.
	input := `{"name":"get_app_state","args":{}}`
	for i := 1; i <= 5; i++ {
		got := CheckForLoop(w, "Read", input)
		var want string
		switch {
		case i >= HardLoopThreshold:
			want = "hard"
		case i >= SoftLoopThreshold:
			want = "soft"
		default:
			want = "ok"
		}
		if got.Kind != want {
			t.Errorf("Read call #%d: kind=%s, want %s", i, got.Kind, want)
		}
		RecordToolCall(w, "Read", input, got.Hash)
	}
}

// TestRecordToolCall_WindowCap: the rolling window is capped at
// 2*LoopWindowSize to bound memory.
func TestRecordToolCall_WindowCap(t *testing.T) {
	w := &LoopGuardWindow{}
	cap := LoopWindowSize * 2
	for i := 0; i < cap+10; i++ {
		input := `{"i":` + itoa(i) + `}`
		got := CheckForLoop(w, "Bash", input)
		RecordToolCall(w, "Bash", input, got.Hash)
	}
	if len(w.Entries) > cap {
		t.Errorf("window size %d exceeds cap %d", len(w.Entries), cap)
	}
}
