package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// cua_screenshot_read.go bridges cua-driver window screenshots to the
// chat UI. The bundled daemon captures each window to a JPEG on disk
// (Capture.swift / Daemon.swift default "/tmp/alma-cu-state-*.jpg") and
// returns only the path over the wire — never the bytes. The webview
// can't read host files, so this binding is the single, deliberately
// narrow bridge that turns an on-disk screenshot into a data-URI the
// chat transcript can render inline (the Codex-style "watch the agent
// work" affordance).

// maxCuaScreenshotBytes caps how large a file the webview may pull in as
// an inline base64 image. Cua screenshots are JPEG capped at ~1568px
// long-side (a few hundred KB worst case); 8 MB is generous headroom
// while still refusing a runaway read.
const maxCuaScreenshotBytes = 8 << 20

// ReadCuaScreenshotBase64 reads a cua-driver screenshot from disk and
// returns it as a ready-to-use "data:<mediaType>;base64,<data>" string
// the webview can drop straight into <img src>. It is intentionally
// narrow — only image files living under a temp dir are eligible — so a
// compromised renderer can't turn it into an arbitrary-file-read gadget.
//
// Returns ("", nil) when the file is simply gone (temp cleanup or a
// reloaded stale session) so the caller renders nothing rather than an
// error. Returns an error only for a refused path or a real read fault.
func (a *App) ReadCuaScreenshotBase64(path string) (string, error) {
	clean := filepath.Clean(path)
	// Resolve symlinks BEFORE validating. filepath.Clean is purely
	// lexical, so a symlink under /tmp pointing at a sensitive file (e.g.
	// ~/.ssh/id_rsa) would pass the allow-list prefix check while
	// os.ReadFile silently follows the link — an arbitrary-file-read
	// gadget for a compromised renderer. EvalSymlinks collapses the link
	// so the allow-list is enforced on the real target. allowedCuaScreenshotRoots
	// already includes /private/tmp, so /tmp -> /private/tmp still matches.
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("resolve cua screenshot path: %w", err)
	}
	if !isAllowedCuaScreenshotPath(resolved) {
		return "", fmt.Errorf("refused to read non-screenshot path %q", path)
	}
	file, err := openCuaScreenshotFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open cua screenshot: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat cua screenshot: %w", err)
	}
	if info.IsDir() || info.Size() == 0 {
		return "", nil
	}
	if info.Size() > maxCuaScreenshotBytes {
		return "", fmt.Errorf("cua screenshot too large: %d bytes", info.Size())
	}
	confirmed, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("re-resolve cua screenshot path: %w", err)
	}
	if !isAllowedCuaScreenshotPath(confirmed) {
		return "", fmt.Errorf("refused to read non-screenshot path %q", path)
	}
	currentInfo, err := os.Stat(confirmed)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat confirmed cua screenshot: %w", err)
	}
	if !os.SameFile(info, currentInfo) {
		return "", fmt.Errorf("refused to read changed screenshot path %q", path)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxCuaScreenshotBytes+1))
	if err != nil {
		return "", fmt.Errorf("read cua screenshot: %w", err)
	}
	if len(data) > maxCuaScreenshotBytes {
		return "", fmt.Errorf("cua screenshot too large: %d bytes", len(data))
	}
	media := "image/jpeg"
	if strings.HasSuffix(strings.ToLower(resolved), ".png") {
		media = "image/png"
	}
	return "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// isAllowedCuaScreenshotPath gates ReadCuaScreenshotBase64 to image files
// under a temp directory. The bundled daemon hardcodes "/tmp/alma-cu-
// state-*.jpg" and the SKILL.md examples route explicit
// --screenshot-out-file paths through /tmp too; os.TempDir() (the macOS
// per-user "/var/folders/.../T") is also allowed for callers that target
// it. Anything outside those roots, or without a jpg/png extension, is
// refused so the webview can't read arbitrary host files.
func isAllowedCuaScreenshotPath(clean string) bool {
	if !filepath.IsAbs(clean) {
		return false
	}
	switch strings.ToLower(filepath.Ext(clean)) {
	case ".jpg", ".jpeg", ".png":
	default:
		return false
	}
	for _, root := range allowedCuaScreenshotRoots() {
		if pathWithin(clean, root) {
			return true
		}
	}
	return false
}

// allowedCuaScreenshotRoots returns the temp roots a screenshot may live
// under, including symlink-resolved forms so "/tmp" (a symlink to
// "/private/tmp" on macOS) and the resolved os.TempDir() both match.
func allowedCuaScreenshotRoots() []string {
	roots := []string{"/tmp", "/private/tmp", os.TempDir()}
	seen := make(map[string]struct{}, len(roots)*2)
	out := make([]string, 0, len(roots)*2)
	add := func(p string) {
		c := filepath.Clean(p)
		if c == "" || c == "." {
			return
		}
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	for _, r := range roots {
		add(r)
		if resolved, err := filepath.EvalSymlinks(r); err == nil {
			add(resolved)
		}
	}
	return out
}

// pathWithin reports whether target is dir itself or nested beneath it,
// using filepath.Rel so ".." escapes are rejected.
func pathWithin(target, dir string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
