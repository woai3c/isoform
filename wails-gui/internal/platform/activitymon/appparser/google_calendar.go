package appparser

import (
	"regexp"
	"strings"
)

var googleCalendarWebHosts = []string{
	"calendar.google.com",
}

type googleCalendarParser struct{}

func init() {
	Register(&googleCalendarParser{})
}

// CanParse always returns false — Google Calendar is web-only.
func (p *googleCalendarParser) CanParse(string) bool { return false }

func (p *googleCalendarParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, googleCalendarWebHosts...)
}

func (p *googleCalendarParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "google_calendar",
		Label:       "Google Calendar",
		ContentType: "events",
		URLHosts:    append([]string(nil), googleCalendarWebHosts...),
	}
}

// ParseTree extracts visible events from Google Calendar.
//
// Google Calendar tags each event element with an aria-label of the form
// "Event title, 10:00am to 11:00am, Calendar Name, Location" — the same
// shape Apple Calendar uses on its native AXButton/AXCell descriptions,
// so we reuse the calendarCollectEvents / calendarRenderBody helpers
// rather than duplicate the time-pattern walk.
//
// Returns nil when the visible view doesn't contain matchable events
// (e.g. settings page, search empty state) so the browser chrome
// stripper can produce useful text instead.
func (p *googleCalendarParser) ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent {
	if tree == nil {
		return nil
	}
	root := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWebArea" })
	if root == nil {
		return nil
	}

	events := calendarCollectEvents(root)
	if len(events) == 0 {
		events = googleCalendarCollectEditorEvents(root)
	}
	body := calendarRenderBody(events)
	// Claim the page even when body is empty (no events yet, or the
	// create-event popover is open with an unfilled title). Falling back
	// to the generic browser chrome stripper here would let it scrape
	// the entire Calendar UI ("创建" / "任务" / weekday cells / ...) into
	// the snapshot, which downstream consumers can't tell apart from
	// real page text. An empty events payload keeps the contentType
	// honest and lets the next forced capture overwrite it.

	return &ParsedContent{
		ContentType: "events",
		Title:       strings.TrimSpace(windowTitle),
		Body:        body,
		URL:         url,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "google_calendar",
			"source":   "web",
		},
	}
}

var googleCalendarEditorTimePattern = regexp.MustCompile(
	`(?i)(?:上午|下午|早上|晚上|中午|凌晨)?\s*\d{1,2}:\d{2}|\d{1,2}(?::\d{2})?\s*(?:am|pm)`,
)

// googleCalendarEditorIgnoredLabels lists the Google Calendar event-editor
// popover UI strings that must never be promoted to a parsed event title.
// Both the Chinese and English UIs are covered — when only one locale was
// listed, English-locale users had every chrome label ("Create", "Save",
// "More options") leak into body extraction.
//
// The list mirrors the labels visible on the create-event popover and its
// inline action chips; if Google adds or renames a chip, add it here. We
// don't filter by role+heuristic instead because Google reuses AXButton /
// AXStaticText for both chrome and actual content (e.g. event title field
// is an AXTextField, attendee chips are AXButton).
var googleCalendarEditorIgnoredLabels = map[string]bool{
	// Chinese (zh-CN)
	"创建":                  true,
	"关闭":                  true,
	"活动":                  true,
	"任务":                  true,
	"预约安排":                true,
	"预约安排 新":              true,
	"新":                   true,
	"添加标题":                true,
	"添加邀请对象":              true,
	"添加 Google Meet 视频会议": true,
	"添加地点":                true,
	"添加":                  true,
	"地点":                  true,
	"添加说明或Google 云端硬盘附件": true,
	"说明":            true,
	"或":             true,
	"Google 云端硬盘附件": true,
	"更多选项":          true,
	"保存":            true,
	"时区":            true,
	"不重复":           true,
	// English (en-US)
	"Create":                             true,
	"Close":                              true,
	"Event":                              true,
	"Task":                               true,
	"Appointment schedule":               true,
	"Appointment schedule New":           true,
	"New":                                true,
	"Add title":                          true,
	"Add invitees":                       true,
	"Add Google Meet video conferencing": true,
	"Add location":                       true,
	"Add":                                true,
	"Location":                           true,
	"Add description or Google Drive attachment": true,
	"Description":             true,
	"or":                      true,
	"Google Drive attachment": true,
	"More options":            true,
	"Save":                    true,
	"Time zone":               true,
	"Does not repeat":         true,
	// Locale-independent punctuation/separators
	"–": true,
	"-": true,
}

func googleCalendarCollectEditorEvents(root *AXNode) []calendarEvent {
	labels := googleCalendarCollectLabels(root)
	if len(labels) == 0 {
		return nil
	}

	for i, label := range labels {
		if !googleCalendarLooksLikeEditorDateTime(label) {
			continue
		}
		title := googleCalendarFindEditorTitle(labels[:i])
		if title == "" {
			continue
		}
		timeText := googleCalendarEditorTime(label)
		if timeText == "" {
			continue
		}
		return []calendarEvent{{
			Title:   title,
			Time:    timeText,
			sortKey: calendarSortKey(timeText),
		}}
	}
	return nil
}

func googleCalendarCollectLabels(root *AXNode) []string {
	seen := map[string]bool{}
	var labels []string
	Traverse(root, func(n *AXNode) bool {
		switch n.Role {
		case "AXButton", "AXCell", "AXStaticText", "AXLink", "AXGroup", "AXTextField":
		default:
			return true
		}
		label := calendarLabelText(n)
		if label == "" || seen[label] {
			return true
		}
		seen[label] = true
		labels = append(labels, label)
		return true
	})
	return labels
}

func googleCalendarLooksLikeEditorDateTime(label string) bool {
	if !googleCalendarEditorTimePattern.MatchString(label) {
		return false
	}
	if !strings.Contains(label, "至") &&
		!strings.Contains(label, " to ") &&
		!strings.Contains(label, "–") &&
		!strings.Contains(label, "-") {
		return false
	}
	stripped := calendarFullDatePattern.ReplaceAllString(label, " ")
	stripped = googleCalendarEditorTimePattern.ReplaceAllString(stripped, " ")
	stripped = calendarTemporalMetadataPattern.ReplaceAllString(stripped, " ")
	for _, r := range stripped {
		if !calendarSeparatorRunes[r] {
			return false
		}
	}
	return true
}

func googleCalendarFindEditorTitle(labels []string) string {
	for i := len(labels) - 1; i >= 0; i-- {
		label := strings.TrimSpace(labels[i])
		if label == "" || googleCalendarEditorIgnoredLabels[label] {
			continue
		}
		if calendarLabelIsNonEventStatus(label) || calendarEventTimePattern.MatchString(label) {
			continue
		}
		if !calendarLabelHasNonTemporalContent(label) {
			continue
		}
		return label
	}
	return ""
}

func googleCalendarEditorTime(label string) string {
	matches := googleCalendarEditorTimePattern.FindAllString(label, -1)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[0]) + " – " + strings.TrimSpace(matches[1])
	}
	if len(matches) == 1 {
		return strings.TrimSpace(matches[0])
	}
	return ""
}
