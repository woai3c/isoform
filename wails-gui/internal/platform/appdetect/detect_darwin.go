//go:build darwin

package appdetect

import (
	"os"
	"path/filepath"
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

// knownApps is the predefined list of applications to detect on macOS.
var knownApps = []InstalledApp{
	// Editors
	{Name: "VS Code", AppBundle: "Visual Studio Code.app", Category: "editor"},
	{Name: "Cursor", AppBundle: "Cursor.app", Category: "editor"},
	{Name: "Windsurf", AppBundle: "Windsurf.app", Category: "editor"},
	{Name: "Zed", AppBundle: "Zed.app", Category: "editor"},
	{Name: "Sublime Text", AppBundle: "Sublime Text.app", Category: "editor"},
	// Terminals
	{Name: "Terminal", AppBundle: "Utilities/Terminal.app", Category: "terminal"},
	{Name: "Ghostty", AppBundle: "Ghostty.app", Category: "terminal"},
	{Name: "Warp", AppBundle: "Warp.app", Category: "terminal"},
	{Name: "iTerm2", AppBundle: "iTerm.app", Category: "terminal"},
	{Name: "Alacritty", AppBundle: "Alacritty.app", Category: "terminal"},
}

// DetectInstalledApps returns the list of known editors/terminals that are
// currently installed on this machine. Finder is always appended as a
// file_manager entry.
func DetectInstalledApps() []InstalledApp {
	var installed []InstalledApp
	searchDirs := []string{"/Applications", "/System/Applications"}

	for _, app := range knownApps {
		for _, dir := range searchDirs {
			if _, err := os.Stat(filepath.Join(dir, app.AppBundle)); err == nil {
				installed = append(installed, app)
				break
			}
		}
	}

	// Finder is always available on macOS.
	installed = append(installed, InstalledApp{
		Name:      "Finder",
		AppBundle: "Finder.app",
		Category:  "file_manager",
	})

	return installed
}

// OpenInApp opens the given project directory using the specified application.
func OpenInApp(appBundle string, projectPath string) error {
	if err := validateProjectPath(projectPath); err != nil {
		return err
	}

	// Strip the .app suffix to get the application name for `open -a`.
	appName := strings.TrimSuffix(appBundle, ".app")
	// Handle subdirectory bundles like "Utilities/Terminal.app".
	if idx := strings.LastIndex(appName, "/"); idx >= 0 {
		appName = appName[idx+1:]
	}
	return cmdutil.Command("open", "-a", appName, projectPath).Start()
}
