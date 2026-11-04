package main

import (
	"strconv"
	"strings"
	"time"
)

// computerUses24Hour reports the computer's own 12- or 24-hour setting.
// Tests replace it.
var computerUses24Hour = systemUses24HourClock

// computerClock is a local time of day in the computer's format, the clock
// the app shows everywhere (frontend lib/brief-time.ts): "7:00 AM",
// "7:00" in Chinese, or "07:00" when the computer uses 24-hour time.
func computerClock(at time.Time, lang string) string {
	hour, minute := at.Hour(), at.Minute()
	// Chinese reads 24-hour with no leading zero and no 上午/下午: "17:28"
	// (today-review v1.1.20 R47).
	if strings.HasPrefix(lang, "zh") {
		return strconv.Itoa(hour) + ":" + strconv.Itoa(minute/10) + strconv.Itoa(minute%10)
	}
	if computerUses24Hour() {
		return at.Format("15:04")
	}
	clock := strconv.Itoa((hour+11)%12+1) + ":" + strconv.Itoa(minute/10) + strconv.Itoa(minute%10)
	if hour < 12 {
		return clock + " AM"
	}
	return clock + " PM"
}

// GetClockFormat tells the frontend the computer's clock: "24h" or "12h".
func (a *App) GetClockFormat() string {
	if computerUses24Hour() {
		return "24h"
	}
	return "12h"
}
