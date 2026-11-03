package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Guard fixture homes must not inherit the system /tmp write exemption.
// Keep them under the checkout, or an explicitly configured fixture root.
func guardTestHomeDir(t *testing.T) string {
	t.Helper()
	tempRoot := os.Getenv("ISOFORM_TEST_FIXTURE_ROOT")
	if tempRoot == "" {
		var err error
		tempRoot, err = os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
	}
	tempRoot, err := filepath.Abs(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(tempRoot); err == nil {
		tempRoot = resolved
	}
	if tempRoot == "/tmp" || strings.HasPrefix(tempRoot, "/tmp/") || tempRoot == "/private/tmp" || strings.HasPrefix(tempRoot, "/private/tmp/") {
		t.Skip("guard fixtures must live outside /tmp; set ISOFORM_TEST_FIXTURE_ROOT to a non-temporary directory")
	}
	home, err := os.MkdirTemp(tempRoot, "iso-guard-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	return home
}
