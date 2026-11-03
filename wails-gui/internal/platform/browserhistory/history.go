// Package browserhistory reads the History databases of Chromium browsers on
// macOS: where each profile keeps one, how to read it while the browser holds
// it, the page visits it records, and a digest of them (the sites visited
// most, the searches and the pages read after each). Safari's History.db
// needs Full Disk Access and is not read. Private (incognito) windows never
// write to History, so their visits never reach it.
package browserhistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the "sqlite" driver for the History copy
)

// Browser is a Chromium browser whose History Isoform can read.
type Browser struct {
	// Name is the label shown and stored, such as "Chrome".
	Name string
	// BundleID lets the user's protected-apps list exclude a browser.
	BundleID string
	// Root is the data directory under ~/Library/Application Support.
	Root string
}

// Browsers lists the Chromium browsers read.
var Browsers = []Browser{
	{Name: "Chrome", BundleID: "com.google.Chrome", Root: "Google/Chrome"},
	{Name: "Chrome Beta", BundleID: "com.google.Chrome.beta", Root: "Google/Chrome Beta"},
	{Name: "Chrome Canary", BundleID: "com.google.Chrome.canary", Root: "Google/Chrome Canary"},
	{Name: "Chromium", BundleID: "org.chromium.Chromium", Root: "Chromium"},
	{Name: "Brave", BundleID: "com.brave.Browser", Root: "BraveSoftware/Brave-Browser"},
	{Name: "Edge", BundleID: "com.microsoft.edgemac", Root: "Microsoft Edge"},
	{Name: "Vivaldi", BundleID: "com.vivaldi.Vivaldi", Root: "Vivaldi"},
	{Name: "Arc", BundleID: "company.thebrowser.Browser", Root: "Arc/User Data"},
}

// Profile is one browser profile's History database.
type Profile struct {
	Browser Browser
	// Profile is the profile directory: "Default" or "Profile N".
	Profile string
	Path    string
}

// Label names the profile as "Chrome Default".
func (p Profile) Label() string { return p.Browser.Name + " " + p.Profile }

// Profiles lists the History databases that exist under home.
func Profiles(home string) []Profile {
	var out []Profile
	base := filepath.Join(home, "Library", "Application Support")
	for _, browser := range Browsers {
		dir := filepath.Join(base, filepath.FromSlash(browser.Root))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name != "Default" && !strings.HasPrefix(name, "Profile ") {
				continue
			}
			path := filepath.Join(dir, name, "History")
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				out = append(out, Profile{Browser: browser, Profile: name, Path: path})
			}
		}
	}
	return out
}

// ModTime is when the database last changed: the later of the file and its
// WAL, since a running browser writes new visits to the WAL first.
func ModTime(historyPath string) (time.Time, error) {
	info, err := os.Stat(historyPath)
	if err != nil {
		return time.Time{}, err
	}
	latest := info.ModTime()
	if wal, err := os.Stat(historyPath + "-wal"); err == nil && wal.ModTime().After(latest) {
		latest = wal.ModTime()
	}
	return latest, nil
}

// WithCopy copies a History database with its WAL (the browser holds the
// file while it runs) into a fresh directory under scratchDir, opens the
// copy read-only, runs fn, and removes the copy before it returns. An empty
// scratchDir uses the system temporary directory.
func WithCopy(ctx context.Context, historyPath, scratchDir string, fn func(*sql.DB) error) error {
	dir, err := os.MkdirTemp(scratchDir, "history-")
	if err != nil {
		return fmt.Errorf("history scratch dir: %w", err)
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "History")
	if err := copyFile(historyPath, copyPath); err != nil {
		return err
	}
	// The WAL may hold the newest visits; a missing WAL is normal.
	if err := copyFile(historyPath+"-wal", copyPath+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := sql.Open("sqlite", copyPath+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return fmt.Errorf("open history copy: %w", err)
	}
	defer db.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(db)
}

// copyFile clones src to dst where the disk allows it (no blocks written for
// a History file that can be hundreds of megabytes) and copies it otherwise.
func copyFile(src, dst string) error {
	if err := cloneFile(src, dst); err == nil {
		return nil
	} else if errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// chromiumEpochOffset is the number of microseconds between 1601-01-01 (the
// Chromium time origin) and 1970-01-01.
const chromiumEpochOffset = int64(11644473600) * 1_000_000

// ChromiumTime converts a time to Chromium's microseconds since 1601.
func ChromiumTime(t time.Time) int64 {
	return t.UnixMicro() + chromiumEpochOffset
}

// FromChromiumTime converts Chromium's microseconds since 1601 to a time.
func FromChromiumTime(value int64) time.Time {
	return time.UnixMicro(value - chromiumEpochOffset).UTC()
}

// Visit is one page visit as History records it.
type Visit struct {
	// ID is the visit's id in that History database.
	ID    int64
	URL   string
	Title string
	At    time.Time
}

// chromiumSubframe is the core transition type of a navigation inside a frame
// the page loaded by itself, not one the user made.
const chromiumSubframe = 3

// ReadVisits lists the visits after the given time, oldest first, at most
// limit of them. Navigations inside frames that the page made by itself are
// left out, and so are visits synced from the user's other devices (newer
// Chromium keeps those in History too, marked with their device): only what
// happened on this Mac is read.
func ReadVisits(ctx context.Context, db *sql.DB, after time.Time, limit int) ([]Visit, error) {
	foreign := ""
	synced, err := hasColumn(ctx, db, "visits", "originator_cache_guid")
	if err != nil {
		return nil, err
	}
	if synced {
		foreign = " AND COALESCE(v.originator_cache_guid, '') = ''"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT v.id, v.visit_time, u.url, COALESCE(u.title, '')
		FROM visits v JOIN urls u ON u.id = v.url
		WHERE v.visit_time > ? AND (v.transition & 255) != ?`+foreign+`
		ORDER BY v.visit_time ASC
		LIMIT ?
	`, ChromiumTime(after), chromiumSubframe, limit)
	if err != nil {
		return nil, fmt.Errorf("query history visits: %w", err)
	}
	defer rows.Close()
	var out []Visit
	for rows.Next() {
		var v Visit
		var at int64
		if err := rows.Scan(&v.ID, &at, &v.URL, &v.Title); err != nil {
			return nil, fmt.Errorf("read history visit: %w", err)
		}
		v.At = FromChromiumTime(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("history columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// BrowserNamed finds a browser by its Name.
func BrowserNamed(name string) (Browser, bool) {
	for _, b := range Browsers {
		if b.Name == name {
			return b, true
		}
	}
	return Browser{}, false
}
