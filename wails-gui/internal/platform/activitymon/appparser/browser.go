package appparser

import (
	"sort"
	"strings"

	"wails-gui/internal/browsers"
)

// The browsers this parser takes are internal/browsers' list, the one
// Isoform's call detection and tab-address reader use too.

// browserParserBundles lists the macOS browsers, sorted, for the catalog.
func browserParserBundles() []string {
	ids := browsers.MacBundleIDs()
	sort.Strings(ids)
	return ids
}

// browserChromePrefixes are line prefixes typical of browser UI chrome
// (toolbar buttons, tab bar, bookmarks bar, extensions). When a line starts
// with one of these (after trimming), it's dropped from the page content.
var browserChromePrefixes = []string{
	"New Tab", "Close Tab", "Reload this page", "Go back one page",
	"Go forward one page", "View site information", "Show bookmarks",
	"Customize and control", "Chrome menu", "Extensions", "Search tabs",
	"Reading list", "Downloads", "History",
	"Safari menu", "Show tab overview", "Show sidebar", "Share",
	"Back/Forward", "Site-specific",
	"Reload Page", "Add bookmark for this page", "Bookmarks Menu",
	"Tab Overview", "Show/hide the sidebar",
	"Show tab switcher", "Show downloads", "View bookmarks",
}

// browserChromeExact is a set of exact lines (after trim) that are dropped.
var browserChromeExact = map[string]bool{
	"Back":                   true,
	"Forward":                true,
	"Reload":                 true,
	"Stop":                   true,
	"Home":                   true,
	"Address and search bar": true,
	"Bookmarks":              true,
	"Downloads":              true,
	"Extensions":             true,
	"Menu":                   true,
	"Tab bar":                true,
	"Location Bar":           true,
	"AXTabGroup":             true,
	"Toolbar":                true,
	"TabBar":                 true,
	"Sidebar":                true,
	"Pinned Tabs":            true,
	"New Tab":                true,
	"Close":                  true,
	"+":                      true,
	"...":                    true,
}

type browserParser struct{}

func init() {
	Register(&browserParser{})
}

func (p *browserParser) CanParse(bundleID string) bool {
	family := browsers.FamilyOf(bundleID)
	return family != browsers.NotABrowser && family != browsers.WindowsBrowser
}

func (p *browserParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "browser",
		Label:       "Browser (generic fallback)",
		ContentType: "web_page",
		Bundles:     browserParserBundles(),
	}
}

// Parse strips browser chrome (toolbar, tab bar, bookmarks, extensions UI)
// from the AX tree text, leaving only the actual web page content.
//
// Heuristic approach (same strategy as LittleBird's BrowserStripper but
// adapted for our flat line-based AX output): drop lines that match known
// chrome prefixes or exact chrome labels. The remaining lines should be
// dominated by the page content.
func (p *browserParser) Parse(axText, bundleID, windowTitle, url string) *ParsedContent {
	if axText == "" {
		return nil
	}

	lines := strings.Split(axText, "\n")
	kept := make([]string, 0, len(lines))

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if browserChromeExact[line] {
			continue
		}
		if hasBrowserChromePrefix(line) {
			continue
		}
		// Drop very short single-word lines that are likely button labels.
		if len(line) <= 2 {
			continue
		}
		kept = append(kept, line)
	}

	body := strings.Join(kept, "\n")
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	return &ParsedContent{
		ContentType: "web_page",
		Title:       windowTitle,
		Body:        body,
		URL:         url,
		Metadata: map[string]string{
			"bundleId": bundleID,
		},
	}
}

func hasBrowserChromePrefix(line string) bool {
	for _, prefix := range browserChromePrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
