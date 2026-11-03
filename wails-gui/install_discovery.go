package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"
)

// installDiscoveryDirName names the per-user directory we drop install.json
// into. Lives under os.UserConfigDir() — that maps to %APPDATA%/Yansu on
// Windows, ~/Library/Application Support/Yansu on macOS, and ~/.config/Yansu
// on Linux.
// It follows the build flavor, so a development Isoform never points external
// tools at itself as the installed Yansu.
func installDiscoveryDirName() string { return productName() }

// installDiscoveryFileName is the marker file external tools (most notably
// the Yansu skill that ships with coding agents) read to locate the running
// desktop app on disk. Windows ships a portable .zip that the user can
// extract anywhere, so there is no fixed install path to hard-code — this
// file is the discovery hook.
const installDiscoveryFileName = "install.json"

// installDiscovery is the JSON payload we write on every app launch. Schema
// is intentionally minimal and additive — external readers (skills, MCPs)
// MUST ignore unknown fields so we can extend this without breaking them.
type installDiscovery struct {
	// AppDir is the directory containing the running executable. On Windows
	// this is where `yansu-cli-bundle/`, `git-bundle/`, etc. live.
	AppDir string `json:"appDir"`
	// AppPath is the absolute path to the running executable.
	AppPath string `json:"appPath"`
	// CLI is the absolute path to the bundled yansu CLI binary that talks
	// to this app's local activity backend. Empty when the bundled CLI is
	// not findable (e.g. `wails dev` without a build).
	CLI string `json:"cli,omitempty"`
	// CLIInstalled is the home-dir copy maintained by ensureBundledYansuCLI.
	// Provided as a fallback for callers that can't reach AppDir (e.g. when
	// the portable bundle has been moved or unmounted since launch).
	CLIInstalled string `json:"cliInstalled,omitempty"`
	// Version is the app version baked in at build time via -ldflags.
	Version string `json:"version"`
	// Platform is runtime.GOOS — windows / darwin / linux.
	Platform string `json:"platform"`
	// WrittenAt is an RFC3339 timestamp of when this file was last written,
	// so stale discovery files from a previous install can be detected.
	WrittenAt string `json:"writtenAt"`
}

// writeInstallDiscovery refreshes the install.json marker so external tools
// can locate this app and its bundled CLI without asking the user. Safe to
// call on every launch — the write is one small file via an atomic rename
// and any failure is logged but never blocks startup.
func writeInstallDiscovery() {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("[install-discovery] cannot resolve executable: %v", err)
		return
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Printf("[install-discovery] cannot resolve user config dir: %v", err)
		return
	}

	discoveryDir := filepath.Join(configDir, installDiscoveryDirName())
	if err := os.MkdirAll(discoveryDir, 0o755); err != nil {
		log.Printf("[install-discovery] cannot create discovery dir %s: %v", discoveryDir, err)
		return
	}

	payload := installDiscovery{
		AppDir:       filepath.Dir(exePath),
		AppPath:      exePath,
		CLI:          findBundledYansuCLI(),
		CLIInstalled: installedYansuCLIPath(),
		Version:      Version,
		Platform:     goruntime.GOOS,
		WrittenAt:    time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		log.Printf("[install-discovery] cannot marshal payload: %v", err)
		return
	}

	discoveryPath := filepath.Join(discoveryDir, installDiscoveryFileName)
	tmpPath := discoveryPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		log.Printf("[install-discovery] write tmp failed: %v", err)
		return
	}
	if err := os.Rename(tmpPath, discoveryPath); err != nil {
		_ = os.Remove(tmpPath)
		log.Printf("[install-discovery] rename failed: %v", err)
		return
	}

	log.Printf("[install-discovery] wrote %s", discoveryPath)
}
