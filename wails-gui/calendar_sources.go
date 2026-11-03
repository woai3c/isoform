package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

// Which calendars the user actually uses is learned from two signals that
// need no permission: the Chromium browser history (copied with its WAL while
// the browser runs; Safari's history needs Full Disk Access and is skipped)
// and the URLs the activity monitor captured on screen. Both feed
// calendar_sources, beside the accounts EventKit reports.

// webCalendarHostFragments are the URL fragments the screen signal looks for.
var webCalendarHostFragments = []string{
	"calendar.google.com",
	"outlook.office.com/calendar",
	"outlook.office365.com/calendar",
	"outlook.live.com/calendar",
	"calendar.notion.so",
	"app.fantastical.com",
	"fantastical.app",
}

// webCalendarDetector finds web calendars in the browser history and on
// screen and records them as sources. The two readers are fields so tests
// can supply visits without a browser.
type webCalendarDetector struct {
	store   *storage.Store
	now     func() time.Time
	history func(ctx context.Context, since time.Time) []calendar.WebVisit
	screen  func(since time.Time) []calendar.WebVisit
}

func newWebCalendarDetector(a *App) *webCalendarDetector {
	return &webCalendarDetector{
		store:   a.store,
		now:     time.Now,
		history: readChromiumCalendarVisits,
		screen:  a.screenCalendarVisits,
	}
}

