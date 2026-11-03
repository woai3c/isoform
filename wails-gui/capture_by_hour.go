package main

import (
	"fmt"
	"time"

	"wails-gui/internal/platform/activitymon"
)

type CaptureHour struct {
	HourStart     int64 `json:"hourStart"`
	Screens       int64 `json:"screens"`
	SpeechMinutes int64 `json:"speechMinutes"`
}

type CaptureByHour struct {
	Paused   bool          `json:"paused"`
	NotReady bool          `json:"notReady"`
	DayStart int64         `json:"dayStart"`
	Hours    []CaptureHour `json:"hours"`
}

// GetCaptureByHour returns today's screen and distinct speech-minute counts by local hour.
func (a *App) GetCaptureByHour() (CaptureByHour, error) {
	return a.captureByHour(time.Now())
}

func (a *App) captureByHour(now time.Time) (CaptureByHour, error) {
	if a == nil {
		return CaptureByHour{}, nil
	}
	start, _ := captureCountWindow(0, now)
	out := CaptureByHour{DayStart: start.UnixMilli(), Hours: []CaptureHour{}}
	if a.IsClosedEyesModeEnabled() {
		out.Paused = true
		return out, nil
	}
	if a.activity == nil || a.activity.store == nil {
		out.NotReady = true
		return out, nil
	}
	// Use the same sources and lower bound as captureCountQueries. Grouping
	// by clock minute avoids reading screen rows or speech payloads into Go.
	tx, err := a.activity.store.reader().Begin()
	if err != nil {
		return CaptureByHour{}, err
	}
	defer tx.Rollback()
	byHour := map[int64]CaptureHour{}
	queries := []string{
		`SELECT timestamp / 60000, COUNT(*) FROM activity_snapshots snap
		CROSS JOIN activity_sessions s ON s.id = snap.session_id
		WHERE timestamp >= ?` + sqlExcludeProtected + ` GROUP BY timestamp / 60000`,
		`SELECT timestamp / 60000, 1 FROM activity_events e
		CROSS JOIN activity_sessions s ON s.id = e.session_id
		WHERE kind = '` + string(activitymon.EventSpeech) + `' AND timestamp >= ?` + sqlExcludeProtected + ` GROUP BY timestamp / 60000`,
	}
	for source, query := range queries {
		rows, err := tx.Query(query, out.DayStart)
		if err != nil {
			return CaptureByHour{}, fmt.Errorf("capture_by_hour: %w", err)
		}
		for rows.Next() {
			var minute, count int64
			if err := rows.Scan(&minute, &count); err != nil {
				rows.Close()
				return CaptureByHour{}, err
			}
			hour := observationHourStart(time.UnixMilli(minute * 60000).In(start.Location())).UnixMilli()
			row := byHour[hour]
			row.HourStart = hour
			if source == 0 {
				row.Screens += count
			} else {
				row.SpeechMinutes += count
			}
			byHour[hour] = row
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return CaptureByHour{}, readErr
		}
	}
	if err := tx.Commit(); err != nil {
		return CaptureByHour{}, err
	}
	if len(byHour) > 0 {
		first := now
		for hour := range byHour {
			if at := time.UnixMilli(hour); at.Before(first) {
				first = at
			}
		}
		for hour := observationHourStart(first.In(start.Location())); !hour.After(now); hour = hour.Add(time.Hour) {
			row := byHour[hour.UnixMilli()]
			row.HourStart = hour.UnixMilli()
			out.Hours = append(out.Hours, row)
		}
	}
	if a.IsClosedEyesModeEnabled() {
		out.Paused, out.Hours = true, []CaptureHour{}
	}
	return out, nil
}

// Subtraction retains the offset of a repeated hour at the autumn DST change.
func observationHourStart(at time.Time) time.Time {
	return at.Add(-time.Duration(at.Minute())*time.Minute - time.Duration(at.Second())*time.Second - time.Duration(at.Nanosecond()))
}
