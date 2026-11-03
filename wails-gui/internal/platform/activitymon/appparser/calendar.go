package appparser

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// calendarBundleIDs lists the macOS Calendar app.
var calendarBundleIDs = map[string]bool{
	"com.apple.iCal": true,
}

type calendarParser struct{}

func init() {
	Register(&calendarParser{})
}

func (p *calendarParser) CanParse(bundleID string) bool {
	return calendarBundleIDs[bundleID]
}

func (p *calendarParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "calendar",
		Label:       "Apple Calendar",
		ContentType: "events",
		Bundles:     []string{"com.apple.iCal"},
	}
}

// ParseTree extracts visible events from Calendar.app.
//
// Calendar's day/week/month views render each event as a small interactive
// element with an AXDescription in the form:
//
//	"Event title, from 10:00 AM to 11:00 AM, Location" (day/week view)
//	"Event title, 10 AM, Calendar Name"                 (month view)
//
// Event roles vary by view: AXCell in month view, AXButton or AXGroup in
// day/week view. We collect anything with a time-shaped description, dedupe,
// and sort by the embedded start time so output is chronological.
func (p *calendarParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	events := calendarCollectEvents(window)
	if len(events) == 0 {
		return nil
	}

	body := calendarRenderBody(events)
	if strings.TrimSpace(body) == "" {
		return nil
	}

	return &ParsedContent{
		ContentType: "events",
		Title:       windowTitle,
		Body:        body,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "calendar",
		},
	}
}

// calendarEvent captures one Calendar event.
type calendarEvent struct {
	Title    string
	Time     string // "10:00 AM – 11:00 AM" or "All day"
	Location string
	sortKey  string // best-effort start time for sorting
}

// calendarEventTimePattern matches the time fragment inside an event
// description. Examples: "10:00 AM", "from 10:00 to 11:00", "all-day",
// "10–11 AM", "10:00 – 11:00", "10:00", a bare "10am".
var calendarEventTimePattern = regexp.MustCompile(
	`(?i)(?:全天|\b(?:all[- ]day|from\s*\d{1,2}(?::\d{2})?\s*(?:am|pm)?\s*to\s*\d{1,2}(?::\d{2})?\s*(?:am|pm)?|\d{1,2}(?::\d{2})?\s*[–-]\s*\d{1,2}(?::\d{2})?\s*(?:am|pm)?|\d{1,2}:\d{2}\s*(?:am|pm)?|\d{1,2}\s*(?:am|pm))\b)`,
)

// calendarFullDatePattern matches a fully-qualified date string (with year).
// Event chips in the calendar grid never carry a full date — the grid cell
// itself expresses the date. A label that contains a full date is therefore
// almost always coming from the event-detail popover's date/time row
// (e.g. "2026年5月13日  11:00 – 12:00"), which we want to ignore.
var calendarFullDatePattern = regexp.MustCompile(
	`(?i)` +
		// Chinese: 2026年5月13日
		`\d{4}年\d{1,2}月\d{1,2}日` +
		// Chinese month/day cells and popover rows: 5月 10日 (星期日)
		`|\d{1,2}月\s*\d{1,2}日(?:\s*\([^)]*\))?` +
		// English long: May 13, 2026 / September 1 2026
		`|\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sept?(?:ember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{1,2},?\s+\d{4}\b` +
		// English day-first: 13 May 2026
		`|\b\d{1,2}\s+(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sept?(?:ember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{4}\b` +
		// ISO-ish: 2026-05-13, 2026/05/13
		`|\b\d{4}[-/]\d{1,2}[-/]\d{1,2}\b`,
)

var calendarTemporalMetadataPattern = regexp.MustCompile(
	`(?i)上午|下午|早上|晚上|中午|凌晨|时区|不重复|不会重复|(?:周|星期)[一二三四五六日天]|(?:time\s*zone|timezone|does\s+not\s+repeat|non[- ]?repeating?)`,
)

var calendarNonEventStatusPattern = regexp.MustCompile(
	`(?i)没有全天活动|没有活动|有\s*\d+\s*个全天活动|\bno\s+(?:all[- ]day\s+)?events?\b|^\s*\d+\s+all[- ]day\s+events?\b`,
)

// calendarSeparatorRunes are characters that commonly separate date/time
// tokens within a label. Used by calendarLabelHasNonTemporalContent when
// deciding whether a label is "just" a date+time fragment.
var calendarSeparatorRunes = map[rune]bool{
	' ': true, '\t': true, '\n': true,
	',': true, '，': true,
	'.': true, '。': true,
	'·': true, ':': true, '：': true,
	';': true, '；': true,
	'-': true, '–': true, '—': true,
	'@': true,
	'至': true,
}

