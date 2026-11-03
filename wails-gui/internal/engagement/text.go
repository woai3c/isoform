package engagement

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text messages (notifications design section 7a): one line that starts with
// "Isoform:", at most TextLimit characters, rendered from CopySet.Text. A text
// may offer reply codes; a reply of a code, or of its number in the list (1
// for the first), answers the moment with that action, the way the phone's
// notification button does.

// TextLimit bounds one text: one segment in the GSM alphabet, three when the
// text needs Unicode, which is also the relay's cap for a Unicode text.
const TextLimit = 160

// textMinParam is the shortest a parameter is cut to while a text is fitted.
const textMinParam = 12

// TextReply is one reply code a text offers and the action id it answers.
type TextReply struct {
	Code   string
	Action string
}

// textReplies are the codes each kind's text offers, in the order the text
// names them. Every action is one of the kind's own. An action the phone gates
// behind the owner check (Authenticated, such as stopping blocked work) is
// never offered by text, because a text proves nothing about who sent it.
var textReplies = map[Kind][]TextReply{
	KindWorkBlocked:      {{Code: "A", Action: "answer"}, {Code: "L", Action: "later"}},
	KindSuggestion:       {{Code: "N", Action: "dismiss"}, {Code: "L", Action: "not_now"}},
	KindGoalCheckIn:      {{Code: "Z", Action: "snooze"}},
	KindPersonReappeared: {{Code: "X", Action: "stop_person"}},
	KindLearnedToConfirm: {{Code: "K", Action: "keep"}, {Code: "C", Action: "correct"}},
}

// TextReplies returns the codes a kind's text offers, in order.
func TextReplies(kind Kind) []TextReply {
	return append([]TextReply(nil), textReplies[kind]...)
}

// TextOffersReplies reports whether the sent body still carries the catalog's
// complete reply instructions. Generic lines and truncated instructions do not
// open a code question, even when the kind normally offers reply codes.
func TextOffersReplies(kind Kind, lang, body string) bool {
	replies := textReplies[kind]
	if len(replies) == 0 {
		return false
	}
	set, ok := Copy(kind, lang)
	if !ok {
		return false
	}
	// Reply instructions follow the last parameter in every coded template.
	instructions := oneLine(set.Text[strings.LastIndex(set.Text, "}")+1:])
	for _, reply := range replies {
		if !strings.Contains(instructions, " "+reply.Code+" ") {
			return false
		}
	}
	return strings.HasSuffix(body, instructions)
}

// MatchTextReply returns the action a reply answers for the kind: one of its
// codes in any case, or the code's number in the list, ignoring surrounding
// space and trailing punctuation. Anything else is not a reply to the kind.
func MatchTextReply(kind Kind, reply string) (string, bool) {
	word := strings.ToUpper(strings.TrimRight(strings.TrimSpace(reply), ".!"))
	replies := textReplies[kind]
	for _, r := range replies {
		if word == r.Code {
			return r.Action, true
		}
	}
	if n, err := strconv.Atoi(word); err == nil && n >= 1 && n <= len(replies) {
		return replies[n-1].Action, true
	}
	return "", false
}

// textHints is the one line sent when a short reply matches no code.
var textHints = map[string]string{
	"en":    "Isoform: reply with one of the codes in the last text, or open Isoform on your computer.",
	"zh-CN": "Isoform：请回复上一条短信中的代码，或在电脑上打开 Isoform。",
}

// TextHint returns the hint in a language, falling back to English.
func TextHint(lang string) string {
	return textHints[normalizeLang(lang)]
}

// textLinkPattern and textPhoneRunPattern are the relay's patterns for a link
// and a phone number (relay/internal/sms/text.go, HubBody); the relay refuses
// a text that carries either, because every text goes out under one shared
// carrier registration that promises neither. Keep the two copies the same.
var (
	textLinkPattern     = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|\bwww\.)[\x21-\x7e]*|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:[a-z]{2,24}/[\x21-\x7e]*|(?:` + textLinkTLDs + `)\b(?:/[\x21-\x7e]*)?)`)
	textPhoneRunPattern = regexp.MustCompile(`\+?\(?[0-9][0-9 ().+-]*[0-9]\)?`)
)

const (
	textLinkTLDs    = "com|net|org|edu|gov|io|co|me|ly|app|dev|link|info|biz|xyz|us|uk|ca|au|de|fr|ru|cn|top|site|online|shop|club|gl|to|cc|tk|ml|ga|cf|gq|live|click|page|gg|tv|ws|cloud|tech|store|icu|vip|work|pro|one|ai"
	textPhoneDigits = 7
)

// textSafeParam drops links and phone numbers (runs of seven digits or more)
// from a parameter. Titles, names and mail subjects come from outside the
// app, so a text must not carry a link or a number someone else chose.
func textSafeParam(value string) string {
	value = textLinkPattern.ReplaceAllString(value, " ")
	return textPhoneRunPattern.ReplaceAllStringFunc(value, func(run string) string {
		digits := 0
		for _, r := range run {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= textPhoneDigits {
			return " "
		}
		return run
	})
}

// TextMessage renders a kind's text in a language from the moment's
// parameters, as one line of at most TextLimit characters. Links and phone
// numbers are dropped from every parameter, and control characters in a
// parameter become spaces. When the text is too long the
// longest parameter is cut, again and again, so the reply codes at the end of
// the template survive; only a template that is too long by itself is cut at
// the end. It reports false for a kind without a text.
func TextMessage(kind Kind, lang string, params map[string]string) (string, bool) {
	set, ok := Copy(kind, lang, params)
	if !ok || strings.TrimSpace(set.Text) == "" {
		return "", false
	}
	clean := make(map[string]string, len(params))
	for key, value := range params {
		clean[key] = oneLine(textSafeParam(value))
	}
	for {
		text := oneLine(Render(set.Text, clean))
		over := utf8.RuneCountInString(text) - TextLimit
		if over <= 0 {
			return text, true
		}
		longest, size := "", 0
		for key, value := range clean {
			if n := utf8.RuneCountInString(value); n > size && strings.Contains(set.Text, "{"+key+"}") {
				longest, size = key, n
			}
		}
		// A cut parameter ends with "...", so one already near the floor
		// cannot get shorter; the text itself is cut instead.
		if size <= textMinParam+3 {
			return cutText(text, TextLimit), true
		}
		keep := size - over - 3
		if keep < textMinParam {
			keep = textMinParam
		}
		clean[longest] = strings.TrimRight(string([]rune(clean[longest])[:keep]), " ") + "..."
	}
}

// oneLine turns control characters into spaces and collapses runs of space.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// cutText shortens text to limit characters, ending with an ellipsis.
func cutText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimRight(string(runes[:limit-3]), " ") + "..."
}

// ContainsTextLink shares the delivery link policy with proactive validation.
// It intentionally does not apply the phone-run pattern to localized dates.
func ContainsTextLink(value string) bool {
	return textLinkPattern.MatchString(value)
}

// StripTextLinks removes links using the delivery link policy.
func StripTextLinks(value string) string {
	return textLinkPattern.ReplaceAllString(value, " ")
}
