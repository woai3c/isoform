//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// systemAppsCacheTTL bounds how long a scanned /Applications listing is reused.
// Scanning is expensive (PlistBuddy + sips fork per .app, ~300+ subprocess
// spawns on a typical Mac), so we cache the result and refresh in the
// background once the TTL elapses.
const systemAppsCacheTTL = 5 * time.Minute

var (
	systemAppsCacheMu sync.Mutex
	systemAppsCache   []SystemApp
	systemAppsCacheAt time.Time
	systemAppsSF      singleflight.Group

	// appleLanguagesRe extracts each quoted locale token from the output of
	// `defaults read -g AppleLanguages`. Compiled once at package init.
	appleLanguagesRe = regexp.MustCompile(`"([^"]+)"`)

	// bundleIconCache memoizes per-bundle-id icon resolutions (including
	// misses, stored as "") so repeated timeline renders never re-enter
	// LaunchServices/AppKit for the same app.
	bundleIconCacheMu sync.Mutex
	bundleIconCache   = make(map[string]string)
)

// mdfindTimeout caps the Spotlight query in discoverAppPaths so a corrupted or
// overloaded Spotlight index can never hang the scan.
const mdfindTimeout = 5 * time.Second

// scanSubprocLimit bounds the per-app fan-out (PlistBuddy + plutil + mdls + sips
// per goroutine). Without this, a typical Mac with hundreds of installed apps
// can transiently spawn 1000+ subprocesses concurrently and trip ulimit -u or
// the system-wide open-files limit. NumCPU*2 gives good throughput on modern
// hardware while staying well under any reasonable resource ceiling.
var scanSubprocLimit = runtime.NumCPU() * 2

// readAppBundleInfo extracts the bundle identifier and display names from a
// macOS .app bundle. Returns the bundle ID, the localized display name (per
// the user's AppleLanguages preferences, e.g. "微信" on a zh-Hans system), and
// the canonical English name from Info.plist (kept for search matching so
// users can find apps by either localized or English name).
func readAppBundleInfo(appPath string, locales []string) (bundleID, localizedName, englishName string, err error) {
	plistPath := filepath.Join(appPath, "Contents", "Info.plist")

	bidOut, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", plistPath).Output()
	if err != nil {
		return "", "", "", fmt.Errorf("read bundle identifier: %w", err)
	}
	bundleID = strings.TrimSpace(string(bidOut))

	// Canonical name from Info.plist (typically English): CFBundleDisplayName,
	// then CFBundleName, then the bundle directory name.
	nameOut, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleDisplayName", plistPath).Output()
	if err != nil {
		nameOut, err = exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleName", plistPath).Output()
	}
	if err != nil {
		englishName = strings.TrimSuffix(filepath.Base(appPath), ".app")
	} else {
		englishName = strings.TrimSpace(string(nameOut))
	}

	localizedName = readLocalizedAppName(appPath, locales)
	if localizedName == "" {
		localizedName = englishName
	}

	return bundleID, localizedName, englishName, nil
}

// readLocalizedAppName looks up the user-locale-specific app display name.
// First tries the bundle's .lproj/InfoPlist.strings (works for third-party
// apps like WeChat → 微信), then falls back to mdls kMDItemDisplayName which
// covers Apple system apps that don't ship InfoPlist.strings but are indexed
// by Spotlight (e.g. Calendar → 日历). Returns "" if neither yields a name.
func readLocalizedAppName(appPath string, locales []string) string {
	resources := filepath.Join(appPath, "Contents", "Resources")
	for _, loc := range locales {
		stringsPath := filepath.Join(resources, loc+".lproj", "InfoPlist.strings")
		if _, err := os.Stat(stringsPath); err != nil {
			continue
		}
		for _, key := range []string{"CFBundleDisplayName", "CFBundleName"} {
			out, err := exec.Command("plutil", "-extract", key, "raw", stringsPath).Output()
			if err != nil {
				continue
			}
			if v := strings.TrimSpace(string(out)); v != "" {
				return v
			}
		}
	}
	// Spotlight fallback: catches system apps whose localization lives in
	// strings catalogs not directly readable via plutil.
	if out, err := exec.Command("mdls", "-name", "kMDItemDisplayName", "-raw", appPath).Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" && v != "(null)" {
			return strings.TrimSuffix(v, ".app")
		}
	}
	return ""
}

