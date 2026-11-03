package gate

import (
	"path/filepath"
	"testing"
)

// realTempDir is t.TempDir() with symlinks resolved: on macOS the temp folder
// lives under /var (a symlink to /private/var) and authority grants compare
// the resolved path of a resource with the grant's prefix, so a fixture built
// on the unresolved path is denied.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
