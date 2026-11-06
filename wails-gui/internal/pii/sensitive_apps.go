package pii

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// sensitiveAppBundleIDs maps known sensitive application bundle ID prefixes
// to their category. When the frontmost app matches, the entire screenshot
// should be masked rather than relying on per-field PII detection.
var sensitiveAppBundleIDs = map[string]string{
	// Password managers
	"com.1password":              "password_manager",
	"com.agilebits":              "password_manager",
	"com.bitwarden":              "password_manager",
	"com.lastpass":               "password_manager",
	"com.dashlane":               "password_manager",
	"org.keepassxc":              "password_manager",
	"com.enpass":                 "password_manager",
	"com.nordpass":               "password_manager",
	"com.roboform":               "password_manager",
	"de.marcels-it-blog.KeePass": "password_manager",

	// macOS Keychain & Passwords
	"com.apple.keychainaccess": "password_manager",
	"com.apple.Passwords":      "password_manager",
}

var sensitiveAppBundleIDsLower = func() map[string]string {
	lower := make(map[string]string, len(sensitiveAppBundleIDs))
	for prefix, category := range sensitiveAppBundleIDs {
		lower[strings.ToLower(prefix)] = category
	}
	return lower
}()

var defaultSensitiveAppNames = []string{
	"1password", "bitwarden", "lastpass", "dashlane", "keepassxc", "keepass",
	"enpass", "nordpass", "roboform", "keychain access", "passwords", "钥匙串访问", "密码",
}

var defaultSensitiveAppNamesLower = func() map[string]struct{} {
	names := make(map[string]struct{}, len(defaultSensitiveAppNames))
	for _, name := range defaultSensitiveAppNames {
		names[name] = struct{}{}
	}
	return names
}()

// userSensitiveApps holds user-configured sensitive app bundle prefixes,
// mapping bundle prefix -> label. Unlike the defaults map (whose values
// are category slugs like "password_manager"), the label here is the app's
// DISPLAY NAME as picked in Settings (settings-page.tsx passes the app
// name, e.g. {"com.apple.journal": "手记"}), and the persisted ui_state
// payload matches. Name-based matching (IsSensitiveAppName) relies on
// this. Guarded by userAppsMu.
var (
	userAppsMu            sync.RWMutex
	userAppsPublicationMu sync.RWMutex
	userSensitiveApps     = map[string]string{}
)

// SensitiveAppEntry represents a sensitive app in the combined list
// returned to the frontend.
type SensitiveAppEntry struct {
	BundlePrefix string `json:"bundlePrefix"`
	Category     string `json:"category"`
	IsDefault    bool   `json:"isDefault"`
	Icon         string `json:"icon"`
}

