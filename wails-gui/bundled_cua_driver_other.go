//go:build !darwin

package main

// CuaDriverPaths is declared on non-darwin platforms purely so callers
// share one type. The Locate function below always returns an error, so
// no field is ever populated.
type CuaDriverPaths struct {
	AppPath    string
	BinaryPath string
}

// LocateBundledCuaDriver always reports unsupported on non-darwin builds.
// cua-driver is macOS-only; see scripts/build-cua-driver-bundle.sh which
// no-ops on Windows/Linux for the same reason.
func LocateBundledCuaDriver() (*CuaDriverPaths, error) {
	return nil, errCuaDriverUnsupported
}

func installedCuaDriverPath() string { return "" }
