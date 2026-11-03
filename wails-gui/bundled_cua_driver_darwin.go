//go:build darwin

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
)

// CuaDriverPaths describes a located cua-driver bundle on disk.
type CuaDriverPaths struct {
	// AppPath is the absolute path to the CuaDriver.app directory.
	AppPath string
	// BinaryPath is the absolute path to CuaDriver.app/Contents/MacOS/cua-driver.
	BinaryPath string
}

// LocateBundledCuaDriver returns the on-disk paths for the embedded
// CuaDriver.app, or errCuaDriverUnsupported when the current build target
// can't run it. Search order:
//
//  1. Yansu.app/Contents/Resources/cua-driver-bundle/CuaDriver.app — the
//     production embed produced by build-wails.sh.
//  2. wails-gui/cua-driver-bundle/CuaDriver.app next to the dev binary or
//     the working directory — what `start-wails-gui.sh` sees.
//
// We never copy the bundle out of its embed location: TCC on macOS binds
// grants to a code-signing requirement that includes the binary's signing
// identity, so relocating it would invalidate prior grants on every
// upgrade. Callers spawn the binary in place.
func LocateBundledCuaDriver() (*CuaDriverPaths, error) {
	if goruntime.GOARCH != "arm64" {
		return nil, errCuaDriverUnsupported
	}

	for _, appPath := range bundledCuaDriverAppCandidates() {
		appPath = strings.TrimSpace(appPath)
		if appPath == "" {
			continue
		}
		binPath := filepath.Join(appPath, "Contents", "MacOS", cuaDriverBinaryName)
		if !isExecutableFile(binPath) {
			continue
		}
		if !compatibleCuaDriverBundle(appPath) {
			log.Printf("[cua-driver] skipping incompatible bundle at %s", appPath)
			continue
		}
		return &CuaDriverPaths{AppPath: appPath, BinaryPath: binPath}, nil
	}
	return nil, fmt.Errorf("cua-driver bundle not found in app Resources or working directory")
}

const minAlmaCuaDriverBundleVersion = "0.2.0"

func compatibleCuaDriverBundle(appPath string) bool {
	versionPath := filepath.Join(filepath.Dir(appPath), ".version")
	raw, err := os.ReadFile(versionPath)
	if err != nil {
		return false
	}
	version := strings.TrimSpace(string(raw))
	return semverAtLeast(version, minAlmaCuaDriverBundleVersion)
}

func semverAtLeast(version, minimum string) bool {
	v := parseSemver(version)
	m := parseSemver(minimum)
	for i := range v {
		if v[i] > m[i] {
			return true
		}
		if v[i] < m[i] {
			return false
		}
	}
	return true
}

func parseSemver(version string) [3]int {
	var out [3]int
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(version, "v")), ".")
	for i := 0; i < len(parts) && i < len(out); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}
		}
		out[i] = n
	}
	return out
}

// installedCuaDriverPath returns BinaryPath if the bundled cua-driver is
// available, otherwise empty string. Mirrors installedBunPath so callers
// can branch without dealing with the typed error.
func installedCuaDriverPath() string {
	paths, err := LocateBundledCuaDriver()
	if err != nil || paths == nil {
		return ""
	}
	return paths.BinaryPath
}

// bundledCuaDriverAppCandidates returns the paths we'll probe in priority
// order. The list is short on purpose — we only support the production
// embed and the dev-mode location, not arbitrary discovery, so the user
// can't end up running an old un-rebranded CuaDriver.app by mistake.
func bundledCuaDriverAppCandidates() []string {
	candidates := make([]string, 0, 4)

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			// macOS .app bundle: Contents/MacOS/Yansu → ../Resources/cua-driver-bundle/CuaDriver.app
			filepath.Join(exeDir, "..", "Resources", cuaDriverBundleDirName, cuaDriverAppName),
			// Dev binary co-located with the bundle dir.
			filepath.Join(exeDir, cuaDriverBundleDirName, cuaDriverAppName),
		)
	}

	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			// `wails dev` from inside wails-gui/.
			filepath.Join(wd, cuaDriverBundleDirName, cuaDriverAppName),
			// `go test ./...` or similar from the repo root.
			filepath.Join(wd, "wails-gui", cuaDriverBundleDirName, cuaDriverAppName),
		)
	}

	return candidates
}