func calendarLabelIsNonEventStatus(label string) bool {
	return calendarNonEventStatusPattern.MatchString(label)
}

// calendarLabelHasNonTemporalContent reports whether the label still has any
// substantive text after time tokens, full dates, and common separators are
// stripped. Returns false for labels like "2026年5月13日  11:00 – 12:00"
// that appear inside event-detail popovers.
func calendarLabelHasNonTemporalContent(label string) bool {
	// Strip full dates first: an ISO date like "2026-05-13" contains a
	// "\d-\d" run that calendarEventTimePattern would otherwise greedily
	// eat as if it were a "10–11" hour range, leaving "2026-" behind and
	// fooling the check.
	stripped := calendarFullDatePattern.ReplaceAllString(label, " ")
	stripped = calendarEventTimePattern.ReplaceAllString(stripped, " ")
	stripped = calendarTemporalMetadataPattern.ReplaceAllString(stripped, " ")
	for _, r := range stripped {
		if !calendarSeparatorRunes[r] {
			return true
		}
	}
	return false
}

// calendarLabelText returns the most descriptive label string on a node:
// description first (AppleScript's aria-label equivalent), then title
// (Apple Calendar native event cells), then value (Google Calendar's
// event chips sometimes carry the label here).
func calendarLabelText(n *AXNode) string {
	if n == nil {
		return ""
	}
	if s := strings.TrimSpace(n.Description); s != "" {
		return s
	}
	if s := strings.TrimSpace(n.Title); s != "" {
		return s
	}
	return strings.TrimSpace(n.Value)
}

// calendarCollectEvents walks for nodes whose label string looks like an
// event: includes a time token (with or without a comma separator). The
// walk accepts the roles Apple and Google Calendar use to render event
// chips. AXGroup is allowed even when nested because Google Calendar
// week-view chips can have a few internal spans before the inner text.
func calendarCollectEvents(window *AXNode) []calendarEvent {
	seen := map[string]bool{}
	var events []calendarEvent
	Traverse(window, func(n *AXNode) bool {
		switch n.Role {
		case "AXButton", "AXCell", "AXStaticText", "AXLink", "AXGroup":
			// fall through
		default:
			return true
		}
		label := calendarLabelText(n)
		if label == "" {
			return true
		}
		if calendarLabelIsNonEventStatus(label) {
			return true
		}
		if !calendarEventTimePattern.MatchString(label) {
			return true
		}
		if !calendarLabelHasNonTemporalContent(label) {
			return true
		}
		ev := calendarParseDescription(label)
		if !calendarLabelHasNonTemporalContent(ev.Title) {
			return true
		}
		key := ev.Time + "\x00" + ev.Title + "\x00" + ev.Location
		if seen[key] {
			return true
		}
		seen[key] = true
		events = append(events, ev)
		return true
	})
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].sortKey < events[j].sortKey
	})
	return events
}

// calendarParseDescription splits a calendar event label into title /
// time / location. Two label shapes are common:
//
//	"Title, Time, Location"   (Apple Calendar, Google Calendar aria-labels)
//	"Title Time"              (Google Calendar week-view chips, no commas)
//
// In the comma form we pick the first segment that matches the time
// pattern; everything before is the title, everything after is location
// or calendar. In the comma-less form we slice around the regex match
// position so the title doesn't accidentally swallow the time string.
func calendarParseDescription(desc string) calendarEvent {
	parts := calendarSplitDescription(desc)
	if len(parts) > 1 {
		timeIdx := -1
		for i, p := range parts {
			if calendarEventTimePattern.MatchString(p) {
				timeIdx = i
				break
			}
		}
		if timeIdx < 0 {
			return calendarEvent{Title: desc, sortKey: desc}
		}
		ev := calendarEvent{Time: parts[timeIdx]}
		if timeIdx == 0 {
			for i := 1; i < len(parts); i++ {
				if calendarLabelHasNonTemporalContent(parts[i]) {
					ev.Title = parts[i]
					if i+1 < len(parts) {
						ev.Location = strings.TrimSpace(strings.Join(parts[i+1:], ", "))
					}
					break
				}
			}
		} else {
			ev.Title = strings.TrimSpace(strings.Join(parts[:timeIdx], ", "))
			if timeIdx+1 < len(parts) {
				ev.Location = strings.TrimSpace(strings.Join(parts[timeIdx+1:], ", "))
			}
		}
		calendarRecoverEndTime(&ev)
		ev.sortKey = calendarSortKey(ev.Time)
		return ev
	}

	loc := calendarEventTimePattern.FindStringIndex(desc)
	if loc == nil {
		return calendarEvent{Title: strings.TrimSpace(desc), sortKey: desc}
	}
	ev := calendarEvent{
		Title:   strings.TrimSpace(strings.Trim(desc[:loc[0]], " ,–-")),
		Time:    strings.TrimSpace(desc[loc[0]:loc[1]]),
		sortKey: "",
	}
	if loc[1] < len(desc) {
		ev.Location = strings.TrimSpace(strings.Trim(desc[loc[1]:], " ,–-"))
	}
	calendarRecoverEndTime(&ev)
	ev.sortKey = calendarSortKey(ev.Time)
	return ev
}

