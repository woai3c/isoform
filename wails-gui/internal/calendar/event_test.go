package calendar

import (
	"testing"
	"time"
)

func TestNormalizeEventKit(t *testing.T) {
	raw := RawEvent{
		UID:            "ABC-123",
		LocalID:        "local-1",
		OccurrenceDate: "2026-10-14T09:00:00Z",
		CalendarID:     "cal-1",
		CalendarTitle:  "Work",
		SourceType:     "caldav",
		SourceTitle:    "Google",
		Title:          "  Pricing   sync ",
		Start:          "2026-10-14T09:00:00Z",
		End:            "2026-10-14T09:30:00Z",
		Location:       "Room 4",
		URL:            "https://zoom.us/j/123456?pwd=abc",
		Notes:          "agenda",
		Availability:   "Busy",
		Status:         "Confirmed",
		OrganizerEmail: "MAILTO:Ann@Example.com",
		OrganizerName:  "Ann",
		Attendees: []RawAttendee{
			{Name: "Ann", Email: "ann@example.com", Role: "chair", Status: "accepted"},
			{Name: "Me", Email: "me@example.com", Role: "required", Status: "pending", IsMe: true},
			{Name: "", Email: ""},
		},
	}
	ev, ok := NormalizeEventKit(raw)
	if !ok {
		t.Fatal("expected a usable event")
	}
	if ev.ID == "" || ev.ID != EventID(SourceEventKit, "ABC-123", mustTime("2026-10-14T09:00:00Z")) {
		t.Fatalf("unexpected id %q", ev.ID)
	}
	if ev.Title != "Pricing sync" {
		t.Fatalf("title %q", ev.Title)
	}
	if ev.Provider != "google" {
		t.Fatalf("provider %q", ev.Provider)
	}
	if ev.ConferenceURL != "https://zoom.us/j/123456?pwd=abc" || ev.ConferenceHost != "Zoom" {
		t.Fatalf("conference %q %q", ev.ConferenceURL, ev.ConferenceHost)
	}
	if ev.OrganizerEmail != "ann@example.com" {
		t.Fatalf("organizer %q", ev.OrganizerEmail)
	}
	if ev.MyStatus != StatusPending {
		t.Fatalf("my status %q", ev.MyStatus)
	}
	if len(ev.Attendees) != 2 || !ev.Attendees[0].IsMe {
		t.Fatalf("attendees %+v", ev.Attendees)
	}
	if ev.AttendeeCount() != 1 || !ev.HasOthers() || !ev.IsMeetingLike() {
		t.Fatalf("attendee count %d", ev.AttendeeCount())
	}
	if ev.Availability != "busy" || ev.Status != "confirmed" || !ev.Busy() {
		t.Fatalf("availability %q status %q", ev.Availability, ev.Status)
	}
}

func TestNormalizeEventKitFallbacks(t *testing.T) {
	if _, ok := NormalizeEventKit(RawEvent{Start: "2026-10-14T09:00:00Z"}); ok {
		t.Fatal("an event without any identifier must be dropped")
	}
	if _, ok := NormalizeEventKit(RawEvent{UID: "x", Start: "not a time"}); ok {
		t.Fatal("an event without a start must be dropped")
	}
	ev, ok := NormalizeEventKit(RawEvent{LocalID: "local-9", Start: "2026-10-14T09:00:00Z", End: "2026-10-14T08:00:00Z"})
	if !ok || ev.UID != "local-9" {
		t.Fatalf("local id fallback: %+v %v", ev, ok)
	}
	if !ev.End.Equal(ev.Start.Add(time.Hour)) {
		t.Fatalf("an end before the start becomes one hour: %v", ev.End)
	}
	allDay, _ := NormalizeEventKit(RawEvent{UID: "d", Start: "2026-10-14T00:00:00Z", AllDay: true})
	if !allDay.End.Equal(allDay.Start.Add(24 * time.Hour)) {
		t.Fatalf("all-day end %v", allDay.End)
	}
}