// IsCoveredByDefaults reports whether the given bundle prefix is already
// covered by the hardcoded default sensitive apps, i.e. a user entry with
// this prefix would be redundant. Matching is case-insensitive and
// prefix-based, mirroring IsSensitiveApp.
func IsCoveredByDefaults(bundlePrefix string) bool {
	lower := strings.ToLower(bundlePrefix)
	for prefix := range sensitiveAppBundleIDsLower {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// SetUserSensitiveApps replaces the entire user-configured sensitive apps map.
// Entries already covered by the hardcoded defaults are dropped so legacy
// configs (saved before those apps became defaults) don't produce duplicate
// list entries.
// userSensitiveAppsVersion advances on every change to the user's sensitive apps,
// so a reader can compare one number instead of the whole list.
var userSensitiveAppsVersion atomic.Uint64

// UserSensitiveAppsVersion returns the version of the user's sensitive apps.
func UserSensitiveAppsVersion() uint64 { return userSensitiveAppsVersion.Load() }

func SetUserSensitiveApps(apps map[string]string) {
	userAppsPublicationMu.Lock()
	defer userAppsPublicationMu.Unlock()
	userAppsMu.Lock()
	defer userAppsMu.Unlock()
	defer userSensitiveAppsVersion.Add(1)
	userSensitiveApps = make(map[string]string, len(apps))
	for k, v := range apps {
		if IsCoveredByDefaults(k) {
			continue
		}
		userSensitiveApps[k] = v
	}
}

// GetUserSensitiveApps returns a copy of the user-configured sensitive apps map.
func GetUserSensitiveApps() map[string]string {
	userAppsMu.RLock()
	defer userAppsMu.RUnlock()
	out := make(map[string]string, len(userSensitiveApps))
	for k, v := range userSensitiveApps {
		out[k] = v
	}
	return out
}

// SensitiveAppPolicySnapshot returns the bundle prefixes and display names
// used by the sensitive-app predicates, copied under one policy lock.
func SensitiveAppPolicySnapshot() (bundlePrefixes, names []string) {
	userAppsMu.RLock()
	defer userAppsMu.RUnlock()

	bundlePrefixes = make([]string, 0, len(sensitiveAppBundleIDs)+len(userSensitiveApps))
	for prefix := range sensitiveAppBundleIDs {
		bundlePrefixes = append(bundlePrefixes, prefix)
	}
	names = append(names, defaultSensitiveAppNames...)
	for prefix, label := range userSensitiveApps {
		bundlePrefixes = append(bundlePrefixes, prefix)
		names = append(names, label)
	}
	return bundlePrefixes, names
}

// AddUserSensitiveApp adds a single entry to the user-configured sensitive apps.
// Adding an app already covered by the hardcoded defaults is a no-op: it is
// always protected regardless, and storing it would duplicate the default
// entry in the settings list.
func AddUserSensitiveApp(bundlePrefix, category string) {
	userAppsPublicationMu.Lock()
	defer userAppsPublicationMu.Unlock()
	if IsCoveredByDefaults(bundlePrefix) {
		return
	}
	userAppsMu.Lock()
	defer userAppsMu.Unlock()
	userSensitiveApps[bundlePrefix] = category
	userSensitiveAppsVersion.Add(1)
}

// RemoveUserSensitiveApp removes a single entry from the user-configured sensitive apps.
func RemoveUserSensitiveApp(bundlePrefix string) {
	userAppsPublicationMu.Lock()
	defer userAppsPublicationMu.Unlock()
	userAppsMu.Lock()
	defer userAppsMu.Unlock()
	delete(userSensitiveApps, bundlePrefix)
	userSensitiveAppsVersion.Add(1)
}

// IsSensitiveApp checks whether the given bundle ID belongs to a known
// sensitive application. Returns the category (e.g. "password_manager")
// and true if matched, or empty string and false otherwise.
// Matching is prefix-based: "com.1password.desktop" matches "com.1password".
// Checks both the hardcoded default list and user-configured apps.
func IsSensitiveApp(bundleID string) (category string, sensitive bool) {
	if bundleID == "" {
		return "", false
	}
	lower := strings.ToLower(bundleID)

	// Check hardcoded defaults.
	for prefix, cat := range sensitiveAppBundleIDsLower {
		if strings.HasPrefix(lower, prefix) {
			return cat, true
		}
	}

	// Check user-configured apps.
	userAppsMu.RLock()
	defer userAppsMu.RUnlock()
	for prefix, cat := range userSensitiveApps {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return cat, true
		}
	}

	return "", false
}

// IsSensitiveAppName checks whether the given display name matches a default
// sensitive app or a user-configured app's stored label (see userSensitiveApps).
// Used by consumers that only have
// session-level app-name lists (no bundle IDs), e.g. activity-context
// prompts and suggestion inputs. Matching is case-insensitive.
func IsSensitiveAppName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	if _, ok := defaultSensitiveAppNamesLower[lower]; ok {
		return true
	}
	userAppsMu.RLock()
	defer userAppsMu.RUnlock()
	for _, label := range userSensitiveApps {
		if strings.ToLower(strings.TrimSpace(label)) == lower {
			return true
		}
	}
	return false
}

// FilterSensitiveAppNames returns names with user-protected display names
// removed. The single shared implementation for every consumer that holds
// name-only lists (session app_names, prompt inputs) — do not reimplement
// per call site.
func FilterSensitiveAppNames(names []string) []string {
	filtered := make([]string, 0, len(names))
	for _, n := range names {
		if IsSensitiveAppName(n) {
			continue
		}
		filtered = append(filtered, n)
	}
	return filtered
}

// AllBundlePrefixes returns all sensitive bundle ID prefixes (defaults + user).
// Used to scan visible windows for any sensitive app.
func AllBundlePrefixes() []string {
	userAppsMu.RLock()
	defer userAppsMu.RUnlock()

	prefixes := make([]string, 0, len(sensitiveAppBundleIDs)+len(userSensitiveApps))
	for p := range sensitiveAppBundleIDs {
		prefixes = append(prefixes, p)
	}
	for p := range userSensitiveApps {
		prefixes = append(prefixes, p)
	}
	return prefixes
}

// GetAllSensitiveApps returns a combined list of default and user-configured
// sensitive apps, sorted by bundle prefix. Each entry indicates whether it
// comes from the hardcoded defaults or was added by the user.
func GetAllSensitiveApps() []SensitiveAppEntry {
	userAppsMu.RLock()
	defer userAppsMu.RUnlock()

	entries := make([]SensitiveAppEntry, 0, len(sensitiveAppBundleIDs)+len(userSensitiveApps))

	for prefix, cat := range sensitiveAppBundleIDs {
		entries = append(entries, SensitiveAppEntry{
			BundlePrefix: prefix,
			Category:     cat,
			IsDefault:    true,
		})
	}

	for prefix, cat := range userSensitiveApps {
		entries = append(entries, SensitiveAppEntry{
			BundlePrefix: prefix,
			Category:     cat,
			IsDefault:    false,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].BundlePrefix < entries[j].BundlePrefix
	})

	return entries
}

// TryLockSensitiveAppsForPublication holds the policy stable through a short
// final read and publication. This separate barrier permits ordinary policy
// getters without recursive userAppsMu reads when a writer is waiting.
func TryLockSensitiveAppsForPublication() bool { return userAppsPublicationMu.TryRLock() }

func UnlockSensitiveAppsForPublication() { userAppsPublicationMu.RUnlock() }
