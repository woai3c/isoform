package identity

import (
	"strings"
	"unicode"
)

// appDisplayNames maps the lowercase app identifier (the form stored in
// app_handles.app and principal_sightings.app) to a human-readable
// display name that respects each app's brand casing. The list is
// intentionally narrow — only apps Yansu actually parses today — so a
// typo or unknown id falls through to titleCase(app) instead of
// silently rendering wrong.
var appDisplayNames = map[string]string{
	// Chat apps (the set IsChatApp recognises)
	"wechat":             "WeChat",
	"telegram":           "Telegram",
	"slack":              "Slack",
	"discord":            "Discord",
	"feishu":             "Feishu",
	"imessage":           "iMessage",
	"messages":           "Messages",
	"line":               "LINE",
	"qq":                 "QQ",
	"whatsapp":           "WhatsApp",
	"lark":               "Lark",
	"kakaotalk":          "KakaoTalk",
	"facebook_messenger": "Facebook Messenger",
	"messenger":          "Messenger",
	"signal":             "Signal",
	"teams":              "Microsoft Teams",
	"ringcentral":        "RingCentral",
	"zoom_chat":          "Zoom",

	// Common non-chat apps Yansu still resolves senders from
	"mail":    "Mail",
	"gmail":   "Gmail",
	"chatgpt": "ChatGPT",
	"github":  "GitHub",
	"linear":  "Linear",
	"notion":  "Notion",
	"sentry":  "Sentry",
}

// AppDisplayName returns a human-friendly label for an app identifier
// (e.g. "wechat" → "WeChat", "discord" → "Discord"). Unknown ids fall
// back to title-case so a new parser doesn't have to ship a UI change
// to look acceptable. Empty input returns "".
func AppDisplayName(app string) string {
	app = strings.TrimSpace(strings.ToLower(app))
	if app == "" {
		return ""
	}
	if v, ok := appDisplayNames[app]; ok {
		return v
	}
	return titleCase(app)
}

// titleCase upper-cases the first rune of each underscore- or
// dash-separated chunk and joins with a single space. Conservative
// fallback for unknown app ids.
func titleCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		rs := []rune(p)
		rs[0] = unicode.ToUpper(rs[0])
		parts[i] = string(rs)
	}
	return strings.Join(parts, " ")
}
