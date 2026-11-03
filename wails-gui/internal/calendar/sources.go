package calendar

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// Source kinds: a calendar account macOS knows, or a calendar the user opens
// in a browser.
const (
	SourceKindEventKit = "eventkit"
	SourceKindWeb      = "web"
)

// How a source was noticed.
const (
	DetectedByEventKit = "eventkit"
	DetectedByHistory  = "history"
	DetectedByScreen   = "screen"
	DetectedByPeek     = "peek"
)

// Source is one calendar Isoform can see or has seen the user use.
type Source struct {
	// ID is "eventkit:<source identifier>" or "web:<provider>".
	ID       string
	Kind     string
	Provider string
	// Title is the account or calendar name macOS reports, or the site's
	// host for a web calendar.
	Title      string
	DetectedBy string
	// Calendars counts the calendars under an EventKit source.
	Calendars int
	FirstSeen time.Time
	LastSeen  time.Time
	// LastRead is when events were last read from this source.
	LastRead time.Time
}

// RawCalendar is one EKCalendar as the bridge serialises it.
type RawCalendar struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	SourceIdentifier string `json:"sourceId"`
	SourceTitle      string `json:"sourceTitle"`
	SourceType       string `json:"sourceType"`
	Subscribed       bool   `json:"subscribed"`
}

// SourcesFromEventKit groups the calendars macOS reports by account.
func SourcesFromEventKit(calendars []RawCalendar, now time.Time) []Source {
	byID := map[string]*Source{}
	for _, cal := range calendars {
		sourceID := strings.TrimSpace(cal.SourceIdentifier)
		if sourceID == "" {
			sourceID = strings.ToLower(strings.TrimSpace(cal.SourceType) + ":" + strings.TrimSpace(cal.SourceTitle))
		}
		id := "eventkit:" + sourceID
		src, ok := byID[id]
		if !ok {
			src = &Source{
				ID:         id,
				Kind:       SourceKindEventKit,
				Provider:   ProviderForEventKitSource(cal.SourceType, cal.SourceTitle),
				Title:      strings.TrimSpace(cal.SourceTitle),
				DetectedBy: DetectedByEventKit,
				FirstSeen:  now,
				LastSeen:   now,
			}
			byID[id] = src
		}
		src.Calendars++
	}
	out := make([]Source, 0, len(byID))
	for _, src := range byID {
		out = append(out, *src)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// webCalendarSites lists the web calendars Isoform recognises in a URL.
var webCalendarSites = []struct {
	provider string
	host     string
	path     string
}{
	{"google", "calendar.google.com", ""},
	{"outlook", "outlook.office.com", "/calendar"},
	{"outlook", "outlook.office365.com", "/calendar"},
	{"outlook", "outlook.live.com", "/calendar"},
	{"notion", "calendar.notion.so", ""},
	{"fantastical", "app.fantastical.com", ""},
	{"fantastical", "fantastical.app", ""},
}

// ClassifyWebCalendarURL names the web calendar a URL belongs to. It returns
// false for any other page, including the rest of Outlook and Notion.
func ClassifyWebCalendarURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.Path)
	for _, site := range webCalendarSites {
		if host != site.host {
			continue
		}
		if site.path != "" && !strings.HasPrefix(path, site.path) {
			continue
		}
		return site.provider, true
	}
	return "", false
}

// WebSourceID is the source id of a web calendar provider.
func WebSourceID(provider string) string {
	return "web:" + strings.ToLower(strings.TrimSpace(provider))
}

// WebSourceHost is the site shown for a web calendar provider.
func WebSourceHost(provider string) string {
	for _, site := range webCalendarSites {
		if site.provider == provider {
			return site.host
		}
	}
	return ""
}

// WebCalendarURL is the week view Isoform opens to read a web calendar.
func WebCalendarURL(provider string) string {
	switch provider {
	case "google":
		return "https://calendar.google.com/calendar/u/0/r/week"
	case "outlook":
		return "https://outlook.office.com/calendar/view/week"
	default:
		return ""
	}
}

// PeekableProviders lists the web calendars a peek knows how to read.
func PeekableProviders() []string { return []string{"google", "outlook"} }

// coveredBy says which EventKit providers already carry a web provider's
// events: a Google account added to macOS covers the Google web calendar, an
// Exchange account covers Outlook on the web.
var coveredBy = map[string][]string{
	"google":  {"google"},
	"outlook": {"exchange", "outlook"},
}

// WebCalendarsToPeek returns the web calendar providers the user has used
// (seen in the browser within recentWindow of now) whose account is not among
// the EventKit sources, in a stable order. Those are the calendars a peek
// must read, because nothing else can.
func WebCalendarsToPeek(sources []Source, now time.Time, recentWindow time.Duration) []string {
	eventKit := map[string]bool{}
	for _, src := range sources {
		if src.Kind == SourceKindEventKit {
			eventKit[src.Provider] = true
		}
	}
	var out []string
	for _, provider := range PeekableProviders() {
		var seen bool
		for _, src := range sources {
			if src.Kind != SourceKindWeb || src.Provider != provider {
				continue
			}
			if recentWindow > 0 && now.Sub(src.LastSeen) > recentWindow {
				continue
			}
			seen = true
		}
		if !seen {
			continue
		}
		covered := false
		for _, p := range coveredBy[provider] {
			if eventKit[p] {
				covered = true
			}
		}
		if !covered {
			out = append(out, provider)
		}
	}
	return out
}
