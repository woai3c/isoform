package main

import "strings"

// systemNoiseAppClass keeps the historical work-pattern exclusions (1)
// separate from additional presentation-only exclusions (2).
func systemNoiseAppClass(name, bundleID string) int {
	key := strings.ToLower(strings.TrimSpace(bundleID))
	name = strings.ToLower(strings.Join(strings.Fields(name), " "))
	values := []string{key, strings.TrimPrefix(key, "name:"), name}
	for _, value := range values {
		switch value {
		case "loginwindow", "windowmanager", "window manager", "systemuiserver", "system ui server",
			"controlcenter", "control center", "notificationcenter", "notification center", "notificationcenterui",
			"dock", "screensaverengine", "screen saver engine":
			return 1
		}
	}
	switch key {
	case "com.apple.loginwindow", "com.apple.windowmanager", "com.apple.systemuiserver", "com.apple.controlcenter",
		"com.apple.notificationcenterui", "com.apple.dock", "com.apple.screensaver.engine":
		return 1
	}
	for _, value := range values {
		switch value {
		case "windowserver", "window server", "spotlight", "usernotificationcenter", "user notification center",
			"com.apple.windowserver", "com.apple.spotlight", "com.apple.usernotificationcenter":
			return 2
		}
	}
	return 0
}

func isSystemNoiseApp(name, bundleID string) bool { return systemNoiseAppClass(name, bundleID) != 0 }
