package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/browserhistory"
)

// The brief's watching section, from the browser history: the sites visited
// most in the brief's window and the research threads (each search and how
// many pages followed it). Page titles stay on the Mac; only hosts and
// search queries, scrubbed when the visits were stored, reach the phone.
const (
	mobileBriefWebSource       = "web"
	mobileBriefWatchingSites   = 4
	mobileBriefWatchingThreads = 4
)

// collectMobileBriefWatching adds the web candidates to in.Watching.
func collectMobileBriefWatching(a *App, in *mobileBriefInput) {
	collectMobileBriefWatchingContext(context.Background(), a, in)
}

func collectMobileBriefWatchingContext(ctx context.Context, a *App, in *mobileBriefInput) {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return
	}
	rows, err := a.activity.store.ListBrowserVisitsContext(ctx, in.WindowStart.UnixMilli(), in.AsOf.UnixMilli())
	if err != nil {
		log.Printf("[mobile-brief] browser history unavailable: %v", err)
		return
	}
	// A browser protected since its visits were stored is left out.
	kept := rows[:0]
	for _, row := range rows {
		browser, known := browserhistory.BrowserNamed(row.Browser)
		if !known || row.VisitedAt <= 0 || !policy.allows(activitySourceProvenance{
			Apps: []string{browser.BundleID, browser.Name}, StartTime: row.VisitedAt, EndTime: row.VisitedAt + 1,
			Private: activitymon.IsPrivateBrowsing(row.Title),
		}) {
			continue
		}
		kept = append(kept, row)
	}
	in.Watching = append(in.Watching, mobileBriefWatchingCandidates(kept)...)
}

// mobileBriefWatchingCandidates turns stored visits into the top sites and
// research threads.
func mobileBriefWatchingCandidates(rows []BrowserVisitRow) []mobileBriefCandidate {
	if len(rows) == 0 {
		return nil
	}
	visits := make([]browserhistory.Visit, 0, len(rows))
	for _, row := range rows {
		visits = append(visits, browserhistory.Visit{URL: row.URL, Title: row.Title, At: time.UnixMilli(row.VisitedAt)})
	}
	var out []mobileBriefCandidate
	// Ranking and joined research threads depend on the entire input, not
	// just the final query's last visit. Keep every contributing browser and
	// the conservative capture interval, without URLs or page titles.
	source := &MobileBriefWebSource{StartMs: rows[0].VisitedAt, EndMs: rows[0].VisitedAt + 1}
	for _, row := range rows {
		source.Browsers = append(source.Browsers, row.Browser)
		source.StartMs = min(source.StartMs, row.VisitedAt)
		source.EndMs = max(source.EndMs, row.VisitedAt+1)
	}
	source.Browsers = compactStrings(source.Browsers)
	sort.Strings(source.Browsers)
	for _, site := range browserhistory.TopSites(visits, mobileBriefWatchingSites) {
		detail := "1 visit"
		if site.Visits != 1 {
			detail = fmt.Sprintf("%d visits", site.Visits)
		}
		out = append(out, mobileBriefCandidate{
			SourceKind: mobileBriefWebSource,
			SourceID:   "site:" + site.Host,
			Title:      site.Host,
			Detail:     detail,
			Status:     "visited",
			Kind:       "site",
			At:         site.Last,
			WebSource:  source,
		})
	}
	for _, thread := range browserhistory.ResearchThreads(visits, mobileBriefWatchingThreads) {
		sum := sha256.Sum256([]byte(strings.ToLower(thread.Query)))
		out = append(out, mobileBriefCandidate{
			SourceKind: mobileBriefWebSource,
			SourceID:   "search:" + hex.EncodeToString(sum[:8]),
			Title:      thread.Query,
			Detail:     mobileBriefThreadDetail(thread),
			Status:     "researched",
			Kind:       "research",
			At:         thread.Last,
			WebSource:  source,
		})
	}
	return out
}

func (a *App) mobileBriefWebSourceAllowsRead(source *MobileBriefWebSource) bool {
	// A site or query hash alone cannot prove which browser supplied it.
	if source == nil || len(source.Browsers) == 0 || source.StartMs <= 0 || source.EndMs <= source.StartMs {
		return false
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return false
	}
	apps := make([]string, 0, len(source.Browsers)*2)
	for _, name := range source.Browsers {
		browser, known := browserhistory.BrowserNamed(name)
		if !known {
			return false
		}
		apps = append(apps, browser.BundleID, browser.Name)
	}
	return policy.allows(activitySourceProvenance{Apps: apps, StartTime: source.StartMs, EndTime: source.EndMs})
}

// mobileBriefThreadDetail says what followed a search: "Read 3 pages".
func mobileBriefThreadDetail(thread browserhistory.Thread) string {
	switch thread.Pages {
	case 0:
		return "Searched"
	case 1:
		return "Searched, then read 1 page"
	default:
		return fmt.Sprintf("Searched, then read %d pages", thread.Pages)
	}
}
