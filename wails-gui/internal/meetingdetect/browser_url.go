package meetingdetect

import (
	"net/url"
	"regexp"
	"strings"
)

// A browser page's title is whatever the page says, and a chat can be named
// anything ("Zoom Meeting prep", "Chat | Microsoft Teams"), so a browser
// call is confirmed by the address of the tab in front: the call pages of
// the meeting sites.

// meetCodePattern is a Google Meet meeting code: abc-defg-hij.
var meetCodePattern = regexp.MustCompile(`^[a-z]{3}-[a-z]{4}-[a-z]{3}$`)

// BrowserMeetingForURL returns the meeting whose call page the address is,
// or "": Google Meet (meet.google.com/<code>), Zoom's web client (zoom.us or
// zoom.com, /wc/ or /j/), Microsoft Teams meeting pages (teams.microsoft.com,
// teams.live.com or teams.cloud.microsoft with a meeting path), Jitsi Meet
// (meet.jit.si/<room>) and Whereby (whereby.com/<room>).
func BrowserMeetingForURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(u.EscapedPath())
	room := strings.Trim(path, "/")
	switch site := meetingSite(host); site {
	case "Google Meet":
		if meetCodePattern.MatchString(strings.SplitN(room, "/", 2)[0]) {
			return site
		}
	case "Zoom":
		if strings.Contains(path, "/wc/") || strings.HasPrefix(path, "/j/") {
			return site
		}
	case "Microsoft Teams":
		whole := strings.ToLower(u.EscapedPath() + "?" + u.RawQuery + "#" + u.Fragment)
		for _, hint := range []string{"meetup-join", "/meet/", "meetingjoin", "calling"} {
			if strings.Contains(whole, hint) {
				return site
			}
		}
	case "Jitsi Meet":
		if room != "" {
			return site
		}
	case "Whereby":
		if room != "" && !strings.Contains(room, "/") && !wherebySitePages[room] {
			return site
		}
	}
	return ""
}

// wherebySitePages are whereby.com's own pages, not rooms.
var wherebySitePages = map[string]bool{
	"pricing": true, "login": true, "signup": true, "user": true, "information": true, "blog": true,
	"about": true, "contact": true, "terms": true, "privacy": true, "download": true, "org": true, "support": true,
}

// meetingSite names the meeting site a host belongs to, or "".
func meetingSite(host string) string {
	if host == "meet.google.com" {
		return "Google Meet"
	}
	if hostWithin(host, "zoom.us") || hostWithin(host, "zoom.com") {
		return "Zoom"
	}
	if hostWithin(host, "teams.microsoft.com") || hostWithin(host, "teams.live.com") || hostWithin(host, "teams.cloud.microsoft") {
		return "Microsoft Teams"
	}
	if host == "meet.jit.si" {
		return "Jitsi Meet"
	}
	if hostWithin(host, "whereby.com") {
		return "Whereby"
	}
	return ""
}

// hostWithin reports whether host is domain or one of its subdomains.
func hostWithin(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}
