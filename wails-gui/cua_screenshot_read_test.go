package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCuaScreenshotBase64ReadsAllowedTempImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	data := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write screenshot: %v", err)
	}

	var app App
	got, err := app.ReadCuaScreenshotBase64(path)
	if err != nil {
		t.Fatalf("ReadCuaScreenshotBase64: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if got != want {
		t.Fatalf("data URL = %q, want %q", got, want)
	}
}

// cuaOutsideAllowedRootsDir returns a fresh directory whose real path is under
// none of allowedCuaScreenshotRoots. t.TempDir() always is, and so is the
// checkout when it lives under /tmp, so it tries the user's cache directory,
// the home directory and then the package directory, and skips when every one
// of them is an allowed root.
func cuaOutsideAllowedRootsDir(t *testing.T) string {
	t.Helper()
	var bases []string
	if dir, err := os.UserCacheDir(); err == nil {
		bases = append(bases, dir)
	}
	if dir, err := os.UserHomeDir(); err == nil {
		bases = append(bases, dir)
	}
	if dir, err := filepath.Abs("."); err == nil {
		bases = append(bases, dir)
	}
	for _, base := range bases {
		dir, err := os.MkdirTemp(base, "cua-outside-*")
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || isAllowedCuaScreenshotPath(filepath.Join(resolved, "secret.jpg")) {
			_ = os.RemoveAll(dir)
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}
	t.Skipf("no writable directory outside the allowed screenshot roots %v among %v", allowedCuaScreenshotRoots(), bases)
	return ""
}

func TestReadCuaScreenshotBase64RejectsSymlinkOutsideAllowedRoots(t *testing.T) {
	outside := filepath.Join(cuaOutsideAllowedRootsDir(t), "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(t.TempDir(), "shot.jpg")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var app App
	_, err := app.ReadCuaScreenshotBase64(link)
	if err == nil {
		t.Fatal("ReadCuaScreenshotBase64 unexpectedly accepted symlink outside allowed roots")
	}
	if !strings.Contains(err.Error(), "refused to read non-screenshot path") {
		t.Fatalf("error = %v, want refused path error", err)
	}
}

func TestReadCuaScreenshotBase64RejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.jpg")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create screenshot: %v", err)
	}
	if err := file.Truncate(maxCuaScreenshotBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("truncate screenshot: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close screenshot: %v", err)
	}

	var app App
	_, err = app.ReadCuaScreenshotBase64(path)
	if err == nil {
		t.Fatal("ReadCuaScreenshotBase64 unexpectedly accepted oversized file")
	}
	if !strings.Contains(err.Error(), "cua screenshot too large") {
		t.Fatalf("error = %v, want too-large error", err)
	}
}
