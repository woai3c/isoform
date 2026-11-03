package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"yansu-agent/agent/tools/guard"
)

const grepDefaultTimeout = 60 * time.Second

// GrepTool wraps the system `rg` (ripgrep) binary after pruning protected
// files and directories from its traversal.
type GrepTool struct{}

// NewGrepTool returns a Grep tool.
func NewGrepTool() *GrepTool { return &GrepTool{} }

func (*GrepTool) Name() string { return "Grep" }

func (*GrepTool) Description() string {
	return "Search for a regex pattern in files (powered by ripgrep when installed). " +
		"Supports glob/type filters, context lines, and file/content output modes. " +
		"Defaults to case-sensitive search and skips .git/node_modules."
}

func (*GrepTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"pattern"},
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string"},
			"path":        map[string]any{"type": "string"},
			"glob":        map[string]any{"type": "string"},
			"type":        map[string]any{"type": "string"},
			"output_mode": map[string]any{"type": "string", "enum": []any{"content", "files_with_matches", "count"}},
			"-n":          map[string]any{"type": "boolean"},
			"-i":          map[string]any{"type": "boolean"},
			"-A":          map[string]any{"type": "integer"},
			"-B":          map[string]any{"type": "integer"},
			"-C":          map[string]any{"type": "integer"},
			"multiline":   map[string]any{"type": "boolean"},
			"head_limit":  map[string]any{"type": "integer"},
		},
	}
}

func (*GrepTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *GrepTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	if err := CheckProtectedPaths(t.Name(), call.Input, call.WorkDir); err != nil {
		return ToolResult{}, err
	}
	pattern, _ := call.Input["pattern"].(string)
	if pattern == "" {
		return ToolResult{}, fmt.Errorf("pattern is required")
	}
	path, _ := call.Input["path"].(string)
	if path == "" {
		path = call.WorkDir
	}
	cleaned, err := guard.ValidateAbsolutePath(path)
	if err != nil {
		return ToolResult{}, err
	}
	if call.WorkDir != "" {
		if err := guard.WithinAllowed(cleaned, []string{call.WorkDir}); err != nil {
			return ToolResult{}, fmt.Errorf("%w (workspace: %s)", err, call.WorkDir)
		}
	}
	path = cleaned

	mode, _ := call.Input["output_mode"].(string)
	if mode == "" {
		mode = "files_with_matches"
	}

	args := []string{"--no-config", "--no-follow", "--color=never"}
	switch mode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	case "content":
		// default content output
	}
	if v, _ := call.Input["-n"].(bool); v {
		args = append(args, "-n")
	}
	if v, _ := call.Input["-i"].(bool); v {
		args = append(args, "-i")
	}
	if v, ok := asInt(call.Input["-A"]); ok && v > 0 {
		args = append(args, fmt.Sprintf("-A%d", v))
	}
	if v, ok := asInt(call.Input["-B"]); ok && v > 0 {
		args = append(args, fmt.Sprintf("-B%d", v))
	}
	if v, ok := asInt(call.Input["-C"]); ok && v > 0 {
		args = append(args, fmt.Sprintf("-C%d", v))
	}
	if v, _ := call.Input["multiline"].(bool); v {
		args = append(args, "-U", "--multiline-dotall")
	}
	if glob, _ := call.Input["glob"].(string); glob != "" {
		args = append(args, "-g", glob)
	}
	if typ, _ := call.Input["type"].(string); typ != "" {
		args = append(args, "-t", typ)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, grepDefaultTimeout)
	defer cancel()
	exclusions, err := protectedGrepExclusions(cmdCtx, path, call.WorkDir)
	if err != nil {
		return ToolResult{}, fmt.Errorf("check search paths: %w", err)
	}
	for _, glob := range exclusions {
		args = append(args, "-g", glob)
	}

	args = append(args, "--", pattern)
	if path != "" {
		args = append(args, path)
	}

	ec := exec.CommandContext(cmdCtx, "rg", args...)
	var out bytes.Buffer
	ec.Stdout = &out
	ec.Stderr = &out

	err = ec.Run()
	if err != nil {
		// rg exits 1 when no matches — that's not an error for us.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return ToolResult{Content: "(no matches)"}, nil
		}
		// rg not installed? fallback will signal a different error.
		if strings.Contains(err.Error(), "executable file not found") {
			return ToolResult{}, fmt.Errorf("ripgrep (rg) is not installed; please install it or use Glob+Read")
		}
		return ToolResult{}, fmt.Errorf("rg: %v (%s)", err, out.String())
	}

	headLimit, _ := asInt(call.Input["head_limit"])
	if headLimit <= 0 {
		headLimit = 200
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) > headLimit {
		truncated := strings.Join(lines[:headLimit], "\n")
		truncated += fmt.Sprintf("\n(output truncated at head_limit=%d lines)", headLimit)
		return ToolResult{Content: truncated}, nil
	}
	return ToolResult{Content: out.String()}, nil
}
