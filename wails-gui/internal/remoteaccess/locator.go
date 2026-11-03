package remoteaccess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// LocateBundledBinary resolves an explicit override, application resources,
// development bundle paths, and finally PATH, in that order.
func LocateBundledBinary(overrideEnv, bundleDir, appBinary, developmentBinary, pathBinary string) (string, error) {
	if override := os.Getenv(overrideEnv); override != "" {
		if err := executable(override); err != nil {
			return "", fmt.Errorf("%s=%q: %w", overrideEnv, override, err)
		}
		return filepath.Abs(override)
	}

	var candidates []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, bundleDir, appBinary),
			filepath.Join(exeDir, "..", "Resources", bundleDir, appBinary),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, bundleDir, developmentBinary),
			filepath.Join(cwd, "wails-gui", bundleDir, developmentBinary),
		)
	}
	for _, candidate := range candidates {
		if err := executable(candidate); err == nil {
			return filepath.Abs(candidate)
		}
	}
	pathBinaries := []string{pathBinary}
	for _, name := range pathBinaries {
		if path, err := exec.LookPath(name); err == nil {
			if err := executable(path); err == nil {
				return filepath.Abs(path)
			}
		}
	}
	return "", errors.New("binary not found; checked app resources, development bundles, and PATH")
}

// DevelopmentBinaryName returns the platform-specific bundle filename.
func DevelopmentBinaryName(name string) string {
	return fmt.Sprintf("%s-%s-%s", name, runtime.GOOS, runtime.GOARCH)
}
