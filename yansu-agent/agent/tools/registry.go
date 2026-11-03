package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
)

// groupPrefixPattern restricts MCP-style tool group prefixes to alphanumerics,
// underscore, and dash. The separator between prefix and tool name is "__".
var groupPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Tool is the contract every native agent tool implements. The registry
// uses this interface to dispatch tool_use blocks from the Anthropic
// API into Go-side execution.
type Tool interface {
	// Name is the Anthropic tool name the model will use.
	Name() string
	// Description is what we tell the model the tool does.
	Description() string
	// JSONSchema returns the input schema used for model guidance AND
	// for local validation in Execute.
	JSONSchema() map[string]any
	// NeedsPermission reports whether the tool requires a user
	// confirmation for the given input before executing.
	NeedsPermission(input map[string]any) (bool, PermissionPrompt)
	// Execute runs the tool against a parsed input map. Implementations
	// must respect ctx cancellation. A returned error is mapped to a
	// tool_result with is_error=true; no error means success and the
	// string output becomes the tool_result content.
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}

// SourceReadTool opts a read into final transport publication checks. The
// per-run server withholds its result event until this fence succeeds, constructs
// the MCP result under the fence, then releases it before UI persistence (which
// may use the source database). Replays must request a fresh source read.
type SourceReadTool interface {
	Tool
	RequiresFreshRead() bool
	LockResultPublication(context.Context, ToolCall) (func(), error)
}

// ToolCall packages everything a tool needs to run.
type ToolCall struct {
	// WorkDir is the session's working directory (always absolute).
	WorkDir string
	// Input is the tool_use input JSON, parsed. For streaming
	// partial-JSON the caller should have used tolerant_json.Parse
	// before dispatch.
	Input map[string]any
	// ID is the tool_use block id (echoed back in tool_result).
	ID string
	// Permission, when set, tracks an already-granted permission so
	// the tool can verify the user accepted this specific invocation.
	Permission *GrantedPermission
	// Authority carries the capability evaluation for this call. Native
	// agent runs always set it; keeping it optional preserves direct tool
	// unit tests and non-agent embedding callers.
	Authority *AuthorityRuntime
}

// ToolResult is the structured response a tool returns.
type ToolResult struct {
	// Content is the textual payload; for Read this may contain
	// Anthropic image blocks rendered via ToAnthropicContent.
	Content string
	// Warnings surface soft issues to the model (e.g. "fuzzy match at
	// L4") without marking the call as failed.
	Warnings []string
	// Metadata is an opaque JSON object the caller may include in
	// logs / UI.
	Metadata map[string]any
	// Blocks, when non-nil, are structured content blocks (images,
	// etc) that should become an Anthropic multi-block tool_result
	// instead of a plain string.
	Blocks []ContentBlock
	// IsError marks the result as a failure for the model. The tool
	// still returned (no Go error from Execute), but the caller wants
	// the model to see is_error=true on the tool_result block — used
	// e.g. by WebFetch when the upstream returns HTTP 4xx/5xx so the
	// model doesn't treat the error page body as a successful fetch
	// and retry the same dead URL forever.
	IsError bool
}

// ContentBlock is one entry of a multi-block tool_result, mirroring
// the Anthropic schema.
type ContentBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

// ImageSource is the base64 image source for image blocks.
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// PermissionPrompt is the user-facing description of why a confirmation
// is being requested.
type PermissionPrompt struct {
	Reason  string
	Prefix  string // for "always allow <prefix>"
	Options []PermissionOption
}

// PermissionOption is one choice the user can pick.
type PermissionOption struct {
	ID   string
	Name string
	Kind string // allow_once | allow_always | reject_once
}

// GrantedPermission records a user's granted permission for a specific
// tool_use (so the tool can verify after the fact).
type GrantedPermission struct {
	ToolUseID string
	Kind      string
	Prefix    string
}

// Registry tracks the available tools and dispatches calls.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. Duplicate names panic — this is programming
// error, not a runtime condition.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tool %q already registered", t.Name()))
	}
	r.tools[t.Name()] = t
}

