// Package calendar holds the pure parts of Isoform's view of the day ahead:
// event normalisation, conference link extraction, matching events to
// meeting sessions, the shape of a day, which calendars the user actually
// uses, and the rules for reading a web calendar while the Mac is idle. It
// has no dependency on EventKit, the store, the browser or the clock, so every
// rule is a table test.
package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Source names where an event came from.
const (
	SourceEventKit = "eventkit"
	SourceWeb      = "web"
)

// Attendee statuses and roles follow EKParticipant, lower-cased.
const (
	StatusUnknown   = "unknown"
	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusDeclined  = "declined"
	StatusTentative = "tentative"
)

// Event is one occurrence of a calendar event after normalisation. Times are
// absolute; AllDay events span local midnight to midnight in TimeZone.
type Event struct {
	// ID is a stable hash of the source, the UID and the occurrence start.
	ID string
	// UID is the calendarItemExternalIdentifier (or the local identifier when
	// the external one is empty); LocalID is the eventIdentifier.
	UID     string
	LocalID string
	// Source is SourceEventKit or SourceWeb.
	Source        string
	CalendarID    string
	CalendarTitle string
	// Provider is the account behind the calendar: icloud, google, exchange,
	// outlook, local, subscribed or other.
	Provider string

	Title    string
	Start    time.Time
	End      time.Time
	AllDay   bool
	TimeZone string

	Location      string
	Latitude      float64
	Longitude     float64
	HasCoordinate bool
	URL           string
	ConferenceURL string
	// ConferenceHost is the meeting platform behind ConferenceURL ("Zoom",
	// "Google Meet", "Microsoft Teams", "WebEx"), matching the names
	// meetingdetect reports.
	ConferenceHost string

	// Availability is busy, free, tentative or unavailable.
	Availability string
	// Status is the event status: none, confirmed, tentative or canceled.
	Status string
	// MyStatus is the current user's participation status.
	MyStatus       string
	OrganizerEmail string
	OrganizerName  string
	Recurring      bool
	Attendees      []Attendee
}

// Attendee is one participant of an event.
type Attendee struct {
	Name   string
	Email  string
	Role   string
	Status string
	IsMe   bool
}

// AttendeeCount counts attendees other than the current user.
func (e Event) AttendeeCount() int {
	n := 0
	for _, a := range e.Attendees {
		if !a.IsMe {
			n++
		}
	}
	return n
}

// HasOthers reports whether anyone besides the current user is invited.
func (e Event) HasOthers() bool { return e.AttendeeCount() > 0 }

// IsMeetingLike reports whether the event has other attendees or a conference
// link: the events meeting_prep cares about.
func (e Event) IsMeetingLike() bool {
	return e.HasOthers() || e.ConferenceURL != ""
}

// InPerson reports whether the event has a place but no conference link.
func (e Event) InPerson() bool {
	return strings.TrimSpace(e.Location) != "" && e.ConferenceURL == ""
}

// Busy reports whether the event blocks time: not free, not declined, not
// cancelled.
func (e Event) Busy() bool {
	if e.Availability == "free" || e.Status == "canceled" || e.MyStatus == StatusDeclined {
		return false
	}
	return true
}

// Overlaps reports whether the event's span intersects [from, to).
func (e Event) Overlaps(from, to time.Time) bool {
	return e.Start.Before(to) && e.End.After(from)
}

// RawEvent is one occurrence as the EventKit bridge serialises it. Times are
// RFC 3339 strings, so the JSON stays readable in logs.
type RawEvent struct {
	UID               string        `json:"uid"`
	LocalID           string        `json:"localId"`
	OccurrenceDate    string        `json:"occurrenceDate"`
	CalendarID        string        `json:"calendarId"`
	CalendarTitle     string        `json:"calendarTitle"`
	SourceID          string        `json:"sourceId"`
	ExternalID        string        `json:"externalId"`
	CreationTimestamp float64       `json:"creationTimestamp"`
	SourceType        string        `json:"sourceType"`
	SourceTitle       string        `json:"sourceTitle"`
	Title             string        `json:"title"`
	Start             string        `json:"start"`
	End               string        `json:"end"`
	AllDay            bool          `json:"allDay"`
	TimeZone          string        `json:"timeZone"`
	Availability      string        `json:"availability"`
	Status            string        `json:"status"`
	Location          string        `json:"location"`
	Latitude          float64       `json:"latitude"`
	Longitude         float64       `json:"longitude"`
	HasCoordinate     bool          `json:"hasCoordinate"`
	URL               string        `json:"url"`
	Notes             string        `json:"notes"`
	Recurring         bool          `json:"recurring"`
	OrganizerName     string        `json:"organizerName"`
	OrganizerEmail    string        `json:"organizerEmail"`
	Attendees         []RawAttendee `json:"attendees"`
	CurrentUserStatus string        `json:"currentUserStatus"`
}

