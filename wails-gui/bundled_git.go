package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	gitBundleDirName = "git-bundle"
	gitInstallRoot   = "git"

	// MinGit version to download when not bundled (Windows only).
	minGitVersion = "2.47.1"
	minGitRelease = "2.47.1.windows.1"
)

// git resolution state is split between a short-lived cache of the last known
// usable path and startup coordination for the async bundled install.
var (
	resolvedGitMu          sync.RWMutex
	resolvedGitBin         string
	gitLookPath            = exec.LookPath
	bundledGitInstallStart atomic.Bool
	bundledGitInstallDone  = make(chan struct{})
	bundledGitInstallOnce  = &sync.Once{}
	bundledGitWaitTimeout  = 2 * time.Minute
)

// ResolvedGitPath returns the path to the git binary.
// Resolution order:
//  1. System git (user's own git with their config, SSH keys, credentials)
//  2. Bundled/installed git (~/.yansu-agent/git/bin/git)
func ResolvedGitPath() string {
	if cached := cachedResolvedGitPath(); cached != "" && isExecutableFile(cached) {
		return cached
	}

	if path := resolveGitPathNow(); path != "" {
		setResolvedGitPath(path)
		return path
	}

	if bundledGitInstallStart.Load() {
		timer := time.NewTimer(bundledGitWaitTimeout)
		defer timer.Stop()

		select {
		case <-bundledGitInstallDone:
		case <-timer.C:
		}

		if path := resolveGitPathNow(); path != "" {
			setResolvedGitPath(path)
			return path
		}
	}

	setResolvedGitPath("")
	return ""
}

func cachedResolvedGitPath() string {
	resolvedGitMu.RLock()
	defer resolvedGitMu.RUnlock()
	return resolvedGitBin
}

func setResolvedGitPath(path string) {
	resolvedGitMu.Lock()
	defer resolvedGitMu.Unlock()
	resolvedGitBin = strings.TrimSpace(path)
}

func resolveGitPathNow() string {
	if p, err := gitLookPath("git"); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	return installedGitPath()
}

// ensureBundledGit makes sure git is available at ~/.yansu-agent/git/bin/git.
//
// Resolution order:
//  1. Copy from app bundle (Resources/git-bundle/bin/git) — full DMG install
//  2. Already installed at ~/.yansu-agent/git/bin/git — previous run
//  3. Missing Windows Git requires a confirmed tool download plan.
//
// Returns the installed path or an error.
func ensureBundledGit() (string, error) {
	dataDir, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	destDir := filepath.Join(dataDir, gitInstallRoot, "bin")
	destPath := filepath.Join(destDir, gitExeName)

	// 1. Try copying from app bundle (best case: full DMG install).
	if srcDir := findBundledGitDir(); srcDir != "" {
		gitDir := filepath.Join(dataDir, gitInstallRoot)
		if err := copyDirRecursive(srcDir, gitDir); err != nil {
			return "", fmt.Errorf("install bundled git: %w", err)
		}
		if goruntime.GOOS != "windows" {
			_ = os.Chmod(destPath, 0o755)
		}
		log.Printf("[git] installed bundled git to %s", destPath)
		return destPath, nil
	}

	// 2. Already installed from a previous run.
	if isExecutableFile(destPath) {
		log.Printf("[git] using previously installed git at %s", destPath)
		return destPath, nil
	}

	return "", fmt.Errorf("git not ready: bundled binary missing; Windows fallback requires a confirmed download plan")
}

// gitDownloadURL reads the shipped manifest without making a request.
func gitDownloadURL() string {
	entry, err := gitDownloadEntry(goruntime.GOOS + "/" + goruntime.GOARCH)
	if err != nil {
		return ""
	}
	return entry.Source
}

// installedGitPath returns the path to the bundled git binary
// at ~/.yansu-agent/git/bin/git, or empty string if not found.
func installedGitPath() string {
	dataDir, err := flavorDataDir()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(dataDir, gitInstallRoot, "bin", gitExeName)
	if isExecutableFile(candidate) {
		return candidate
	}
	return ""
}

// findBundledGitDir searches standard bundle locations for the git directory.
func findBundledGitDir() string {
	for _, candidate := range bundledGitDirCandidates() {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		binPath := filepath.Join(candidate, "bin", gitExeName)
		if isExecutableFile(binPath) {
			return candidate
		}
	}
	return ""
}

// bundledGitDirCandidates returns paths relative to the executable,
// macOS Resources, and the working directory.
func bundledGitDirCandidates() []string {
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	return toolBundleCandidates(exe, wd, gitBundleDirName)
}

// extractZip extracts a zip archive from in-memory bytes into destDir.
func extractZip(data []byte, destDir string) error {
	return extractZipFromReader(bytes.NewReader(data), int64(len(data)), destDir)
}

// extractZipFromReader extracts a zip archive from an io.ReaderAt into destDir.
func extractZipFromReader(r io.ReaderAt, size int64, destDir string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}

	for _, f := range zr.File {
		target := filepath.Join(destDir, f.Name) //nolint:gosec
		// Prevent zip slip.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o755)
		if err != nil {
			rc.Close()
			return err
		}

		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// extractTarGz extracts a .tar.gz archive from in-memory bytes into destDir.
func extractTarGz(data []byte, destDir string) error {
	return extractTarGzFromReader(bytes.NewReader(data), destDir)
}

// extractTarGzFromReader extracts a .tar.gz archive from a streaming reader into destDir.
func extractTarGzFromReader(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		target := filepath.Join(destDir, header.Name) //nolint:gosec
		// Prevent tar slip.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)|0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return copyErr
			}
		case tar.TypeSymlink:
			// Validate symlink target stays within destDir.
			resolvedLink := header.Linkname
			if !filepath.IsAbs(resolvedLink) {
				resolvedLink = filepath.Join(filepath.Dir(target), resolvedLink)
			}
			if !strings.HasPrefix(filepath.Clean(resolvedLink), filepath.Clean(destDir)+string(os.PathSeparator)) {
				log.Printf("[git] skipping symlink escaping destDir: %s -> %s", header.Name, header.Linkname)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("create symlink %s: %w", header.Name, err)
			}
		}
	}
	return nil
}

// copyDirRecursive copies src directory to dst, creating dst if needed.
// Symlinks are preserved as symlinks rather than being materialized as regular files.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relPath)

		// Check for symlink via Lstat (filepath.Walk follows symlinks,
		// so info won't reflect the link itself).
		linfo, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return lstatErr
		}

		if linfo.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			// Remove existing entry to avoid "file exists" on re-copy.
			_ = os.Remove(targetPath)
			return os.Symlink(linkTarget, targetPath)
		}

		if info.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		tmpPath := targetPath + ".tmp"
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(tmpPath, data, info.Mode()|0o755); err != nil {
			return err
		}
		return os.Rename(tmpPath, targetPath)
	})
}
