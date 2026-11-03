package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"yansu-agent/agent/tools/guard"
)

const writeMaxBytes = 1 * 1024 * 1024 // 1 MB per Write call

// WriteTool overwrites an entire file, creating parent dirs as needed.
type WriteTool struct {
	locks *guard.FileLockManager
}

// NewWriteTool constructs a Write tool that shares a file-lock manager
// with Edit to serialize concurrent writes to the same path.
func NewWriteTool(locks *guard.FileLockManager) *WriteTool {
	if locks == nil {
		locks = guard.NewFileLockManager(0)
	}
	return &WriteTool{locks: locks}
}

func (WriteTool) Name() string { return "Write" }

func (WriteTool) Description() string {
	return "Write content to a file (overwriting if it exists). Parent directories are created automatically. " +
		"The path must be absolute. To modify part of an existing file, prefer Edit."
}

func (WriteTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"path", "content"},
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Absolute path."},
			"content": map[string]any{"type": "string", "description": "Full file contents."},
		},
	}
}

func (WriteTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *WriteTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
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
	// When the loop sets a WorkDir, confine writes to that workspace so
	// the model can't silently land files in a sibling directory it
	// guessed from app name. Empty WorkDir = legacy/test caller, no
	// confinement (matches WithinAllowed's empty-roots semantics).
	if call.WorkDir != "" {
		if err := guard.WithinAllowed(path, []string{call.WorkDir}); err != nil {
			return ToolResult{}, fmt.Errorf("%w (workspace: %s)", err, call.WorkDir)
		}
	}
	content, _ := call.Input["content"].(string)
	if len(content) > writeMaxBytes {
		return ToolResult{}, fmt.Errorf("content is too large (%d bytes > %d bytes); use Edit for partial updates", len(content), writeMaxBytes)
	}
	if bytes.IndexByte([]byte(content), 0) >= 0 {
		return ToolResult{}, fmt.Errorf("content contains a NUL byte; Write rejects binary input")
	}

	lock := t.locks.Lock(path)
	lock.Lock()
	defer lock.Unlock()

	// Check existence for the result message — and also preserve CRLF
	// style when overwriting a CRLF file with LF-only content.
	existing, readErr := os.ReadFile(path)
	existed := readErr == nil
	if existed && bytes.Contains(existing, []byte("\r\n")) && !bytes.Contains([]byte(content), []byte("\r\n")) {
		// Reflow to match the file's existing EOL style.
		content = eolNormalizeToCRLF(content)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{}, fmt.Errorf("mkdir parents: %w", err)
	}

	tmp, err := tempPath(path)
	if err != nil {
		return ToolResult{}, err
	}

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return ToolResult{}, fmt.Errorf("create tmp: %w", err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return ToolResult{}, fmt.Errorf("write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return ToolResult{}, fmt.Errorf("fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return ToolResult{}, fmt.Errorf("close tmp: %w", err)
	}

	// If ctx was cancelled before rename, clean up the tmp file.
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmp)
		return ToolResult{}, err
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return ToolResult{}, fmt.Errorf("rename: %w", err)
	}

	if existed {
		return ToolResult{
			Content:  fmt.Sprintf("File overwritten: %s (%d bytes)", path, len(content)),
			Metadata: map[string]any{"existed": true, "new_size": len(content), "old_size": len(existing)},
		}, nil
	}
	return ToolResult{
		Content:  fmt.Sprintf("File created: %s (%d bytes)", path, len(content)),
		Metadata: map[string]any{"existed": false, "new_size": len(content)},
	}, nil
}

func tempPath(path string) (string, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return fmt.Sprintf("%s.tmp-%s", path, hex.EncodeToString(nonce)), nil
}

func eolNormalizeToCRLF(s string) string {
	// Idempotent: first normalize to LF, then expand to CRLF.
	lf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			continue
		}
		lf = append(lf, s[i])
	}
	out := make([]byte, 0, len(lf)+16)
	for _, b := range lf {
		if b == '\n' {
			out = append(out, '\r', '\n')
			continue
		}
		out = append(out, b)
	}
	return string(out)
}
