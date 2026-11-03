//go:build !windows

package main

func telemetryWindowsVersion() string { return "unknown" }

// isWindows11OrGreater always returns false on non-Windows platforms.
func isWindows11OrGreater() bool { return false }
