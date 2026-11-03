package main

import (
	"context"
	"errors"
)

const todayScreenAskedKey = "today.screen_permission_asked"

type TodayPermissionStates struct {
	ScreenRecording string `json:"screenRecording"`
	Calendars       string `json:"calendars"`
}

// ScreenCapture exposes a granted preflight but no public denied/not-asked
// distinction. Preserve answers made through Today or the existing setup.
func todayScreenPermissionState(granted, asked bool, setupState string) string {
	if granted {
		return "granted"
	}
	if asked || setupState == "denied" {
		return "denied"
	}
	return "not_determined"
}

func todayCalendarPermissionState(authorization string) string {
	switch authorization {
	case "granted":
		return "granted"
	case "not_determined":
		return "not_determined"
	default:
		return "denied"
	}
}

// GetTodayPermissionStates probes only the sources used by the brief. It never
// asks macOS for permission.
func (a *App) GetTodayPermissionStates() (TodayPermissionStates, error) {
	if a == nil || a.store == nil {
		return TodayPermissionStates{}, errors.New("permission state unavailable")
	}
	asked, err := a.store.GetUIState(context.Background(), todayScreenAskedKey)
	if err != nil {
		return TodayPermissionStates{}, err
	}
	screen := false
	for _, item := range todayBriefPermissionStatus(a).Items {
		if item.Key == "screen_recording" {
			screen = item.Granted
		}
	}
	setup := loadSetupPermissions(setupPermissionsPath())
	setupScreen := ""
	for _, answer := range setup.Answers {
		if answer.Key == "screen_recording" {
			setupScreen = answer.State
		}
	}
	return TodayPermissionStates{ScreenRecording: todayScreenPermissionState(screen, asked == "true", setupScreen), Calendars: todayCalendarPermissionState(calendarAuthorization())}, nil
}

// RequestTodayPermission is called only from a person's explicit Turn on.
// It uses the same native request serialization as Settings and setup.
func (a *App) RequestTodayPermission(key string) (bool, error) {
	if a == nil || a.store == nil {
		return false, errors.New("permission state unavailable")
	}
	switch key {
	case "screen_recording", calendarPermissionKey:
	default:
		return false, errors.New("unsupported Today permission")
	}
	granted := a.RequestPermission(key)
	if key == "screen_recording" {
		if err := a.store.SetUIState(context.Background(), todayScreenAskedKey, "true"); err != nil {
			return granted, err
		}
		if granted {
			// Reuse the existing live permission check that starts capture only
			// when all required permissions are present.
			a.GetPlatformPermissions()
		}
	}
	if key == calendarPermissionKey && granted && !a.isPermissionEnabled(key) {
		if err := a.SetPermissionEnabled(key, true); err != nil {
			return granted, err
		}
	}
	a.emitBrowserEvent("permissions:changed")
	return granted, nil
}
