//go:build !darwin

package main

// cuaFrontmost is a no-op on non-darwin builds. cua-driver itself is
// macOS-only (NSWorkspace + AX), so the proxy's focus-change audit log
// has nothing to compare against on Linux/Windows.
func cuaFrontmost() (pid int32, bundleID string, name string) {
	return 0, "", ""
}