// preferredLocales returns the user's preferred lproj directory names in
// priority order, expanded to cover base-language fallbacks and both dash and
// underscore forms (zh-Hans-CN → zh-Hans-CN, zh_Hans_CN, zh-Hans, zh_Hans, zh).
// Read once at scan time; the result is small and cached implicitly by being
// passed into each readAppBundleInfo call.
func preferredLocales() []string {
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return []string{"en"}
	}
	// Output is a plist array; extract each quoted token.
	matches := appleLanguagesRe.FindAllStringSubmatch(string(out), -1)

	seen := make(map[string]bool)
	var expanded []string
	addVariant := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		expanded = append(expanded, v)
	}
	for _, m := range matches {
		loc := m[1]
		// Also drop the script tag from 3-part locales so we generate
		// language-region pairs that match how some apps name their .lproj
		// dirs: zh-Hans-CN → zh-CN (Chrome uses zh_CN.lproj, not zh_Hans.lproj).
		if parts := strings.Split(loc, "-"); len(parts) >= 3 {
			noScript := parts[0] + "-" + parts[len(parts)-1]
			addVariant(noScript)
			addVariant(strings.ReplaceAll(noScript, "-", "_"))
		}
		// Walk from full locale down to base language, both dash + underscore.
		for {
			addVariant(loc)
			addVariant(strings.ReplaceAll(loc, "-", "_"))
			i := strings.LastIndex(loc, "-")
			if i < 0 {
				break
			}
			loc = loc[:i]
		}
	}
	// Always keep English as a final fallback.
	addVariant("en")
	return expanded
}

// SystemApp represents an application found in /Applications.
type SystemApp struct {
	bundlePath  string
	Name        string `json:"name"`                  // localized for display
	EnglishName string `json:"englishName,omitempty"` // canonical, for cross-locale search
	BundleID    string `json:"bundleId"`
	Icon        string `json:"icon"` // base64 data URL (PNG, 64x64)
}

// appIconsForBundleIDs resolves native app icons for the given bundle ids via
// LaunchServices, independent of the /Applications scan (and therefore of its
// multi-second first-run latency and install-root limits). Misses are cached
// as empty strings and omitted from the result, so the frontend can treat an
// absent key as a definitive "no native icon" and fall back gracefully.
func appIconsForBundleIDs(bundleIDs []string) map[string]string {
	result := make(map[string]string, len(bundleIDs))
	for _, bundleID := range bundleIDs {
		bundleID = strings.TrimSpace(bundleID)
		if bundleID == "" {
			continue
		}
		bundleIconCacheMu.Lock()
		icon, cached := bundleIconCache[bundleID]
		bundleIconCacheMu.Unlock()
		if !cached {
			icon = nativeAppIconForBundleID(bundleID)
			bundleIconCacheMu.Lock()
			bundleIconCache[bundleID] = icon
			bundleIconCacheMu.Unlock()
		}
		if icon != "" {
			result[bundleID] = icon
		}
	}
	return result
}

// listSystemApps returns the cached /Applications listing, scanning on the
// first call (or once the TTL has elapsed). The mutex protects only the cache
// read/write; the actual scan runs outside the lock under singleflight so
// concurrent callers share one scan instead of serializing behind a long-held
// mutex (a settings open during a background pre-warm previously had to wait
// for the pre-warm to finish before the frontend could render anything).
func listSystemApps() []SystemApp {
	systemAppsCacheMu.Lock()
	if systemAppsCache != nil && time.Since(systemAppsCacheAt) < systemAppsCacheTTL {
		cached := systemAppsCache
		systemAppsCacheMu.Unlock()
		return cached
	}
	systemAppsCacheMu.Unlock()

	v, _, _ := systemAppsSF.Do("scan", func() (any, error) {
		// Re-check inside the singleflight slot in case another goroutine
		// populated the cache while we were waiting to enter.
		systemAppsCacheMu.Lock()
		if systemAppsCache != nil && time.Since(systemAppsCacheAt) < systemAppsCacheTTL {
			cached := systemAppsCache
			systemAppsCacheMu.Unlock()
			return cached, nil
		}
		systemAppsCacheMu.Unlock()

		fresh := scanSystemApps()

		systemAppsCacheMu.Lock()
		systemAppsCache = fresh
		systemAppsCacheAt = time.Now()
		systemAppsCacheMu.Unlock()
		return fresh, nil
	})
	if v == nil {
		return nil
	}
	return v.([]SystemApp)
}

