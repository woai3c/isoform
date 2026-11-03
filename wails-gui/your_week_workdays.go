package main

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

const yourWeekWorkDaysStateKey = "your_week.workdays.v1"

type yourWeekWorkDaysState struct {
	WorkDays   []time.Weekday `json:"workDays"`
	ComputedAt time.Time      `json:"computedAt"`
}

func yourWeekDefaultWorkDays() []time.Weekday {
	return []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
}

func normalizeYourWeekWorkDays(days []time.Weekday) []time.Weekday {
	seen := make(map[time.Weekday]bool, len(days))
	out := make([]time.Weekday, 0, len(days))
	for _, day := range days {
		if day < time.Sunday || day > time.Saturday || seen[day] {
			continue
		}
		seen[day] = true
		out = append(out, day)
	}
	sort.Slice(out, func(i, j int) bool {
		return (int(out[i])+6)%7 < (int(out[j])+6)%7
	})
	return out
}

func sameYourWeekWorkDays(left, right []time.Weekday) bool {
	left, right = normalizeYourWeekWorkDays(left), normalizeYourWeekWorkDays(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (a *App) yourWeekStoredWorkDays(ctx context.Context) []time.Weekday {
	if a == nil || a.store == nil {
		return yourWeekDefaultWorkDays()
	}
	raw, err := a.store.GetUIState(ctx, yourWeekWorkDaysStateKey)
	if err != nil || raw == "" {
		return yourWeekDefaultWorkDays()
	}
	var state yourWeekWorkDaysState
	if json.Unmarshal([]byte(raw), &state) != nil {
		return yourWeekDefaultWorkDays()
	}
	days := normalizeYourWeekWorkDays(state.WorkDays)
	if len(days) == 0 {
		return yourWeekDefaultWorkDays()
	}
	return days
}

func (a *App) refreshYourWeekWorkDays(ctx context.Context, now time.Time) {
	if a == nil || a.store == nil {
		return
	}
	localNow := now.In(time.Local)
	if localNow.IsZero() {
		localNow = time.Now().In(time.Local)
	}
	if raw, err := a.store.GetUIState(ctx, yourWeekWorkDaysStateKey); err == nil && raw != "" {
		var state yourWeekWorkDaysState
		if json.Unmarshal([]byte(raw), &state) == nil && !state.ComputedAt.IsZero() &&
			state.ComputedAt.In(time.Local).Format("2006-01-02") == localNow.Format("2006-01-02") {
			return
		}
	}
	state := yourWeekWorkDaysState{WorkDays: a.yourWeekLearnWorkDays(ctx, localNow), ComputedAt: localNow}
	if data, err := json.Marshal(state); err == nil {
		_ = a.store.SetUIState(ctx, yourWeekWorkDaysStateKey, string(data))
	}
}

func (a *App) yourWeekLearnWorkDays(ctx context.Context, now time.Time) []time.Weekday {
	if a == nil || a.store == nil || a.activity == nil || a.activity.store == nil {
		return yourWeekDefaultWorkDays()
	}
	localNow := now.In(time.Local)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.Local)
	daysSinceMonday := (int(today.Weekday()) + 6) % 7
	currentWeek := today.AddDate(0, 0, -daysSinceMonday)
	installed := a.firstWeekInstall(ctx)
	if installed.IsZero() {
		return yourWeekDefaultWorkDays()
	}
	installedDay := installed.In(time.Local)
	installedDay = time.Date(installedDay.Year(), installedDay.Month(), installedDay.Day(), 0, 0, 0, 0, time.Local)
	if installedDay.AddDate(0, 0, 14).After(currentWeek) {
		return yourWeekDefaultWorkDays()
	}

	presentWeeks := make(map[time.Weekday]int, 7)
	for weekOffset := 4; weekOffset > 0; weekOffset-- {
		weekStart := currentWeek.AddDate(0, 0, -7*weekOffset)
		for dayOffset := 0; dayOffset < 7; dayOffset++ {
			dayStart := weekStart.AddDate(0, 0, dayOffset)
			_, present, err := a.activityMomentTimeMs(ctx, dayStart.UnixMilli(), dayStart.AddDate(0, 0, 1).UnixMilli())
			if err == nil && present > 0 {
				presentWeeks[dayStart.Weekday()]++
			}
		}
	}
	var learned []time.Weekday
	for day, weeks := range presentWeeks {
		if weeks >= 2 {
			learned = append(learned, day)
		}
	}
	learned = normalizeYourWeekWorkDays(learned)
	if len(learned) == 0 {
		return yourWeekDefaultWorkDays()
	}
	return learned
}
