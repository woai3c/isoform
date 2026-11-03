package native

import (
	"strings"
	"testing"
)

const testInventory = "Google Chrome (com.google.Chrome) pid=677\n  window: \"收件箱 - Gmail\""

func fixedInventory(s string) func() string { return func() string { return s } }

func cuaResult(body string) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolUseID: "tu1", Content: body}
}

func TestMaybeInjectInventory_PrependsOnFirstNonListAppsCall(t *testing.T) {
	state := &CuaInventoryState{}
	blk := MaybeInjectInventory(state, "cua_driver", `{"name":"get_window_state"}`, cuaResult("AX TREE"), fixedInventory(testInventory))

	body, ok := blk.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", blk.Content)
	}
	if !strings.Contains(body, "收件箱 - Gmail") {
		t.Errorf("inventory not prepended: %q", body)
	}
	if !strings.Contains(body, "AX TREE") {
		t.Errorf("original body lost: %q", body)
	}
	if !strings.HasPrefix(body, cuaInventoryPreludeHeader) {
		t.Errorf("inventory should come first, got: %q", body)
	}
	if !state.Injected {
		t.Error("state should be marked injected after a successful prepend")
	}
}

func TestMaybeInjectInventory_OnlyOnce(t *testing.T) {
	state := &CuaInventoryState{}
	_ = MaybeInjectInventory(state, "cua_driver", `{"name":"get_window_state"}`, cuaResult("first"), fixedInventory(testInventory))

	// Second call must not prepend again.
	blk := MaybeInjectInventory(state, "cua_driver", `{"name":"click"}`, cuaResult("second"), fixedInventory(testInventory))
	if blk.Content.(string) != "second" {
		t.Errorf("inventory injected twice: %q", blk.Content)
	}
}

func TestMaybeInjectInventory_FirstCallIsListApps(t *testing.T) {
	state := &CuaInventoryState{}
	// list_apps already carries the inventory — must not double-prepend, but
	// must mark injected so a later call doesn't prepend.
	blk := MaybeInjectInventory(state, "cua_driver", `{"name":"list_apps"}`, cuaResult("APPS WITH WINDOWS"), fixedInventory(testInventory))
	if blk.Content.(string) != "APPS WITH WINDOWS" {
		t.Errorf("list_apps result should be untouched: %q", blk.Content)
	}
	if !state.Injected {
		t.Error("list_apps first call should mark injected")
	}
	next := MaybeInjectInventory(state, "cua_driver", `{"name":"get_window_state"}`, cuaResult("body"), fixedInventory(testInventory))
	if next.Content.(string) != "body" {
		t.Errorf("must not prepend after list_apps already covered it: %q", next.Content)
	}
}

func TestMaybeInjectInventory_EmptyFetchRetriesLater(t *testing.T) {
	state := &CuaInventoryState{}
	// Fetch fails (daemon hiccup): no prepend, NOT marked injected.
	blk := MaybeInjectInventory(state, "cua_driver", `{"name":"get_window_state"}`, cuaResult("body"), fixedInventory(""))
	if blk.Content.(string) != "body" {
		t.Errorf("nothing should be prepended on empty fetch: %q", blk.Content)
	}
	if state.Injected {
		t.Error("empty fetch must not burn the one-shot; should retry later")
	}
	// Next call with a working fetch should now inject.
	blk2 := MaybeInjectInventory(state, "cua_driver", `{"name":"click"}`, cuaResult("body2"), fixedInventory(testInventory))
	if !strings.Contains(blk2.Content.(string), "收件箱 - Gmail") {
		t.Errorf("inventory should inject on the retry: %q", blk2.Content)
	}
}

func TestMaybeInjectInventory_NonStringResultSkips(t *testing.T) {
	state := &CuaInventoryState{}
	imageBlk := ContentBlock{Type: "tool_result", ToolUseID: "tu1", Content: []ContentBlock{{Type: "image"}}}
	out := MaybeInjectInventory(state, "cua_driver", `{"name":"screenshot"}`, imageBlk, fixedInventory(testInventory))
	if _, ok := out.Content.([]ContentBlock); !ok {
		t.Errorf("image result should be left structured, got %T", out.Content)
	}
	if state.Injected {
		t.Error("must not burn the one-shot on a non-text result")
	}
}

func TestMaybeInjectInventory_IgnoresNonCuaAndNilState(t *testing.T) {
	state := &CuaInventoryState{}
	blk := MaybeInjectInventory(state, "Read", `{"name":"get_window_state"}`, cuaResult("body"), fixedInventory(testInventory))
	if blk.Content.(string) != "body" || state.Injected {
		t.Error("non-cua_driver call must be untouched and not mark injected")
	}
	// nil state must not panic.
	_ = MaybeInjectInventory(nil, "cua_driver", `{"name":"click"}`, cuaResult("body"), fixedInventory(testInventory))
}
