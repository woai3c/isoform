//go:build windows

package main

import (
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTelemetryWindowsVersionWorksWithoutShell(t *testing.T) {
	// Telemetry must work without resolving cmd.exe or any other subprocess.
	t.Setenv("PATH", t.TempDir())
	version := windows.RtlGetVersion()
	want := fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
	if got := telemetryWindowsVersion(); got != want {
		t.Fatalf("OS version = %q, want native release %q", got, want)
	}
}