func calendarSplitDescription(desc string) []string {
	if !strings.ContainsAny(desc, ",，") {
		return nil
	}
	raw := strings.FieldsFunc(desc, func(r rune) bool {
		return r == ',' || r == '，'
	})
	parts := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// calendarRecoverEndTime pulls a leaked "结束于HH:MM" / "ends at HH:MM"
// fragment out of ev.Location and merges it into ev.Time as a range. The
// fragment ends up in Location because macOS Calendar's localized
// AXDescription puts the start- and end-time on opposite sides of a
// comma — the start matches the time regex first and everything after
// (incl. the end-time) falls into the location slot.
func calendarRecoverEndTime(ev *calendarEvent) {
	if ev.Location == "" {
		return
	}
	m := calendarEndTimePattern.FindStringSubmatchIndex(ev.Location)
	if m == nil {
		return
	}
	endTime := strings.TrimSpace(ev.Location[m[2]:m[3]])
	if endTime != "" && ev.Time != "" && !calendarTimeIsRange(ev.Time) {
		ev.Time = ev.Time + " – " + endTime
	}
	ev.Location = ev.Location[:m[0]] + ev.Location[m[1]:]
	ev.Location = strings.TrimSpace(strings.Trim(ev.Location, calendarLocationTrimChars))
}

var calendarTimeExtract = regexp.MustCompile(`(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`)

// calendarEndTimePattern matches a "结束于HH:MM" / "ends at HH:MM" /
// "until HH:MM" fragment that macOS Calendar's localized AXDescription
// leaks into the trailing comma-segment of an event label. Without this
// the trailing fragment ends up as `ev.Location`, and renderBody emits
// " @ ，结束于10:00。" — an `@` that has nothing to do with a venue.
var calendarEndTimePattern = regexp.MustCompile(
	`(?i)(?:结束于|ends?\s+at|until)\s*(\d{1,2}(?::\d{2})?\s*(?:am|pm)?)`,
)

// calendarTimeRangeMarkers are tokens whose presence in `ev.Time` means
// the field already spans a range (e.g. "10:00 – 11:00", "10 to 11 AM").
// When the time is already a range we don't append the recovered end
// time again, just clean up the leaked location fragment.
var calendarTimeRangeMarkers = []string{"–", "—", "-", " to ", "至"}

func calendarTimeIsRange(t string) bool {
	lower := strings.ToLower(t)
	for _, m := range calendarTimeRangeMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// calendarLocationTrimChars are the punctuation runes we strip off a
// Location after extracting an end-time fragment from it. Mixes ASCII
// and CJK forms because the leaked fragment carries both.
const calendarLocationTrimChars = " .,，。·:：;；-–—@\t"

// calendarSortKey normalizes a time fragment to a 24h "HH:MM" string so
// events within a single day sort chronologically. All-day events sort
// first.
func calendarSortKey(timeFragment string) string {
	lower := strings.ToLower(timeFragment)
	if strings.Contains(lower, "all") || strings.Contains(lower, "全天") {
		return "00:00"
	}
	m := calendarTimeExtract.FindStringSubmatch(lower)
	if len(m) == 0 {
		return timeFragment
	}
	hr, err := strconv.Atoi(m[1])
	if err != nil {
		return timeFragment
	}
	mn := 0
	if m[2] != "" {
		mn, _ = strconv.Atoi(m[2])
	}
	switch m[3] {
	case "pm":
		if hr != 12 {
			hr += 12
		}
	case "am":
		if hr == 12 {
			hr = 0
		}
	}
	return fmt.Sprintf("%02d:%02d", hr, mn)
}

// calendarRenderBody formats events as a chronological bullet list.
func calendarRenderBody(events []calendarEvent) string {
	var sb strings.Builder
	for i, e := range events {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("- ")
		if e.Time != "" {
			sb.WriteString(e.Time)
			sb.WriteString(" · ")
		}
		sb.WriteString(e.Title)
		if e.Location != "" {
			sb.WriteString(" @ ")
			sb.WriteString(e.Location)
		}
	}
	return sb.String()
}
