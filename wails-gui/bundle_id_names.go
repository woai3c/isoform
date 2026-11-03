package main

import "strings"

// bundleIDPrettyName mirrors the frontend's bundleIDToName() for common
// IDs so labels read "About Slack" rather than the raw reverse-DNS
// identifier (macOS) or the raw .exe filename (Windows).
//
// Two namespaces share this function:
//   - macOS: reverse-DNS bundle IDs like `com.apple.Safari`. The
//     trailing dot-component is the app name, so the fallback uses it.
//   - Windows: process .exe basenames like `Cursor.exe`. The .exe stem
//     is the app name, and trailing-component logic would yield "Exe"
//     for everything — strip the `.exe` first.
func bundleIDPrettyName(bundleID string) string {
	known := map[string]string{
		// macOS reverse-DNS bundle IDs.
		"com.tinyspeck.slackmacgap":  "Slack",
		"com.apple.Safari":           "Safari",
		"com.google.Chrome":          "Chrome",
		"com.brave.Browser":          "Brave",
		"com.microsoft.edgemac":      "Edge",
		"company.thebrowser.Browser": "Arc",
		"com.apple.MobileSMS":        "Messages",
		"com.apple.mail":             "Mail",
		"com.apple.Notes":            "Notes",
		"com.microsoft.Outlook":      "Outlook",
		"com.openai.chat":            "ChatGPT",
		"com.apple.Terminal":         "Terminal",
		"com.googlecode.iterm2":      "iTerm2",
		"com.mitchellh.ghostty":      "Ghostty",
		"dev.warp.Warp-Stable":       "Warp",
		"us.zoom.xos":                "Zoom",
		"com.microsoft.teams2":       "Teams",
		"com.hnc.Discord":            "Discord",
		"com.notion.id":              "Notion",
		"linear":                     "Linear",
		"com.linear.Linear":          "Linear",
		// Windows .exe basenames (matched case-insensitively below).
		"cursor.exe":          "Cursor",
		"code.exe":            "VS Code",
		"chrome.exe":          "Chrome",
		"msedge.exe":          "Edge",
		"firefox.exe":         "Firefox",
		"brave.exe":           "Brave",
		"arc.exe":             "Arc",
		"slack.exe":           "Slack",
		"discord.exe":         "Discord",
		"notion.exe":          "Notion",
		"figma.exe":           "Figma",
		"telegram.exe":        "Telegram",
		"wechat.exe":          "WeChat",
		"qq.exe":              "QQ",
		"feishu.exe":          "Feishu",
		"lark.exe":            "Lark",
		"explorer.exe":        "Explorer",
		"powershell.exe":      "PowerShell",
		"windowsterminal.exe": "Terminal",
		"obsidian.exe":        "Obsidian",
		"spotify.exe":         "Spotify",
	}
	if pretty, ok := known[bundleID]; ok {
		return pretty
	}
	if pretty, ok := known[strings.ToLower(bundleID)]; ok {
		return pretty
	}

	// Windows .exe — strip the suffix and capitalize the stem.
	if strings.HasSuffix(strings.ToLower(bundleID), ".exe") {
		stem := bundleID[:len(bundleID)-4]
		if stem == "" {
			return bundleID
		}
		if stem[0] >= 'a' && stem[0] <= 'z' {
			return string(stem[0]-32) + stem[1:]
		}
		return stem
	}

	// macOS reverse-DNS — take the trailing component.
	last := bundleID
	for i := len(bundleID) - 1; i >= 0; i-- {
		if bundleID[i] == '.' {
			last = bundleID[i+1:]
			break
		}
	}
	if last == "" {
		return bundleID
	}
	if last[0] >= 'a' && last[0] <= 'z' {
		return string(last[0]-32) + last[1:]
	}
	return last
}
