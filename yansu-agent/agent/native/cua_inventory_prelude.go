package native

import (
	"context"
	"strings"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

// cua_inventory_prelude.go gives the model an "opening map" of the windows
// already open on its FIRST Computer Use interaction — the way
// kwwk-computer-use-core (OpenBridge's engine) returns a startup window
// inventory from its `start` action.
//
// Why this exists: without an opening inventory the model has no idea which
// windows are already open (is Gmail up? in which window? showing what?), so
// it navigates by trial-and-error — launch/click/type, snapshot, discover it
// guessed wrong, retry. That trial-and-error is the root cause of the long
// "click back to inbox" loops that pile up snapshots and waste turns. The
// EFFICIENCY guidance in the cua_driver tool description only *asks* the model
// to check open windows first; this guarantees it structurally instead,
// independent of whether the model complies.
//
// Mechanism: on the first cua_driver call of a run whose sub-tool is not
// itself list_apps (whose own result already carries the inventory), fetch
// list_apps once and prepend its open-window list to that call's tool_result.
// The model sees the map without having to ask for it. A failed/empty fetch or
// a non-text (image) result is a no-op that leaves the call untouched and
// retries on the next cua_driver call, so a flaky daemon never blocks real work.

// CuaInventoryState tracks whether the one-time opening inventory has been
// injected for this run. A single struct (not keyed) because one run drives
// one Computer Use session.
type CuaInventoryState struct {
	Injected bool
}

const cuaInventoryPreludeHeader = "[Computer Use — windows already open right now (your opening map). " +
	"If the window holding what you need is listed below, call get_window_state on it directly; " +
	"do NOT open a new window or navigate from scratch.]\n"

const cuaInventoryPreludeSeparator = "\n\n--- (the tool result you requested follows) ---\n"

// MaybeInjectInventory prepends a one-time open-window inventory to the first
// cua_driver tool_result of a run, then marks the state so later calls are
// untouched. It returns blk unchanged for non-cua_driver calls, after the
// inventory was already injected, or when the fetch/result can't carry it.
//
// fetchInventory is the loop-provided list_apps fetch; it returns "" on
// failure. We only set Injected=true once we have actually delivered the
// inventory (or confirmed the first call is itself list_apps), so a transient
// daemon failure doesn't silently burn the one-shot.
func MaybeInjectInventory(
	state *CuaInventoryState,
	toolName, rawInput string,
	blk ContentBlock,
	fetchInventory func() string,
) ContentBlock {
	if state == nil || state.Injected || !strings.EqualFold(strings.TrimSpace(toolName), "cua_driver") {
		return blk
	}

	// The first call IS list_apps — its own result already lists open windows.
	// Mark injected so we don't prepend a duplicate on a later call.
	if cuaSubToolName(rawInput) == "list_apps" {
		state.Injected = true
		return blk
	}

	inv := strings.TrimSpace(fetchInventory())
	if inv == "" {
		// Daemon hiccup / list_apps failed — try again on the next call
		// rather than burning the one-shot on an empty inventory.
		return blk
	}

	body, ok := blk.Content.(string)
	if !ok {
		// Structured/image result (e.g. screenshot): can't prepend text.
		// Leave it and retry on the next textual call.
		return blk
	}

	state.Injected = true
	blk.Content = cuaInventoryPreludeHeader + inv + cuaInventoryPreludeSeparator + body
	return blk
}

// fetchCuaInventory runs list_apps under the run's authority and returns its
// text, or "" when the run may not list apps or the call failed.
func fetchCuaInventory(ctx context.Context, cfg *LoopConfig) string {
	tool := cfg.Registry.Get("cua_driver")
	if tool == nil {
		return ""
	}
	input := map[string]any{"name": "list_apps"}
	evaluation := evaluateToolAuthority(ctx, cfg, tool, input)
	if evaluation.Decision != authority.DecisionAllow {
		return ""
	}
	res, err := cfg.Registry.Execute(ctx, "cua_driver", tools.ToolCall{
		WorkDir: cfg.WorkDir,
		Input:   input,
		Authority: &tools.AuthorityRuntime{
			Context:    evaluation.Context,
			Evaluator:  cfg.AuthorityEvaluator,
			Evaluation: &evaluation,
			Recorded:   true,
		},
	})
	if err != nil || res.IsError {
		return ""
	}
	return res.Content
}
