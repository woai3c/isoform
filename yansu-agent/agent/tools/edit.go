package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"yansu-agent/agent/tools/guard"
)

// EditTool implements the Edit tool with multi-level fuzzy matching
// (see edit_match.go for the level descriptions).
type EditTool struct {
	locks *guard.FileLockManager
}

// NewEditTool constructs an Edit tool. Pass the same FileLockManager
// as Write so concurrent writes to the same file serialize.
func NewEditTool(locks *guard.FileLockManager) *EditTool {
	if locks == nil {
		locks = guard.NewFileLockManager(0)
	}
	return &EditTool{locks: locks}
}

func (EditTool) Name() string { return "Edit" }

func (EditTool) Description() string {
	return "Edit a file by replacing exact occurrences of old_string with new_string. " +
		"The old_string must match exactly, including whitespace. Set replace_all=true to " +
		"replace every occurrence; otherwise old_string must be unique. Do not include the " +
		"line-number prefix (\"NNN\\t\") emitted by Read."
}

func (EditTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"path", "old_string", "new_string"},
		"properties": map[string]any{
			"path":        map[string]any{"type": "string", "description": "Absolute path."},
			"old_string":  map[string]any{"type": "string", "description": "Exact text to replace."},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text."},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence."},
		},
	}
}

func (EditTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *EditTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	if err := CheckProtectedPaths(t.Name(), call.Input, call.WorkDir); err != nil {
		return ToolResult{}, err
	}
	if err := enforcePersistenceApproval(ctx, t.Name(), call); err != nil {
		return ToolResult{}, err
	}
	rawPath, _ := call.Input["path"].(string)
	path, err := guard.ValidateAbsolutePath(rawPath)
	if err != nil {
		return ToolResult{}, err
	}
	// Confine edits to the session WorkDir for the same reason Write
	// is confined — see fs_write.go for the full rationale.
	if call.WorkDir != "" {
		if err := guard.WithinAllowed(path, []string{call.WorkDir}); err != nil {
			return ToolResult{}, fmt.Errorf("%w (workspace: %s)", err, call.WorkDir)
		}
	}
	oldStr, _ := call.Input["old_string"].(string)
	newStr, _ := call.Input["new_string"].(string)
	replaceAll, _ := call.Input["replace_all"].(bool)

	if oldStr == "" {
		return ToolResult{}, fmt.Errorf("old_string is empty; use Write to create or overwrite a file")
	}
	if oldStr == newStr {
		return ToolResult{}, fmt.Errorf("old_string and new_string are identical (no-op)")
	}

	lock := t.locks.Lock(path)
	lock.Lock()
	defer lock.Unlock()

	original, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("read: %w", err)
	}
	// BOM retention policy: we strip BOM during matching (via L3) but
	// preserve it at write-time. So we split the BOM off up front.
	bom := []byte{}
	body := original
	if len(body) >= 3 && body[0] == 0xEF && body[1] == 0xBB && body[2] == 0xBF {
		bom = body[:3]
		body = body[3:]
	}
	fileStr := string(body)

	matches, level, err := FindMatches(fileStr, oldStr)
	if err != nil {
		if err == ErrLinePrefix {
			return ToolResult{}, err
		}
		return ToolResult{}, fmt.Errorf("no matching region for old_string in %s: %w", path, err)
	}

	// Disambiguation.
	if len(matches) > 1 && !replaceAll {
		return ToolResult{}, fmt.Errorf("found %d matches for old_string at level %s; pass replace_all=true or disambiguate", len(matches), level)
	}

	// Apply.
	var newBody string
	warnings := []string{}
	switch level {
	case MatchL4, MatchL6:
		// Reindent new_string per match.
		if replaceAll {
			newBody = applyAll(fileStr, matches, func(m MatchResult) string {
				return ReindentNewString(newStr, m.ReplaceWith)
			})
		} else {
			m := matches[0]
			m.ReplaceWith = ReindentNewString(newStr, m.ReplaceWith)
			newBody = ApplyMatch(fileStr, m)
		}
		warnings = append(warnings, fmt.Sprintf("fuzzy match at %s; please verify the replacement preserves the correct indentation", level))
	case MatchL5:
		// Whitespace-loose match — replace the original span with new_string
		// as-is. The caller pre-verified the rewrite is intentional by
		// issuing the Edit; we warn about the loose match.
		if replaceAll {
			newBody = applyAll(fileStr, matches, func(m MatchResult) string { return newStr })
		} else {
			m := matches[0]
			m.ReplaceWith = newStr
			newBody = ApplyMatch(fileStr, m)
		}
		warnings = append(warnings, fmt.Sprintf("fuzzy match at %s (whitespace-insensitive); please verify the replacement", level))
	case MatchL2:
		// The matches index into the normalized-EOL file. Switch file
		// to the normalized form to keep offsets coherent.
		fileStr = normalizeEOL(fileStr)
		if replaceAll {
			newBody = applyAll(fileStr, matches, func(m MatchResult) string { return newStr })
		} else {
			m := matches[0]
			m.ReplaceWith = newStr
			newBody = ApplyMatch(fileStr, m)
		}
		warnings = append(warnings, "EOL normalized to LF during match")
	case MatchL3:
		// Match mapped onto the stripped-BOM file; offsets are coherent
		// with fileStr.
		if replaceAll {
			newBody = applyAll(fileStr, matches, func(m MatchResult) string { return newStr })
		} else {
			m := matches[0]
			m.ReplaceWith = newStr
			newBody = ApplyMatch(fileStr, m)
		}
		warnings = append(warnings, fmt.Sprintf("fuzzy match at %s (trailing whitespace ignored)", level))
	default: // L1
		if replaceAll {
			newBody = strings.ReplaceAll(fileStr, oldStr, newStr)
		} else {
			m := matches[0]
			m.ReplaceWith = newStr
			newBody = ApplyMatch(fileStr, m)
		}
	}

	out := append([]byte{}, bom...)
	out = append(out, newBody...)

	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}

	tmp, err := tempPath(path)
	if err != nil {
		return ToolResult{}, err
	}
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return ToolResult{}, fmt.Errorf("rename: %w", err)
	}

	countMsg := "1 occurrence replaced"
	if replaceAll && len(matches) > 1 {
		countMsg = fmt.Sprintf("%d occurrences replaced", len(matches))
	}
	return ToolResult{
		Content:  fmt.Sprintf("%s in %s (match_level=%s)", countMsg, path, level),
		Warnings: warnings,
		Metadata: map[string]any{
			"match_level":    string(level),
			"replaced_count": len(matches),
			"old_size":       len(original),
			"new_size":       len(out),
		},
	}, nil
}

// applyAll replaces all non-overlapping matches in file, building the
// output via a buffered walk. The rewriter is called once per match so
// indent-sensitive levels can customize per-match.
func applyAll(file string, matches []MatchResult, rewrite func(MatchResult) string) string {
	var b bytes.Buffer
	b.Grow(len(file))
	prev := 0
	for _, m := range matches {
		if m.Start < prev {
			// Overlapping match — shouldn't happen given how FindMatches
			// skips past each hit, but skip defensively.
			continue
		}
		b.WriteString(file[prev:m.Start])
		b.WriteString(rewrite(m))
		prev = m.End
	}
	b.WriteString(file[prev:])
	return b.String()
}
