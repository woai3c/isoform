package main

import (
	"sort"
	"time"
)

// Include every civil day a changed moment can appear in, including carries.
// Advance the local calendar so spring/autumn days are never treated as 24h.
func activityAddSpanDates(dates map[string]bool, start, end int64) {
	if start <= 0 {
		return
	}
	last := max(start, end-1)
	for day := startOfDayLocal(start); day <= last; day = activityThreadDayEnd(day) {
		dates[time.UnixMilli(day).In(time.Local).Format("2006-01-02")] = true
	}
}

func activityAddRowDates(dates map[string]bool, row ActivityRow) {
	activityAddSpanDates(dates, row.StartMs, row.EndMs)
	if row.DayStartMs > 0 {
		activityAddSpanDates(dates, row.DayStartMs, row.DayStartMs)
	}
}

func (a *App) emitActivityDateUpdate(dates map[string]bool) {
	if a == nil {
		return
	}
	if len(dates) == 0 {
		a.emitBrowserEvent("activity:activities-updated")
		return
	}
	days := make([]string, 0, len(dates))
	for day := range dates {
		days = append(days, day)
	}
	sort.Strings(days)
	today := time.Now().In(time.Local).Format("2006-01-02")
	a.emitBrowserEvent("activity:activities-updated", struct {
		Dates []string `json:"dates"`
		Live  bool     `json:"live,omitempty"`
	}{days, len(days) == 1 && days[0] == today})
}
