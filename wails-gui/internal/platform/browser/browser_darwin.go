//go:build darwin

package browser

import "os"

// FindChromium returns the path to a Chromium-based browser binary that
// supports --app mode, or "" if none is found.
func FindChromium() string {
	paths := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
