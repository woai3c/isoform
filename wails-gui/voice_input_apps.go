package main

import (
	"sort"
	"sync"
)

// voiceInputAppBundleIDs lists apps whose primary purpose is converting the
// user's voice into text for some other destination (chat box, email, editor).
// Speech captured while one of these is frontmost is the user dictating
// elsewhere, not addressing Yansu — recording it would be redundant and
// privacy-leaky.
//
// Maintained as a denylist (not an allowlist of "real meeting apps") because
// Yansu's background listening is ambient-by-design — see
// background_audio.go's injectSpeechEvent for the broader pipeline. An
// allowlist would silently drop legitimate non-meeting speech (in-person
// conversations, dictating notes while coding, etc.).
//
// Map keys are platform-specific identifiers, both forms in the same map
// because the lookup key (ctx.App.BundleID) is set by the platform monitor:
//
//   - macOS: real bundle ID, e.g. "com.goodsnooze.MacWhisper".
//     Verify with: osascript -e 'id of app "<appname>"'
//   - Windows: lowercase exe basename, e.g. "wisprflow.exe".
//     Verify with Task Manager → Details column, or Get-Process in PowerShell.
//
// Wrong identifiers are not a correctness risk — they simply fail to match
// and the entry has no effect.
var voiceInputAppBundleIDs = map[string]string{
	// macOS
	"com.goodsnooze.MacWhisper": "MacWhisper",
	"com.withaqua.Aqua":         "Aqua Voice", // TODO-verify
	"app.typeless.Typeless":     "Typeless",   // TODO-verify
	"com.wispr.flow":            "Wispr Flow", // TODO-verify
	"com.superwhisper.app":      "Superwhisper",
	"com.prajwalmote.VoiceInk":  "VoiceInk", // TODO-verify
	// Windows (lowercase exe basename — see monitor_windows.go FrontmostAppInfo)
	"wisprflow.exe": "Wispr Flow", // TODO-verify
	"whispr.exe":    "Whispr",     // TODO-verify
	"voiceink.exe":  "VoiceInk",   // TODO-verify
}

// userVoiceInputApps holds user-configured voice-input bundle IDs.
// Guarded by userVoiceInputAppsMu. Populated at startup from the UI state
// store (see voice_input_apps_bindings.go) and mutated through the
// AddUserVoiceInputApp / RemoveUserVoiceInputApp helpers.
//
// Map key is the bundle ID; value is the user-visible name we saw at the
// time of adding (kept so the settings UI can render the entry even if
// the app is not currently running and ListSystemApps can't enrich it).
var (
	userVoiceInputAppsMu            sync.RWMutex
	userVoiceInputAppsPublicationMu sync.RWMutex
	userVoiceInputApps              = map[string]string{}
)

// VoiceInputAppEntry is the combined entry returned to the settings UI and
// to the merged-list lookup helpers. IsDefault distinguishes built-in
// entries (which the user cannot remove) from user-added ones.
type VoiceInputAppEntry struct {
	BundleID  string `json:"bundleId"`
	AppName   string `json:"appName"`
	IsDefault bool   `json:"isDefault"`
}

// isVoiceInputApp returns true when the given bundle ID is in either the
// built-in voice-input denylist or the user-configured list. Empty bundle
// IDs return false — the stub FrontmostAppInfo on non-darwin platforms
// returns an empty AppInfo, so this check becomes a no-op outside macOS.
func isVoiceInputApp(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	if _, ok := voiceInputAppBundleIDs[bundleID]; ok {
		return true
	}
	userVoiceInputAppsMu.RLock()
	defer userVoiceInputAppsMu.RUnlock()
	_, ok := userVoiceInputApps[bundleID]
	return ok
}

