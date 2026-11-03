package tools

import (
	"context"
	"testing"
)

type fakeTool struct {
	name     string
	schema   map[string]any
	execFn   func(ctx context.Context, call ToolCall) (ToolResult, error)
	needPerm bool
}

func (f *fakeTool) Name() string               { return f.name }
func (f *fakeTool) Description() string        { return "fake tool for tests" }
func (f *fakeTool) JSONSchema() map[string]any { return f.schema }
func (f *fakeTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	if f.needPerm {
		return true, PermissionPrompt{Reason: "test"}
	}
	return false, PermissionPrompt{}
}
func (f *fakeTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	if f.execFn != nil {
		return f.execFn(ctx, call)
	}
	return ToolResult{Content: "ok"}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "Read"})
	if r.Get("Read") == nil {
		t.Fatal("Get returned nil for Read")
	}
	if r.Get("Unknown") != nil {
		t.Fatal("Get should return nil for unknown tool")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "Read"})
	r.Register(&fakeTool{name: "Write"})
	r.Register(&fakeTool{name: "Bash"})

	all := r.List(nil)
	if len(all) != 3 {
		t.Errorf("List all = %d, want 3", len(all))
	}
	filtered := r.List([]string{"Read", "Grep"})
	if len(filtered) != 1 {
		t.Errorf("List filtered = %d, want 1", len(filtered))
	}
}

func TestRegistry_Execute_UnknownTool(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "Nonexistent", ToolCall{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRegistry_Execute_SchemaValidation(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"path"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
			"n":    map[string]any{"type": "integer"},
		},
	}
	r := NewRegistry()
	r.Register(&fakeTool{name: "Read", schema: schema})

	// Missing required field.
	_, err := r.Execute(context.Background(), "Read", ToolCall{Input: map[string]any{}})
	if err == nil {
		t.Error("expected error for missing required field")
	}

	// Wrong type.
	_, err = r.Execute(context.Background(), "Read", ToolCall{Input: map[string]any{"path": 123}})
	if err == nil {
		t.Error("expected error for wrong type")
	}

	// Correct input.
	_, err = r.Execute(context.Background(), "Read", ToolCall{Input: map[string]any{"path": "/tmp/x"}})
	if err != nil {
		t.Errorf("unexpected error for valid input: %v", err)
	}

	// Integer via float64 (JSON unmarshal default) accepted.
	_, err = r.Execute(context.Background(), "Read", ToolCall{Input: map[string]any{"path": "/tmp/x", "n": 5.0}})
	if err != nil {
		t.Errorf("unexpected error for float-encoded integer: %v", err)
	}
}

func TestRegistry_Execute_PanicRecovery(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "Explode", execFn: func(ctx context.Context, call ToolCall) (ToolResult, error) {
		panic("kaboom")
	}})
	_, err := r.Execute(context.Background(), "Explode", ToolCall{})
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
}

func TestRegistry_ReplaceGroup_AddsAndReplaces(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "Read"}) // built-in survives across replacements

	if err := r.ReplaceGroup("mcp__fs", []Tool{
		&fakeTool{name: "mcp__fs__list"},
		&fakeTool{name: "mcp__fs__read"},
	}); err != nil {
		t.Fatalf("first ReplaceGroup: %v", err)
	}
	if r.Get("mcp__fs__list") == nil || r.Get("mcp__fs__read") == nil {
		t.Fatal("expected both fs tools registered")
	}
	if r.Get("Read") == nil {
		t.Fatal("built-in Read should not be touched by ReplaceGroup")
	}

	// Second call with a different set: old members must disappear, new ones appear.
	if err := r.ReplaceGroup("mcp__fs", []Tool{
		&fakeTool{name: "mcp__fs__write"},
	}); err != nil {
		t.Fatalf("second ReplaceGroup: %v", err)
	}
	if r.Get("mcp__fs__list") != nil || r.Get("mcp__fs__read") != nil {
		t.Fatal("old group members should have been removed")
	}
	if r.Get("mcp__fs__write") == nil {
		t.Fatal("expected new fs tool registered")
	}
	if r.Get("Read") == nil {
		t.Fatal("built-in Read should still be present after replacement")
	}
}

func TestRegistry_ReplaceGroup_EmptyUnregisters(t *testing.T) {
	r := NewRegistry()
	if err := r.ReplaceGroup("mcp__git", []Tool{
		&fakeTool{name: "mcp__git__log"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := r.ReplaceGroup("mcp__git", nil); err != nil {
		t.Fatalf("empty replace: %v", err)
	}
	if r.Get("mcp__git__log") != nil {
		t.Fatal("group should be empty after replace with nil")
	}
}

func TestRegistry_ReplaceGroup_GroupsAreIsolated(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "Read"})

	if err := r.ReplaceGroup("mcp__a", []Tool{&fakeTool{name: "mcp__a__one"}}); err != nil {
		t.Fatalf("group a: %v", err)
	}
	if err := r.ReplaceGroup("mcp__b", []Tool{&fakeTool{name: "mcp__b__one"}}); err != nil {
		t.Fatalf("group b: %v", err)
	}
	// Replacing group a must not touch group b.
	if err := r.ReplaceGroup("mcp__a", nil); err != nil {
		t.Fatalf("clear a: %v", err)
	}
	if r.Get("mcp__b__one") == nil {
		t.Fatal("group b leaked across to group a clear")
	}
	if r.Get("Read") == nil {
		t.Fatal("built-in Read leaked across to group a clear")
	}
}

func TestRegistry_ReplaceGroup_InvalidPrefix(t *testing.T) {
	r := NewRegistry()
	cases := []string{"", "has space", "has/slash", "has.dot", "has:colon"}
	for _, p := range cases {
		if err := r.ReplaceGroup(p, nil); err == nil {
			t.Errorf("ReplaceGroup(%q) should have rejected the prefix", p)
		}
	}
}

func TestRegistry_ReplaceGroup_RejectsToolOutsideGroup(t *testing.T) {
	r := NewRegistry()
	err := r.ReplaceGroup("mcp__fs", []Tool{
		&fakeTool{name: "mcp__fs__list"},
		&fakeTool{name: "Read"}, // not in group
	})
	if err == nil {
		t.Fatal("expected rejection of tool outside the declared group")
	}
	if r.Get("mcp__fs__list") != nil {
		t.Fatal("partial registration leaked despite validation error")
	}
}

func TestTypeMatches(t *testing.T) {
	cases := []struct {
		typ  string
		v    any
		want bool
	}{
		{"string", "hi", true},
		{"string", 1, false},
		{"integer", 1.0, true},
		{"integer", "1", false},
		{"boolean", true, true},
		{"boolean", "true", false},
		{"array", []any{1, 2}, true},
		{"array", map[string]any{}, false},
		{"object", map[string]any{}, true},
		{"null", nil, true},
		{"null", 0, false},
		{"unknown", "whatever", true},
	}
	for _, c := range cases {
		if got := typeMatches(c.typ, c.v); got != c.want {
			t.Errorf("typeMatches(%s, %v) = %v, want %v", c.typ, c.v, got, c.want)
		}
	}
}
