// Package browsers is the one list of web browsers Isoform knows, shared by
// call detection (meetingdetect), the activity monitor and its tab-address
// reader (activitymon) and the page parser (appparser), so a browser that
// counts as one in one place counts in all of them.
package browsers

import "strings"

// Family is how a browser's tabs can be read over AppleScript.
type Family int

const (
	// NotABrowser is any other app.
	NotABrowser Family = iota
	// Chromium browsers answer "URL of active tab of front window".
	Chromium
	// Safari answers "URL of front document".
	Safari
	// Firefox does not expose its tabs over AppleScript.
	Firefox
	// WindowsBrowser is a browser on Windows, by lowercase exe name.
	WindowsBrowser
)

// macBundles are the macOS browsers by bundle ID.
var macBundles = map[string]Family{
	"com.google.Chrome":                   Chromium,
	"com.google.Chrome.beta":              Chromium,
	"com.google.Chrome.dev":               Chromium,
	"com.google.Chrome.canary":            Chromium,
	"com.brave.Browser":                   Chromium,
	"com.brave.Browser.beta":              Chromium,
	"com.brave.Browser.nightly":           Chromium,
	"com.microsoft.edgemac":               Chromium,
	"com.microsoft.edgemac.Beta":          Chromium,
	"com.microsoft.edgemac.Dev":           Chromium,
	"com.microsoft.edgemac.Canary":        Chromium,
	"com.vivaldi.Vivaldi":                 Chromium,
	"com.operasoftware.Opera":             Chromium,
	"com.operasoftware.OperaGX":           Chromium,
	"company.thebrowser.Browser":          Chromium, // Arc
	"com.apple.Safari":                    Safari,
	"com.apple.SafariTechnologyPreview":   Safari,
	"org.mozilla.firefox":                 Firefox,
	"org.mozilla.firefoxdeveloperedition": Firefox,
	"org.mozilla.nightly":                 Firefox,
}

// windowsExes are the Windows browsers by lowercase exe name.
var windowsExes = map[string]bool{
	"chrome.exe":  true,
	"msedge.exe":  true,
	"firefox.exe": true,
	"brave.exe":   true,
	"opera.exe":   true,
	"vivaldi.exe": true,
}

// FamilyOf returns the browser family of an app, or NotABrowser.
func FamilyOf(id string) Family {
	if family, ok := macBundles[id]; ok {
		return family
	}
	if strings.HasPrefix(id, "company.thebrowser.Browser") {
		return Chromium // Arc's variants
	}
	if windowsExes[strings.ToLower(id)] {
		return WindowsBrowser
	}
	return NotABrowser
}

// IsBrowser reports whether the app (macOS bundle ID or Windows exe name)
// is a web browser.
func IsBrowser(id string) bool {
	return FamilyOf(id) != NotABrowser
}

// MacBundleIDs lists the macOS browsers' bundle IDs, for catalogs and tests.
func MacBundleIDs() []string {
	ids := make([]string, 0, len(macBundles))
	for id := range macBundles {
		ids = append(ids, id)
	}
	return ids
}

// WindowsExeNames lists the Windows browsers' exe names.
func WindowsExeNames() []string {
	names := make([]string, 0, len(windowsExes))
	for name := range windowsExes {
		names = append(names, name)
	}
	return names
}
