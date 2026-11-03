package appparser

import "strings"

// Helpers shared by the office_document.go / office_spreadsheet.go /
// office_presentation.go parsers. Kept in a separate file so the
// Microsoft Office + Apple iWork parsers can be shipped in independent
// commits without one depending on another's file being present.

// officeStripAppSuffix removes a trailing " - <App>" / " — <App>" /
// " – <App>" from a window title. Word / Excel / PowerPoint use the
// ASCII hyphen; Pages / Numbers / Keynote use the em-dash. Returns
// the trimmed title or "" if stripping leaves nothing.
//
// appName is the lowercase short name we already wrote to metadata
// ("word", "excel", …); this function recovers the canonical display
// form ("Word", "Excel", …) for the match.
func officeStripAppSuffix(title, appName string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	display := officeDisplayName(appName)
	if display == "" {
		return title
	}
	for _, sep := range []string{" — ", " – ", " - "} {
		if cut, ok := strings.CutSuffix(title, sep+display); ok {
			return strings.TrimSpace(cut)
		}
	}
	return title
}

// officeDisplayName maps the lowercase metadata key to the user-facing
// app name that appears in window titles.
func officeDisplayName(appName string) string {
	switch appName {
	case "word":
		return "Word"
	case "pages":
		return "Pages"
	case "excel":
		return "Excel"
	case "numbers":
		return "Numbers"
	case "powerpoint":
		return "PowerPoint"
	case "keynote":
		return "Keynote"
	}
	return ""
}