func TestNormalizeEventKitAllDayFallbackAcrossDST(t *testing.T) {
	for _, day := range calendarDSTTransitionDays(t) {
		t.Run(day.Format("2006-01-02"), func(t *testing.T) {
			ev, ok := NormalizeEventKit(RawEvent{
				UID:      "all-day-" + day.Format("2006-01-02"),
				Start:    day.Format(time.RFC3339),
				AllDay:   true,
				TimeZone: day.Location().String(),
			})
			want := day.AddDate(0, 0, 1)
			if !ok || !ev.End.Equal(want) {
				t.Fatalf("all-day end = %s, want %s", ev.End, want)
			}
		})
	}
}

func TestBusy(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want bool
	}{
		{"busy", Event{Availability: "busy"}, true},
		{"free", Event{Availability: "free"}, false},
		{"canceled", Event{Availability: "busy", Status: "canceled"}, false},
		{"declined", Event{Availability: "busy", MyStatus: StatusDeclined}, false},
		{"tentative", Event{Availability: "tentative"}, true},
	}
	for _, tc := range cases {
		if got := tc.ev.Busy(); got != tc.want {
			t.Errorf("%s: Busy() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExtractConferenceLink(t *testing.T) {
	cases := []struct {
		name                 string
		location, url, notes string
		wantLink, wantHost   string
	}{
		{"zoom in location", "https://us02web.zoom.us/j/8812345678?pwd=x", "", "", "https://us02web.zoom.us/j/8812345678?pwd=x", "Zoom"},
		{"meet in url", "Room 2", "https://meet.google.com/abc-defg-hij", "", "https://meet.google.com/abc-defg-hij", "Google Meet"},
		{"teams in notes with trailing period", "", "", "Join: https://teams.microsoft.com/l/meetup-join/19%3ameeting_x%40thread.v2/0?context=%7b%7d.", "https://teams.microsoft.com/l/meetup-join/19%3ameeting_x%40thread.v2/0?context=%7b%7d", "Microsoft Teams"},
		{"webex", "", "", "https://acme.webex.com/acme/j.php?MTID=m1", "https://acme.webex.com/acme/j.php?MTID=m1", "WebEx"},
		{"location wins over notes", "https://meet.google.com/aaa-bbbb-ccc", "", "https://zoom.us/j/1", "https://meet.google.com/aaa-bbbb-ccc", "Google Meet"},
		{"plain url is not a meeting", "", "https://example.com/doc", "see https://example.com/notes", "", ""},
		{"zoom marketing page is not a meeting", "", "https://zoom.us/pricing", "", "", ""},
		{"nothing", "Cafe", "", "bring the deck", "", ""},
	}
	for _, tc := range cases {
		link, host := ExtractConferenceLink(tc.location, tc.url, tc.notes)
		if link != tc.wantLink || host != tc.wantHost {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, link, host, tc.wantLink, tc.wantHost)
		}
	}
}

func TestProviderForEventKitSource(t *testing.T) {
	cases := []struct {
		typ, title, want string
	}{
		{"exchange", "Work", "exchange"},
		{"caldav", "iCloud", "icloud"},
		{"mobileme", "iCloud", "icloud"},
		{"caldav", "Google", "google"},
		{"caldav", "owner@example.com", "other"},
		{"caldav", "Office 365", "exchange"},
		{"local", "On My Mac", "local"},
		{"subscribed", "Holidays", "subscribed"},
		{"caldav", "Fastmail", "other"},
	}
	for _, tc := range cases {
		if got := ProviderForEventKitSource(tc.typ, tc.title); got != tc.want {
			t.Errorf("(%q, %q) = %q, want %q", tc.typ, tc.title, got, tc.want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail(" MAILTO:Ann@Example.COM "); got != "ann@example.com" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "ann", "@x", "ann@", "a b@c"} {
		if got := NormalizeEmail(bad); got != "" {
			t.Errorf("%q should be dropped, got %q", bad, got)
		}
	}
}

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}
