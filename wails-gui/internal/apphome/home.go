// Package apphome resolves the data root shared by the desktop and its tools.
package apphome

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dir honors an absolute YANSU_AGENT_HOME before the flavor's default folder.
// An Isoform root must never resolve inside the published app's data folder.
func Dir(defaultDirName string) (string, error) {
	if defaultDirName == "" {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("YANSU_APP_FLAVOR"))) {
		case "isoform":
			defaultDirName = ".isoform-agent"
		default:
			defaultDirName = ".yansu-agent"
		}
	}
	dir := strings.TrimSpace(os.Getenv("YANSU_AGENT_HOME"))
	if dir != "" && !filepath.IsAbs(dir) {
		return "", fmt.Errorf("YANSU_AGENT_HOME must be an absolute path (expand ~ in your shell): %q", dir)
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("user home must be an absolute path: %q", home)
		}
		dir = filepath.Join(home, defaultDirName)
	}
	dir = filepath.Clean(dir)
	if defaultDirName == ".isoform-agent" {
		if err := rejectPublishedHome(dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// DeveloperDir keeps repository utilities isolated even in a release shell.
func DeveloperDir() (string, error) {
	return Dir(".isoform-agent")
}

func rejectPublishedHome(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		// An explicit absolute override is sufficient without a user home.
		return nil
	}
	legacy, err := resolveExistingParents(filepath.Join(home, ".yansu-agent"))
	if err != nil {
		return fmt.Errorf("resolve published app data folder: %w", err)
	}
	resolved, err := resolveExistingParents(dir)
	if err != nil {
		return fmt.Errorf("resolve Isoform data folder: %w", err)
	}
	compareLegacy, compareResolved := legacy, resolved
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		// These platforms commonly use case-insensitive volumes. Refuse a
		// case-only spelling even before either folder has been created.
		compareLegacy, compareResolved = strings.ToLower(legacy), strings.ToLower(resolved)
	}
	relative, err := filepath.Rel(compareLegacy, compareResolved)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
		return fmt.Errorf("Isoform data folder %q resolves inside the published app's data folder %q; use a separate folder", dir, legacy)
	}
	return nil
}

// Resolve aliases even when the final data directory has not been created yet.
func resolveExistingParents(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveExistingParents(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
