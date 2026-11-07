package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// These patterns recognize display text, not machine timestamps. Keep the
// normalizer separate from provenance and from the past-clock removal gate.
const briefChineseClockPattern = `(?:凌晨|早上|上午|中午|下午|晚上)\s*[0-2]?[0-9](?:(?:点|时)(?:半|[0-5]?[0-9]分?)?|:[0-5][0-9](?::[0-5][0-9])?)`

// Bare colon numbers in source prose can be aspect ratios or media offsets.
// Only a period marker makes them unambiguous clocks; metadata is formatted
// directly from time.Time instead of passing through this prose parser.
var briefTextClock = regexp.MustCompile(`(?i)\b[0-2]?[0-9](?::[0-5][0-9](?::[0-5][0-9])?)?\s*[ap]\.?m\b|` + briefChineseClockPattern)
var briefTextBareClock = regexp.MustCompile(`\b(?:[01]?[0-9]|2[0-3]):[0-5][0-9]\b`)

var briefTextClockParts = regexp.MustCompile(`(?i)^(\d{1,2})(?::(\d{2}))?(?::\d{2})?\s*([ap])?\.?m?\.?$`)
var briefTextChineseClockParts = regexp.MustCompile(`^(凌晨|早上|上午|中午|下午|晚上)\s*(\d{1,2})(?:(?:点|时)(?:(半)|(\d{1,2})分?)?|:(\d{2})(?::\d{2})?)$`)
var briefTextRange = regexp.MustCompile(`(?i)(?:\bfrom\s+|从)?(` + briefTextClock.String() + `)\s*(?:to|[-–—]|至|到)\s*(` + briefTextClock.String() + `)`)
var briefTextDate = regexp.MustCompile(`(?i)\b(?:(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun)(?:day|sday|nesday|rsday|urday)?,?\s+)?(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+(\d{1,2})(?:,?\s+(\d{4}))?\b`)

