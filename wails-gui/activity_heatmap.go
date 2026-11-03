package main

import (
	"context"
	"fmt"
	"time"
)

// GetActivityMonthStats supplies only the 42 calendar cells. No weekly digest,
// comparison period, OCR text, app breakdown or model generation is involved.
// Source gates and present-time arithmetic are the same as Day and Week.
func (a *App) GetActivityMonthStats(month string) (perfValue []DayStatBrief, perfErr error) {
	defer logPageBinding("GetActivityMonthStats", time.Now(), &perfValue)
	first, err := time.ParseInLocation("2006-01", month, time.Local)
	if err != nil {
		return nil, fmt.Errorf("invalid month: %w", err)
	}
	if a.activity == nil || a.activity.store == nil {
		return nil, fmt.Errorf("activity monitor not initialized")
	}
	start := first.AddDate(0, 0, -(int(first.Weekday())+6)%7)
	end := start.AddDate(0, 0, 42)
	store := a.activity.store
	sessions, err := store.modelSessionsOverlapping(start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return nil, err
	}
	sessions = a.filterActivityModelSessions(store, sessions)
	var breaks activityPresenceBreak
	if len(sessions) > 1 {
		breaks, err = a.activityPresenceBreaks(context.Background(), store, start.UnixMilli(), end.UnixMilli())
		if err != nil {
			return nil, err
		}
	}
	days := make([]DayStatBrief, 0, 42)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		lo, hi := day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()
		var overlapping []ActivitySessionRow
		for _, session := range sessions {
			if session.EndTime >= lo && session.StartTime < hi {
				overlapping = append(overlapping, session)
			}
		}
		active, _ := activityPresentTimeWithBreaks(overlapping, lo, hi, breaks)
		days = append(days, DayStatBrief{Date: day.Format("2006-01-02"), ActiveMs: active, SessionCount: len(overlapping)})
	}
	return days, nil
}
