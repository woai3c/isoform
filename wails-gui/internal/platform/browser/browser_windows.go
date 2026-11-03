//go:build windows

package browser

import (
	"os"
	"path/filepath"
)

// FindChromium returns the path to a Chromium-based browser binary that
// supports --app mode, or "" if none is found.
func FindChromium() string {
	envDirs := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
	}
	relPaths := []string{
		`Google\Chrome\Application\chrome.exe`,
		`Microsoft\Edge\Application\msedge.exe`,
		`BraveSoftware\Brave-Browser\Application\brave.exe`,
		`Chromium\Application\chrome.exe`,
	}
	for _, dir := range envDirs {
		if dir == "" {
			continue
		}
		for _, rel := range relPaths {
			p := filepath.Join(dir, rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
