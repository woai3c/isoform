package appdetect

import (
	"fmt"
	"os"
)

// InstalledApp represents an application detected on the user's machine.
type InstalledApp struct {
	Name      string `json:"name"`
	AppBundle string `json:"app_bundle"` // e.g. "Visual Studio Code.app" or "code.exe"
	Category  string `json:"category"`   // "editor" | "terminal" | "file_manager"
}

// validateProjectPath checks that the project path is a valid, existing directory.
func validateProjectPath(projectPath string) error {
	if projectPath == "" {
		return fmt.Errorf("project path is required")
	}
	info, err := os.Stat(projectPath)
	if err != nil {
		return fmt.Errorf("cannot access path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", projectPath)
	}
	return nil
}
