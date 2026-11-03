package main

import (
	"log"
	"os"
	"strings"

	"wails-gui/internal/apphome"
)

// Build flavor. The release app and the development app are both Isoform:
// bundle id com.wails.Isoform and data folder ~/.isoform-agent, so the person
// keeps one database across both. The flavor comes from YANSU_APP_FLAVOR (set
// by scripts/start-wails-gui.sh for dev runs) or from the -X main.buildFlavor
// ldflag (scripts/build-wails.sh sets it for release and development builds).
// Unflavored binaries such as go test keep the legacy Yansu flavor.
// Info.dev.plist carries the matching bundle name and identifier for wails dev.
//
// The build channel separates the release from the development build of the
// same identity: only a release build checks for updates.

// buildFlavor is set with -ldflags "-X main.buildFlavor=isoform".
var buildFlavor string

// buildChannel is set with -ldflags "-X main.buildChannel=release" by
// scripts/build-wails.sh release builds; development builds leave it empty.
var buildChannel string

const buildChannelRelease = "release"

// isReleaseChannel reports whether build-wails.sh built this binary as the release.
func isReleaseChannel() bool {
	return strings.ToLower(strings.TrimSpace(buildChannel)) == buildChannelRelease
}

const (
	appFlavorYansu   = "yansu"
	appFlavorIsoform = "isoform"
	appFlavorEnv     = "YANSU_APP_FLAVOR"
	// agentHomeEnv names the data folder for the embedded agent packages and
	// the bundled yansu CLI, so every process of one flavor shares one folder.
	agentHomeEnv = "YANSU_AGENT_HOME"
)

// configDirName is the data folder under the home directory for this flavor.
var configDirName = configDirForFlavor(appFlavor())

// appFlavor keeps a compiled flavor fixed. Unflavored development binaries
// can select their flavor through the environment; otherwise they use Yansu.
func appFlavor() string {
	for _, raw := range []string{buildFlavor, os.Getenv(appFlavorEnv)} {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case appFlavorIsoform:
			return appFlavorIsoform
		case appFlavorYansu:
			return appFlavorYansu
		default:
		}
	}
	return appFlavorYansu
}

// appFlavorName is the product name of a flavor.
func appFlavorName(flavor string) string {
	switch flavor {
	case appFlavorIsoform:
		return "Isoform"
	default:
		return "Yansu"
	}
}

// configDirForFlavor is the data folder name of a flavor.
func configDirForFlavor(flavor string) string {
	switch flavor {
	case appFlavorIsoform:
		return ".isoform-agent"
	default:
		return ".yansu-agent"
	}
}

// productName is the user-facing name of this build.
func productName() string {
	return appFlavorName(appFlavor())
}

// flavorDataDir is the single root for desktop data, caches and bundled tools.
// Resolve it at use time so an explicit agent home also applies before main()
// exports the default for embedded packages and child processes.
func flavorDataDir() (string, error) {
	return apphome.Dir(configDirName)
}

// exportAgentHome tells the embedded agent packages and every CLI this
// process spawns which data folder to use. An explicit YANSU_AGENT_HOME in the
// environment wins, so a developer can still point one run elsewhere.
func exportAgentHome() error {
	dir, err := flavorDataDir()
	if err != nil {
		return err
	}
	if err := os.Setenv(agentHomeEnv, dir); err != nil {
		return err
	}
	log.Printf("[flavor] %s data folder: %s", productName(), dir)
	return nil
}
