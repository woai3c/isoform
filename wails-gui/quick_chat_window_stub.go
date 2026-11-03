//go:build !darwin && !windows

package main

// ShowQuickChatWindow is a no-op on Linux. The in-app Ctrl+J dialog
// remains the only way to invoke QuickChat there.
func (a *App) ShowQuickChatWindow(_ string) {}

// CloseQuickChatWindow is a no-op on Linux.
func (a *App) CloseQuickChatWindow() {}

// pushQuickChatSuggestions is a no-op on Linux.
func (a *App) pushQuickChatSuggestions(_ []RecommendedQuestion, _ string) {}

// pushQuickChatLoading is a no-op on Linux.
func (a *App) pushQuickChatLoading(_ string) {}
