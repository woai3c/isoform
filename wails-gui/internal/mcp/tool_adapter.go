package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	"yansu-agent/agent/authority"
	yansutools "yansu-agent/agent/tools"
)

// anthropicToolNameLimit is the upper bound on tool names in the
// Anthropic API (the regex is ^[a-zA-Z0-9_-]{1,128}$). We compose
// "mcp__<serverID>__<sanitizedToolName>"; the id pattern caps len(id)
// at 31 so the tool segment always has at least 90 chars to play with.
const anthropicToolNameLimit = 128

// normalizeForExposure underscore-normalises a server-id or tool-name
// segment for the model-visible tool name. Anthropic's regex accepts
// both '-' and '_', but every built-in tool in the wild uses snake_case
// and LLMs routinely "auto-correct" hyphens to underscores in their
// generated tool_use blocks — that mismatch then misses the registry's
// exact-match dispatch and surfaces as "unknown tool", triggering
// retry storms (observed e.g. with `upstash-context7` /
// `resolve-library-id`). Normalising at registration so the model only
// ever sees underscores keeps dispatch deterministic.
func normalizeForExposure(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// ExposedServerID returns the underscore-normalised form of an MCP
// server id — the same form that appears between "mcp__" and "__<tool>"
// in registered tool names. Prompt-builders use this to ensure any
// reference shown to the model matches what the registry will accept;
// listing the raw hyphenated id alongside underscore-prefixed tool
// names invites the model to splice the two and produce a name that
// fails to dispatch.
func ExposedServerID(rawID string) string {
	return normalizeForExposure(rawID)
}

// sanitizeToolNameSegment maps an MCP-supplied tool name into a string
// that satisfies Anthropic's character set ([a-zA-Z0-9_-]). MCP server
// authors in the wild use dots, slashes, and colons inside tool names;
// the API rejects those, so we collapse every disallowed rune to '_'.
// Hyphens are also folded to underscores (see normalizeForExposure).
// The original name is preserved on the adapter and used when calling
// tools/call so the MCP server still recognises the request.
func sanitizeToolNameSegment(name string) string {
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			b.WriteRune(r)
		default:
			// Includes '-' and any non-alphanumeric: collapse to '_'
			// so the exposed name is unambiguously snake_case.
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}

// buildExposedName composes the exposed tool name and clamps the
// result to the Anthropic limit by trimming the tool tail. The server
// id is underscore-normalised so the prefix the model sees is
// snake_case-consistent with the tool segment; this matches what
// GroupPrefix uses for registry dispatch.
func buildExposedName(serverID, sanitizedTool string) string {
	prefix := "mcp__" + normalizeForExposure(serverID) + "__"
	full := prefix + sanitizedTool
	if len(full) <= anthropicToolNameLimit {
		return full
	}
	keep := anthropicToolNameLimit - len(prefix)
	if keep <= 0 {
		// Defensive: id validation guarantees keep >= 90, so this
		// branch is unreachable in practice. Fall back to the bare
		// prefix rather than silently exposing a mis-composed name.
		return prefix
	}
	return prefix + sanitizedTool[:keep]
}

// resolveUniqueExposedName picks an Anthropic-safe + dedup-safe
// exposed name. If two distinct MCP tool names collapse to the same
// sanitized form (e.g. "do.thing" and "do_thing" both become
// "do_thing"), append "_2", "_3", ... while still respecting the
// 128-char cap. taken is updated with every name we hand out.
func resolveUniqueExposedName(serverID, originalToolName string, taken map[string]bool) string {
	sanitized := sanitizeToolNameSegment(originalToolName)
	candidate := buildExposedName(serverID, sanitized)
	if !taken[candidate] {
		taken[candidate] = true
		return candidate
	}
	prefix := "mcp__" + normalizeForExposure(serverID) + "__"
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("_%d", n)
		budget := anthropicToolNameLimit - len(prefix) - len(suffix)
		if budget < 1 {
			// id is too long for any deduped variant to fit; in
			// practice unreachable given the id length cap. Return
			// the original (pre-conflict) candidate; the caller
			// observes one tool overwritten, not a registration crash.
			return candidate
		}
		base := sanitized
		if len(base) > budget {
			base = base[:budget]
		}
		next := prefix + base + suffix
		if !taken[next] {
			taken[next] = true
			return next
		}
	}
}

// ToolCaller is the narrow capability the adapter needs from the
// manager. Defined as an interface so unit tests can substitute a
// fake without spawning real subprocesses.
type ToolCaller interface {
	CallTool(ctx context.Context, id, name string, args map[string]any) (*mcp.CallToolResult, error)
}

