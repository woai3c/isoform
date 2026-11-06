package main

import "testing"

// R68: the voice says a brief's clocks as words; the screen keeps the digits.
func TestMobileBriefSpokenClocksSayTimesAsWords(t *testing.T) {
	cases := map[string]string{
		"Your one-on-one is at 9:00.":               "Your one-on-one is at nine o'clock.",
		"You leave for the airport at 11:35.":       "You leave for the airport at eleven thirty five.",
		"Standup moved to 9:05 AM.":                 "Standup moved to nine oh five in the morning.",
		"Dinner at 7:45 pm.":                        "Dinner at seven forty five in the evening.",
		"Lunch at 12:20 PM and 3:00 PM":             "Lunch at twelve twenty in the afternoon and three o'clock in the afternoon",
		"Pay the tax before 23:00 and $400 by two.": "Pay the tax before 23:00 and $400 by two.",
	}
	for input, want := range cases {
		if got := mobileBriefSpokenClocks(input, "en"); got != want {
			t.Errorf("mobileBriefSpokenClocks(%q) = %q, want %q", input, got, want)
		}
	}
	if got := mobileBriefSpokenClocks("9:00 一对一", "zh-CN"); got != "9:00 一对一" {
		t.Fatalf("zh clock changed: %q", got)
	}
}
