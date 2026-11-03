package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Persistent "Always allow this server" trust for MCP servers lives in
// a JSON file alongside the hub state. We intentionally keep it
// separate from state.json so a manual edit / accidental corruption
// of one file doesn't take the other with it, and so trust survives a
// state.json schema migration unchanged.
//
// File layout:
//   ~/.yansu-agent/mcp-hub/trusted.json
//   {
//     "version": 1,
//     "trusted_servers": ["server-id-a", "server-id-b"]
//   }
//
// Storage is a set (uniqueness enforced on save). All access is
// serialised via mcpTrustedStateMu. Mutators go through
// withMcpTrustedState so a load-modify-save race can't drop entries.
// loadMcpTrustedState caches the parsed state in memory so list-RPCs
// (which call isMcpServerTrusted once per row) don't re-read+re-parse
// the file on every render.

const mcpTrustedStateVersion = 1

type mcpTrustedState struct {
	Version        int      `json:"version"`
	TrustedServers []string `json:"trusted_servers"`
}

var (
	mcpTrustedStateMu sync.Mutex
	// mcpTrustedCache holds the last-known-good trust state. nil means
	// "not yet loaded from disk this process lifetime"; loadMcpTrusted
	// State populates it on first call and saveMcpTrustedState replaces
	// it wholesale on each successful write. Protected by
	// mcpTrustedStateMu — every read and every write of this pointer
	// happens with the lock held, so concurrent RPC handlers see a
	// consistent view without copying on the read path.
	mcpTrustedCache *mcpTrustedState
)

func mcpTrustedStatePath() (string, error) {
	base, err := mcpHubBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "trusted.json"), nil
}

// loadMcpTrustedState returns the in-memory cached state, loading from
// disk on first call. Caller must hold mcpTrustedStateMu. The returned
// pointer aliases the cache — callers that need to mutate must do so
// through withMcpTrustedState, which works on a copy and replaces the
// cache atomically on a successful save.
func loadMcpTrustedState() (*mcpTrustedState, error) {
	if mcpTrustedCache != nil {
		return mcpTrustedCache, nil
	}
	path, err := mcpTrustedStatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		mcpTrustedCache = &mcpTrustedState{Version: mcpTrustedStateVersion, TrustedServers: []string{}}
		return mcpTrustedCache, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read mcp trusted state: %w", err)
	}
	var state mcpTrustedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse mcp trusted state: %w", err)
	}
	if state.Version == 0 {
		state.Version = mcpTrustedStateVersion
	}
	if state.TrustedServers == nil {
		state.TrustedServers = []string{}
	}
	mcpTrustedCache = &state
	return mcpTrustedCache, nil
}

// saveMcpTrustedState writes state atomically (temp file + rename) and
// updates the in-memory cache only after a successful rename. Caller
// must hold mcpTrustedStateMu. A write failure leaves the cache
// pointing at the previous state, so subsequent reads still reflect
// disk reality.
func saveMcpTrustedState(state *mcpTrustedState) error {
	if state == nil {
		state = &mcpTrustedState{}
	}
	state.Version = mcpTrustedStateVersion
	// Normalise: deduplicate + sort so the file is stable across writes
	// (no spurious diff churn on disk).
	seen := make(map[string]bool, len(state.TrustedServers))
	deduped := make([]string, 0, len(state.TrustedServers))
	for _, id := range state.TrustedServers {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		deduped = append(deduped, id)
	}
	sort.Strings(deduped)
	state.TrustedServers = deduped

	path, err := mcpTrustedStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create mcp hub directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp trusted state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), "trusted.json.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if anything before the rename fails. After a
	// successful rename, tmpPath no longer exists so Remove is a no-op.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write mcp trusted state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close mcp trusted state temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace mcp trusted state: %w", err)
	}
	mcpTrustedCache = state
	return nil
}