// McpToolAdapter wraps a single MCP-server-provided tool so that it
// satisfies the yansu-agent tools.Tool contract. One adapter is created
// per (server, tool) pair and registered into the agent loop's tool
// registry under an Anthropic-safe exposed name. Failures from the
// underlying MCP call are returned as errors so the registry maps
// them to is_error tool_results — per problem 4 decision (B), we let
// the model see structured failures instead of retrying or circuit-
// breaking on its behalf.
type McpToolAdapter struct {
	serverID string
	// tool.Name is the *original* MCP tool name and is what we send
	// on the wire when calling tools/call. exposedName (below) is what
	// the model sees.
	tool   mcp.Tool
	caller ToolCaller

	// trusted, when true, lets the adapter skip the user-confirmation
	// prompt. Persisted across processes via the MCP trusted-server
	// file; flipped at runtime by SetTrusted when the user toggles the
	// Settings UI or picks "Always allow this server" on a prompt.
	// atomic.Bool so the agent loop can read it without taking a
	// per-call lock.
	trusted atomic.Bool

	// exposedName is the registry- and Anthropic-safe full name
	// ("mcp__<serverID>__<sanitizedTool>", ≤128 chars, deduped within
	// a server's tool set). The bridge precomputes this; tests that
	// construct adapters directly can use AdapterToolName.
	exposedName string
}

// NewToolAdapter constructs an adapter from a server id, an
// Anthropic-safe exposed name (see resolveUniqueExposedName /
// AdapterToolName), the tool metadata returned by the server's
// tools/list, and a caller capable of dispatching tools/call.
// trusted=true skips confirmation prompts; keep it false unless the
// user has explicitly trusted the server.
func NewToolAdapter(serverID, exposedName string, tool mcp.Tool, caller ToolCaller, trusted bool) *McpToolAdapter {
	a := &McpToolAdapter{
		serverID:    serverID,
		tool:        tool,
		caller:      caller,
		exposedName: exposedName,
	}
	a.trusted.Store(trusted)
	return a
}

// SetTrusted toggles the trust flag at runtime. Used by the bridge
// after the user picks "Always allow this server" (or flips the
// Settings toggle) so the in-flight adapter immediately stops asking
// for confirmation. Safe to call concurrently with NeedsPermission.
func (a *McpToolAdapter) SetTrusted(trusted bool) {
	a.trusted.Store(trusted)
}

// IsTrusted reports the current persistent-trust flag. Exposed for
// tests and for the bridge when it needs to surface state without
// reading the adapter directly.
func (a *McpToolAdapter) IsTrusted() bool {
	return a.trusted.Load()
}

// AdapterToolName returns the canonical (sanitized + truncated)
// exposed name for serverID/toolName WITHOUT cross-tool dedup.
// Exposed so callers (tests, logs) can compute the name without
// instantiating an adapter; production code routes through the bridge
// which deduplicates conflicting sanitized names.
func AdapterToolName(serverID, toolName string) string {
	return buildExposedName(serverID, sanitizeToolNameSegment(toolName))
}

func (a *McpToolAdapter) Name() string {
	if a.exposedName != "" {
		return a.exposedName
	}
	return AdapterToolName(a.serverID, a.tool.Name)
}

// OriginalToolName returns the tool name as advertised by the MCP
// server. Exposed primarily for tests; production code uses it
// implicitly via Execute.
func (a *McpToolAdapter) OriginalToolName() string {
	return a.tool.Name
}

func (a *McpToolAdapter) Description() string {
	if a.tool.Description != "" {
		return a.tool.Description
	}
	return fmt.Sprintf("MCP tool %q on server %q", a.tool.Name, a.serverID)
}

// JSONSchema converts the MCP-side input schema into the map[string]any
// form the agent loop's registry uses. mcp-go stores the schema as a
// typed struct OR as a raw JSON message; we round-trip through JSON so
// we don't have to mirror its internal field set.
func (a *McpToolAdapter) JSONSchema() map[string]any {
	if len(a.tool.RawInputSchema) > 0 {
		var out map[string]any
		if err := json.Unmarshal(a.tool.RawInputSchema, &out); err == nil {
			return out
		}
	}
	data, err := json.Marshal(a.tool.InputSchema)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"type": "object"}
	}
	if out == nil {
		out = map[string]any{}
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	return out
}

