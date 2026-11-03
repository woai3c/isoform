package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yansu-agent/agent/tools/guard"
)

const (
	readMaxFileSize     = 20 * 1024 * 1024 // 20 MB hard ceiling
	readDefaultLineCap  = 2000
	readLineNumberWidth = 6
)

// ReadTool implements the Read tool (refactor doc §4.3.2).
type ReadTool struct{}

// NewReadTool constructs a Read tool.
func NewReadTool() *ReadTool { return &ReadTool{} }

func (ReadTool) Name() string { return "Read" }

func (ReadTool) Description() string {
	return "Read a file from the local filesystem and return its contents with line numbers. " +
		"Supports text, images (returned as image blocks), and Jupyter notebooks. " +
		"The path must be absolute. For large files use the offset/limit parameters."
}

func (ReadTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"path"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file. Relative paths are rejected.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "1-indexed line number to start reading from (default 1).",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Maximum number of lines to return (default 2000).",
			},
		},
	}
}

func (ReadTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t ReadTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	if err := CheckProtectedPaths(t.Name(), call.Input, call.WorkDir); err != nil {
		return ToolResult{}, err
	}
	rawPath, _ := call.Input["path"].(string)
	path, err := guard.ValidateAbsolutePath(rawPath)
	if err != nil {
		return ToolResult{}, err
	}
	if call.WorkDir != "" {
		if err := guard.WithinAllowed(path, []string{call.WorkDir}); err != nil {
			return ToolResult{}, fmt.Errorf("%w (workspace: %s)", err, call.WorkDir)
		}
	}

	// Deliberately refuse to follow symlinks via Lstat — the tool spec
	// says "don't follow". If the target IS a symlink and it points
	// inside the allowed workspace we could allow, but we don't have
	// that context here so we bail conservatively.
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ToolResult{}, fmt.Errorf("file does not exist: %s", path)
		}
		return ToolResult{}, fmt.Errorf("stat: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ToolResult{}, fmt.Errorf("%s is a symbolic link; symlinks are not followed", path)
	}
	if info.IsDir() {
		return ToolResult{}, fmt.Errorf("%s is a directory, not a file", path)
	}
	if info.Size() > readMaxFileSize {
		return ToolResult{}, fmt.Errorf("file is too large (%d bytes > %d bytes); use Grep or split into chunks", info.Size(), readMaxFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("read: %w", err)
	}

	// Image handling: return a multi-block tool_result so vision works.
	if ext := strings.ToLower(filepath.Ext(path)); isImageExt(ext) {
		media := imageMediaType(ext)
		if media == "" {
			return ToolResult{}, fmt.Errorf("unsupported image type: %s", ext)
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		return ToolResult{
			Content: fmt.Sprintf("(image: %s, %d bytes)", media, len(data)),
			Blocks: []ContentBlock{
				{Type: "image", Source: &ImageSource{Type: "base64", MediaType: media, Data: encoded}},
				{Type: "text", Text: fmt.Sprintf("Image at %s (%s, %d bytes)", path, media, len(data))},
			},
		}, nil
	}

	if isBinary(data) {
		return ToolResult{}, fmt.Errorf("%s appears to be a binary file; Read only supports text and images", path)
	}

	// Strip a UTF-8 BOM if present (not included in the line-numbered
	// output — Edit is aligned with the same convention).
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	// Empty file: emit a sentinel rather than an empty string so the
	// model doesn't confuse "empty" with "read failed".
	if len(data) == 0 {
		return ToolResult{Content: "<file exists but is empty>"}, nil
	}

	offset, _ := asInt(call.Input["offset"])
	limit, _ := asInt(call.Input["limit"])
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = readDefaultLineCap
	}

	// Preserve original line endings — Edit relies on byte-level match.
	// We split on LF but preserve the original byte sequence per line
	// so CRLF files still round-trip.
	lines := splitPreservingEOL(data)
	total := len(lines)

	start := offset
	if offset > 0 {
		start = offset - 1 // interpret offset as 1-indexed
	}
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	truncated := total > limit && offset == 0

	var buf bytes.Buffer
	for i := start; i < end; i++ {
		fmt.Fprintf(&buf, "%*d\t%s", readLineNumberWidth, i+1, lines[i])
	}
	out := buf.String()
	if truncated {
		out += fmt.Sprintf("\n(file has %d lines; first %d shown — pass offset/limit to page)", total, end-start)
	}
	return ToolResult{Content: out}, nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

func splitPreservingEOL(data []byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i+1]))
			start = i + 1
		}
	}
	if start < len(data) {
		// Last line without trailing newline — append a newline so the
		// line-numbered output has consistent formatting. (Edit still
		// matches on the original bytes in the file.)
		lines = append(lines, string(data[start:])+"\n")
	}
	return lines
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

func imageMediaType(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	}
	return ""
}

// isBinary performs a rough heuristic: >30% non-text bytes in the
// first 4 KB means we treat it as binary. ASCII TAB, LF, CR, and
// printable chars (0x20-0x7E) plus UTF-8 leading bytes count as text.
func isBinary(data []byte) bool {
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if len(sample) == 0 {
		return false
	}
	nonText := 0
	for _, b := range sample {
		switch {
		case b == 0:
			return true // nulls are a strong binary signal
		case b == '\t' || b == '\n' || b == '\r':
			// fine
		case b < 0x20:
			nonText++
		case b == 0x7F:
			nonText++
		}
	}
	return nonText*10 > len(sample)*3 // >30%
}
