//go:build !windows

package main

// applyDwmRoundedFrameless is the Win11 frameless-rounded-corners hack
// (see dwm_rounded_windows.go). On macOS the frameless NSWindow paints
// the CSS border-radius natively without compositing artifacts, so no
// extra work is needed — this stub keeps main.go cross-platform.
func applyDwmRoundedFrameless(_ string) {}

// hideFromTaskbar is a Windows-only WS_EX_TOOLWINDOW trick. macOS
// frameless NSWindow + LSUIElement (or `accessory`) handles dock
// suppression a different way. Release builds launch the helper from
// its own LSUIElement .app bundle, while dev builds also set accessory
// activation policy at startup.
func hideFromTaskbar(_ string) {}

// setOnWindowDeactivated wires up auto-hide-on-blur on Windows. On
// macOS we'd hook NSWindow's `windowDidResignKey` via Cocoa bindings
// to do the same; not implemented yet (the macOS spotlight already
// has the focus-loss behaviour by default for accessory apps).
func setOnWindowDeactivated(_ func()) {}

// applyDwmDarkMode is the Windows-only DWMWA_USE_IMMERSIVE_DARK_MODE
// hint (see dwm_rounded_windows.go). On macOS the frameless NSWindow
// has no light/dark border to swap, so this is a no-op.
func applyDwmDarkMode(_ string, _ bool) {}
