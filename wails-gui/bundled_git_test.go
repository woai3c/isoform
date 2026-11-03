package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// --- extractZip ---

func TestExtractZipBasic(t *testing.T) {
	dest := t.TempDir()
	data := createTestZip(t, map[string]string{
		"bin/git":        "fake-git-binary",
		"libexec/helper": "helper-content",
	})

	if err := extractZip(data, dest); err != nil {
		t.Fatalf("extractZip() error: %v", err)
	}

	assertFileContent(t, filepath.Join(dest, "bin", "git"), "fake-git-binary")
	assertFileContent(t, filepath.Join(dest, "libexec", "helper"), "helper-content")
}

func TestExtractZipCreatesDirectories(t *testing.T) {
	dest := t.TempDir()
	data := createTestZip(t, map[string]string{
		"a/b/c/deep.txt": "deep",
	})

	if err := extractZip(data, dest); err != nil {
		t.Fatalf("extractZip() error: %v", err)
	}

	assertFileContent(t, filepath.Join(dest, "a", "b", "c", "deep.txt"), "deep")
}

func TestExtractZipSlipPrevention(t *testing.T) {
	// Create a zip with a path traversal entry.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("../../../etc/evil.txt")
	if err != nil {
		t.Fatalf("zip Create: %v", err)
	}
	_, _ = fw.Write([]byte("evil"))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}

	dest := t.TempDir()
	if err := extractZip(buf.Bytes(), dest); err != nil {
		t.Fatalf("extractZip() error: %v", err)
	}

	// The evil file should NOT exist outside dest.
	evilPath := filepath.Join(dest, "..", "..", "..", "etc", "evil.txt")
	if _, err := os.Stat(evilPath); err == nil {
		t.Fatalf("zip slip prevention failed: %s should not exist", evilPath)
	}
}

// --- extractTarGz ---

func TestExtractTarGzBasic(t *testing.T) {
	dest := t.TempDir()
	data := createTestTarGz(t, map[string]string{
		"bin/git":    "fake-git",
		"share/info": "readme",
	})

	if err := extractTarGz(data, dest); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}

	assertFileContent(t, filepath.Join(dest, "bin", "git"), "fake-git")
	assertFileContent(t, filepath.Join(dest, "share", "info"), "readme")
}

func TestExtractTarGzSlipPrevention(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: "../../../etc/evil.txt",
		Mode: 0o644,
		Size: 4,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar WriteHeader: %v", err)
	}
	if _, err := tw.Write([]byte("evil")); err != nil {
		t.Fatalf("tar Write: %v", err)
	}
	tw.Close()
	gz.Close()

	dest := t.TempDir()
	if err := extractTarGz(buf.Bytes(), dest); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}

	evilPath := filepath.Join(dest, "..", "..", "..", "etc", "evil.txt")
	if _, err := os.Stat(evilPath); err == nil {
		t.Fatalf("tar slip prevention failed: %s should not exist", evilPath)
	}
}

func TestExtractTarGzSymlinkValid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require special privileges on Windows")
	}
	dest := t.TempDir()

	// Create a tar.gz with a valid internal symlink.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// Regular file.
	hdr := &tar.Header{Name: "bin/git", Mode: 0o755, Size: 8, Typeflag: tar.TypeReg}
	tw.WriteHeader(hdr)
	tw.Write([]byte("fake-git"))

	// Valid symlink within destDir.
	tw.WriteHeader(&tar.Header{Name: "bin/git-link", Typeflag: tar.TypeSymlink, Linkname: "git"})
	tw.Close()
	gz.Close()

	if err := extractTarGz(buf.Bytes(), dest); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}

	target, err := os.Readlink(filepath.Join(dest, "bin", "git-link"))
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "git" {
		t.Fatalf("symlink target = %q, want %q", target, "git")
	}
}

func TestExtractTarGzSymlinkEscapeBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require special privileges on Windows")
	}
	dest := t.TempDir()

	// Create a tar.gz with a symlink that escapes destDir.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "bin/evil-link", Typeflag: tar.TypeSymlink, Linkname: "../../../etc/passwd"})
	tw.Close()
	gz.Close()

	if err := extractTarGz(buf.Bytes(), dest); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}

	// The symlink should NOT have been created.
	if _, err := os.Lstat(filepath.Join(dest, "bin", "evil-link")); err == nil {
		t.Fatal("symlink escaping destDir should have been blocked")
	}
}

