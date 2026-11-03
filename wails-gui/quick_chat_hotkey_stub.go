//go:build !darwin && !windows

package main

// StartQuickChatHotkey is a no-op on Linux. The in-app keyboard
// shortcut (Ctrl+J) continues to work; the global hotkey is
// implemented for macOS (double-tap Option) and Windows (Alt+Space)
// only.
func (a *App) StartQuickChatHotkey() error { return nil }

// StopQuickChatHotkey is a no-op on Linux.
func (a *App) StopQuickChatHotkey() {}

// applyHotkeyToPlatform is a no-op on platforms without a global hook.
func applyHotkeyToPlatform(_ QuickChatHotkeyConfig) {}