// withMcpTrustedState atomically loads, mutates, and persists trust
// entries. fn receives a copy of the current state so its mutations
// don't pollute the cache before saveMcpTrustedState succeeds; on a
// successful save the cache is replaced wholesale with the new state.
// Returning an error from fn aborts the save and leaves the cache
// untouched.
func withMcpTrustedState(fn func(*mcpTrustedState) error) error {
	mcpTrustedStateMu.Lock()
	defer mcpTrustedStateMu.Unlock()
	current, err := loadMcpTrustedState()
	if err != nil {
		return err
	}
	next := &mcpTrustedState{
		Version:        current.Version,
		TrustedServers: append([]string{}, current.TrustedServers...),
	}
	if err := fn(next); err != nil {
		return err
	}
	return saveMcpTrustedState(next)
}

// isMcpServerTrusted reports whether the given server id is currently
// in the trusted set. Read-only; serves from the in-memory cache after
// the first call so list-RPC paths don't repeatedly read+parse the
// trust file.
func isMcpServerTrusted(id string) bool {
	if id == "" {
		return false
	}
	mcpTrustedStateMu.Lock()
	defer mcpTrustedStateMu.Unlock()
	state, err := loadMcpTrustedState()
	if err != nil {
		return false
	}
	return slices.Contains(state.TrustedServers, id)
}

// setMcpServerTrusted adds or removes id from the trusted set.
// Returns an error only on disk I/O failure; toggling a non-existent
// id off is a no-op.
func setMcpServerTrusted(id string, trusted bool) error {
	if id == "" {
		return fmt.Errorf("mcp server id is required")
	}
	return withMcpTrustedState(func(s *mcpTrustedState) error {
		filtered := make([]string, 0, len(s.TrustedServers))
		present := false
		for _, sid := range s.TrustedServers {
			if sid == id {
				present = true
				if trusted {
					filtered = append(filtered, sid)
				}
				continue
			}
			filtered = append(filtered, sid)
		}
		if trusted && !present {
			filtered = append(filtered, id)
		}
		s.TrustedServers = filtered
		return nil
	})
}

// serverIDFromPromptPrefix recovers a real MCP server id from the
// "mcp__<exposed>__" prefix that PermissionPrompt.Prefix advertises.
// The exposed segment is the underscore-normalised form (see
// normalizeForExposure). Returns "", false when nothing matches.
//
// Map-iteration order is irrelevant here because mcpServerIDPattern
// (mcp_hub.go) forbids underscores in the id — every persisted id
// normalises to a unique exposed form, so at most one entry can match
// `exposed` and the random iteration order picks the same id every
// time. The same invariant holds for composioManagedServerIDs (those
// constants also avoid underscores).
func serverIDFromPromptPrefix(prefix string) (string, bool) {
	const head = "mcp__"
	const tail = "__"
	if len(prefix) <= len(head)+len(tail) {
		return "", false
	}
	if prefix[:len(head)] != head {
		return "", false
	}
	if prefix[len(prefix)-len(tail):] != tail {
		return "", false
	}
	exposed := prefix[len(head) : len(prefix)-len(tail)]
	state, err := loadMcpHubState()
	if err == nil {
		for id := range state.Servers {
			if strings.ReplaceAll(id, "-", "_") == exposed {
				return id, true
			}
		}
	}
	// Composio-managed servers aren't persisted in state.json — they're
	// injected at runtime by the supervisor (see ListMcpServers). Match
	// against the hardcoded id set so "Always allow this server" works
	// for them too.
	for _, id := range composioManagedServerIDs() {
		if strings.ReplaceAll(id, "-", "_") == exposed {
			return id, true
		}
	}
	return "", false
}

// composioManagedServerIDs returns the set of composio-managed MCP
// server ids known at compile time. Kept here (rather than in
// composio.go) so the trust-prefix lookup stays self-contained.
func composioManagedServerIDs() []string {
	ids := make([]string, 0, len(composioManagedApps))
	for _, app := range composioManagedApps {
		id, err := composioManagedServerID(app)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
