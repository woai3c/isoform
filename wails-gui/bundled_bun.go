package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
	"wails-gui/internal/platform/cmdutil"
)

const (
	bunBundleDirName = "bun-bundle"
	bunInstallRoot   = "bin"
)

var bundledBunExecuteTimeout = 5 * time.Second

// ensureBundledBun makes sure bun is available at ~/.yansu-agent/bin/bun.
//
// Resolution order:
//  1. Copy from app bundle (Resources/bun-bundle/bin/bun) — full DMG install
//  2. Already installed at ~/.yansu-agent/bin/bun — previous run or DMG install
//
// Missing bundles leave the runtime unavailable; startup never downloads tools.
//
// Returns the installed path or an error.
func ensureBundledBun() (string, error) {
	dataDir, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	destDir := filepath.Join(dataDir, bunInstallRoot)
	destPath := filepath.Join(destDir, bunExeName)

	// 1. Try copying from app bundle (best case: full DMG install).
	if srcPath := findBundledBun(); srcPath != "" {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", fmt.Errorf("create install directory: %w", err)
		}
		if shouldCopyBundledCLI(srcPath, destPath) {
			if err := copyFileExecutable(srcPath, destPath); err != nil {
				return "", fmt.Errorf("install bundled bun binary: %w", err)
			}
			log.Printf("[bun] installed bundled binary to %s", destPath)
		}
		if goruntime.GOOS != "windows" {
			_ = os.Chmod(destPath, 0o755)
		}
		// Verify the binary actually runs on this CPU architecture.
		// The app bundle may contain an arm64 binary on an Intel Mac
		// (or vice versa) when built on a different architecture.
		if canExecuteBun(destPath) {
			return destPath, nil
		}
		_ = os.Remove(destPath)
		return "", fmt.Errorf("bun not ready: bundled binary cannot run on %s/%s; rebuild the bun bundle", goruntime.GOOS, goruntime.GOARCH)
	}

	// 2. Already installed from a previous run.
	if isExecutableFile(destPath) {
		if canExecuteBun(destPath) {
			log.Printf("[bun] using previously installed bun at %s", destPath)
			return destPath, nil
		}
		_ = os.Remove(destPath)
		return "", fmt.Errorf("bun not ready: installed binary cannot run on %s/%s", goruntime.GOOS, goruntime.GOARCH)
	}

	return "", fmt.Errorf("bun not ready: bundled binary missing; run scripts/download-bun.sh when building Isoform")
}

// installedBunPath returns the path to the installed bun binary,
// or empty string if not found.
// It checks the bundled install location first, then falls back to
// common user install paths and finally $PATH.
func installedBunPath() string {
	dataDir, err := flavorDataDir()
	if err == nil {
		// 1. Bundled install location (~/.yansu-agent/bin/bun)
		candidate := filepath.Join(dataDir, bunInstallRoot, bunExeName)
		if isExecutableFile(candidate) {
			return candidate
		}
	}

	// 2. Common user install: ~/.bun/bin/bun
	if homeDir, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(homeDir, ".bun", "bin", bunExeName)
		if isExecutableFile(candidate) {
			log.Printf("[bun] using user-installed bun at %s", candidate)
			return candidate
		}
	}

	// 3. Fallback to $PATH
	if p, err := exec.LookPath("bun"); err == nil {
		log.Printf("[bun] using system bun from PATH at %s", p)
		return p
	}

	return ""
}

// findBundledBun searches standard bundle locations for the bun binary.
func findBundledBun() string {
	for _, candidate := range bundledBunPathCandidates() {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// bundledBunPathCandidates returns paths relative to the executable,
// macOS Resources, and the working directory — same strategy as yansu CLI.
func bundledBunPathCandidates() []string {
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	return toolBundleCandidates(exe, wd, bunBundleDirName, "bin", bunExeName)
}

// canExecuteBun verifies that the bun binary at path can actually run on
// this CPU architecture. Returns false for "bad CPU type" and similar
// exec errors that indicate an architecture mismatch.
func canExecuteBun(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), bundledBunExecuteTimeout)
	defer cancel()

	cmd := cmdutil.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "BUN_DEBUG_QUIET=1")
	return cmd.Run() == nil
}

// GetBunPath returns the path to the installed bun binary.
// Exposed to the frontend via Wails binding.
func (a *App) GetBunPath() string {
	return installedBunPath()
}
