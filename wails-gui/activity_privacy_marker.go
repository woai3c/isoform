package main

import (
	"regexp"
	"strings"
	"unicode"
)

// activityPrivacyMarker matches a redaction tag from pii.RedactText, including a
// truncated tag, plus the separator in front of it and any word glued to it.
// The glued word is part of what was taken out, so it goes too.
var activityPrivacyMarker = regexp.MustCompile(`(?i)\s*[·•|/:\-—,]?\s*\[redacted(?::[a-z_]*)?\]?[\p{L}\p{N}_]*`)

// activityPrivacyEdge is a separator left at either end once a tag is removed.
var activityPrivacyEdge = regexp.MustCompile(`^[\s·•|/:\-—,]+|[\s·•|/:\-—,]+$`)

// activityPrivacyMarkerStrip removes redaction tags from text an Activity
// surface will show. A tag never reaches a screen: the words around it read on,
// and text left with no letters or digits returns "" so the caller can show a
// plain "a private item" instead.
func activityPrivacyMarkerStrip(text string) string {
	if !strings.Contains(strings.ToLower(text), "[redacted") {
		return text
	}
	out := activityPrivacyMarker.ReplaceAllString(text, " ")
	out = strings.Join(strings.Fields(out), " ")
	out = activityPrivacyEdge.ReplaceAllString(out, "")
	for _, r := range out {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return out
		}
	}
	return ""
}
