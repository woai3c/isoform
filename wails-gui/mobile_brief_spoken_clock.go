package main

import (
	"regexp"
	"strconv"
	"strings"
)

// spokenClockPattern matches a clock as the brief writes it on screen: 9:00,
// 11:35, 6:50 AM, 7:05 pm.
var spokenClockPattern = regexp.MustCompile(`\b(1[0-2]|0?[1-9]):([0-5][0-9])(?:\s*([AaPp])\.?\s*[Mm]\.?)?\b`)

var spokenNumberWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}

var spokenTensWords = []string{"", "", "twenty", "thirty", "forty", "fifty"}

// spokenMinutes says 0-59 as a person says it after the hour: "thirty five".
func spokenMinutes(minute int) string {
	if minute < 20 {
		return spokenNumberWords[minute]
	}
	word := spokenTensWords[minute/10]
	if minute%10 != 0 {
		word += " " + spokenNumberWords[minute%10]
	}
	return word
}

// mobileBriefSpokenClocks says the clocks of an English brief as a person says
// them: "9:00" is "nine o'clock", "11:35" is "eleven thirty five", "9:05" is
// "nine oh five", and an AM or PM is named by its part of day. The screen keeps
// the digits; only the voice reads these words. Other languages are unchanged.
func mobileBriefSpokenClocks(text, lang string) string {
	if lang != "en" {
		return text
	}
	return spokenClockPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := spokenClockPattern.FindStringSubmatch(match)
		hour, _ := strconv.Atoi(parts[1])
		minute, _ := strconv.Atoi(parts[2])
		say := spokenNumberWords[hour]
		switch {
		case minute == 0:
			say += " o'clock"
		case minute < 10:
			say += " oh " + spokenNumberWords[minute]
		default:
			say += " " + spokenMinutes(minute)
		}
		switch strings.ToLower(parts[3]) {
		case "a":
			say += " in the morning"
		case "p":
			if hour == 12 || hour <= 4 {
				say += " in the afternoon"
			} else {
				say += " in the evening"
			}
		}
		return say
	})
}
