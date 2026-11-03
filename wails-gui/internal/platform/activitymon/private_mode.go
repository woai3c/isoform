package activitymon

import "strings"

// Standalone mode titles and delimited mode markers indicate private browsing.
// Page prose such as "How to use Incognito - Google Chrome" does not.
var privateModePatterns = []string{
	"Private Browsing", // Safari, Firefox
	"Incognito",        // Chrome, Brave
	"InPrivate",        // Edge
	"无痕",               // Chrome and Safari in Chinese
	"无痕浏览",             // Chromium and Safari in Chinese
	"无痕模式",             // Chromium in Chinese
	"隐身模式",             // older Chrome in Chinese
}

// IsPrivateBrowsing checks whether the given window title indicates
// the browser is in a private/incognito mode.
func IsPrivateBrowsing(windowTitle string) bool {
	windowTitle = strings.TrimSpace(windowTitle)
	for _, pattern := range privateModePatterns {
		if windowTitle == pattern {
			return true
		}
		// Generic one-word markers can also be ordinary page names.
		if pattern != "Incognito" && pattern != "无痕" {
			if strings.HasPrefix(windowTitle, pattern+" - ") || strings.HasPrefix(windowTitle, pattern+" — ") {
				return true
			}
		}
		for _, suffix := range []string{" (" + pattern + ")", " [" + pattern + "]", " - " + pattern, " — " + pattern} {
			if strings.HasSuffix(windowTitle, suffix) {
				return true
			}
		}
		for _, browser := range []string{"Google Chrome", "Chromium", "Brave", "Microsoft Edge", "Mozilla Firefox", "Safari"} {
			if strings.HasSuffix(windowTitle, " - "+browser+" "+pattern) || strings.HasSuffix(windowTitle, " — "+browser+" "+pattern) {
				return true
			}
		}
	}
	return false
}
