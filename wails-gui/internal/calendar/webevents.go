package calendar

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WebEventItem is one event chip read from a web calendar's week view by the
// accessibility parser: raw title, time, location and date text.
type WebEventItem struct {
	Title    string
	Time     string
	Location string
	Date     string
}

// ParseWebEvents turns chips into events for a web provider. Local times are
// read in loc. Chips without a full date or without a readable time are
// skipped: a guessed day would feed false meeting reminders and conflicts.
// Ids are stable across reads of the same title at the same start, so a
// repeated read updates rather than duplicates.
func ParseWebEvents(items []WebEventItem, provider string, now time.Time, loc *time.Location) []Event {
	if loc == nil {
		loc = time.UTC
	}
	var out []Event
	seen := map[string]bool{}
	for _, item := range items {
		title := cleanText(item.Title)
		if title == "" {
			continue
		}
		day, ok := ParseWebDate(item.Date, now, loc)
		if !ok {
			continue
		}
		start, end, allDay, ok := ParseWebTimeRange(item.Time, day, loc)
		if !ok {
			continue
		}
		ev := Event{
			UID:          provider + "\x00" + strings.ToLower(title) + "\x00" + strings.ToLower(cleanText(item.Location)),
			Source:       SourceWeb,
			CalendarID:   WebSourceID(provider),
			Provider:     provider,
			Title:        title,
			Start:        start,
			End:          end,
			AllDay:       allDay,
			TimeZone:     loc.String(),
			Location:     cleanText(item.Location),
			Availability: "busy",
			Status:       "confirmed",
			MyStatus:     StatusUnknown,
		}
		ev.ID = EventID(SourceWeb, ev.UID, start)
		if seen[ev.ID] {
			continue
		}
		seen[ev.ID] = true
		ev.ConferenceURL, ev.ConferenceHost = ExtractConferenceLink(ev.Location, "", "")
		out = append(out, ev)
	}
	SortByStart(out)
	return out
}

