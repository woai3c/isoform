package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCanExecuteBunSucceedsForRunnableBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script helper is unix-only")
	}

	path := writeExecutableScript(t, "#!/bin/sh\nexit 0\n")
	if !canExecuteBun(path) {
		t.Fatal("canExecuteBun() = false, want true for runnable binary")
	}
}

func TestCanExecuteBunTimesOutForHungBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script helper is unix-only")
	}

	oldTimeout := bundledBunExecuteTimeout
	bundledBunExecuteTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		bundledBunExecuteTimeout = oldTimeout
	})

	path := writeExecutableScript(t, "#!/bin/sh\nsleep 1\n")

	start := time.Now()
	if canExecuteBun(path) {
		t.Fatal("canExecuteBun() = true, want false when version check times out")
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("canExecuteBun() took %s, want timeout-bound execution", elapsed)
	}
}

func writeExecutableScript(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "bun-helper")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	return path
}
