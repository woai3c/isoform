package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	yansutools "yansu-agent/agent/tools"
)

// fakeCaller implements ToolCaller for unit tests so we can exercise
// the adapter without spawning subprocesses. Each call records its
// arguments; the response/err pair is whatever the test sets.
type fakeCaller struct {
	resp     *mcp.CallToolResult
	err      error
	lastID   string
	lastName string
	lastArgs map[string]any
	calls    int
}

func (f *fakeCaller) CallTool(_ context.Context, id, name string, args map[string]any) (*mcp.CallToolResult, error) {
	f.calls++
	f.lastID = id
	f.lastName = name
	f.lastArgs = args
	return f.resp, f.err
}

func sampleTool() mcp.Tool {
	return mcp.NewTool("echo",
		mcp.WithDescription("echo the text back"),
		mcp.WithString("text", mcp.Required(), mcp.Description("text to echo")),
	)
}

func TestAdapter_NameAndSchema(t *testing.T) {
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), &fakeCaller{}, false)
	if got := a.Name(); got != "mcp__fs__echo" {
		t.Errorf("Name = %q, want mcp__fs__echo", got)
	}
	schema := a.JSONSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil || props["text"] == nil {
		t.Errorf("expected schema.properties.text in %v", schema)
	}
}

func TestAdapter_NeedsPermissionByDefault(t *testing.T) {
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), &fakeCaller{}, false)
	need, prompt := a.NeedsPermission(map[string]any{"text": "hi"})
	if !need {
		t.Error("untrusted MCP tool should require permission")
	}
	if !strings.Contains(prompt.Reason, "echo") {
		t.Errorf("prompt should mention tool name; got %q", prompt.Reason)
	}
	if prompt.Prefix != "mcp__fs__" {
		t.Errorf("prompt.Prefix = %q, want mcp__fs__", prompt.Prefix)
	}
}

func TestAdapter_TrustedSkipsPermission(t *testing.T) {
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), &fakeCaller{}, true)
	need, _ := a.NeedsPermission(nil)
	if need {
		t.Error("trusted MCP tool should not require permission")
	}
}

func TestAdapter_ExecuteSuccessReturnsContent(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "hello world"},
			},
		},
	}
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), caller, false)
	res, err := a.Execute(context.Background(), yansutools.ToolCall{
		Input: map[string]any{"text": "hi"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Content != "hello world" {
		t.Errorf("Content = %q, want 'hello world'", res.Content)
	}
	if caller.lastID != "fs" || caller.lastName != "echo" {
		t.Errorf("caller dispatch wrong: id=%s name=%s", caller.lastID, caller.lastName)
	}
}

func TestAdapter_ExecuteCallerErrorBecomesIsError(t *testing.T) {
	caller := &fakeCaller{err: errors.New("transport closed")}
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), caller, false)
	_, err := a.Execute(context.Background(), yansutools.ToolCall{Input: map[string]any{}})
	if err == nil {
		t.Fatal("expected error so registry maps to is_error=true")
	}
	if !strings.Contains(err.Error(), "transport closed") {
		t.Errorf("error should wrap caller error; got %v", err)
	}
}

func TestAdapter_ExecuteIsErrorResponseStillPropagates(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "tool said no"},
			},
		},
	}
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), caller, false)
	res, err := a.Execute(context.Background(), yansutools.ToolCall{Input: map[string]any{}})
	if err == nil {
		t.Error("IsError=true response should produce an error so the registry marks is_error")
	}
	if !strings.Contains(res.Content, "tool said no") {
		t.Errorf("error content should still surface; got %q", res.Content)
	}
}

func TestAdapter_ExecuteMixedContentProducesBlocks(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "see image"},
				mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "base64bytes"},
			},
		},
	}
	a := NewToolAdapter("fs", AdapterToolName("fs", "echo"), sampleTool(), caller, false)
	res, err := a.Execute(context.Background(), yansutools.ToolCall{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "see image") {
		t.Errorf("text portion missing: %q", res.Content)
	}
	if len(res.Blocks) == 0 {
		t.Fatal("expected non-text Blocks for image content")
	}
	hasImage := false
	for _, b := range res.Blocks {
		if b.Type == "image" {
			hasImage = true
			if b.Source == nil || b.Source.MediaType != "image/png" {
				t.Errorf("image block missing source/mediaType: %+v", b)
			}
		}
	}
	if !hasImage {
		t.Error("no image block in result")
	}
}