// Preserve unsupported numeric forms (decimals, fractions and grouped digits)
// rather than interpreting their final integer as a different duration.
var briefTextDuration = regexp.MustCompile(`(?i)(^|[^\d.,/:])(?:\b(?:(\d+)\s*(?:hours?|hrs?|h)\s*)?(\d+)\s*(?:minutes?|mins?|min)\b|\b(\d+)\s*(?:hours?|hrs?|h)\b)`)
var briefTextChineseDuration = regexp.MustCompile(`(^|[^\d.,/:])(\d+)\s*小时(?:\s*(\d+)\s*分钟?)?`)
var briefTextMachineValue = regexp.MustCompile(`https?://[^\s]+|\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

func briefChinese(language string) bool {
	lower := strings.ToLower(strings.TrimSpace(language))
	return strings.HasPrefix(lower, "zh") || strings.Contains(lower, "chinese")
}

func briefClockParts(value string) (int, int, bool) {
	if parts := briefTextChineseClockParts.FindStringSubmatch(strings.TrimSpace(value)); parts != nil {
		hour, _ := strconv.Atoi(parts[2])
		minute, _ := strconv.Atoi(mobileBriefFirstText(parts[4], parts[5]))
		if parts[3] == "半" {
			minute = 30
		}
		switch parts[1] {
		case "凌晨", "早上", "上午":
			if hour == 12 {
				hour = 0
			}
		case "晚上":
			if hour == 12 {
				hour = 0
			} else if hour < 12 {
				hour += 12
			}
		case "中午", "下午":
			if hour < 12 {
				hour += 12
			}
		}
		return hour, minute, hour < 24 && minute < 60
	}
	parts := briefTextClockParts.FindStringSubmatch(strings.TrimSpace(value))
	if parts == nil {
		return 0, 0, false
	}
	hour, _ := strconv.Atoi(parts[1])
	minute, _ := strconv.Atoi(parts[2])
	if parts[3] != "" {
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
		hour %= 12
		if strings.EqualFold(parts[3], "p") {
			hour += 12
		}
	}
	return hour, minute, hour < 24
}

func briefShortClock(hour, minute int, language string) string {
	if briefChinese(language) {
		// 24-hour, no 上午/下午 (today-review v1.1.20 R47).
		return fmt.Sprintf("%d:%02d", hour, minute)
	}
	period := "AM"
	if hour >= 12 {
		period = "PM"
	}
	h := (hour+11)%12 + 1
	return fmt.Sprintf("%d:%02d %s", h, minute, period)
}

// briefSpokenClock keeps pronunciation text natural for TTS. Display copy
// always uses briefShortClock, including generated prose and prepared chips.
func briefSpokenClock(hour, minute int, language string) string {
	if briefChinese(language) {
		// Speech keeps the spoken period ("下午6点"); only written clocks
		// are 24-hour (today-review v1.1.20 R47).
		head := briefSpokenChinesePeriod(hour) + strconv.Itoa((hour+11)%12+1)
		switch minute {
		case 0:
			return head + "点"
		case 30:
			return head + "点半"
		default:
			return fmt.Sprintf("%s点%d分", head, minute)
		}
	}
	period := "am"
	if hour >= 12 {
		period = "pm"
	}
	h := (hour+11)%12 + 1
	if minute == 0 {
		return fmt.Sprintf("%d%s", h, period)
	}
	return fmt.Sprintf("%d:%02d%s", h, minute, period)
}

func briefSpokenChinesePeriod(hour int) string {
	switch true {
	case hour < 6:
		return "凌晨"
	case hour == 12:
		return "中午"
	case hour > 18:
		return "晚上"
	case hour > 12:
		// Six in the evening is conventionally 下午6点 in the brief.
		return "下午"
	}
	return "上午"
}

// normalizeBriefText shares exact clock values between written and spoken
// copy. Speech retains exact appointment boundaries and instruction durations.
// Dates use the brief's own local as-of day, never the playback day's date.
func normalizeBriefText(text, language string, reference time.Time, spoken bool) string {
	return normalizeBriefTextMode(text, language, reference, spoken, false)
}

// Approximation is an explicit option for observed activity summaries, never
// a default for shared Read Aloud, calendar plans or uncategorized prose.
func normalizeBriefActivityText(text, language string, reference time.Time) string {
	return normalizeBriefTextMode(text, language, reference, true, true)
}

func normalizeBriefTextMode(text, language string, reference time.Time, spoken, activity bool) string {
	if spans := briefTextMachineValue.FindAllStringIndex(text, -1); len(spans) > 0 {
		var out strings.Builder
		last := 0
		for _, span := range spans {
			out.WriteString(normalizeBriefTextMode(text[last:span[0]], language, reference, spoken, activity))
			out.WriteString(text[span[0]:span[1]])
			last = span[1]
		}
		out.WriteString(normalizeBriefTextMode(text[last:], language, reference, spoken, activity))
		return out.String()
	}
	clock := briefShortClock
	if spoken {
		clock = briefSpokenClock
	}
	if spoken {
		text = briefTextRange.ReplaceAllStringFunc(text, func(value string) string {
			parts := briefTextRange.FindStringSubmatch(value)
			from, fm, ok := briefClockParts(parts[1])
			to, tm, endOK := briefClockParts(parts[2])
			if !ok || !endOK {
				return value
			}
			// Activity ranges rarely need minute-level precision. Preserve
			// meaningful minutes (especially half hours); round within ten.
			round := func(h, m int) (int, int) {
				switch true {
				case m <= 10:
					return h, 0
				case m >= 50:
					return (h + 1) % 24, 0
				default:
					return h, m
				}
			}
			roundedFrom, roundedFM := round(from, fm)
			roundedTo, roundedTM := round(to, tm)
			if activity && (roundedFrom != roundedTo || roundedFM != roundedTM) {
				from, fm, to, tm = roundedFrom, roundedFM, roundedTo, roundedTM
			}
			if briefChinese(language) {
				return "从" + clock(from, fm, language) + "到" + clock(to, tm, language)
			}
			return "from " + clock(from, fm, language) + " to " + clock(to, tm, language)
		})
		text = briefTextDuration.ReplaceAllStringFunc(text, func(value string) string {
			parts := briefTextDuration.FindStringSubmatch(value)
			hours, _ := strconv.Atoi(parts[2])
			minutes, _ := strconv.Atoi(parts[3])
			if parts[4] != "" {
				hours, _ = strconv.Atoi(parts[4])
			}
			return parts[1] + briefSpokenDuration(hours*60+minutes, language, activity)
		})
		text = briefTextChineseDuration.ReplaceAllStringFunc(text, func(value string) string {
			parts := briefTextChineseDuration.FindStringSubmatch(value)
			hours, _ := strconv.Atoi(parts[2])
			minutes, _ := strconv.Atoi(parts[3])
			return parts[1] + briefSpokenDuration(hours*60+minutes, language, activity)
		})
	}
	text = briefTextClock.ReplaceAllStringFunc(text, func(value string) string {
		hour, minute, ok := briefClockParts(value)
		if !ok {
			return value
		}
		return clock(hour, minute, language)
	})
	if reference.IsZero() {
		return text
	}
	var out strings.Builder
	last := 0
	for _, span := range briefTextDate.FindAllStringIndex(text, -1) {
		day := briefTextDay(text[span[0]:span[1]], language, reference)
		prefix := text[last:span[0]]
		if !briefChinese(language) {
			switch day {
			case "today", "yesterday", "tomorrow":
				// "on Friday" reads well; "on yesterday" does not.
				prefix = briefTextOnBeforeDay.ReplaceAllString(prefix, "$1")
			}
			// A day that opens a sentence keeps its capital.
			before := strings.TrimRight(out.String()+prefix, " ")
			if before == "" || strings.HasSuffix(before, ".") || strings.HasSuffix(before, "\n") {
				day = mobileBriefCapitalize(day)
			}
		}
		out.WriteString(prefix)
		out.WriteString(day)
		last = span[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

var briefTextOnBeforeDay = regexp.MustCompile(`(?i)(^|\s)on\s+$`)

// briefTextDay rewrites one written date relative to the brief's day the way
// the brief names days (mobileBriefDayWords): today, yesterday and tomorrow,
// a weekday this week, "last Thursday" in the previous week, and a date from
// seven days away. The brief's own day never reads as its weekday.
func briefTextDay(value, language string, reference time.Time) string {
	parts := briefTextDate.FindStringSubmatch(value)
	month, err := time.Parse("Jan", strings.ToUpper(parts[1][:1])+strings.ToLower(parts[1][1:3]))
	if err != nil {
		return value
	}
	day, _ := strconv.Atoi(parts[2])
	year := reference.Year()
	if parts[3] != "" {
		year, _ = strconv.Atoi(parts[3])
	}
	at := time.Date(year, month.Month(), day, 0, 0, 0, 0, reference.Location())
	if at.Month() != month.Month() || at.Day() != day {
		return value
	}
	if parts[3] == "" {
		// An abbreviated December/January date can cross a year.
		for _, candidate := range []time.Time{at.AddDate(-1, 0, 0), at.AddDate(1, 0, 0)} {
			if candidate.Sub(reference).Abs() < at.Sub(reference).Abs() {
				at = candidate
			}
		}
	}
	// Compare civil dates so DST cannot move the seven-day boundary.
	civil := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	if days := int(civil(at).Sub(civil(reference)).Hours() / 24); days > -7 && days < 7 {
		return mobileBriefDayWords(at, reference, reference.Location(), mobileBriefVoiceLang(language))
	}
	if briefChinese(language) {
		if parts[3] != "" && year != reference.Year() {
			return fmt.Sprintf("%d年%d月%d日", year, at.Month(), at.Day())
		}
		return fmt.Sprintf("%d月%d日", at.Month(), at.Day())
	}
	if parts[3] != "" && year != reference.Year() {
		return at.Format("January 2, 2006")
	}
	return at.Format("January 2")
}

func briefSpokenDuration(minutes int, language string, activity bool) string {
	// Prefer quarters for hour-long activity totals within five minutes.
	if activity && minutes >= 60 {
		rounded := (minutes + 7) / 15 * 15
		if abs := rounded - minutes; abs >= -5 && abs <= 5 {
			minutes = rounded
		}
	}
	hours, rest := minutes/60, minutes%60
	if briefChinese(language) {
		switch true {
		case hours == 1 && rest == 30:
			return "一个半小时"
		case rest == 30 && hours > 0:
			return fmt.Sprintf("%d个半小时", hours)
		case hours == 0:
			return fmt.Sprintf("%d分钟", rest)
		case rest == 0:
			return fmt.Sprintf("%d小时", hours)
		default:
			return fmt.Sprintf("%d小时%d分钟", hours, rest)
		}
	}
	number := func(n int) string {
		if n >= 0 && n <= 12 {
			return []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}[n]
		}
		return strconv.Itoa(n)
	}
	hourText := number(hours) + " hours"
	if hours == 1 {
		hourText = "an hour"
	}
	switch true {
	case hours == 0:
		if rest == 1 {
			return "a minute"
		}
		return number(rest) + " minutes"
	case rest == 0:
		return hourText
	case rest == 15:
		if hours > 1 {
			return number(hours) + " and a quarter hours"
		}
		return hourText + " and a quarter"
	case rest == 30:
		if hours > 1 {
			return number(hours) + " and a half hours"
		}
		return hourText + " and a half"
	case rest == 45:
		if hours > 1 {
			return number(hours) + " and three quarter hours"
		}
		return hourText + " and three quarters"
	default:
		return hourText + " and " + briefSpokenDuration(rest, language, false)
	}
}

func mobileBriefTextReference(brief MobileBriefV1) time.Time {
	if at, err := time.Parse(time.RFC3339, brief.AsOf); err == nil {
		loc := mobileBriefNarrativeLocation(brief)
		// An early brief is written for its slot's day, not the day it was made.
		if day, err := time.ParseInLocation("2006-01-02", brief.LocalDate, loc); err == nil && day.Format("2006-01-02") != at.In(loc).Format("2006-01-02") {
			return day
		}
		return at.In(loc)
	}
	at, _ := time.ParseInLocation("2006-01-02", brief.LocalDate, mobileBriefNarrativeLocation(brief))
	return at
}
