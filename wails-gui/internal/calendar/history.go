package calendar

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"wails-gui/internal/platform/browserhistory"
)

// WebVisit is one web calendar the browser history shows the user opened.
type WebVisit struct {
	Provider string
	URL      string
	LastSeen time.Time
	Visits   int
	// Browser is the data directory the visit came from (Chrome, Brave...).
	Browser string
}

// ChromiumHistoryFiles lists the History databases that exist under home.
// The returned names are the browser labels for the paths.
func ChromiumHistoryFiles(home string) map[string]string {
	out := map[string]string{}
	for _, profile := range browserhistory.Profiles(home) {
		out[profile.Path] = profile.Label()
	}
	return out
}

// ReadWebCalendarVisits copies one Chromium History database (with its WAL,
// since the browser holds the file while it runs) into scratchDir and lists
// the web calendars visited since the given time. The copy is removed before
// it returns.
func ReadWebCalendarVisits(ctx context.Context, historyPath, scratchDir, browser string, since time.Time) ([]WebVisit, error) {
	var visits []WebVisit
	err := browserhistory.WithCopy(ctx, historyPath, scratchDir, func(db *sql.DB) error {
		var err error
		visits, err = readWebCalendarVisits(ctx, db, browser, since)
		return err
	})
	return visits, err
}

func readWebCalendarVisits(ctx context.Context, db *sql.DB, browser string, since time.Time) ([]WebVisit, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT url, visit_count, last_visit_time
		FROM urls
		WHERE last_visit_time >= ?
		  AND (url LIKE 'https://calendar.google.com/%'
		    OR url LIKE 'https://outlook.office.com/calendar%'
		    OR url LIKE 'https://outlook.office365.com/calendar%'
		    OR url LIKE 'https://outlook.live.com/calendar%'
		    OR url LIKE 'https://calendar.notion.so/%'
		    OR url LIKE 'https://app.fantastical.com/%'
		    OR url LIKE 'https://fantastical.app/%')
	`, ChromiumTime(since))
	if err != nil {
		return nil, fmt.Errorf("query history copy: %w", err)
	}
	defer rows.Close()
	byProvider := map[string]*WebVisit{}
	for rows.Next() {
		var rawURL string
		var visits int
		var lastVisit int64
		if err := rows.Scan(&rawURL, &visits, &lastVisit); err != nil {
			return nil, fmt.Errorf("read history row: %w", err)
		}
		provider, ok := ClassifyWebCalendarURL(rawURL)
		if !ok {
			continue
		}
		seen := FromChromiumTime(lastVisit)
		cur, exists := byProvider[provider]
		if !exists {
			byProvider[provider] = &WebVisit{Provider: provider, URL: rawURL, LastSeen: seen, Visits: visits, Browser: browser}
			continue
		}
		cur.Visits += visits
		if seen.After(cur.LastSeen) {
			cur.LastSeen = seen
			cur.URL = rawURL
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read history rows: %w", err)
	}
	out := make([]WebVisit, 0, len(byProvider))
	for _, v := range byProvider {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, nil
}

// ChromiumTime converts a time to Chromium's microseconds since 1601.
func ChromiumTime(t time.Time) int64 { return browserhistory.ChromiumTime(t) }

// FromChromiumTime converts Chromium's microseconds since 1601 to a time.
func FromChromiumTime(value int64) time.Time { return browserhistory.FromChromiumTime(value) }

// MergeWebVisits folds visits from several browsers into one source per
// provider, keeping the newest sighting and summing visit counts.
func MergeWebVisits(visits []WebVisit) []WebVisit {
	byProvider := map[string]*WebVisit{}
	for _, v := range visits {
		cur, ok := byProvider[v.Provider]
		if !ok {
			copyV := v
			byProvider[v.Provider] = &copyV
			continue
		}
		cur.Visits += v.Visits
		if v.LastSeen.After(cur.LastSeen) {
			cur.LastSeen = v.LastSeen
			cur.URL = v.URL
			cur.Browser = v.Browser
		}
	}
	out := make([]WebVisit, 0, len(byProvider))
	for _, v := range byProvider {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}
