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

// chineseAppNames are the apps with their own Chinese name (design review
// N44). Every other app keeps its brand name in Chinese text too.
var chineseAppNames = map[string]string{
	"wechat": "微信",
	"weixin": "微信",
	"feishu": "飞书",
}

// AppDisplayNameIn is AppDisplayName in the UI language: zh-CN uses an app's
// Chinese name where it has one (WeChat is 微信). It also takes a display
// name ("WeChat"), so a label already written in English can be localized.
func AppDisplayNameIn(app, lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
		if v, ok := ChineseAppName(app); ok {
			return v
		}
	}
	return AppDisplayName(app)
}

// ChineseAppName is the app's own Chinese name, when it has one.
func ChineseAppName(app string) (string, bool) {
	v, ok := chineseAppNames[strings.TrimSpace(strings.ToLower(app))]
	return v, ok
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
