package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func setQuarantine(t *testing.T, path string) {
	t.Helper()
	// Zero-date, unapproved flags — the shape the kernel stamps when a
	// quarantine-flagged process creates a file.
	val := []byte("0081;00000000;Yansu;")
	if err := unix.Lsetxattr(path, quarantineXattr, val, 0); err != nil {
		t.Fatalf("set quarantine on %s: %v", path, err)
	}
}

func hasQuarantine(t *testing.T, path string) bool {
	t.Helper()
	buf := make([]byte, 256)
	_, err := unix.Lgetxattr(path, quarantineXattr, buf)
	switch err {
	case nil:
		return true
	case unix.ENOATTR:
		return false
	default:
		t.Fatalf("get quarantine on %s: %v", path, err)
		return false
	}
}

func TestStripQuarantineRecursive(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Test.app")
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(macos, "Test")
	if err := os.WriteFile(exe, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(bundle, "Contents", "Info.plist")
	if err := os.WriteFile(plain, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bundle, "Contents", "Current")
	if err := os.Symlink("MacOS", link); err != nil {
		t.Fatal(err)
	}

	// Quarantine the bundle root, a directory, the executable, and the
	// symlink itself; leave Info.plist clean to cover the ENOATTR path.
	setQuarantine(t, bundle)
	setQuarantine(t, macos)
	setQuarantine(t, exe)
	setQuarantine(t, link)

	removed, err := stripQuarantineRecursive(bundle)
	if err != nil {
		t.Fatalf("stripQuarantineRecursive: %v", err)
	}
	if removed != 4 {
		t.Errorf("removed = %d, want 4", removed)
	}

	for _, p := range []string{bundle, macos, exe, plain, link} {
		if hasQuarantine(t, p) {
			t.Errorf("%s still has %s", p, quarantineXattr)
		}
	}
}

func TestStripQuarantineRecursiveDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setQuarantine(t, outside)

	bundle := filepath.Join(root, "Test.app")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bundle, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := stripQuarantineRecursive(bundle); err != nil {
		t.Fatalf("stripQuarantineRecursive: %v", err)
	}
	if !hasQuarantine(t, outside) {
		t.Error("strip followed a symlink out of the bundle and removed the target's xattr")
	}
}

func TestStripQuarantineRecursiveCleanTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := stripQuarantineRecursive(root)
	if err != nil {
		t.Fatalf("stripQuarantineRecursive: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}
