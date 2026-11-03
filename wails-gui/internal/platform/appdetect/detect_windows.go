//go:build windows

package appdetect

import (
	"os"
	"os/exec"
	"path/filepath"
	"wails-gui/internal/platform/cmdutil"
)

// knownApps is the predefined list of applications to detect on Windows.
var knownApps = []InstalledApp{
	// Editors
	{Name: "VS Code", AppBundle: "Microsoft VS Code/Code.exe", Category: "editor"},
	{Name: "Cursor", AppBundle: "Cursor/Cursor.exe", Category: "editor"},
	{Name: "Windsurf", AppBundle: "Windsurf/Windsurf.exe", Category: "editor"},
	{Name: "Sublime Text", AppBundle: "Sublime Text/sublime_text.exe", Category: "editor"},
	// Terminals
	{Name: "Windows Terminal", AppBundle: "wt.exe", Category: "terminal"},
}

// DetectInstalledApps returns the list of known editors/terminals that are
// currently installed on this machine. Explorer is always appended as a
// file_manager entry.
func DetectInstalledApps() []InstalledApp {
	var installed []InstalledApp

	programFiles := os.Getenv("ProgramFiles")
	localAppData := os.Getenv("LOCALAPPDATA")

	searchDirs := []string{}
	if programFiles != "" {
		searchDirs = append(searchDirs, programFiles)
	}
	if localAppData != "" {
		searchDirs = append(searchDirs, filepath.Join(localAppData, "Programs"))
	}

	for _, app := range knownApps {
		// First try LookPath (for apps in PATH like wt.exe).
		if _, err := exec.LookPath(app.AppBundle); err == nil {
			installed = append(installed, app)
			continue
		}
		// Then search common install directories.
		for _, dir := range searchDirs {
			if _, err := os.Stat(filepath.Join(dir, app.AppBundle)); err == nil {
				installed = append(installed, app)
				break
			}
		}
	}

	// Explorer is always available on Windows.
	installed = append(installed, InstalledApp{
		Name:      "Explorer",
		AppBundle: "explorer.exe",
		Category:  "file_manager",
	})

	return installed
}

// OpenInApp opens the given project directory using the specified application.
func OpenInApp(appBundle string, projectPath string) error {
	if err := validateProjectPath(projectPath); err != nil {
		return err
	}

	// Try the bundle path directly.
	exePath := appBundle

	// If not absolute, search in common directories.
	if !filepath.IsAbs(exePath) {
		if resolved, lookErr := exec.LookPath(appBundle); lookErr == nil {
			exePath = resolved
		} else {
			programFiles := os.Getenv("ProgramFiles")
			localAppData := os.Getenv("LOCALAPPDATA")
			for _, dir := range []string{programFiles, filepath.Join(localAppData, "Programs")} {
				candidate := filepath.Join(dir, appBundle)
				if _, statErr := os.Stat(candidate); statErr == nil {
					exePath = candidate
					break
				}
			}
		}
	}

	return cmdutil.Command(exePath, projectPath).Start()
}