var (
	webDateLongPattern    = regexp.MustCompile(`(?i)\b(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sept?(?:ember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(\d{4})\b`)
	webDateDayFirst       = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sept?(?:ember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\.?\s+(\d{4})\b`)
	webDateISO            = regexp.MustCompile(`\b(\d{4})[-/](\d{1,2})[-/](\d{1,2})\b`)
	webDateChineseFull    = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	webDateChineseNoYear  = regexp.MustCompile(`(\d{1,2})月\s*(\d{1,2})日`)
	webTimeToken          = regexp.MustCompile(`(?i)(上午|下午|早上|晚上|中午|凌晨)?\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)?`)
	webTimeRangeSeparator = regexp.MustCompile(`(?i)\s*(?:–|—|-|to|至|~)\s*`)
)

var monthByName = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

// ParseWebDate reads a full date in the forms "October 14, 2026", "Oct 14
// 2026", "14 October 2026", "2026-10-14", "2026/10/14", "2026年10月14日" or
// "10月14日" (year taken from now). It returns local midnight in loc.
func ParseWebDate(text string, now time.Time, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.UTC
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}, false
	}
	if m := webDateChineseFull.FindStringSubmatch(text); m != nil {
		return makeDate(m[1], m[2], m[3], loc)
	}
	if m := webDateISO.FindStringSubmatch(text); m != nil {
		return makeDate(m[1], m[2], m[3], loc)
	}
	if m := webDateLongPattern.FindStringSubmatch(text); m != nil {
		if month, ok := monthByName[strings.ToLower(m[1][:3])]; ok {
			return makeDate(m[3], strconv.Itoa(int(month)), m[2], loc)
		}
	}
	if m := webDateDayFirst.FindStringSubmatch(text); m != nil {
		if month, ok := monthByName[strings.ToLower(m[2][:3])]; ok {
			return makeDate(m[3], strconv.Itoa(int(month)), m[1], loc)
		}
	}
	if m := webDateChineseNoYear.FindStringSubmatch(text); m != nil {
		return makeDate(strconv.Itoa(now.In(loc).Year()), m[1], m[2], loc)
	}
	return time.Time{}, false
}

func makeDate(year, month, day string, loc *time.Location) (time.Time, bool) {
	y, errY := strconv.Atoi(year)
	m, errM := strconv.Atoi(month)
	d, errD := strconv.Atoi(day)
	if errY != nil || errM != nil || errD != nil || m < 1 || m > 12 || d < 1 || d > 31 || y < 1970 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, loc)
	if t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// ParseWebTimeRange reads "10am – 11am", "10:30am to 11:15am", "10:00 –
// 11:00", "10 – 11 AM" (one meridiem for both), "from 10:00 AM to 11:00 AM",
// "上午10:00 至 下午12:00" or "all day" / "全天" on the given local day. A
// lone start time gets one hour. It returns false for anything else.
func ParseWebTimeRange(text string, day time.Time, loc *time.Location) (start, end time.Time, allDay bool, ok bool) {
	if loc == nil {
		loc = time.UTC
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return time.Time{}, time.Time{}, false, false
	}
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	if strings.Contains(lower, "all day") || strings.Contains(lower, "all-day") || strings.Contains(lower, "全天") {
		return dayStart, dayStart.AddDate(0, 0, 1), true, true
	}
	lower = strings.TrimPrefix(lower, "from ")
	parts := webTimeRangeSeparator.Split(lower, 2)
	first, firstMeridiem, ok1 := parseClockToken(parts[0])
	if !ok1 {
		return time.Time{}, time.Time{}, false, false
	}
	var second clockValue
	var secondMeridiem string
	ok2 := false
	if len(parts) == 2 {
		second, secondMeridiem, ok2 = parseClockToken(parts[1])
	}
	// "10 – 11 AM": the meridiem written once applies to both ends, and
	// "11:30 – 1pm" means the start is in the morning.
	if firstMeridiem == "" && secondMeridiem != "" {
		firstMeridiem = secondMeridiem
		if ok2 && first.hour > second.hour && first.hour != 12 && secondMeridiem == "pm" {
			firstMeridiem = "am"
		}
	}
	start = first.at(dayStart, firstMeridiem, loc)
	if ok2 {
		if secondMeridiem == "" {
			secondMeridiem = firstMeridiem
		}
		end = second.at(dayStart, secondMeridiem, loc)
		if !end.After(start) {
			// An end before the start crosses noon ("11 – 1") or midnight.
			if secondMeridiem == "" || (secondMeridiem == "am" && firstMeridiem == "am") {
				end = end.Add(12 * time.Hour)
			}
			if !end.After(start) {
				end = end.Add(12 * time.Hour)
			}
		}
	} else {
		end = start.Add(time.Hour)
	}
	return start, end, false, true
}

type clockValue struct {
	hour, minute int
}

// at places the clock value on the given day in loc through time.Date, so
// a day that gains or loses an hour (DST) still reads the wall clock.
func (c clockValue) at(day time.Time, meridiem string, loc *time.Location) time.Time {
	hour := c.hour
	switch meridiem {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, c.minute, 0, 0, loc)
}

// parseClockToken reads "10", "10:30", "10am", "10:30 pm", "下午3:00". The
// meridiem is "am", "pm" or "" when the text carries none.
func parseClockToken(text string) (clockValue, string, bool) {
	m := webTimeToken.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return clockValue{}, "", false
	}
	hour, err := strconv.Atoi(m[2])
	if err != nil || hour > 24 {
		return clockValue{}, "", false
	}
	minute := 0
	if m[3] != "" {
		minute, _ = strconv.Atoi(m[3])
	}
	meridiem := strings.ReplaceAll(strings.ToLower(m[4]), ".", "")
	switch m[1] {
	case "上午", "早上", "凌晨":
		meridiem = "am"
	case "下午", "晚上":
		meridiem = "pm"
	case "中午":
		if hour < 12 {
			meridiem = "pm"
		}
	}
	if hour == 24 {
		hour = 0
	}
	return clockValue{hour: hour, minute: minute}, meridiem, true
}
