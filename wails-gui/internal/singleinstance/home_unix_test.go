//go:build !windows

package singleinstance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockAndSocketHonorAgentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	override := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", override)
	lock, err := acquireLock("isolation")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := os.Stat(filepath.Join(override, ".isolation.lock")); err != nil {
		t.Fatal(err)
	}
	got, err := socketPath("isolation")
	if err != nil || got != filepath.Join(override, ".isolation.sock") {
		t.Fatalf("socket path = %q, %v", got, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("user home was touched: %v, %v", entries, err)
	}
}
