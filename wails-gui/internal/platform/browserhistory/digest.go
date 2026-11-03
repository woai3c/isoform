package browserhistory

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// Site is how much one site was visited.
type Site struct {
	Host   string
	Visits int
	Last   time.Time
}

// Thread is one search and the pages read after it: what the user was
// researching.
type Thread struct {
	Query string
	At    time.Time
	Last  time.Time
	// Pages counts the distinct pages read after the search.
	Pages int
	// Titles are the first few of those pages, in order.
	Titles []string
}

// threadWindow is how long after a search a page still counts as read for
// it; the next search ends it sooner.
const threadWindow = 20 * time.Minute

// threadTitles is how many page titles a thread keeps.
const threadTitles = 3

// searchSuffixes are the endings search engines put on a results page title.
var searchSuffixes = []string{
	" - Google Search", " - Google 搜索", " - Google 検索", " - Google Suche", " - Recherche Google",
	" - Bing", " - 搜索", " at DuckDuckGo", " - Kagi Search", " - Brave Search", "_百度搜索",
}

// searchPaths names the results page of each search host.
var searchPaths = map[string][]string{
	"google":     {"/search"},
	"bing":       {"/search"},
	"duckduckgo": {"", "/"},
	"kagi":       {"/search"},
	"brave":      {"/search"},
	"baidu":      {"/s"},
}

// Host is a link's host without "www.", lower-cased; "" for anything that is
// not a web link.
func Host(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// googleHost reports whether a host is Google's own: "google." followed by a
// country domain such as "com", "de" or "co.uk", never a look-alike such as
// "google.attacker.example".
func googleHost(host string) bool {
	rest, ok := strings.CutPrefix(host, "google.")
	if !ok {
		return false
	}
	labels := strings.Split(rest, ".")
	if len(labels) > 2 {
		return false
	}
	for _, label := range labels {
		if len(label) < 2 || len(label) > 3 {
			return false
		}
	}
	return true
}

// searchHost reports whether a host serves search results, and its key in
// searchPaths.
func searchHost(host string) (string, bool) {
	switch {
	case googleHost(host):
		return "google", true
	case host == "bing.com" || host == "cn.bing.com":
		return "bing", true
	case host == "duckduckgo.com" || host == "html.duckduckgo.com":
		return "duckduckgo", true
	case host == "kagi.com":
		return "kagi", true
	case host == "search.brave.com":
		return "brave", true
	case host == "baidu.com" || host == "m.baidu.com":
		return "baidu", true
	default:
		return "", false
	}
}

// SearchQuery reads the query of a search results page from its title; ok is
// false for any other page.
func SearchQuery(rawURL, title string) (string, bool) {
	host := Host(rawURL)
	key, ok := searchHost(host)
	if !ok {
		return "", false
	}
	u, _ := url.Parse(rawURL)
	pathOK := false
	for _, path := range searchPaths[key] {
		if u.Path == path {
			pathOK = true
		}
	}
	if !pathOK {
		return "", false
	}
	title = strings.TrimSpace(title)
	for _, suffix := range searchSuffixes {
		if query, found := strings.CutSuffix(title, suffix); found {
			query = strings.TrimSpace(query)
			return query, query != ""
		}
	}
	return "", false
}

// TopSites counts visits per site, most visited first, at most limit. Search
// results pages are left out: ResearchThreads covers them.
func TopSites(visits []Visit, limit int) []Site {
	bySite := map[string]*Site{}
	for _, v := range visits {
		host := Host(v.URL)
		if host == "" || host == "localhost" || host == "127.0.0.1" {
			continue
		}
		if _, search := searchHost(host); search {
			continue
		}
		site := bySite[host]
		if site == nil {
			site = &Site{Host: host}
			bySite[host] = site
		}
		site.Visits++
		if v.At.After(site.Last) {
			site.Last = v.At
		}
	}
	out := make([]Site, 0, len(bySite))
	for _, site := range bySite {
		out = append(out, *site)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Visits != out[j].Visits {
			return out[i].Visits > out[j].Visits
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Host < out[j].Host
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ResearchThreads finds each search and the pages read in the twenty minutes
// after it (until the next search), newest first, at most limit. The same
// query searched again joins its earlier thread.
func ResearchThreads(visits []Visit, limit int) []Thread {
	sorted := append([]Visit(nil), visits...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	byQuery := map[string]*Thread{}
	var order []string
	var current *Thread
	seenPage := map[*Thread]map[string]bool{}
	for _, v := range sorted {
		if query, ok := SearchQuery(v.URL, v.Title); ok {
			key := strings.ToLower(query)
			thread := byQuery[key]
			if thread == nil {
				thread = &Thread{Query: query, At: v.At}
				byQuery[key] = thread
				order = append(order, key)
				seenPage[thread] = map[string]bool{}
			}
			thread.Last = v.At
			current = thread
			continue
		}
		if current == nil || v.At.Sub(current.Last) > threadWindow {
			current = nil
			continue
		}
		title := strings.TrimSpace(v.Title)
		page := title
		if page == "" {
			page = v.URL
		}
		if seenPage[current][page] {
			continue
		}
		seenPage[current][page] = true
		current.Pages++
		if title != "" && len(current.Titles) < threadTitles {
			current.Titles = append(current.Titles, title)
		}
	}
	out := make([]Thread, 0, len(order))
	for _, key := range order {
		out = append(out, *byQuery[key])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
