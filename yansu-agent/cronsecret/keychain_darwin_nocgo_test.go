//go:build darwin && !cgo

package cronsecret

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinWithoutNativeKeychainFailsClosed(t *testing.T) {
	home := isolatedHome(t)
	legacy := filepath.Join(home, "cron-secret")
	if err := os.WriteFile(legacy, []byte("legacy-credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("non-native build did not fail closed on read: %v", err)
	}
	if err := Store(home, "replacement"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("non-native build did not fail closed on write: %v", err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != "legacy-credential" {
		t.Fatalf("unsupported Keychain build changed legacy data: %v", err)
	}
}
