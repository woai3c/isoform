package handoff

import (
	"regexp"
	"strings"
)

const (
	// maxCriteria bounds how many success criteria one brief can carry.
	maxCriteria = 12
	// maxCriterionRunes bounds one criterion's text in runes.
	maxCriterionRunes = 300
)

// criterionMarker matches the list marker in front of a criterion: a bullet or
// number, then an optional checkbox.
var criterionMarker = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+(?:\[[ xX]\]\s+)?|^\[[ xX]\]\s+`)

// CriteriaFromBody reads the success criteria out of a brief's acceptance
// section. The author writes "# Acceptance criteria" (the section also answers
// "how to verify"); the reader accepts a few equivalent headings, any heading
// level and bullets, numbers or checkboxes, and returns nil when the brief has
// no such section. It never fails: a brief the reader cannot follow simply has
// no criteria, and the verifier reports that as nothing verifiable.
func CriteriaFromBody(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []string
	inSection := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			inSection = isCriteriaHeading(line)
			continue
		}
		if !inSection || line == "" {
			continue
		}
		text := strings.TrimSpace(criterionMarker.ReplaceAllString(line, ""))
		if text == "" {
			continue
		}
		if len(out) == maxCriteria {
			break
		}
		out = append(out, truncateCriterion(text))
	}
	return out
}

// isCriteriaHeading reports whether a markdown heading opens a criteria section.
func isCriteriaHeading(line string) bool {
	title := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
	title = strings.TrimSpace(strings.TrimSuffix(title, ":"))
	switch title {
	case "acceptance criteria", "success criteria", "how to verify", "verification":
		return true
	}
	return false
}

func truncateCriterion(text string) string {
	runes := []rune(text)
	if len(runes) <= maxCriterionRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxCriterionRunes])) + "..."
}
