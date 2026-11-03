//go:build darwin

package main

// setupNativePermissions reads setup's status keys, in setupPermissionOrder,
// except Mail: macOS answers about Mail only while it runs, and an Apple
// Events status read may not delay this one, so Mail keeps its saved answer.
func setupNativePermissions() PlatformPermissions {
	return PlatformPermissions{Platform: "darwin", Supported: true, Items: []PermissionItem{
		{Key: calendarPermissionKey, Granted: calendarAccessGranted()},
		{Key: remindersPermissionKey, Granted: remindersAccessGranted()},
		{Key: "contacts", Granted: contactsAccessGranted()},
		{Key: "microphone", Granted: microphoneAccess()},
		{Key: "system_audio", Granted: systemAudioAccess()},
		{Key: "accessibility", Granted: accessibilityAccess()},
		{Key: "screen_recording", Granted: preflightScreenCaptureAccess()},
	}}
}

// setupPermissionAskable reports whether macOS can show key's prompt now.
func setupPermissionAskable(key string) bool {
	switch key {
	case mailPermissionKey:
		return mailRunning()
	default:
		return true
	}
}
