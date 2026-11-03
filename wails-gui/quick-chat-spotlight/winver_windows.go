//go:build windows

package main

import "golang.org/x/sys/windows"

// isWindows11OrGreater returns true on Windows 11 (build 22000+) so
// main.go can pick the Mica backdrop instead of plain transparency.
func isWindows11OrGreater() bool {
	vi := windows.RtlGetVersion()
	return vi.BuildNumber >= 22000
}
