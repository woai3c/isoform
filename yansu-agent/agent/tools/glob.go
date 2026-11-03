package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"yansu-agent/agent/tools/guard"
)

// GlobTool finds files matching a doublestar-capable pattern.
type GlobTool struct{}

// NewGlobTool returns a Glob tool.
func NewGlobTool() *GlobTool { return &GlobTool{} }

func (*GlobTool) Name() string { return "Glob" }

func (*GlobTool) Description() string {
	return "Find files matching a doublestar glob pattern (e.g. **/*.go). " +
		"Results are sorted by mtime (most recent first) and capped at 1000 entries."
}

func (*GlobTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"pattern"},
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string", "description": "absolute root; defaults to work dir"},
		},
	}
}

func (*GlobTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *GlobTool) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	if err := CheckProtectedPaths(t.Name(), call.Input, call.WorkDir); err != nil {
		return ToolResult{}, err
	}
	pattern, _ := call.Input["pattern"].(string)
	if pattern == "" {
		return ToolResult{}, fmt.Errorf("pattern is required")
	}
	if strings.Contains(pattern, "..") {
		return ToolResult{}, fmt.Errorf("pattern may not contain `..`")
	}

	root, _ := call.Input["path"].(string)
	if root == "" {
		root = call.WorkDir
	}
	cleaned, err := guard.ValidateAbsolutePath(root)
	if err != nil {
		return ToolResult{}, err
	}
	if call.WorkDir != "" {
		if err := guard.WithinAllowed(cleaned, []string{call.WorkDir}); err != nil {
			return ToolResult{}, fmt.Errorf("%w (workspace: %s)", err, call.WorkDir)
		}
	}

	fsys := newProtectedSearchFS(cleaned, call.WorkDir)
	matches, err := doublestar.Glob(fsys, pattern, doublestar.WithNoFollow())
	if err != nil {
		return ToolResult{}, fmt.Errorf("glob: %w", err)
	}

	type entry struct {
		path string
		mod  int64
	}
	list := make([]entry, 0, len(matches))
	for _, rel := range matches {
		full := filepath.Join(cleaned, rel)
		info, err := os.Lstat(full)
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		list = append(list, entry{path: full, mod: info.ModTime().UnixNano()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod > list[j].mod })

	if len(list) > 1000 {
		list = list[:1000]
	}
	var b strings.Builder
	for _, e := range list {
		b.WriteString(e.path)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return ToolResult{Content: "(no matches)"}, nil
	}
	return ToolResult{Content: b.String()}, nil
}
