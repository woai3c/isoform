package native

// cua_snapshot_prune.go folds stale Computer-Use window snapshots out of
// the conversation history before every API call.
//
// Why this exists: each cua_driver `get_window_state` (and `get_app_state`)
// call returns a full window AX tree — for a rich web app like Gmail that is
// easily tens of thousands of tokens. The agent re-snapshots on every turn,
// so a 30-step Computer-Use run accumulates 30 full snapshots in history and
// blows past the upstream context window (sub2api routes Claude requests to a
// 262144-token Kimi K2.x upstream — see compact.go's ContextWindowOverrideEnv)
// with a "request exceeded model token limit" 400.
//
// The fix mirrors how kwwk-computer-use-core (OpenBridge's engine) frames it:
// "follow-up actions operate on the LATEST captured app state; element indexes
// become stale after navigation." A snapshot older than the most recent few is
// dead weight — the UI it described has already changed, and the refs/indexes
// it exposed don't resolve against the current window anyway (cua-driver
// regenerates refs on every snapshot). So we keep the most recent N snapshot
// results in full and fold the rest to a short placeholder.
//
// This is the token-window complement to image_budget.go's
// EnforceRequestByteBudget: that one reacts to the 2 MiB wire-byte limit by
// shrinking images; this one proactively bounds the token cost of repeated AX
// snapshots. Both mutate messages in place and preserve tool_use ↔ tool_result
// pairing (only the result body is swapped, never its Type/ToolUseID).

const (
	// cuaSnapshotKeepRecent is how many of the most recent cua_driver
	// snapshot results stay in full. Two gives the model a before/after
	// pair to reason about the effect of its last action while still
	// folding everything older.
	cuaSnapshotKeepRecent = 2

	// cuaSnapshotMinFoldBytes skips folding small snapshot results (empty
	// windows, error envelopes) where the placeholder would not save
	// meaningful context — and never makes a body larger than it was.
	cuaSnapshotMinFoldBytes = 2048

	// cuaStaleSnapshotPlaceholder replaces a folded snapshot body. It keeps
	// the model oriented (it knows a snapshot happened here) and tells it
	// how to recover the detail if it genuinely needs the older state.
	cuaStaleSnapshotPlaceholder = "[stale UI snapshot pruned to save context — only the most recent cua_driver get_window_state result(s) are kept in full. The UI has since changed; re-run get_window_state if you need the current state.]"
)

// cuaSnapshotReadTools are the cua_driver sub-tools whose results carry a
// full window AX snapshot. Older results from these are safe to fold; action
// tools (click, type_text, …) return small results and are left untouched.
var cuaSnapshotReadTools = map[string]bool{
	"get_window_state": true,
	"get_app_state":    true,
}

// CuaSnapshotPruneStats reports what PruneStaleCuaSnapshots folded, for
// logging and telemetry. Zero means nothing was folded.
type CuaSnapshotPruneStats struct {
	FoldedCount int
	BytesFreed  int
}

// Touched reports whether any snapshot was folded.
func (s CuaSnapshotPruneStats) Touched() bool { return s.FoldedCount > 0 }

// PruneStaleCuaSnapshots folds the body of all but the most recent
// cuaSnapshotKeepRecent cua_driver snapshot tool_results into a short
// placeholder. Idempotent: a body already equal to the placeholder, or below
// cuaSnapshotMinFoldBytes, is left alone, so calling this before every turn
// folds each snapshot exactly once (when it becomes stale) and is a no-op
// thereafter.
//
// Mutations are done in place on the messages slice (via pointers into the
// backing ContentBlock arrays) and persist across the run — a snapshot is
// folded once, not re-walked into a placeholder every turn.
func PruneStaleCuaSnapshots(messages []Message) CuaSnapshotPruneStats {
	var stats CuaSnapshotPruneStats

	// Pass 1: collect, in conversation order, the tool_use IDs of every
	// cua_driver snapshot-read call.
	var snapshotIDs []string
	for i := range messages {
		blocks, ok := messages[i].Content.([]ContentBlock)
		if !ok {
			continue
		}
		for j := range blocks {
			b := &blocks[j]
			if b.Type != "tool_use" || b.Name != "cua_driver" || b.ID == "" {
				continue
			}
			input, ok := b.Input.(map[string]any)
			if !ok {
				continue
			}
			if sub, _ := input["name"].(string); cuaSnapshotReadTools[sub] {
				snapshotIDs = append(snapshotIDs, b.ID)
			}
		}
	}
	if len(snapshotIDs) <= cuaSnapshotKeepRecent {
		return stats
	}

	// Everything except the last keepRecent is eligible to fold.
	fold := make(map[string]bool, len(snapshotIDs)-cuaSnapshotKeepRecent)
	for _, id := range snapshotIDs[:len(snapshotIDs)-cuaSnapshotKeepRecent] {
		fold[id] = true
	}

	// Pass 2: fold the matching tool_result bodies in place.
	for i := range messages {
		blocks, ok := messages[i].Content.([]ContentBlock)
		if !ok {
			continue
		}
		for j := range blocks {
			b := &blocks[j]
			if b.Type != "tool_result" || !fold[b.ToolUseID] {
				continue
			}
			if freed := foldCuaSnapshotBody(b); freed > 0 {
				stats.FoldedCount++
				stats.BytesFreed += freed
			}
		}
	}
	return stats
}

// foldCuaSnapshotBody replaces a snapshot tool_result's body with the stale
// placeholder, preserving Type/ToolUseID/IsError so tool_use ↔ tool_result
// pairing stays valid. Returns approximate bytes freed, or 0 when the body is
// already folded or too small to bother with.
func foldCuaSnapshotBody(b *ContentBlock) int {
	switch c := b.Content.(type) {
	case string:
		if c == cuaStaleSnapshotPlaceholder || len(c) < cuaSnapshotMinFoldBytes {
			return 0
		}
		freed := len(c) - len(cuaStaleSnapshotPlaceholder)
		b.Content = cuaStaleSnapshotPlaceholder
		return freed
	case []ContentBlock:
		// Snapshot results are normally plain strings; handle the
		// multi-block shape defensively (e.g. if an image block ever rides
		// along) by summing its bytes and collapsing to one placeholder.
		total := 0
		for k := range c {
			total += approxBlockBytes(&c[k])
		}
		if total < cuaSnapshotMinFoldBytes {
			return 0
		}
		b.Content = cuaStaleSnapshotPlaceholder
		return total - len(cuaStaleSnapshotPlaceholder)
	}
	return 0
}
