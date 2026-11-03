//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// telemetryWindowsVersion uses the native API so GUI startup never opens a
// console or waits for a child process just to obtain the OS release.
func telemetryWindowsVersion() string {
	vi := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", vi.MajorVersion, vi.MinorVersion, vi.BuildNumber)
}

// isWindows11OrGreater returns true when running on Windows 11 (build 22000+).
func isWindows11OrGreater() bool {
	vi := windows.RtlGetVersion()
	return vi.BuildNumber >= 22000
}
