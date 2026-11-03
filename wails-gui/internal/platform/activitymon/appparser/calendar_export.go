package appparser

import "strings"

// CalendarEvent is one event chip read from a calendar view, as the calendar
// reader (wails-gui/calendar_peek.go) consumes it: the raw title, time and
// location text, plus the full date when the chip's label carried one
// (Google Calendar's week view does; Apple Calendar's grid does not).
type CalendarEvent struct {
	Title    string
	Time     string
	Location string
	Date     string
}

// CollectCalendarEvents walks a calendar window or web area and returns its
// visible events in chronological order. It is the exported face of the
// parser walk the Apple Calendar and Google Calendar parsers share, with the
// date kept instead of being folded into the location.
func CollectCalendarEvents(root *AXNode) []CalendarEvent {
	if root == nil {
		return nil
	}
	if web := FindFirst(root, func(n *AXNode) bool { return n.Role == "AXWebArea" }); web != nil {
		root = web
	}
	seen := map[string]bool{}
	var out []CalendarEvent
	Traverse(root, func(n *AXNode) bool {
		switch n.Role {
		case "AXButton", "AXCell", "AXStaticText", "AXLink", "AXGroup":
		default:
			return true
		}
		label := calendarLabelText(n)
		if label == "" || calendarLabelIsNonEventStatus(label) {
			return true
		}
		date := calendarFullDatePattern.FindString(label)
		stripped := label
		if date != "" {
			stripped = strings.TrimSpace(strings.Replace(label, date, "", 1))
			stripped = strings.Trim(stripped, " ,，.。")
		}
		if !calendarEventTimePattern.MatchString(stripped) || !calendarLabelHasNonTemporalContent(stripped) {
			return true
		}
		ev := calendarParseDescription(stripped)
		if !calendarLabelHasNonTemporalContent(ev.Title) {
			return true
		}
		key := date + "\x00" + ev.Time + "\x00" + ev.Title + "\x00" + ev.Location
		if seen[key] {
			return true
		}
		seen[key] = true
		out = append(out, CalendarEvent{Title: ev.Title, Time: ev.Time, Location: ev.Location, Date: strings.TrimSpace(date)})
		return true
	})
	return out
}