// detect merges the signals and upserts one source per provider. It returns
// how many web sources are known now.
func (d *webCalendarDetector) detect(ctx context.Context) (int, error) {
	if d == nil || d.store == nil {
		return 0, nil
	}
	now := d.now()
	since := now.Add(-calendar.PeekRecentUse)
	// The peek's own tab shows up in the history and on screen; those visits
	// are not the user using the calendar.
	windows := loadCalendarPeekWindows(ctx, d.store)
	var visits []calendar.WebVisit
	if d.history != nil {
		for _, v := range d.history(ctx, since) {
			if windows.contains(v.LastSeen) {
				continue
			}
			v.Browser = calendar.DetectedByHistory
			visits = append(visits, v)
		}
	}
	if d.screen != nil {
		for _, v := range d.screen(since) {
			if windows.contains(v.LastSeen) {
				continue
			}
			v.Browser = calendar.DetectedByScreen
			visits = append(visits, v)
		}
	}
	count := 0
	for _, v := range calendar.MergeWebVisits(visits) {
		if v.LastSeen.Before(since) {
			continue
		}
		row := storage.CalendarSourceRow{
			ID:          calendar.WebSourceID(v.Provider),
			Kind:        calendar.SourceKindWeb,
			Provider:    v.Provider,
			Title:       calendar.WebSourceHost(v.Provider),
			DetectedBy:  v.Browser,
			FirstSeenAt: v.LastSeen.UnixMilli(),
			LastSeenAt:  v.LastSeen.UnixMilli(),
		}
		if err := d.store.UpsertCalendarSource(ctx, row); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// calendarPeekWindowsKey holds the recent peek spans as JSON, so browser
// visits the peek itself made are never counted as the user using a calendar.
const (
	calendarPeekWindowsKey   = "calendar_peek_windows"
	calendarPeekWindowSlack  = 10 * time.Second
	calendarPeekWindowsKeep  = calendar.PeekRecentUse
	calendarPeekWindowsLimit = 400
)

type calendarPeekSpan struct {
	StartAt int64 `json:"startAt"`
	EndAt   int64 `json:"endAt"`
}

type calendarPeekSpans []calendarPeekSpan

// contains reports whether t falls inside a recorded peek, with slack for the
// browser's own timestamps.
func (w calendarPeekSpans) contains(t time.Time) bool {
	ms := t.UnixMilli()
	slack := calendarPeekWindowSlack.Milliseconds()
	for _, win := range w {
		if ms >= win.StartAt-slack && ms <= win.EndAt+slack {
			return true
		}
	}
	return false
}

func loadCalendarPeekWindows(ctx context.Context, store *storage.Store) calendarPeekSpans {
	if store == nil {
		return nil
	}
	raw, err := store.GetUIState(ctx, calendarPeekWindowsKey)
	if err != nil || raw == "" {
		return nil
	}
	var windows calendarPeekSpans
	if err := json.Unmarshal([]byte(raw), &windows); err != nil {
		return nil
	}
	return windows
}

// recordCalendarPeekWindow appends one peek span and drops spans older than
// the recent-use window.
func recordCalendarPeekWindow(ctx context.Context, store *storage.Store, start, end time.Time) error {
	if store == nil {
		return nil
	}
	if end.Before(start) {
		end = start
	}
	windows := loadCalendarPeekWindows(ctx, store)
	cutoff := end.Add(-calendarPeekWindowsKeep).UnixMilli()
	kept := make(calendarPeekSpans, 0, len(windows)+1)
	for _, win := range windows {
		if win.EndAt >= cutoff {
			kept = append(kept, win)
		}
	}
	kept = append(kept, calendarPeekSpan{StartAt: start.UnixMilli(), EndAt: end.UnixMilli()})
	if len(kept) > calendarPeekWindowsLimit {
		kept = kept[len(kept)-calendarPeekWindowsLimit:]
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return store.SetUIState(ctx, calendarPeekWindowsKey, string(raw))
}

// chromiumHistoryMinInterval is how often one History file is copied at
// most; between reads the last result is reused.
const chromiumHistoryMinInterval = time.Hour

type chromiumHistoryEntry struct {
	modTime time.Time
	readAt  time.Time
	visits  []calendar.WebVisit
}

var (
	chromiumHistoryMu    sync.Mutex
	chromiumHistoryCache = map[string]chromiumHistoryEntry{}
)

// readChromiumCalendarVisits reads every Chromium profile's History copy. A
// file is copied again only when it changed since the last read and at most
// once an hour; otherwise the previous visits are returned.
func readChromiumCalendarVisits(ctx context.Context, since time.Time) []calendar.WebVisit {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return readChromiumCalendarVisitsFrom(ctx, calendar.ChromiumHistoryFiles(home), since, time.Now())
}

func readChromiumCalendarVisitsFrom(ctx context.Context, files map[string]string, since, now time.Time) []calendar.WebVisit {
	var out []calendar.WebVisit
	for path, label := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		chromiumHistoryMu.Lock()
		entry, known := chromiumHistoryCache[path]
		chromiumHistoryMu.Unlock()
		if known && (entry.modTime.Equal(info.ModTime()) || now.Sub(entry.readAt) < chromiumHistoryMinInterval) {
			out = append(out, entry.visits...)
			continue
		}
		visits, err := calendar.ReadWebCalendarVisits(ctx, path, "", label, since)
		if err != nil {
			log.Printf("[calendar] browser history (%s) unavailable: %v", label, err)
			continue
		}
		chromiumHistoryMu.Lock()
		chromiumHistoryCache[path] = chromiumHistoryEntry{modTime: info.ModTime(), readAt: now, visits: visits}
		chromiumHistoryMu.Unlock()
		out = append(out, visits...)
	}
	return out
}

// screenCalendarVisits turns captured browser URLs into visits.
func (a *App) screenCalendarVisits(since time.Time) []calendar.WebVisit {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return nil
	}
	sightings, err := a.activity.store.ListRecentBrowserURLSightings(webCalendarHostFragments, since.UnixMilli(), 200)
	if err != nil {
		log.Printf("[calendar] screen sightings unavailable: %v", err)
		return nil
	}
	var out []calendar.WebVisit
	for _, s := range sightings {
		provider, ok := calendar.ClassifyWebCalendarURL(s.URL)
		if !ok {
			continue
		}
		out = append(out, calendar.WebVisit{Provider: provider, URL: s.URL, LastSeen: time.UnixMilli(s.Timestamp), Visits: 1})
	}
	return out
}

// detectWebCalendarSources runs the detector once and logs the outcome. It
// needs no permission, so the sync loop calls it whether or not calendar
// access is granted.
func (a *App) detectWebCalendarSources(ctx context.Context) {
	if a == nil || a.store == nil {
		return
	}
	n, err := newWebCalendarDetector(a).detect(ctx)
	if err != nil {
		log.Printf("[calendar] web calendar detection: %v", err)
		return
	}
	if n > 0 {
		log.Printf("[calendar] %d web calendar(s) in use", n)
	}
}
