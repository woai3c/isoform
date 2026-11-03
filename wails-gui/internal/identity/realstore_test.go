package identity

import (
	"path/filepath"
	"testing"

	"wails-gui/storage"
)

// newRealStore returns a fresh on-disk storage.Store for the contract tests
// in resolver_test.go that need to round-trip through real SQL.
func newRealStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.New(filepath.Join(dir, "identity-test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
