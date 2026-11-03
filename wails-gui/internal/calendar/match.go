package calendar

import (
	"strings"
	"time"
)

// MatchWindow is how far from an event's span a meeting session may open and
// still count as that event: ten minutes early or late.
const MatchWindow = 10 * time.Minute

// MatchSession picks the event behind a meeting that opened at now in the
// named meeting app ("Zoom", "Google Meet", ...). An event qualifies when its
// span, widened by MatchWindow, contains now and it is meeting-like. An event
// whose conference host matches the app wins over one without a link; among
// equals the one starting closest to now wins. It returns nil when nothing
// qualifies.
func MatchSession(events []Event, now time.Time, meetingApp string) *Event {
	return matchBySpan(events, now.Add(-MatchWindow), now.Add(MatchWindow), now, meetingApp)
}

// MatchSpan picks the event behind a meeting session that ran from start to
// end: the same rules as MatchSession, with the session span widened by
// MatchWindow on both sides and the midpoint used to break ties.
func MatchSpan(events []Event, start, end time.Time, meetingApp string) *Event {
	if end.Before(start) {
		end = start
	}
	mid := start.Add(end.Sub(start) / 2)
	return matchBySpan(events, start.Add(-MatchWindow), end.Add(MatchWindow), mid, meetingApp)
}

func matchBySpan(events []Event, from, to, anchor time.Time, meetingApp string) *Event {
	app := strings.ToLower(strings.TrimSpace(meetingApp))
	var best *Event
	bestScore := -1
	var bestDistance time.Duration
	for i := range events {
		ev := &events[i]
		if ev.AllDay || !ev.Busy() || !ev.IsMeetingLike() || !ev.Overlaps(from, to) {
			continue
		}
		score := 0
		if ev.ConferenceHost != "" && app != "" {
			switch {
			case strings.EqualFold(ev.ConferenceHost, app):
				score = 2
			default:
				// A link to another platform is weaker than no link at all:
				// the user is probably in a different meeting.
				score = -1
			}
		} else if ev.ConferenceHost == "" {
			score = 1
		}
		if score < 0 {
			continue
		}
		distance := ev.Start.Sub(anchor)
		if distance < 0 {
			distance = -distance
		}
		if best == nil || score > bestScore || (score == bestScore && distance < bestDistance) {
			best = ev
			bestScore = score
			bestDistance = distance
		}
	}
	return best
}

// LiveEvent returns the busy, meeting-like event in progress at now, allowing
// MatchWindow before its start, or nil. It is the calendar's contribution to
// meeting detection: a live event plus the microphone in use is a meeting
// even in an app the detector does not know.
func LiveEvent(events []Event, now time.Time) *Event {
	return matchBySpan(events, now, now.Add(MatchWindow), now, "")
}
