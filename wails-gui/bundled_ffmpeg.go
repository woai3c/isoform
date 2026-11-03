package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

const (
	ffmpegBundleDirName = "ffmpeg-bundle"
	ffmpegInstallRoot   = "bin"
)

// ensureBundledFFmpeg makes sure ffmpeg is available at ~/.yansu-agent/bin/ffmpeg.
//
// Resolution order:
//  1. Copy from app bundle (Resources/ffmpeg-bundle/bin/ffmpeg) — full DMG install
//  2. Already installed at ~/.yansu-agent/bin/ffmpeg — previous run or DMG install
//
// Missing bundles leave transcoding unavailable; startup never downloads tools.
//
// Returns the installed path or an error.
func ensureBundledFFmpeg() (string, error) {
	dataDir, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	destDir := filepath.Join(dataDir, ffmpegInstallRoot)
	destPath := filepath.Join(destDir, ffmpegExeName)

	// 1. Try copying from app bundle (best case: full DMG install).
	if srcPath := findBundledFFmpeg(); srcPath != "" {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", fmt.Errorf("create install directory: %w", err)
		}
		if shouldCopyBundledCLI(srcPath, destPath) {
			if err := copyFileExecutable(srcPath, destPath); err != nil {
				return "", fmt.Errorf("install bundled ffmpeg binary: %w", err)
			}
			log.Printf("[ffmpeg] installed bundled binary to %s", destPath)
		}
		if goruntime.GOOS != "windows" {
			_ = os.Chmod(destPath, 0o755)
		}
		return destPath, nil
	}

	// 2. Already installed from a previous run.
	if isExecutableFile(destPath) {
		log.Printf("[ffmpeg] using previously installed ffmpeg at %s", destPath)
		return destPath, nil
	}

	return "", fmt.Errorf("ffmpeg not ready: bundled binary missing; run scripts/download-ffmpeg.sh when building Isoform")
}

// installedFFmpegPath returns the path to the installed ffmpeg binary,
// or empty string if not found.
// It checks the bundled install location first, then falls back to $PATH.
func installedFFmpegPath() string {
	dataDir, err := flavorDataDir()
	if err == nil {
		// 1. Bundled install location (~/.yansu-agent/bin/ffmpeg)
		candidate := filepath.Join(dataDir, ffmpegInstallRoot, ffmpegExeName)
		if isExecutableFile(candidate) {
			return candidate
		}
	}

	// 2. Fallback to $PATH
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		log.Printf("[ffmpeg] using system ffmpeg from PATH at %s", p)
		return p
	}

	return ""
}

// findBundledFFmpeg searches standard bundle locations for the ffmpeg binary.
func findBundledFFmpeg() string {
	for _, candidate := range bundledFFmpegPathCandidates() {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// bundledFFmpegPathCandidates returns paths relative to the executable,
// macOS Resources, and the working directory.
func bundledFFmpegPathCandidates() []string {
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	return toolBundleCandidates(exe, wd, ffmpegBundleDirName, "bin", ffmpegExeName)
}
