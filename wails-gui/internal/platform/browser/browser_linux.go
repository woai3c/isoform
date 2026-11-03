//go:build linux

package browser

import "os/exec"

// FindChromium returns the path to a Chromium-based browser binary that
// supports --app mode, or "" if none is found.
func FindChromium() string {
	names := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium-browser",
		"chromium",
		"microsoft-edge",
		"brave-browser",
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
