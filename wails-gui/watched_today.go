package main

import (
	"database/sql"
	"log"
	"time"
)

// watched_today.go answers the "Watching your work" row on Today: when today's
// screen capture began, how many distinct minutes had one, and whether the
// screen permission and observation are on. It is one indexed aggregate over
// today's snapshots, read when Today opens and then once a minute.

// WatchedToday is the Today row's data.
type WatchedToday struct {
	StartedAt       int64 `json:"startedAt"`       // unix ms of the first screen capture today; 0 if none
	Minutes         int   `json:"minutes"`         // distinct minutes today with at least one screen capture
	ScreenRecording bool  `json:"screenRecording"` // the screen permission is granted
	Paused          bool  `json:"paused"`          // observation is paused or closed eyes is on
}

// GetWatchedToday reads today's capture state.
func (a *App) GetWatchedToday() (perfValue WatchedToday) {
	defer logPageBinding("GetWatchedToday", time.Now(), &perfValue)
	if a == nil {
		return WatchedToday{}
	}
	return a.watchedTodayAt(time.Now(), a.watchedTodayScreenGranted())
}

// watchedTodayScreenGranted reads the screen permission from the same platform
// state the Permissions page shows.
func (a *App) watchedTodayScreenGranted() bool {
	for _, item := range a.GetPlatformPermissions().Items {
		if item.Key == "screen_recording" {
			return item.Granted
		}
	}
	return false
}

func (a *App) watchedTodayAt(now time.Time, screenGranted bool) WatchedToday {
	out := WatchedToday{ScreenRecording: screenGranted, Paused: a.IsClosedEyesModeEnabled()}
	if out.Paused || a.activity == nil || a.activity.store == nil {
		return out
	}
	start, _ := captureCountWindow(0, now)
	var first sql.NullInt64
	var minutes int64
	err := a.activity.store.reader().QueryRow(`SELECT MIN(snap.timestamp), COUNT(DISTINCT snap.timestamp / 60000)
		FROM activity_snapshots snap
		CROSS JOIN activity_sessions s ON s.id = snap.session_id
		WHERE snap.timestamp >= ?`+sqlExcludeProtected, start.UnixMilli()).Scan(&first, &minutes)
	if err != nil {
		log.Printf("[watched-today] capture read: %v", err)
		return out
	}
	if first.Valid {
		out.StartedAt = first.Int64
	}
	out.Minutes = int(minutes)
	return out
}
