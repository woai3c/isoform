package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// migrateBrowserSiteDays adds browser_site_days: one row per local day, site
// and browser, rolled up from browser_visits by the routine detector
// (playbook_routine.go). Raw visits are kept three days; this small summary
// (first and last visit, how many, the most visited pages) is kept three
// weeks, long enough to see that a site is opened at the same time each day.
func migrateBrowserSiteDays(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS browser_site_days (
			day      TEXT    NOT NULL,
			host     TEXT    NOT NULL,
			browser  TEXT    NOT NULL,
			first_at INTEGER NOT NULL,
			last_at  INTEGER NOT NULL,
			visits   INTEGER NOT NULL,
			early    INTEGER NOT NULL DEFAULT 0,
			pages    TEXT    NOT NULL DEFAULT '[]',
			PRIMARY KEY (day, host, browser)
		);
		CREATE INDEX IF NOT EXISTS idx_browser_site_days_day ON browser_site_days(day);
	`)
	return err
}

// sitePagesKept bounds the pages one site-day keeps.
const sitePagesKept = 5

// siteEarlyWindow is how soon after a day's first visit a visit counts as
// part of that first sitting (BrowserSiteDayRow.Early).
const siteEarlyWindow = time.Hour

// BrowserSitePage is one page of a site on a day: the stored (scrubbed) link,
// its title and how often it was visited.
type BrowserSitePage struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

// BrowserSiteDayRow is one site on one local day in one browser.
type BrowserSiteDayRow struct {
	Day     string
	Host    string
	Browser string
	FirstAt int64
	LastAt  int64
	Visits  int
	// Early is how many visits came within siteEarlyWindow of the first: a
	// site checked in one sitting has most of its visits there, a tool used
	// all day has few.
	Early int
	Pages []BrowserSitePage
}

// visitHost returns the host of a stored link without a leading "www.", or ""
// for a link that is not on the web.
func visitHost(link string) string {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// rollupBrowserSiteDays groups visits into site-days in loc. Pages are ranked
// by visits, then by the first time they were seen.
func rollupBrowserSiteDays(visits []BrowserVisitRow, loc *time.Location) []BrowserSiteDayRow {
	if loc == nil {
		loc = time.Local
	}
	type pageAcc struct {
		page  BrowserSitePage
		first int64
	}
	type acc struct {
		row   BrowserSiteDayRow
		times []int64
		pages map[string]*pageAcc
	}
	groups := map[string]*acc{}
	var order []string
	for _, visit := range visits {
		host := visitHost(visit.URL)
		if host == "" {
			continue
		}
		day := time.UnixMilli(visit.VisitedAt).In(loc).Format("2006-01-02")
		key := day + "\x00" + host + "\x00" + visit.Browser
		g, ok := groups[key]
		if !ok {
			g = &acc{row: BrowserSiteDayRow{Day: day, Host: host, Browser: visit.Browser, FirstAt: visit.VisitedAt, LastAt: visit.VisitedAt}, pages: map[string]*pageAcc{}}
			groups[key] = g
			order = append(order, key)
		}
		g.row.Visits++
		g.times = append(g.times, visit.VisitedAt)
		if visit.VisitedAt < g.row.FirstAt {
			g.row.FirstAt = visit.VisitedAt
		}
		if visit.VisitedAt > g.row.LastAt {
			g.row.LastAt = visit.VisitedAt
		}
		page, ok := g.pages[visit.URL]
		if !ok {
			page = &pageAcc{page: BrowserSitePage{URL: visit.URL}, first: visit.VisitedAt}
			g.pages[visit.URL] = page
		}
		page.page.Count++
		if title := strings.TrimSpace(visit.Title); title != "" {
			page.page.Title = title
		}
	}
	out := make([]BrowserSiteDayRow, 0, len(order))
	for _, key := range order {
		g := groups[key]
		for _, at := range g.times {
			if at-g.row.FirstAt <= siteEarlyWindow.Milliseconds() {
				g.row.Early++
			}
		}
		pages := make([]*pageAcc, 0, len(g.pages))
		for _, page := range g.pages {
			pages = append(pages, page)
		}
		sort.SliceStable(pages, func(i, j int) bool {
			if pages[i].page.Count != pages[j].page.Count {
				return pages[i].page.Count > pages[j].page.Count
			}
			return pages[i].first < pages[j].first
		})
		for i, page := range pages {
			if i >= sitePagesKept {
				break
			}
			g.row.Pages = append(g.row.Pages, page.page)
		}
		out = append(out, g.row)
	}
	return out
}

// UpsertBrowserSiteDays stores site-days, replacing a stored day of the same
// site and browser.
func (s *ActivityStore) UpsertBrowserSiteDays(rows []BrowserSiteDayRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin site days: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO browser_site_days (day, host, browser, first_at, last_at, visits, early, pages)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, host, browser) DO UPDATE SET
			first_at = excluded.first_at, last_at = excluded.last_at,
			visits = excluded.visits, early = excluded.early, pages = excluded.pages`)
	if err != nil {
		return fmt.Errorf("prepare site days: %w", err)
	}
	defer stmt.Close()
	for _, row := range rows {
		pages := row.Pages
		if pages == nil {
			pages = []BrowserSitePage{}
		}
		raw, err := json.Marshal(pages)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(row.Day, row.Host, row.Browser, row.FirstAt, row.LastAt, row.Visits, row.Early, string(raw)); err != nil {
			return fmt.Errorf("upsert site day: %w", err)
		}
	}
	return tx.Commit()
}

// ListBrowserSiteDays returns the site-days on or after fromDay (YYYY-MM-DD),
// oldest first.
func (s *ActivityStore) ListBrowserSiteDays(fromDay string) ([]BrowserSiteDayRow, error) {
	rows, err := s.db.Query(`
		SELECT day, host, browser, first_at, last_at, visits, early, pages
		FROM browser_site_days WHERE day >= ? ORDER BY day ASC, first_at ASC`, fromDay)
	if err != nil {
		return nil, fmt.Errorf("list site days: %w", err)
	}
	defer rows.Close()
	var out []BrowserSiteDayRow
	for rows.Next() {
		var row BrowserSiteDayRow
		var pages string
		if err := rows.Scan(&row.Day, &row.Host, &row.Browser, &row.FirstAt, &row.LastAt, &row.Visits, &row.Early, &pages); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(pages), &row.Pages)
		out = append(out, row)
	}
	return out, rows.Err()
}

// PruneBrowserSiteDays deletes the site-days before beforeDay (YYYY-MM-DD).
func (s *ActivityStore) PruneBrowserSiteDays(beforeDay string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM browser_site_days WHERE day < ?`, beforeDay)
	if err != nil {
		return 0, fmt.Errorf("prune site days: %w", err)
	}
	return res.RowsAffected()
}

// DeleteBrowserSiteDaysOf deletes every site-day of one browser, for a
// browser the user protects.
func (s *ActivityStore) DeleteBrowserSiteDaysOf(browser string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM browser_site_days WHERE browser = ?`, browser)
	if err != nil {
		return 0, fmt.Errorf("delete site days of %s: %w", browser, err)
	}
	return res.RowsAffected()
}