// RawAttendee is one EKParticipant as the bridge serialises it.
type RawAttendee struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
	IsMe   bool   `json:"isCurrentUser"`
}

// NormalizeEventKit turns one bridge record into an Event. It returns false
// when the record has no usable identity or time.
func NormalizeEventKit(raw RawEvent) (Event, bool) {
	uid := strings.TrimSpace(raw.UID)
	if uid == "" {
		uid = strings.TrimSpace(raw.LocalID)
	}
	start, okStart := parseTime(raw.Start)
	end, okEnd := parseTime(raw.End)
	if uid == "" || !okStart {
		return Event{}, false
	}
	if !okEnd || !end.After(start) {
		end = start.Add(time.Hour)
		if raw.AllDay {
			end = start.AddDate(0, 0, 1)
			if loc, err := time.LoadLocation(strings.TrimSpace(raw.TimeZone)); err == nil {
				end = start.In(loc).AddDate(0, 0, 1)
			}
		}
	}
	occurrence := start
	if t, ok := parseTime(raw.OccurrenceDate); ok {
		occurrence = t
	}
	ev := Event{
		UID:            uid,
		LocalID:        strings.TrimSpace(raw.LocalID),
		Source:         SourceEventKit,
		CalendarID:     strings.TrimSpace(raw.CalendarID),
		CalendarTitle:  strings.TrimSpace(raw.CalendarTitle),
		Provider:       ProviderForEventKitSource(raw.SourceType, raw.SourceTitle),
		Title:          cleanText(raw.Title),
		Start:          start,
		End:            end,
		AllDay:         raw.AllDay,
		TimeZone:       strings.TrimSpace(raw.TimeZone),
		Location:       cleanText(raw.Location),
		Latitude:       raw.Latitude,
		Longitude:      raw.Longitude,
		HasCoordinate:  raw.HasCoordinate,
		URL:            strings.TrimSpace(raw.URL),
		Availability:   normalizeToken(raw.Availability, "busy"),
		Status:         normalizeToken(raw.Status, "none"),
		MyStatus:       normalizeStatus(raw.CurrentUserStatus),
		OrganizerEmail: NormalizeEmail(raw.OrganizerEmail),
		OrganizerName:  cleanText(raw.OrganizerName),
		Recurring:      raw.Recurring,
	}
	ev.ID = EventID(ev.Source, uid, occurrence)
	ev.ConferenceURL, ev.ConferenceHost = ExtractConferenceLink(raw.Location, raw.URL, raw.Notes)
	for _, a := range raw.Attendees {
		att := Attendee{
			Name:   cleanText(a.Name),
			Email:  NormalizeEmail(a.Email),
			Role:   normalizeToken(a.Role, "unknown"),
			Status: normalizeStatus(a.Status),
			IsMe:   a.IsMe,
		}
		if att.Name == "" && att.Email == "" {
			continue
		}
		ev.Attendees = append(ev.Attendees, att)
		if att.IsMe && ev.MyStatus == StatusUnknown {
			ev.MyStatus = att.Status
		}
	}
	sort.SliceStable(ev.Attendees, func(i, j int) bool {
		if ev.Attendees[i].IsMe != ev.Attendees[j].IsMe {
			return ev.Attendees[i].IsMe
		}
		return ev.Attendees[i].Email < ev.Attendees[j].Email
	})
	return ev, true
}

// EventID hashes the source, UID and occurrence start into a stable id.
func EventID(source, uid string, occurrence time.Time) string {
	sum := sha256.Sum256([]byte(source + "\x00" + uid + "\x00" + occurrence.UTC().Format(time.RFC3339)))
	return hex.EncodeToString(sum[:12])
}

