package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	opencliInstallDir    = "opencli"
	opencliBundleDirName = "opencli-bundle"
)

var opencliAdapterCategories = []string{"browser", "desktop"}

// Callers can wait for the local bundle copy, never a network installation.
var (
	opencliInstallOnce sync.Once
	opencliInstallDone = make(chan struct{})
)

// ensureBundledOpenCLI installs only the shipped, pinned package and launcher.
// Updates arrive with Isoform. Neither startup nor discovery installs packages.
func ensureBundledOpenCLI() (string, error) {
	if src := findBundledOpenCLIDir(); src != "" {
		dataDir, err := flavorDataDir()
		if err != nil {
			return "", err
		}
		dest := filepath.Join(dataDir, opencliInstallDir)
		bin := filepath.Join(dest, "bin", opencliExeName)
		version, err := os.ReadFile(filepath.Join(src, ".version"))
		if err != nil {
			return "", fmt.Errorf("opencli not ready: read bundled version: %w", err)
		}
		if strings.TrimSpace(string(version)) == "" || !openCLIPackagePresent(src) {
			return "", fmt.Errorf("opencli not ready: bundled package incomplete")
		}
		installedVersion, _ := os.ReadFile(filepath.Join(dest, ".version"))
		if string(version) != string(installedVersion) || !isExecutableFile(bin) || !openCLIPackagePresent(dest) ||
			shouldCopyBundledCLI(filepath.Join(src, "bin", opencliExeName), bin) {
			// Stage beside the destination so a partial copy is never published.
			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				return "", err
			}
			staging, err := os.MkdirTemp(dataDir, "opencli-stage-")
			if err != nil {
				return "", err
			}
			defer os.RemoveAll(staging)
			if err := copyDirRecursive(src, staging); err != nil {
				return "", fmt.Errorf("install bundled opencli: %w", err)
			}
			if err := os.RemoveAll(dest); err != nil {
				return "", err
			}
			if err := os.Rename(staging, dest); err != nil {
				return "", err
			}
		}
		if !isExecutableFile(filepath.Join(dataDir, bunInstallRoot, bunExeName)) {
			return "", fmt.Errorf("opencli not ready: bundled bun runtime missing")
		}
		return bin, nil
	}
	if p := localOpenCLIPath(); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("opencli not ready: bundled package missing; run scripts/download-opencli.sh when building Isoform")
}

func localOpenCLIPath() string {
	dataDir, err := flavorDataDir()
	if err == nil {
		root := filepath.Join(dataDir, opencliInstallDir)
		candidate := filepath.Join(root, "bin", opencliExeName)
		if isExecutableFile(candidate) && isExecutableFile(filepath.Join(dataDir, bunInstallRoot, bunExeName)) {
			if openCLIPackagePresent(root) {
				return candidate
			}
		}
	}
	return ""
}

func installedOpenCLIPath() string {
	select {
	case <-opencliInstallDone:
	case <-time.After(60 * time.Second):
	}
	return localOpenCLIPath()
}

func findBundledOpenCLIDir() string {
	for _, candidate := range bundledOpenCLIDirCandidates() {
		if isExecutableFile(filepath.Join(candidate, "bin", opencliExeName)) {
			return candidate
		}
	}
	return ""
}

func bundledOpenCLIDirCandidates() []string {
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	return toolBundleCandidates(exe, wd, opencliBundleDirName)
}

// Adapter documentation ships with the pinned bundle, never fetched at startup.
func opencliAdapterDocsDir() string {
	dataDir, err := flavorDataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dataDir, opencliInstallDir, "docs")
}

func LoadAdapterDoc(site string) string {
	if site == "" || filepath.Base(site) != site || strings.ContainsAny(site, `/\\`) {
		return ""
	}
	docsDir := opencliAdapterDocsDir()
	if docsDir == "" {
		return ""
	}
	for _, category := range opencliAdapterCategories {
		data, err := os.ReadFile(filepath.Join(docsDir, category, site+".md"))
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func openCLIPackagePresent(root string) bool {
	info, err := os.Stat(filepath.Join(root, "node_modules", "@jackwener", "opencli", "dist", "src", "main.js"))
	return err == nil && !info.IsDir()
}