// --- copyDirRecursive ---

func TestCopyDirRecursive(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dest")

	// Create source structure.
	writeFile(t, filepath.Join(src, "bin", "git"), "git-binary")
	writeFile(t, filepath.Join(src, "libexec", "git-core", "helper"), "helper")
	writeFile(t, filepath.Join(src, "README"), "readme content")

	if err := copyDirRecursive(src, dst); err != nil {
		t.Fatalf("copyDirRecursive() error: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "bin", "git"), "git-binary")
	assertFileContent(t, filepath.Join(dst, "libexec", "git-core", "helper"), "helper")
	assertFileContent(t, filepath.Join(dst, "README"), "readme content")
}

func TestCopyDirRecursivePreservesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require special privileges on Windows")
	}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dest")

	// Create a file and a symlink to it.
	writeFile(t, filepath.Join(src, "bin", "git"), "git-binary")
	if err := os.Symlink("git", filepath.Join(src, "bin", "git-link")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := copyDirRecursive(src, dst); err != nil {
		t.Fatalf("copyDirRecursive() error: %v", err)
	}

	// Verify symlink is preserved (not materialized).
	linkTarget, err := os.Readlink(filepath.Join(dst, "bin", "git-link"))
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if linkTarget != "git" {
		t.Fatalf("symlink target = %q, want %q", linkTarget, "git")
	}
}

func TestCopyDirRecursiveOverwrites(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Create an existing file in dst that should be overwritten.
	writeFile(t, filepath.Join(dst, "file.txt"), "old")
	writeFile(t, filepath.Join(src, "file.txt"), "new")

	if err := copyDirRecursive(src, dst); err != nil {
		t.Fatalf("copyDirRecursive() error: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "file.txt"), "new")
}

// --- installedGitPath ---

func TestInstalledGitPathNoBundle(t *testing.T) {
	// Point HOME to an empty dir — no bundled git exists.
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := installedGitPath()
	if got != "" {
		t.Fatalf("installedGitPath() = %q, want empty (no bundle)", got)
	}
}

func TestInstalledGitPathFindsBundledGit(t *testing.T) {
	// Simulate a bundled git in HOME/.yansu-agent/git/bin/git
	home := t.TempDir()
	t.Setenv("HOME", home)

	binDir := filepath.Join(home, configDirName, gitInstallRoot, "bin")
	gitBin := filepath.Join(binDir, gitExeName)
	writeFile(t, gitBin, "fake-git")
	if err := os.Chmod(gitBin, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	got := installedGitPath()
	if got != gitBin {
		t.Fatalf("installedGitPath() = %q, want %q", got, gitBin)
	}
}

func TestResolvedGitPathDoesNotCacheEmptyResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldLookPath := gitLookPath
	oldResolved := cachedResolvedGitPath()
	oldStarted := bundledGitInstallStart.Load()
	oldDone := bundledGitInstallDone
	oldOnce := bundledGitInstallOnce
	oldTimeout := bundledGitWaitTimeout

	gitLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	setResolvedGitPath("")
	bundledGitInstallStart.Store(false)
	bundledGitInstallDone = make(chan struct{})
	bundledGitInstallOnce = &sync.Once{}
	bundledGitWaitTimeout = 25 * time.Millisecond

	t.Cleanup(func() {
		gitLookPath = oldLookPath
		setResolvedGitPath(oldResolved)
		bundledGitInstallStart.Store(oldStarted)
		bundledGitInstallDone = oldDone
		bundledGitInstallOnce = oldOnce
		bundledGitWaitTimeout = oldTimeout
	})

	if got := ResolvedGitPath(); got != "" {
		t.Fatalf("ResolvedGitPath() = %q, want empty before bundled install exists", got)
	}

	binDir := filepath.Join(home, configDirName, gitInstallRoot, "bin")
	gitBin := filepath.Join(binDir, gitExeName)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(gitBin, []byte("fake-git"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(gitBin, 0o755); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
	}

	if got := ResolvedGitPath(); got != gitBin {
		t.Fatalf("ResolvedGitPath() after install = %q, want %q", got, gitBin)
	}
}

func TestResolvedGitPathWaitsForBundledInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldLookPath := gitLookPath
	oldResolved := cachedResolvedGitPath()
	oldStarted := bundledGitInstallStart.Load()
	oldDone := bundledGitInstallDone
	oldOnce := bundledGitInstallOnce
	oldTimeout := bundledGitWaitTimeout

	gitLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	setResolvedGitPath("")
	bundledGitInstallStart.Store(true)
	bundledGitInstallDone = make(chan struct{})
	bundledGitInstallOnce = &sync.Once{}
	// This case verifies readiness-channel coordination, not the timeout path.
	// Leave time for fixture filesystem IO on a busy host; the deadline case
	// above retains its short timeout and the production default is unchanged.
	bundledGitWaitTimeout = 5 * time.Second

	t.Cleanup(func() {
		gitLookPath = oldLookPath
		setResolvedGitPath(oldResolved)
		bundledGitInstallStart.Store(oldStarted)
		bundledGitInstallDone = oldDone
		bundledGitInstallOnce = oldOnce
		bundledGitWaitTimeout = oldTimeout
	})

	binDir := filepath.Join(home, configDirName, gitInstallRoot, "bin")
	gitBin := filepath.Join(binDir, gitExeName)
	errCh := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			errCh <- err
			bundledGitInstallOnce.Do(func() { close(bundledGitInstallDone) })
			return
		}
		if err := os.WriteFile(gitBin, []byte("fake-git"), 0o755); err != nil {
			errCh <- err
			bundledGitInstallOnce.Do(func() { close(bundledGitInstallDone) })
			return
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(gitBin, 0o755); err != nil {
				errCh <- err
				bundledGitInstallOnce.Do(func() { close(bundledGitInstallDone) })
				return
			}
		}
		errCh <- nil
		bundledGitInstallOnce.Do(func() { close(bundledGitInstallDone) })
	}()

	got := ResolvedGitPath()
	if err := <-errCh; err != nil {
		t.Fatalf("install goroutine failed: %v", err)
	}
	if got != gitBin {
		t.Fatalf("ResolvedGitPath() = %q, want %q after waiting for bundled install", got, gitBin)
	}
}

// --- findBundledGitDir ---

func TestFindBundledGitDirNotFound(t *testing.T) {
	// In a test environment, no bundle should exist next to test binary.
	got := findBundledGitDir()
	// This should return empty since there's no git-bundle alongside the test binary.
	if got != "" {
		t.Logf("findBundledGitDir() = %q (bundle found, which is fine)", got)
	}
}

// --- gitDownloadURL ---

func TestGitDownloadURLWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}
	url := gitDownloadURL()
	if url == "" {
		t.Fatal("gitDownloadURL() returned empty on Windows")
	}
}

func TestGitDownloadURLNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows test")
	}
	url := gitDownloadURL()
	if url != "" {
		t.Fatalf("gitDownloadURL() = %q on %s, want empty", url, runtime.GOOS)
	}
}

// --- isExecutableFile ---

func TestIsExecutableFileTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	writeFile(t, path, "content")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if !isExecutableFile(path) {
		t.Fatalf("isExecutableFile(%q) = false, want true", path)
	}
}

func TestIsExecutableFileFalseNotExist(t *testing.T) {
	if isExecutableFile("/nonexistent/path/to/bin") {
		t.Fatal("isExecutableFile(nonexistent) = true, want false")
	}
}

func TestIsExecutableFileFalseDirectory(t *testing.T) {
	dir := t.TempDir()
	if isExecutableFile(dir) {
		t.Fatal("isExecutableFile(directory) = true, want false")
	}
}

func TestIsExecutableFileFalseNoExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute bit not meaningful on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "noexec")
	writeFile(t, path, "content")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if isExecutableFile(path) {
		t.Fatalf("isExecutableFile(%q) = true, want false (no exec bit)", path)
	}
}

// --- helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("file %s content = %q, want %q", path, string(data), want)
	}
}

func createTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create(%s): %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("zip Write(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return buf.Bytes()
}

func createTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar WriteHeader(%s): %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar Write(%s): %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}
