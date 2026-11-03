//go:build !darwin

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// listOpenWithApps is macOS-only; other platforms surface just the default
// "Open" + "Reveal" actions (the frontend hides the app sub-menu when the
// list is empty).
func listOpenWithApps(absPath string) []DeliverableOpenWithApp {
	return nil
}

// revealInFileManager selects absPath in the platform file manager.
func revealInFileManager(absPath string) error {
	switch runtime.GOOS {
	case "windows":
		// explorer returns exit code 1 even on success, so ignore the error.
		_ = exec.Command("explorer", "/select,", absPath).Start()
		return nil
	case "linux":
		// No portable "select" verb; open the containing directory instead.
		if err := exec.Command("xdg-open", filepath.Dir(absPath)).Start(); err != nil {
			return fmt.Errorf("reveal in file manager: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("reveal is not supported on this platform")
	}
}

// openWithApp is macOS-only; other platforms have no uniform "open with a
// specific app bundle" verb, so this is unsupported.
func openWithApp(absPath, appPath string) error {
	return fmt.Errorf("open-with is not supported on this platform")
}
