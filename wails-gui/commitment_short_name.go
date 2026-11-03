package main

import (
	"regexp"
	"strings"
	"unicode"
)

var commitmentDictationLead = regexp.MustCompile(`(?i)^(?:(?:ok(?:ay)?|so|um|uh|please)[,.]?\s+)*(?:remind me to |make sure to |make sure (?:that )?i (?:do )?|don'?t (?:let me )?forget to |i (?:really )?(?:need|want|have|ought) to |i(?:'ve| have)? got to |i should |i(?:'ll| will| am going to|'m going to|'m gonna) )`)
var commitmentDictationHedge = regexp.MustCompile(`(?i)\b(?:some sort of|some kind of|sort of|kind of) `)
var commitmentDictationFiller = regexp.MustCompile(`(?i)\s+like\s+(every|each|on|by|before|after)\b`)
var commitmentDictationTail = regexp.MustCompile(`(?i)(?:,?\s+(?:or something(?: like that)?|and stuff|and so on|or whatever))?[.!\s]*$`)

// commitmentShortName removes common spoken scaffolding without guessing at
// meaning: the task, its object and its conditions stay word for word.
func commitmentShortName(source string) string {
	original := strings.Join(strings.Fields(source), " ")
	name := strings.TrimSpace(commitmentDictationLead.ReplaceAllString(original, ""))
	name = commitmentDictationHedge.ReplaceAllString(name, "")
	name = commitmentDictationFiller.ReplaceAllString(name, " $1")
	name = strings.TrimSpace(commitmentDictationTail.ReplaceAllString(name, ""))
	if name == "" || name == strings.TrimRight(original, ".! ") {
		return original
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
