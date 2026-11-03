//go:build !darwin && !windows

package activitymon

// Stub implementation for non-macOS, non-Windows platforms (Linux today).
// Windows has its own real ListRunningApps in computer_use_windows.go;
// Linux can grow one analogously when needed.
func ComputerUseSupported() bool { return false }

// ComputerUsePermitted returns false on non-macOS platforms.
func ComputerUsePermitted() bool { return false }

// CollectAppElements is a no-op on non-macOS platforms.
func CollectAppElements(appName string) []UIElement { return nil }

// ListRunningApps is a no-op on non-macOS platforms.
func ListRunningApps() []RunningApp { return nil }

// ListAppWindows is a no-op on non-macOS platforms.
func ListAppWindows(appName string) []AppWindow { return nil }

// OpenURL is a no-op on non-macOS platforms.
func OpenURL(url string) bool { return false }

// ClickAt is a no-op on non-macOS platforms.
func ClickAt(x, y float64) {}

// TypeText is a no-op on non-macOS platforms.
func TypeText(text string) {}

// PressKey is a no-op on non-macOS platforms.
func PressKey(keyName string) {}

// Hotkey is a no-op on non-macOS platforms.
func Hotkey(combo string) {}

// Scroll is a no-op on non-macOS platforms.
func Scroll(deltaX, deltaY int) {}

// Drag is a no-op on non-macOS platforms.
func Drag(fromX, fromY, toX, toY float64) {}

// PasteText is a no-op on non-macOS platforms.
func PasteText(text string) {}
