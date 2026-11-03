package appparser

import (
	"net/url"
	"strings"
)

// hostMatches reports whether the given rawURL has a host that matches any
// of the provided patterns. A pattern is either an exact host ("notion.so")
// or a leading wildcard ("*.notion.site"). Both schemes (http, https) and
// arbitrary paths are accepted.
//
// Examples:
//
//	hostMatches("https://www.notion.so/team/ABC", "notion.so", "*.notion.site") → true
//	hostMatches("https://acme.notion.site/wiki",  "notion.so", "*.notion.site") → true
//	hostMatches("https://example.com",            "notion.so")                  → false
//
// Used by web parsers to implement URLMatcher. Returns false on URLs that
// fail to parse so callers don't have to special-case empty/invalid input.
func hostMatches(rawURL string, patterns ...string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "*.") {
			suffix := p[1:] // ".notion.site"
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == p {
			return true
		}
		// Treat "notion.so" as also matching "www.notion.so" so each parser
		// doesn't have to enumerate www. as a separate pattern.
		if strings.HasPrefix(host, "www.") && host[len("www."):] == p {
			return true
		}
	}
	return false
}