func (a *McpToolAdapter) NeedsPermission(input map[string]any) (bool, yansutools.PermissionPrompt) {
	if a.trusted.Load() {
		return false, yansutools.PermissionPrompt{}
	}
	return true, yansutools.PermissionPrompt{
		Reason: fmt.Sprintf("Run MCP tool %s on server %s?", a.tool.Name, a.serverID),
		Prefix: "mcp__" + normalizeForExposure(a.serverID) + "__",
		Options: []yansutools.PermissionOption{
			{ID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{ID: "allow_session", Name: "Allow for this chat", Kind: "allow_session"},
			{ID: "allow_always", Name: "Always allow this server", Kind: "allow_always"},
			{ID: "reject_once", Name: "Reject", Kind: "reject_once"},
		},
	}
}

func (a *McpToolAdapter) DescribeAuthority(input map[string]any) authority.Action {
	_ = input
	action := authority.Action{
		Capability:     mcpCapability(a.serverID, a.tool.Name),
		ToolName:       a.Name(),
		Operation:      a.tool.Name,
		Effect:         authority.EffectUnknown,
		ApprovalPrefix: "mcp__" + normalizeForExposure(a.serverID) + "__",
		ApprovalReason: fmt.Sprintf("Run MCP tool %s on server %s?", a.tool.Name, a.serverID),
		Metadata: map[string]string{
			"server_id": a.serverID,
			"tool_name": a.tool.Name,
		},
	}
	applyMCPAnnotations(&action, a.tool)
	if a.trusted.Load() {
		action.AttendedGrantID = "mcp-trusted:" + a.serverID
	}
	return action
}

func applyMCPAnnotations(action *authority.Action, tool mcp.Tool) {
	if action == nil {
		return
	}
	if tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint {
		action.Effect = authority.EffectRead
		action.Reversible = true
		return
	}
	if tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
		action.Effect = authority.EffectPrivileged
	}
}

func mcpCapability(serverID, toolName string) string {
	server := strings.ToLower(sanitizeToolNameSegment(serverID))
	tool := strings.ToLower(sanitizeToolNameSegment(toolName))
	return "mcp." + server + "." + tool
}

func (a *McpToolAdapter) Execute(ctx context.Context, call yansutools.ToolCall) (yansutools.ToolResult, error) {
	res, err := a.caller.CallTool(ctx, a.serverID, a.tool.Name, call.Input)
	if err != nil {
		// Problem-4 decision (B): surface as is_error tool_result.
		// Returning err here causes Registry.Execute to propagate the
		// error which the agent loop converts to is_error=true.
		return yansutools.ToolResult{}, fmt.Errorf("mcp server %q: tool %q failed: %w", a.serverID, a.tool.Name, err)
	}
	if res == nil {
		return yansutools.ToolResult{Content: ""}, nil
	}
	content, blocks := convertContent(res.Content)
	if res.IsError {
		// MCP-level "tool reported error" is also an is_error.
		return yansutools.ToolResult{Content: content, Blocks: blocks}, fmt.Errorf("mcp tool %q reported error", a.tool.Name)
	}
	return yansutools.ToolResult{Content: content, Blocks: blocks}, nil
}

// convertContent maps MCP content blocks (TextContent / ImageContent /
// AudioContent / EmbeddedResource) into the yansu-agent ToolResult
// shape. Text blocks concatenate into Content; image blocks become
// structured Blocks so the agent loop can attach them as image content
// blocks in the next assistant turn.
func convertContent(items []mcp.Content) (string, []yansutools.ContentBlock) {
	if len(items) == 0 {
		return "", nil
	}
	var texts []string
	var blocks []yansutools.ContentBlock
	for _, item := range items {
		switch v := item.(type) {
		case mcp.TextContent:
			texts = append(texts, v.Text)
			blocks = append(blocks, yansutools.ContentBlock{Type: "text", Text: v.Text})
		case mcp.ImageContent:
			blocks = append(blocks, yansutools.ContentBlock{
				Type: "image",
				Source: &yansutools.ImageSource{
					Type:      "base64",
					MediaType: v.MIMEType,
					Data:      v.Data,
				},
			})
		default:
			// AudioContent / EmbeddedResource: stringify via JSON so
			// the model at least sees structured payload metadata.
			data, err := json.Marshal(item)
			if err == nil {
				texts = append(texts, string(data))
			}
		}
	}
	// Only return Blocks if any non-text block was produced; otherwise
	// the textual concatenation is enough and Blocks=nil keeps the
	// tool_result on the simple-string code path.
	hasNonText := false
	for _, b := range blocks {
		if b.Type != "text" {
			hasNonText = true
			break
		}
	}
	if !hasNonText {
		blocks = nil
	}
	return strings.Join(texts, "\n"), blocks
}