// discoverAppPaths returns absolute paths to every user-facing .app bundle
// macOS knows about. Combines a direct walk of the conventional install roots
// (reliable, no Spotlight dependency) with `mdfind` as a safety net for apps
// installed in non-standard locations.
//
// Helper apps nested inside another bundle (e.g. Chrome Helper.app under
// Chrome.app/Contents/Frameworks/...) are filtered out so the picker shows only
// user-facing entries.
func discoverAppPaths() []string {
	// User-facing install roots. Anything outside these is filtered out so
	// mdfind doesn't flood the picker with /System/Library/CoreServices
	// internals, input-method bundles, framework helpers, etc.
	roots := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Applications"))
	}

	isUnderRoot := func(p string) bool {
		for _, r := range roots {
			if strings.HasPrefix(p, r+"/") {
				return true
			}
		}
		return false
	}

	seen := make(map[string]bool)
	var paths []string

	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || !strings.HasSuffix(p, ".app") {
			return
		}
		// Skip helper apps nested inside another .app bundle.
		if strings.Contains(p, ".app/") {
			return
		}
		// Skip anything under a hidden directory (e.g. /Applications/.yansu-update-*/Yansu.app).
		// All discovered paths are absolute and end in ".app", so any "/."
		// substring necessarily means a hidden directory segment in the middle.
		if strings.Contains(p, "/.") {
			return
		}
		// Restrict to user-facing install roots.
		if !isUnderRoot(p) {
			return
		}
		if seen[p] {
			return
		}
		// mdfind may return stale entries; verify the bundle is still on disk.
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}

	// Direct directory walk of the conventional install roots. Hidden entries
	// (names starting with ".") are skipped — they are conventionally staging
	// or cache directories, not user-facing apps. The Yansu auto-updater for
	// instance leaves `.yansu-update-*/Yansu.app` bundles behind that would
	// otherwise show up as ghost duplicates of the installed app.
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(root, name)
			if strings.HasSuffix(name, ".app") {
				add(full)
				continue
			}
			// One level deep: Utilities, Setapp, Microsoft Office, Adobe products, etc.
			subs, err := os.ReadDir(full)
			if err != nil {
				continue
			}
			for _, s := range subs {
				if !s.IsDir() || strings.HasPrefix(s.Name(), ".") {
					continue
				}
				if strings.HasSuffix(s.Name(), ".app") {
					add(filepath.Join(full, s.Name()))
				}
			}
		}
	}

	// Spotlight: catches apps two-or-more levels deep, manual installs to
	// non-standard paths, and per-user MAS container redirects. The seen map
	// dedupes against the directory walk above. Capped with a timeout so a
	// degraded Spotlight index can't hang the scan indefinitely.
	mdCtx, mdCancel := context.WithTimeout(context.Background(), mdfindTimeout)
	defer mdCancel()
	if out, err := exec.CommandContext(mdCtx, "mdfind", "kMDItemContentType == 'com.apple.application-bundle'").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			add(line)
		}
	}

	return paths
}

// scanSystemApps performs the underlying /Applications walk. Each .app bundle
// is read in parallel via PlistBuddy + sips, which is the expensive part.
func scanSystemApps() []SystemApp {
	appPaths := discoverAppPaths()
	if len(appPaths) == 0 {
		return nil
	}

	type result struct {
		app SystemApp
		ok  bool
	}

	results := make([]result, len(appPaths))
	var wg sync.WaitGroup
	locales := preferredLocales()

	// Bounded semaphore so each app's ~4 subprocess fan-out (PlistBuddy, plutil,
	// mdls, sips via NSWorkspace fallback) doesn't push the process past its
	// ulimit -u / open-files ceiling on Macs with many installed apps.
	sem := make(chan struct{}, scanSubprocLimit)

	for i, appPath := range appPaths {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			bid, localizedName, englishName, err := readAppBundleInfo(path, locales)
			if err != nil || bid == "" {
				return
			}
			icon := getAppIconBase64(path)
			app := SystemApp{Name: localizedName, BundleID: bid, Icon: icon, bundlePath: path}
			if englishName != "" && englishName != localizedName {
				app.EnglishName = englishName
			}
			results[idx] = result{app: app, ok: true}
		}(i, appPath)
	}
	wg.Wait()

	// Dedup by bundle ID — the same bundle can appear at multiple paths
	// (auto-updater staging copies, portable installs, etc.) but the picker
	// only needs to show it once since the prefix-match in pii.IsSensitiveApp
	// protects every instance.
	seenBID := make(map[string]bool)
	var apps []SystemApp
	for _, r := range results {
		if !r.ok || seenBID[r.app.BundleID] {
			continue
		}
		seenBID[r.app.BundleID] = true
		apps = append(apps, r.app)
	}
	return apps
}

// getAppIconBase64 returns the app icon as a 64x64 base64 PNG data URL.
// Prefers NSWorkspace (handles every icon format including Assets.car asset
// catalogs that modern apps like Reeder ship), and falls back to the legacy
// PlistBuddy + sips path against CFBundleIconFile if NSWorkspace yields nothing
// (e.g. a degraded launch context).
func getAppIconBase64(appPath string) string {
	if icon := nativeAppIconBase64(appPath); icon != "" {
		return icon
	}
	plistPath := filepath.Join(appPath, "Contents", "Info.plist")

	// Read icon file name from Info.plist.
	iconOut, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIconFile", plistPath).Output()
	if err != nil {
		return ""
	}
	iconName := strings.TrimSpace(string(iconOut))
	if iconName == "" {
		return ""
	}
	// Append .icns if no extension.
	if !strings.Contains(iconName, ".") {
		iconName += ".icns"
	}

	iconPath := filepath.Join(appPath, "Contents", "Resources", iconName)
	if _, err := os.Stat(iconPath); err != nil {
		return ""
	}

	// Convert to 64x64 PNG using sips (fast, always available on macOS).
	tmp, err := os.CreateTemp("", "appicon-*.png")
	if err != nil {
		return ""
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("sips", "-s", "format", "png", "-z", "64", "64", iconPath, "--out", tmpPath)
	if err := cmd.Run(); err != nil {
		return ""
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		return ""
	}

	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}
