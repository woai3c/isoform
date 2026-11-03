package appparser

// UnsupportedApp describes an app we investigated for AX parsing and
// decided NOT to support. Surfacing this list in the DevTools panel
// pre-empts the recurring "why doesn't <common app> work?" question by
// recording what we tried and why it failed.
//
// Entries are declarative and English-only — same convention as
// ParserCatalogEntry.Notes. Add a new row whenever an AX investigation
// terminates in "no path forward via AX" so future readers don't have
// to redo the work.
type UnsupportedApp struct {
	// Label is the human-readable app name (e.g. "WeChat").
	Label string `json:"label"`
	// Bundles lists the macOS bundle IDs we checked. Stored as a slice
	// so apps with multiple distribution channels (Mac App Store vs.
	// direct download) can share a single row.
	Bundles []string `json:"bundles"`
	// Reason is a one-to-two sentence English explanation of what the
	// AX tree exposed (or failed to expose) when we probed.
	Reason string `json:"reason"`
	// ProbedAt is the date we last ran the AX probe and confirmed the
	// app was unparseable. Format: "YYYY-MM-DD" (ISO 8601 date-only).
	// Worth recording because vendor accessibility behavior can change
	// — what was unparseable today may become parseable after a
	// vendor update, and the date tells future readers how stale the
	// verdict is.
	ProbedAt string `json:"probedAt"`
}

// unsupportedApps is the seed list. Append to it as future
// investigations conclude. Keep entries alphabetical by Label.
var unsupportedApps = []UnsupportedApp{
	{
		Label:    "QQ (Windows)",
		Bundles:  []string{"qq.exe"},
		Reason:   "Tencent's QQ Windows client is built on Duilib and ships no IAccessible / UIA provider for its chat surfaces. The UIA tree contains only the OS-assigned window chrome plus the group/contact name from the title bar — messages, contact list, and search results are invisible. The tencent_chat parser flags this as AXCoverageMetadataOnly so OCR becomes the load-bearing text source.",
		ProbedAt: "2026-05-28",
	},
	{
		Label:    "TIM (Windows)",
		Bundles:  []string{"tim.exe"},
		Reason:   "Tencent TIM uses the same Duilib UI framework as QQ with no UIA provider. The activity capture sees only the chat group title via the foreground window title fallback. Tagged AXCoverageMetadataOnly so the activity popup defaults the OCR section open instead of the (effectively empty) UIA section.",
		ProbedAt: "2026-05-28",
	},
	{
		Label:    "WeChat",
		Bundles:  []string{"com.tencent.xinWeChat"},
		Reason:   "Tencent opts WeChat out of macOS Accessibility — the AX tree exposes only the window chrome (close / minimize / fullscreen buttons) and axText is empty. No chats, contacts, or messages are reachable.",
		ProbedAt: "2026-05-21",
	},
	{
		Label:    "WeChat / 微信 (Windows)",
		Bundles:  []string{"weixin.exe", "wechat.exe"},
		Reason:   "Same Duilib-based UI as QQ / TIM — Tencent ships no UIA provider for the chat content. The walker can reach only the window chrome and the title (typically the active contact / group name). OCR is the only viable text source on Windows.",
		ProbedAt: "2026-05-28",
	},
}

// UnsupportedAppsList returns the static list of apps we investigated
// but cannot parse via AX. Built fresh on every call so callers can
// safely retain the slice without mutating the source.
func UnsupportedAppsList() []UnsupportedApp {
	out := make([]UnsupportedApp, len(unsupportedApps))
	copy(out, unsupportedApps)
	return out
}
