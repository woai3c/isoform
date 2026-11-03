//go:build darwin

package main

import (
	"log"
	"strings"
)

// startActivityMonitorIfPermitted checks macOS permissions and starts
// activity monitoring if both Accessibility and Screen Recording are granted.
// The ActivityStore is always initialized so historical sessions remain
// queryable even when monitoring permissions are not granted.
// startActivityMonitorIfPermittedWith uses pre-fetched permission results to
// avoid redundant CGo calls when the caller already checked them.
func (a *App) startActivityMonitorIfPermittedWith(accessibilityGranted, screenCaptureGranted bool) {
	if a.activity == nil {
		a.initActivityMonitor()
	}

	if !accessibilityGranted || !screenCaptureGranted {
		log.Printf(
			"[activity] not started: accessibility=%t screenCapture=%t",
			accessibilityGranted,
			screenCaptureGranted,
		)
		updateMenuBarStatus(2) // no permission
		return
	}

	screenOK := a.isPermissionEnabled("screen_recording")
	a11yOK := a.isPermissionEnabled("accessibility")
	if !screenOK || !a11yOK {
		var reasons []string
		if !screenOK {
			reasons = append(reasons, "screen_recording")
		}
		if !a11yOK {
			reasons = append(reasons, "accessibility")
		}
		log.Printf("[activity] disabled by user (%s), skipping activity monitor start", strings.Join(reasons, ", "))
		return
	}

	if err := a.startActivityMonitor(); err != nil {
		log.Printf("[activity] failed to start: %v", err)
		updateMenuBarStatus(3) // stopped
	}
}

func (a *App) startActivityMonitorIfPermitted() {
	if a.activity == nil {
		a.initActivityMonitor()
	}

	accessibilityGranted := accessibilityAccess()
	screenCaptureGranted := preflightScreenCaptureAccess()
	if !accessibilityGranted || !screenCaptureGranted {
		log.Printf(
			"[activity] not started: accessibility=%t screenCapture=%t",
			accessibilityGranted,
			screenCaptureGranted,
		)
		updateMenuBarStatus(2) // no permission
		return
	}

	// Respect the user's permission toggles.
	screenOK := a.isPermissionEnabled("screen_recording")
	a11yOK := a.isPermissionEnabled("accessibility")
	if !screenOK || !a11yOK {
		var reasons []string
		if !screenOK {
			reasons = append(reasons, "screen_recording")
		}
		if !a11yOK {
			reasons = append(reasons, "accessibility")
		}
		log.Printf("[activity] disabled by user (%s), skipping activity monitor start", strings.Join(reasons, ", "))
		return
	}

	if err := a.startActivityMonitor(); err != nil {
		// Non-fatal: log and continue without activity monitoring
		log.Printf("[activity] failed to start: %v", err)
		updateMenuBarStatus(3) // stopped
	}
}
