//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func getPlatformPermissions() PlatformPermissions {
	var items []PermissionItem

	if isWayland() {
		items = append(items, PermissionItem{
			Key:         "pipewire",
			Label:       "Screen Capture (PipeWire)",
			Description: "PipeWire and xdg-desktop-portal are required for screen capture on Wayland. Install them via your package manager if missing.",
			Granted:     checkPipeWireAvailable(),
			Required:    true,
			Actionable:  false,
			ActionLabel: "",
			Toggleable:  true,
			Enabled:     true,
		})
		items = append(items, PermissionItem{
			Key:         "portal",
			Label:       "Desktop Portal",
			Description: "xdg-desktop-portal provides the screen sharing dialog on Wayland. A compositor-specific backend (e.g. xdg-desktop-portal-gnome or xdg-desktop-portal-wlr) must be installed.",
			Granted:     checkDesktopPortalAvailable(),
			Required:    true,
			Actionable:  false,
			ActionLabel: "",
		})
		items = append(items, PermissionItem{
			Key:         "uinput",
			Label:       "Input Automation (uinput)",
			Description: "Required for keyboard and mouse automation on Wayland via ydotool. Your user must be in the 'input' group with access to /dev/uinput.",
			Granted:     checkUInputAccess(),
			Required:    false,
			Actionable:  false,
			ActionLabel: "",
			Toggleable:  true,
			Enabled:     true,
		})
	} else {
		items = append(items, PermissionItem{
			Key:         "screen_capture",
			Label:       "Screen Capture (X11)",
			Description: "Uses the X11 protocol for screen capture. Available automatically under X11 sessions.",
			Granted:     true,
			Required:    true,
			Actionable:  false,
			ActionLabel: "",
			Toggleable:  true,
			Enabled:     true,
		})
		items = append(items, PermissionItem{
			Key:         "input_automation",
			Label:       "Input Automation (X11)",
			Description: "Uses XTest/xdotool for keyboard and mouse control. Available automatically under X11 sessions.",
			Granted:     true,
			Required:    true,
			Actionable:  false,
			ActionLabel: "",
			Toggleable:  true,
			Enabled:     true,
		})
	}

	items = append(items, PermissionItem{
		Key:         "contacts",
		Label:       "Contacts",
		Description: "Import a vCard (.vcf) file from the Who you know page to add contacts from this computer.",
		Granted:     false,
		Required:    false,
		Actionable:  false,
		ActionLabel: "",
	})

	return PlatformPermissions{
		Platform:  "linux",
		Supported: true,
		Items:     items,
	}
}

func requestPermission(key string) bool {
	return false
}

func openPermissionSettings(key string) error {
	return fmt.Errorf("no interactive permission settings available on Linux for key %q", key)
}

func openPermissionSettingsFromRect(key string, _ float64, _ float64, _ float64, _ float64) error {
	return openPermissionSettings(key)
}

func isWayland() bool {
	return os.Getenv("XDG_SESSION_TYPE") == "wayland" ||
		os.Getenv("WAYLAND_DISPLAY") != ""
}

func checkPipeWireAvailable() bool {
	err := exec.Command("pw-cli", "info", "0").Run()
	return err == nil
}

func checkDesktopPortalAvailable() bool {
	err := exec.Command("dbus-send",
		"--session",
		"--dest=org.freedesktop.portal.Desktop",
		"--type=method_call",
		"--print-reply",
		"/org/freedesktop/portal/desktop",
		"org.freedesktop.DBus.Peer.Ping",
	).Run()
	return err == nil
}

func checkUInputAccess() bool {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func setSystemAudioPermissionGranted(_ bool) {}

// requestMicrophoneBeforeListening is a no-op here: only macOS shows a
// microphone dialog that must not overlap the system audio prompt.
func requestMicrophoneBeforeListening() {}