func TestBridge_RegisterAndUnregister(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"fs": {sampleTool()},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)

	if err := br.Register("fs"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registry.Get("mcp__fs__echo") == nil {
		t.Fatal("echo tool not registered in agent registry")
	}

	if err := br.Unregister("fs"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if registry.Get("mcp__fs__echo") != nil {
		t.Fatal("Unregister did not remove the tool")
	}
}

func TestBridge_DefaultExposureModeRegistersDirectTools(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"large-server": {
			mcp.NewTool("read-file", mcp.WithDescription("read a file")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)

	if err := br.Register("large-server"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registry.Get("mcp__large_server__read_file") == nil {
		t.Fatal("default exposure mode should still register direct upstream tools")
	}
	if registry.Get("mcp__large_server__search_tools") != nil {
		t.Fatal("gateway search tool should not be registered in default direct mode")
	}
}

func TestBridge_GatewayExposureRegistersOnlyFacadeTools(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"large-server": {
			mcp.NewTool("read-file", mcp.WithDescription("read a file")),
			mcp.NewTool("write-file", mcp.WithDescription("write a file")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.SetExposureMode("large-server", ExposureModeGateway); err != nil {
		t.Fatalf("SetExposureMode: %v", err)
	}

	if err := br.Register("large-server"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registry.Get("mcp__large_server__search_tools") == nil {
		t.Fatal("gateway search tool not registered")
	}
	if registry.Get("mcp__large_server__describe_tool") == nil {
		t.Fatal("gateway describe tool not registered")
	}
	if registry.Get("mcp__large_server__call_tool") == nil {
		t.Fatal("gateway call tool not registered")
	}
	if registry.Get("mcp__large_server__read_file") != nil {
		t.Fatal("upstream tool should not be directly registered in gateway mode")
	}
	if got := len(registry.List(nil)); got != 3 {
		t.Fatalf("registered tool count = %d, want exactly 3 gateway tools", got)
	}
}

func TestGatewaySearchToolsOmitsInputSchema(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"fs": {
			mcp.NewTool("read-file",
				mcp.WithDescription("read a file"),
				mcp.WithString("path", mcp.Required(), mcp.Description("path to read")),
			),
			mcp.NewTool("list-dir", mcp.WithDescription("list directory entries")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.SetExposureMode("fs", ExposureModeGateway); err != nil {
		t.Fatalf("SetExposureMode: %v", err)
	}
	if err := br.Register("fs"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := registry.Execute(context.Background(), "mcp__fs__search_tools", yansutools.ToolCall{
		Input: map[string]any{"query": "read", "limit": 5},
	})
	if err != nil {
		t.Fatalf("search_tools Execute: %v", err)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(res.Content), &results); err != nil {
		t.Fatalf("search_tools returned invalid JSON %q: %v", res.Content, err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
	if results[0]["name"] != "read-file" {
		t.Fatalf("first search result name = %v, want read-file", results[0]["name"])
	}
	if _, ok := results[0]["input_schema"]; ok {
		t.Fatalf("search result leaked input_schema: %v", results[0])
	}
	if _, ok := results[0]["schema"]; ok {
		t.Fatalf("search result leaked schema: %v", results[0])
	}
}

func TestGatewayDescribeToolReturnsSchema(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"fs": {
			mcp.NewTool("read-file",
				mcp.WithDescription("read a file"),
				mcp.WithString("path", mcp.Required(), mcp.Description("path to read")),
			),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.SetExposureMode("fs", ExposureModeGateway); err != nil {
		t.Fatalf("SetExposureMode: %v", err)
	}
	if err := br.Register("fs"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := registry.Execute(context.Background(), "mcp__fs__describe_tool", yansutools.ToolCall{
		Input: map[string]any{"tool_name": "read-file"},
	})
	if err != nil {
		t.Fatalf("describe_tool Execute: %v", err)
	}
	var described map[string]any
	if err := json.Unmarshal([]byte(res.Content), &described); err != nil {
		t.Fatalf("describe_tool returned invalid JSON %q: %v", res.Content, err)
	}
	if described["name"] != "read-file" {
		t.Fatalf("described name = %v, want read-file", described["name"])
	}
	if described["input_schema"] == nil {
		t.Fatalf("describe_tool should include input_schema: %v", described)
	}
}

func TestGatewayCallToolDispatchesOriginalUpstreamName(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}},
		},
	}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"upstash-context7": {
			mcp.NewTool("resolve-library-id", mcp.WithDescription("resolve")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.SetExposureMode("upstash-context7", ExposureModeGateway); err != nil {
		t.Fatalf("SetExposureMode: %v", err)
	}
	if err := br.Register("upstash-context7"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := registry.Execute(context.Background(), "mcp__upstash_context7__call_tool", yansutools.ToolCall{
		Input: map[string]any{
			"tool_name": "resolve-library-id",
			"args":      map[string]any{"libraryName": "react"},
		},
	})
	if err != nil {
		t.Fatalf("call_tool Execute: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("Content = %q, want ok", res.Content)
	}
	if caller.lastID != "upstash-context7" {
		t.Fatalf("server id sent on wire = %q, want upstash-context7", caller.lastID)
	}
	if caller.lastName != "resolve-library-id" {
		t.Fatalf("tool name sent on wire = %q, want resolve-library-id", caller.lastName)
	}
	if caller.lastArgs["libraryName"] != "react" {
		t.Fatalf("args sent on wire = %v, want libraryName=react", caller.lastArgs)
	}
}

func TestBridge_RegisterReplacesPriorTools(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"git": {
			mcp.NewTool("log", mcp.WithDescription("log")),
			mcp.NewTool("blame", mcp.WithDescription("blame")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)

	if err := br.Register("git"); err != nil {
		t.Fatalf("Register #1: %v", err)
	}
	if registry.Get("mcp__git__log") == nil || registry.Get("mcp__git__blame") == nil {
		t.Fatal("first registration incomplete")
	}

	// Server hot-reloads with a different tool set.
	mgr.tools["git"] = []mcp.Tool{mcp.NewTool("status", mcp.WithDescription("status"))}
	if err := br.Register("git"); err != nil {
		t.Fatalf("Register #2: %v", err)
	}
	if registry.Get("mcp__git__log") != nil || registry.Get("mcp__git__blame") != nil {
		t.Fatal("old tools should be removed after second Register")
	}
	if registry.Get("mcp__git__status") == nil {
		t.Fatal("new tool missing after second Register")
	}
}

func TestSanitizeToolNameSegment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"echo", "echo"},
		{"file.read", "file_read"},
		{"do/thing", "do_thing"},
		{"foo:bar", "foo_bar"},
		{"with space", "with_space"},
		{"unicode_crab", "unicode_crab"},
		{"", "_"},
		{"keep_under-score", "keep_under_score"},
		{"resolve-library-id", "resolve_library_id"},
	}
	for _, tc := range cases {
		if got := sanitizeToolNameSegment(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildExposedNameRespectsAnthropicLimit(t *testing.T) {
	long := strings.Repeat("a", 200)
	name := buildExposedName("svr", long)
	if len(name) > anthropicToolNameLimit {
		t.Errorf("exposed name length %d exceeds limit %d (%q)", len(name), anthropicToolNameLimit, name)
	}
	if !strings.HasPrefix(name, "mcp__svr__") {
		t.Errorf("prefix dropped after truncation: %q", name)
	}
	// Anthropic regex: ^[a-zA-Z0-9_-]{1,128}$
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			t.Errorf("disallowed rune %q in exposed name %q", r, name)
		}
	}
}

func TestResolveUniqueExposedNameDedupsCollisions(t *testing.T) {
	taken := map[string]bool{}
	a := resolveUniqueExposedName("svr", "do.thing", taken)
	b := resolveUniqueExposedName("svr", "do_thing", taken) // sanitises to same
	c := resolveUniqueExposedName("svr", "do/thing", taken) // ditto
	if a == b || b == c || a == c {
		t.Errorf("expected unique names, got %q %q %q", a, b, c)
	}
	if a != "mcp__svr__do_thing" {
		t.Errorf("primary name = %q, want mcp__svr__do_thing", a)
	}
	if !strings.HasPrefix(b, "mcp__svr__do_thing") {
		t.Errorf("dedup variant lost the base: %q", b)
	}
}

func TestBridge_SanitizesAdvertisedToolNames(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}},
		},
	}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"fs": {
			mcp.NewTool("file.read", mcp.WithDescription("read a file")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.Register("fs"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// The exposed name MUST be sanitized; the dotted form is invalid
	// in the Anthropic API and must never reach the registry.
	if registry.Get("mcp__fs__file_read") == nil {
		t.Fatal("sanitized tool name not registered")
	}
	if registry.Get("mcp__fs__file.read") != nil {
		t.Fatal("dotted tool name should not be exposed to the registry")
	}

	// Calling the registered tool must dispatch the *original* name to
	// the MCP server, otherwise the server won't recognise the tool.
	tool := registry.Get("mcp__fs__file_read")
	if _, err := tool.Execute(context.Background(), yansutools.ToolCall{
		Input: map[string]any{"x": 1},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if caller.lastName != "file.read" {
		t.Errorf("server received name %q, want file.read (the original)", caller.lastName)
	}
}

// TestBridge_HyphenatedNamesExposedAsUnderscored guards the LLM
// dispatch invariant: when a server id or tool name contains hyphens
// (e.g. "upstash-context7" exposing "resolve-library-id"), the
// model-visible name must be all-underscore. LLMs auto-correct hyphens
// to underscores in tool_use blocks, and the registry's exact-match
// lookup would then 404, triggering retry storms. The original names
// must still be used when calling the upstream MCP server.
func TestBridge_HyphenatedNamesExposedAsUnderscored(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}},
		},
	}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"upstash-context7": {
			mcp.NewTool("resolve-library-id", mcp.WithDescription("resolve")),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.Register("upstash-context7"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if registry.Get("mcp__upstash_context7__resolve_library_id") == nil {
		t.Fatal("underscore-normalised name not registered")
	}
	if registry.Get("mcp__upstash-context7__resolve-library-id") != nil {
		t.Fatal("hyphenated name must not be exposed: LLMs will auto-correct it")
	}

	tool := registry.Get("mcp__upstash_context7__resolve_library_id")
	if _, err := tool.Execute(context.Background(), yansutools.ToolCall{
		Input: map[string]any{"libraryName": "react"},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if caller.lastID != "upstash-context7" {
		t.Errorf("server id sent on wire = %q, want upstash-context7", caller.lastID)
	}
	if caller.lastName != "resolve-library-id" {
		t.Errorf("tool name sent on wire = %q, want resolve-library-id", caller.lastName)
	}
}

func TestBridge_RegistryExecuteDispatchesNativeMCPTool(t *testing.T) {
	caller := &fakeCaller{
		resp: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "thought accepted"}},
		},
	}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"sequential-thinking": {
			mcp.NewTool("sequentialthinking",
				mcp.WithDescription("dynamic reflective problem solving"),
				mcp.WithString("thought", mcp.Required()),
				mcp.WithNumber("thoughtNumber", mcp.Required()),
				mcp.WithNumber("totalThoughts", mcp.Required()),
				mcp.WithBoolean("nextThoughtNeeded", mcp.Required()),
			),
		},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)
	if err := br.Register("sequential-thinking"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := registry.Execute(context.Background(), "mcp__sequential_thinking__sequentialthinking", yansutools.ToolCall{
		Input: map[string]any{
			"thought":           "How should we validate this MCP chain?",
			"thoughtNumber":     1,
			"totalThoughts":     3,
			"nextThoughtNeeded": true,
		},
	})
	if err != nil {
		t.Fatalf("Registry.Execute: %v", err)
	}
	if res.Content != "thought accepted" {
		t.Fatalf("Content = %q, want thought accepted", res.Content)
	}
	if caller.calls != 1 {
		t.Fatalf("caller invoked %d times, want 1", caller.calls)
	}
	if caller.lastID != "sequential-thinking" {
		t.Errorf("server id sent on wire = %q, want sequential-thinking", caller.lastID)
	}
	if caller.lastName != "sequentialthinking" {
		t.Errorf("tool name sent on wire = %q, want sequentialthinking", caller.lastName)
	}
}

func TestBridge_GroupPrefixIsolation(t *testing.T) {
	caller := &fakeCaller{}
	mgr := newBridgeFakeManager(map[string][]mcp.Tool{
		"a": {mcp.NewTool("one")},
		"b": {mcp.NewTool("one")},
	}, caller)
	registry := yansutools.NewRegistry()
	br := NewBridge(mgr, registry)

	if err := br.Register("a"); err != nil {
		t.Fatalf("Register a: %v", err)
	}
	if err := br.Register("b"); err != nil {
		t.Fatalf("Register b: %v", err)
	}
	// Unregistering a must not touch b even though they share the
	// "mcp__" stem.
	if err := br.Unregister("a"); err != nil {
		t.Fatalf("Unregister a: %v", err)
	}
	if registry.Get("mcp__b__one") == nil {
		t.Fatal("Unregistering a leaked into b")
	}
}

// bridgeFakeManager fakes the tiny subset of *Manager that Bridge
// uses (Tools, RefreshTools, CallTool). It's distinct from the
// production Manager so adapter tests don't have to spawn helpers.
type bridgeFakeManager struct {
	tools  map[string][]mcp.Tool
	caller *fakeCaller
}

func newBridgeFakeManager(t map[string][]mcp.Tool, caller *fakeCaller) *bridgeFakeManager {
	return &bridgeFakeManager{tools: t, caller: caller}
}

// Tools satisfies the bridge contract by returning the cached fake set.
func (b *bridgeFakeManager) Tools(id string) ([]mcp.Tool, error) {
	tools, ok := b.tools[id]
	if !ok {
		return nil, errors.New("not managed")
	}
	return tools, nil
}

func (b *bridgeFakeManager) RefreshTools(ctx context.Context, id string) ([]mcp.Tool, error) {
	return b.Tools(id)
}

func (b *bridgeFakeManager) CallTool(ctx context.Context, id, name string, args map[string]any) (*mcp.CallToolResult, error) {
	return b.caller.CallTool(ctx, id, name, args)
}
