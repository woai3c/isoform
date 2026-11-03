//go:build darwin

package main

// Today names only these three missing sources. In particular, reading a brief
// must not initialize AVAudioEngine or probe system audio and Messages.
func getTodayBriefPermissionStatus(*App) PlatformPermissions {
	return PlatformPermissions{Items: []PermissionItem{
		{Key: "screen_recording", Granted: preflightScreenCaptureAccess()},
		{Key: "accessibility", Granted: accessibilityAccess()},
		{Key: calendarPermissionKey, Granted: calendarAccessGranted()},
	}}
}
