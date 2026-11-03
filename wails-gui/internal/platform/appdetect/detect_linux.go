//go:build linux

package appdetect

import (
	"os/exec"

	"wails-gui/internal/platform/cmdutil"
)

// knownApps is the predefined list of applications to detect on Linux.
var knownApps = []InstalledApp{
	// Editors
	{Name: "VS Code", AppBundle: "code", Category: "editor"},
	{Name: "Cursor", AppBundle: "cursor", Category: "editor"},
	{Name: "Windsurf", AppBundle: "windsurf", Category: "editor"},
	{Name: "Zed", AppBundle: "zed", Category: "editor"},
	{Name: "Sublime Text", AppBundle: "subl", Category: "editor"},
}

// DetectInstalledApps returns the list of known editors/terminals found in PATH.
func DetectInstalledApps() []InstalledApp {
	var installed []InstalledApp
	for _, app := range knownApps {
		if _, err := exec.LookPath(app.AppBundle); err == nil {
			installed = append(installed, app)
		}
	}

	// xdg-open serves as the generic file manager on Linux.
	installed = append(installed, InstalledApp{
		Name:      "Files",
		AppBundle: "xdg-open",
		Category:  "file_manager",
	})

	return installed
}

// OpenInApp opens the given project directory using the specified application.
func OpenInApp(appBundle string, projectPath string) error {
	if err := validateProjectPath(projectPath); err != nil {
		return err
	}
	return cmdutil.Command(appBundle, projectPath).Start()
}
