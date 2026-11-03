package calendar

import (
	"testing"
	"time"
)

func TestSourcesFromEventKit(t *testing.T) {
	now := mustTime("2026-10-14T06:00:00Z")
	sources := SourcesFromEventKit([]RawCalendar{
		{ID: "c1", Title: "Work", SourceIdentifier: "src-google", SourceTitle: "Google", SourceType: "caldav"},
		{ID: "c2", Title: "Family", SourceIdentifier: "src-google", SourceTitle: "Google", SourceType: "caldav"},
		{ID: "c3", Title: "Home", SourceIdentifier: "src-icloud", SourceTitle: "iCloud", SourceType: "caldav"},
		{ID: "c4", Title: "Birthdays", SourceIdentifier: "", SourceTitle: "Other", SourceType: "birthdays"},
	}, now)
	if len(sources) != 3 {
		t.Fatalf("sources = %+v", sources)
	}
	byID := map[string]Source{}
	for _, s := range sources {
		byID[s.ID] = s
	}
	google := byID["eventkit:src-google"]
	if google.Provider != "google" || google.Calendars != 2 || google.Kind != SourceKindEventKit || google.DetectedBy != DetectedByEventKit {
		t.Fatalf("google %+v", google)
	}
	if byID["eventkit:src-icloud"].Provider != "icloud" {
		t.Fatalf("icloud %+v", byID["eventkit:src-icloud"])
	}
	if _, ok := byID["eventkit:birthdays:other"]; !ok {
		t.Fatalf("a source without an identifier gets one from its type and title: %+v", sources)
	}
}

func TestClassifyWebCalendarURL(t *testing.T) {
	cases := []struct {
		url      string
		provider string
		ok       bool
	}{
		{"https://calendar.google.com/calendar/u/0/r/week", "google", true},
		{"https://calendar.google.com/calendar/u/1/r/day/2026/10/14", "google", true},
		{"https://outlook.office.com/calendar/view/week", "outlook", true},
		{"https://outlook.office365.com/calendar/", "outlook", true},
		{"https://outlook.live.com/calendar/0/view/month", "outlook", true},
		{"https://outlook.office.com/mail/inbox", "", false},
		{"https://calendar.notion.so/", "notion", true},
		{"https://www.notion.so/workspace/page", "", false},
		{"https://app.fantastical.com/", "fantastical", true},
		{"https://mail.google.com/mail/u/0/", "", false},
		{"not a url", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		provider, ok := ClassifyWebCalendarURL(tc.url)
		if provider != tc.provider || ok != tc.ok {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", tc.url, provider, ok, tc.provider, tc.ok)
		}
	}
}

func TestWebCalendarsToPeek(t *testing.T) {
	now := mustTime("2026-10-14T06:00:00Z")
	recent := now.Add(-2 * 24 * time.Hour)
	old := now.Add(-30 * 24 * time.Hour)
	google := Source{ID: WebSourceID("google"), Kind: SourceKindWeb, Provider: "google", LastSeen: recent}
	outlook := Source{ID: WebSourceID("outlook"), Kind: SourceKindWeb, Provider: "outlook", LastSeen: recent}
	notion := Source{ID: WebSourceID("notion"), Kind: SourceKindWeb, Provider: "notion", LastSeen: recent}
	googleAccount := Source{ID: "eventkit:g", Kind: SourceKindEventKit, Provider: "google"}
	exchangeAccount := Source{ID: "eventkit:x", Kind: SourceKindEventKit, Provider: "exchange"}

	cases := []struct {
		name    string
		sources []Source
		want    []string
	}{
		{"both web, neither in macOS", []Source{google, outlook, notion}, []string{"google", "outlook"}},
		{"google covered by its account", []Source{google, outlook, googleAccount}, []string{"outlook"}},
		{"outlook covered by exchange", []Source{google, outlook, exchangeAccount}, []string{"google"}},
		{"web calendar not used lately", []Source{{ID: WebSourceID("google"), Kind: SourceKindWeb, Provider: "google", LastSeen: old}}, nil},
		{"nothing seen", []Source{googleAccount}, nil},
		{"notion is not peekable", []Source{notion}, nil},
	}
	for _, tc := range cases {
		got := WebCalendarsToPeek(tc.sources, now, PeekRecentUse)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		}
	}
}

func TestWebCalendarURL(t *testing.T) {
	if got := WebCalendarURL("google"); got != "https://calendar.google.com/calendar/u/0/r/week" {
		t.Fatalf("google %q", got)
	}
	if got := WebCalendarURL("notion"); got != "" {
		t.Fatalf("notion has no peek url, got %q", got)
	}
	if got := WebSourceHost("outlook"); got != "outlook.office.com" {
		t.Fatalf("outlook host %q", got)
	}
}