// ProviderForEventKitSource maps an EKSource type and title to a provider
// name: icloud, google, exchange, local, subscribed or other.
func ProviderForEventKitSource(sourceType, sourceTitle string) string {
	title := strings.ToLower(strings.TrimSpace(sourceTitle))
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "exchange":
		return "exchange"
	case "local":
		return "local"
	case "subscribed":
		return "subscribed"
	case "mobileme":
		return "icloud"
	case "birthdays":
		return "other"
	}
	switch {
	case strings.Contains(title, "icloud"):
		return "icloud"
	case strings.Contains(title, "google") || strings.Contains(title, "gmail"):
		return "google"
	case strings.Contains(title, "exchange") || strings.Contains(title, "outlook") || strings.Contains(title, "office 365") || strings.Contains(title, "microsoft"):
		return "exchange"
	case title == "":
		return "other"
	default:
		return "other"
	}
}

// ProviderLabel is the user-facing name of a provider.
func ProviderLabel(provider string) string {
	switch provider {
	case "icloud":
		return "iCloud"
	case "google":
		return "Google"
	case "exchange":
		return "Exchange"
	case "outlook":
		return "Outlook"
	case "notion":
		return "Notion"
	case "fantastical":
		return "Fantastical"
	case "local":
		return "On this computer"
	case "subscribed":
		return "Subscribed"
	default:
		return "Other"
	}
}

// conferenceHosts lists the meeting platforms a link can point at. The names
// match what meetingdetect reports, so an event can be matched to a session.
var conferenceHosts = []struct {
	match string
	name  string
}{
	{"zoom.us/j/", "Zoom"},
	{"zoom.us/my/", "Zoom"},
	{"zoom.us/w/", "Zoom"},
	{"zoom.us/s/", "Zoom"},
	{"zoomgov.com/j/", "Zoom"},
	{"meet.google.com/", "Google Meet"},
	{"teams.microsoft.com/l/meetup-join", "Microsoft Teams"},
	{"teams.microsoft.com/meet", "Microsoft Teams"},
	{"teams.live.com/meet", "Microsoft Teams"},
	{"webex.com/", "WebEx"},
	{"whereby.com/", "Whereby"},
	{"meet.jit.si/", "Jitsi Meet"},
	{"app.slack.com/huddle", "Slack"},
	{"meeting.tencent.com/", "Tencent Meeting"},
	{"voovmeeting.com/", "Tencent Meeting"},
	{"vc.feishu.cn/", "Feishu"},
	{"vc.larksuite.com/", "Feishu"},
	{"chime.aws/", "Amazon Chime"},
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'()\[\]]+`)

// ExtractConferenceLink finds the first meeting link in the location, URL and
// notes of an event, in that order, and names its platform. Trailing
// punctuation is dropped. It returns empty strings when there is none.
func ExtractConferenceLink(location, rawURL, notes string) (string, string) {
	for _, text := range []string{location, rawURL, notes} {
		for _, candidate := range urlPattern.FindAllString(text, -1) {
			candidate = strings.TrimRight(candidate, ".,;:!?")
			if host := ConferenceHost(candidate); host != "" {
				return candidate, host
			}
		}
	}
	return "", ""
}

// ConferenceHost names the platform a link points at, or "" for a link that
// is not a meeting link.
func ConferenceHost(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.Host == "" {
		return ""
	}
	hostAndPath := strings.ToLower(parsed.Host + parsed.Path)
	if parsed.Path == "" {
		hostAndPath += "/"
	}
	for _, h := range conferenceHosts {
		if strings.Contains(hostAndPath, h.match) {
			return h.name
		}
	}
	return ""
}

// NormalizeEmail lower-cases an address, strips a mailto: prefix and drops
// values that are not shaped like an address.
func NormalizeEmail(raw string) string {
	e := strings.ToLower(strings.TrimSpace(raw))
	e = strings.TrimPrefix(e, "mailto:")
	at := strings.IndexByte(e, '@')
	if at <= 0 || at == len(e)-1 || strings.ContainsAny(e, " \t<>") {
		return ""
	}
	return e
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(strings.ToValidUTF8(value, "")), " ")
}

func normalizeToken(value, fallback string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return fallback
	}
	return v
}

func normalizeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accepted":
		return StatusAccepted
	case "declined":
		return StatusDeclined
	case "tentative":
		return StatusTentative
	case "pending":
		return StatusPending
	default:
		return StatusUnknown
	}
}

// SortByStart orders events by start time, then title, in place.
func SortByStart(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].Start.Equal(events[j].Start) {
			return events[i].Start.Before(events[j].Start)
		}
		return events[i].Title < events[j].Title
	})
}
