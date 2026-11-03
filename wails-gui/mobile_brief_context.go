package main

import (
	"context"
	"time"
)

// collectMobileBriefContext adds the day marks the context signals kept
// (first input, last input, minutes away) to the day shape, when the day has
// one.
func collectMobileBriefContext(a *App, in *mobileBriefInput) {
	if a == nil || a.store == nil || in.DayShape == nil {
		return
	}
	loc := in.Location
	if loc == nil {
		loc = time.Local
	}
	date := in.AsOf.In(loc).Format("2006-01-02")
	marks, ok := loadContextDayMarks(context.Background(), a.store, date)
	if !ok {
		return
	}
	if marks.FirstActive > 0 {
		in.DayShape.FirstActive = formatMobileBriefTime(time.UnixMilli(marks.FirstActive))
	}
	if marks.LastActive > 0 {
		in.DayShape.LastActive = formatMobileBriefTime(time.UnixMilli(marks.LastActive))
	}
	in.DayShape.BreakMinutes = marks.BreakMinutes()
}
