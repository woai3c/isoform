//go:build !darwin

package main

func getTodayBriefPermissionStatus(*App) PlatformPermissions { return PlatformPermissions{} }