// userListedAppMatches reports whether the user put the app (or the app a
// helper process belongs to) under "Apps Isoform does not listen to".
// Outside calls nothing said while it is in front becomes text, and its own
// calls are never recorded. Built-in voice apps the user did not list are
// heard and labeled "Dictated to <app>".
func userListedAppMatches(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	userVoiceInputAppsMu.RLock()
	defer userVoiceInputAppsMu.RUnlock()
	for listed := range userVoiceInputApps {
		if bundlesMatch(bundleID, listed) {
			return true
		}
	}
	return false
}

// AllVoiceInputAppEntries returns the combined built-in + user list, sorted
// by app name (case-insensitive) for stable rendering in the settings UI.
// Built-in entries are flagged IsDefault=true so the UI can hide the
// remove button on them.
func AllVoiceInputAppEntries() []VoiceInputAppEntry {
	userVoiceInputAppsMu.RLock()
	defer userVoiceInputAppsMu.RUnlock()

	entries := make([]VoiceInputAppEntry, 0, len(voiceInputAppBundleIDs)+len(userVoiceInputApps))
	for bundleID, name := range voiceInputAppBundleIDs {
		entries = append(entries, VoiceInputAppEntry{
			BundleID:  bundleID,
			AppName:   name,
			IsDefault: true,
		})
	}
	for bundleID, name := range userVoiceInputApps {
		// Defensive: skip user entries that duplicate a built-in. Should
		// not happen because AddUserVoiceInputApp checks, but keeps the
		// list consistent if the persisted state somehow drifted.
		if _, isBuiltIn := voiceInputAppBundleIDs[bundleID]; isBuiltIn {
			continue
		}
		entries = append(entries, VoiceInputAppEntry{
			BundleID:  bundleID,
			AppName:   name,
			IsDefault: false,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AppName < entries[j].AppName
	})
	return entries
}

// GetUserVoiceInputApps returns a copy of the user-configured map. Used by
// the bindings layer when serializing for persistence.
func GetUserVoiceInputApps() map[string]string {
	userVoiceInputAppsMu.RLock()
	defer userVoiceInputAppsMu.RUnlock()
	out := make(map[string]string, len(userVoiceInputApps))
	for k, v := range userVoiceInputApps {
		out[k] = v
	}
	return out
}

// SetUserVoiceInputApps replaces the entire user-configured map. Called
// from loadUserVoiceInputApps during startup.
func SetUserVoiceInputApps(apps map[string]string) {
	userVoiceInputAppsPublicationMu.Lock()
	defer userVoiceInputAppsPublicationMu.Unlock()
	userVoiceInputAppsMu.Lock()
	defer userVoiceInputAppsMu.Unlock()
	userVoiceInputApps = make(map[string]string, len(apps))
	for k, v := range apps {
		if k == "" {
			continue
		}
		userVoiceInputApps[k] = v
	}
}

// AddUserVoiceInputApp adds a single bundle ID to the user list. No-op if
// the bundle ID is already in the built-in list (which would shadow it
// anyway).
func AddUserVoiceInputApp(bundleID, appName string) {
	userVoiceInputAppsPublicationMu.Lock()
	defer userVoiceInputAppsPublicationMu.Unlock()
	if bundleID == "" {
		return
	}
	if _, isBuiltIn := voiceInputAppBundleIDs[bundleID]; isBuiltIn {
		return
	}
	userVoiceInputAppsMu.Lock()
	defer userVoiceInputAppsMu.Unlock()
	userVoiceInputApps[bundleID] = appName
}

// RemoveUserVoiceInputApp removes a single bundle ID from the user list.
// No-op if the bundle ID is not present or is a built-in (built-ins
// cannot be removed by the user).
func RemoveUserVoiceInputApp(bundleID string) {
	userVoiceInputAppsPublicationMu.Lock()
	defer userVoiceInputAppsPublicationMu.Unlock()
	if bundleID == "" {
		return
	}
	userVoiceInputAppsMu.Lock()
	defer userVoiceInputAppsMu.Unlock()
	delete(userVoiceInputApps, bundleID)
}
