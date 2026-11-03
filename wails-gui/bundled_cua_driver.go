package main

import "errors"

// cua-driver is the macOS Accessibility-driven computer-use agent from
// https://github.com/trycua/cua. We embed CuaDriver.app under
// Yansu.app/Contents/Resources/cua-driver-bundle/CuaDriver.app at build
// time, with CFBundleIdentifier rewritten to com.yansu.cuadriver and
// re-signed with Yansu's Developer ID + the upstream entitlements
// (com.apple.security.automation.apple-events). At runtime we locate the
// nested binary, spawn `cua-driver serve --no-relaunch` over a Yansu-
// private Unix socket, and route computer-use calls through it.
//
// On TCC: Yansu spawns the daemon via exec.Command (posix_spawn), so
// macOS keeps the daemon's TCC responsibility at the parent — Yansu —
// and the daemon inherits Yansu's existing Accessibility + Screen
// Recording grants. com.yansu.cuadriver therefore never gets its own
// TCC entry under the current spawn model. Rebranding the bundle ID is
// cosmetic (keeps `cua-driver diagnose` from misreporting as the
// upstream Cua AI build) and would matter for TCC isolation only if we
// ever switched to a LaunchServices-based spawn (`open -n -g -a`),
// which is not the current path.
//
// On entitlements: the AppleEvents entitlement IS still needed on the
// nested binary — entitlements live with the Mach-O signature and do
// not propagate from a parent process — so sign_cua_driver_nested in
// build-wails.sh re-applies it under Yansu's Developer ID.

const (
	cuaDriverBundleDirName = "cua-driver-bundle"
	cuaDriverAppName       = "CuaDriver.app"
	cuaDriverBinaryName    = "cua-driver"
	// cuaDriverBundleID is the CFBundleIdentifier baked into the bundle's
	// Info.plist (wails-gui/cua-driver-src/Info.plist). Must stay in sync
	// with NEW_BUNDLE_ID in scripts/build-cua-driver-bundle.sh — TCC grants
	// are bound to this identifier together with the code-signing identity.
	cuaDriverBundleID = "com.yansu.cuadriver"
)

// errCuaDriverUnsupported is returned by LocateBundledCuaDriver on platforms
// where cua-driver is not shipped (everything except darwin-arm64). Callers
// should treat the feature as unavailable and surface a friendly message.
var errCuaDriverUnsupported = errors.New("cua-driver is only available on macOS (Apple Silicon)")
