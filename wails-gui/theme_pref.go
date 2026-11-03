package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Theme preference is owned by the React side (useTheme.tsx). The Go
// side mirrors the latest value the React side has committed so that
// out-of-process consumers (the spotlight helper, currently) can ask
// the host bridge "what theme is the user on right now?" without
// reading another process's localStorage. We persist to disk so a
// fresh launch reflects the user's last choice before React mounts.
//
// Valid values: "paper" (light, default) and "onedark" (dark). Anything
// else falls back to "paper".

var (
	themePrefMu   sync.Mutex
	themePref     string
	themePrefFile = "theme.txt"
)

// themePreferencePath is the on-disk store for the user's theme. We
// keep it in the same per-user data directory the rest of the app
// uses so persistence survives reinstalls.
func themePreferencePath() string {
	dir := configDirOrFallback()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, themePrefFile)
}

// configDirOrFallback resolves ~/.yansu-agent (the dir the rest of
// the app already writes to). Returns empty on hard failure — caller
// should treat the persistence as best-effort and continue.
func configDirOrFallback() string {
	dataRoot, err := flavorDataDir()
	if err != nil || dataRoot == "" {
		return ""
	}
	return dataRoot
}

// loadThemePreferenceFromDisk reads the persisted value, returning
// "paper" if absent / unreadable / unrecognised. Called once at
// startup so the helper can be served the right theme before the
// React side has had a chance to call SetThemePreference.
func loadThemePreferenceFromDisk() {
	path := themePreferencePath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	v := strings.TrimSpace(string(b))
	if v != "paper" && v != "onedark" {
		return
	}
	themePrefMu.Lock()
	themePref = v
	themePrefMu.Unlock()
}

// GetThemePreference is the Wails binding the React side calls on
// mount to bootstrap useTheme's state from disk. Empty string means
// "no persisted preference" — useTheme falls back to localStorage /
// default when it gets that.
func (a *App) GetThemePreference() string {
	themePrefMu.Lock()
	defer themePrefMu.Unlock()
	return themePref
}

// SetThemePreference is the Wails binding the React side calls
// whenever the user picks a theme in settings. Stores in memory so
// the host bridge can serve it to the spotlight helper, and writes
// to disk so the value survives a relaunch.
func (a *App) SetThemePreference(name string) {
	v := strings.TrimSpace(name)
	if v != "paper" && v != "onedark" {
		return
	}
	themePrefMu.Lock()
	themePref = v
	themePrefMu.Unlock()

	path := themePreferencePath()
	if path == "" {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, []byte(v), 0o644)
}

// currentThemePreference is the internal accessor used by the host
// bridge handler. Returns "paper" when no preference is set so the
// spotlight always gets a valid value.
func currentThemePreference() string {
	themePrefMu.Lock()
	defer themePrefMu.Unlock()
	if themePref == "" {
		return "paper"
	}
	return themePref
}
