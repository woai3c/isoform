//go:build !darwin

package cronsecret

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileFallbackIsPrivateAndReadable(t *testing.T) {
	home := isolatedHome(t)
	if err := Store(home, "fallback-secret"); err != nil {
		t.Fatal(err)
	}
	secret, err := Load(home)
	if err != nil || secret != "fallback-secret" {
		t.Fatalf("file fallback read failed: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, "cron-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("credential permissions = %v, want 0600", info.Mode().Perm())
	}
	if err := Store(home, "replacement-secret"); err != nil {
		t.Fatal(err)
	}
	secret, err = Load(home)
	if err != nil || secret != "replacement-secret" {
		t.Fatalf("file fallback replacement failed: %v", err)
	}
}
