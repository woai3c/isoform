//go:build !windows

package main

// isWindows11OrGreater always returns false off Windows.
func isWindows11OrGreater() bool { return false }
