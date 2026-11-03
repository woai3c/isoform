package identity

import "strings"

// chatApps lists the app identifiers that produce real chat senders —
// i.e. counterparties the user has an ongoing conversation with, where
// auto-bucketing a fresh Anonymous principal for each new sender pays
// off as "name this person later" affordance.
//
// Apps NOT in this list (Mail, Gmail, Sentry, GitHub PR comments, RSS
// feeds, Notion comments, ChatGPT, etc.) sometimes produce
// ConversationMessage entries with non-empty Sender fields that look
// like real chat — but those "senders" are usually services,
// newsletters, brand accounts, or one-off email addresses. Auto-
// bucketing them creates noise the user has to manually wade through
// to find the actual people they care about.
//
// The list is intentionally lowercase; callers normalize via IsChatApp.
var chatApps = map[string]struct{}{
	"wechat":             {},
	"telegram":           {},
	"slack":              {},
	"discord":            {},
	"feishu":             {},
	"imessage":           {},
	"messages":           {},
	"line":               {},
	"qq":                 {},
	"whatsapp":           {},
	"lark":               {},
	"kakaotalk":          {},
	"facebook_messenger": {},
	"messenger":          {},
	"signal":             {},
	"teams":              {},
	"zoom_chat":          {},
}

// IsChatApp reports whether the given app id is a chat app whose
// senders should be auto-bucketed into Anonymous principals on first
// sighting. Case- and whitespace-insensitive on the input. Returns
// false for empty input.
func IsChatApp(app string) bool {
	app = strings.TrimSpace(strings.ToLower(app))
	if app == "" {
		return false
	}
	_, ok := chatApps[app]
	return ok
}
