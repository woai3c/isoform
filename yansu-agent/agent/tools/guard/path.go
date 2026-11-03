// Package guard holds the small but load-bearing safety helpers used by
// every tool in the native agent loop: path validation (so Read/Write/Edit
// can't escape the workspace), per-path mutex locks (so parallel Edits to
// the same file serialize), and the Bash command blacklist.
package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateAbsolutePath ensures path is absolute, cleaned, and does not
// contain `..` components. The refactor doc §4.3.2 mandates absolute
// paths for every Read/Write/Edit/Glob call so the model cannot trick
// the tool layer into resolving paths relative to some surprising cwd.
func ValidateAbsolutePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q must be an absolute path (e.g. /abs/path/to/file)", path)
	}
	cleaned := filepath.Clean(path)
	// Reject any lingering `..` after cleaning — this catches things like
	// `/a/../../../etc/passwd` where Clean would normalize but keep the
	// relative parent refs if the path doesn't have enough depth.
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path contains forbidden `..` components: %q", path)
	}
	return cleaned, nil
}

// CanonicalPath resolves every existing symlink component. For a path that
// does not exist yet, it resolves the nearest existing ancestor and then
// appends the missing suffix. This lets write authorization use the same
// canonical workspace identity as read authorization.
func CanonicalPath(path string) (string, error) {
	cleaned, err := ValidateAbsolutePath(path)
	if err != nil {
		return "", err
	}
	existing := cleaned
	missing := make([]string, 0)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(existing)
		if resolveErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", fmt.Errorf("resolve canonical path %q: %w", path, resolveErr)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("resolve canonical path %q: no existing ancestor", path)
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

// WithinAllowed returns nil when path is inside any of the allowed roots.
// Roots themselves must be absolute paths; relative roots will be treated
// as a misconfiguration and cause a hard error.
func WithinAllowed(path string, allowedRoots []string) error {
	cleaned, err := CanonicalPath(path)
	if err != nil {
		return err
	}
	// Empty allow list means no workspace restriction — callers pass this
	// for read-only probes where workspace scoping is irrelevant.
	if len(allowedRoots) == 0 {
		return nil
	}
	for _, root := range allowedRoots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("allowed root %q is not absolute (programming error)", root)
		}
		rootClean, canonicalErr := CanonicalPath(root)
		if canonicalErr != nil {
			return fmt.Errorf("canonicalize allowed root %q: %w", root, canonicalErr)
		}
		relative, relativeErr := filepath.Rel(rootClean, cleaned)
		if relativeErr != nil {
			continue
		}
		if relative == "." || relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside of the allowed workspace roots", path)
}