// ReplaceGroup atomically swaps the set of tools whose names start with
// prefix+"__". This is the dynamic-registration path used by the MCP
// bridge: each MCP server owns a group keyed by "mcp__<server-id>", and
// on every tools/list refresh the bridge calls ReplaceGroup to reflect
// the server's current tool set without disturbing built-in tools or
// other MCP servers' groups.
//
// All tools passed in must have a Name() that starts with prefix+"__";
// any that don't are rejected so a buggy adapter cannot escape its
// namespace and overwrite built-in tools. Passing an empty tools slice
// is the canonical way to "unregister" a group (e.g. when an MCP server
// is disabled or crashes).
//
// Callers are responsible for choosing prefixes that don't shadow each
// other: clearing prefix "a" will also remove tools registered under
// prefix "a__b" because both share the "a__" separator. The MCP bridge
// avoids this by deriving server-id from a slug that excludes "__".
func (r *Registry) ReplaceGroup(prefix string, tools []Tool) error {
	if !groupPrefixPattern.MatchString(prefix) {
		return fmt.Errorf("invalid tool group prefix: %q", prefix)
	}
	groupSep := prefix + "__"
	for _, t := range tools {
		if t == nil {
			return fmt.Errorf("nil tool in group %q", prefix)
		}
		if !strings.HasPrefix(t.Name(), groupSep) {
			return fmt.Errorf("tool %q does not belong to group %q", t.Name(), prefix)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.tools {
		if strings.HasPrefix(name, groupSep) {
			delete(r.tools, name)
		}
	}
	for _, t := range tools {
		r.tools[t.Name()] = t
	}
	return nil
}

// Get returns the tool with the given name, or nil when absent.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns all registered tools, optionally filtered to the given
// allow list. Tools omitted from the allow list are excluded; an empty
// allow list means "all".
func (r *Registry) List(allow []string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allowed := map[string]bool{}
	for _, n := range allow {
		allowed[n] = true
	}
	var out []Tool
	for name, t := range r.tools {
		if len(allow) > 0 && !allowed[name] {
			continue
		}
		_ = name
		out = append(out, t)
	}
	return out
}

// Execute dispatches a tool call with panic recovery and input
// validation. Panics become is_error tool_results so one bad tool
// cannot take down the whole agent loop.
func (r *Registry) Execute(ctx context.Context, name string, call ToolCall) (result ToolResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool %q panicked: %v\n%s", name, p, debug.Stack())
		}
	}()

	t := r.Get(name)
	if t == nil {
		return ToolResult{}, fmt.Errorf("unknown tool: %q", name)
	}
	if call.Input == nil {
		call.Input = map[string]any{}
	}

	// Lightweight structural JSON-schema validation — we don't pull in
	// a full gojsonschema dependency for this. We only enforce
	// "required" fields and the top-level type; deeper constraints are
	// the tool's responsibility (where they have full context).
	if err := validateSchemaBasics(t.JSONSchema(), call.Input); err != nil {
		return ToolResult{}, err
	}
	if err := enforceAuthority(ctx, t, &call); err != nil {
		return ToolResult{}, err
	}

	return t.Execute(ctx, call)
}

func validateSchemaBasics(schema map[string]any, input map[string]any) error {
	if schema == nil {
		return nil
	}
	req, _ := schema["required"].([]any)
	for _, r := range req {
		if key, ok := r.(string); ok {
			if _, present := input[key]; !present {
				return fmt.Errorf("missing required field %q", key)
			}
		}
	}
	props, _ := schema["properties"].(map[string]any)
	for k, v := range input {
		p, _ := props[k].(map[string]any)
		if p == nil {
			// Unknown field — we don't reject (models often include
			// extra descriptive fields) but we leave it to the tool.
			continue
		}
		if typ, _ := p["type"].(string); typ != "" {
			if !typeMatches(typ, v) {
				return fmt.Errorf("field %q: expected type %s, got %T", k, typ, v)
			}
		}
	}
	return nil
}

func typeMatches(typ string, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		switch v.(type) {
		case float64, int, int64, json.Number:
			return true
		}
		return false
	case "number":
		switch v.(type) {
		case float64, int, int64, json.Number:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "null":
		return v == nil
	}
	// Unknown type keyword — don't reject.
	return true
}
