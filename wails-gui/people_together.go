package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/identity"
	"wails-gui/storage"
)

// A moment's title names the person as a participant when the name follows
// a word that makes them the other side (messaged, replied to, with, for, from,
// 和, 给, …) or precedes their own action (sent, shared, 发来, …). A name in
// passing ("platforms Mira mentioned", "about Mira's notes") is not.
var (
	personTogetherRoleBefore = regexp.MustCompile(`(?:^|[^\p{L}])(?:message|messaged|messaging|email|emailed|emailing|text|texted|call|called|calling|ping|pinged|dm|dmed|invite|invited|meet|met|thank|thanked|ask|asked|remind|reminded|tell|told|reply to|replied to|replying to|respond to|responded to|talk to|talked to|with|for|to|from)\s+$|(?:和|跟|与|同|给|为|帮|回复|问)\s*$`)
	personTogetherRoleAfter  = regexp.MustCompile(`^\s+(?:sent|shared|emailed|messaged|texted|called|replied|asked|wrote|invited|forwarded)(?:[^\p{L}]|$)|^\s*(?:发来|发了|分享|回复|邀请|给你|问)`)
	// Conference IDs ("790 818 477", "123-456-7890") never help a person.
	personTogetherMeetingID = regexp.MustCompile(`\s*\b\d{3}(?:[ -]?\d{3}){2}(?:[ -]?\d{1,3})?\b`)
)

// personTogetherNames are the lowercase names a moment's title may use for
// the person: each name, alias and a Latin full name's first word. One-rune
// names would match too much.
func personTogetherNames(p storage.PrincipalRow) []string {
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if len([]rune(name)) >= 2 && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, name := range append([]string{p.DisplayName}, p.Aliases...) {
		add(name)
		if fields := strings.Fields(name); len(fields) > 1 && !personTogetherCJK(name) && len([]rune(fields[0])) >= 3 {
			add(fields[0])
		}
	}
	return names
}

// personTogetherParticipant reports whether title names the person as the
// other side of a message, call or meeting, or as whom the work was for.
func personTogetherParticipant(title string, names []string) bool {
	lower := strings.ToLower(title)
	for _, name := range names {
		for from := 0; from < len(lower); {
			i := strings.Index(lower[from:], name)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(name)
			from = end
			if !personTogetherCJK(name) && (personTogetherLetterAt(lower[:start], true) || personTogetherLetterAt(lower[end:], false)) {
				continue
			}
			if personTogetherRoleBefore.MatchString(lower[:start]) || personTogetherRoleAfter.MatchString(lower[end:]) {
				return true
			}
		}
	}
	return false
}

func personTogetherLetterAt(s string, last bool) bool {
	var r rune
	if last {
		r, _ = utf8.DecodeLastRuneInString(s)
	} else {
		r, _ = utf8.DecodeRuneInString(s)
	}
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func personTogetherCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// personTogetherTitle is a moment's title as a Together line: past tense,
// without a conference ID, and with the app left to the line's "· App"
// ("Message Aiden in WeChat about…" → "Messaged Aiden about…").
func personTogetherTitle(title, app string) string {
	title = personTogetherMeetingID.ReplaceAllString(title, "")
	for _, name := range []string{app, identity.AppDisplayName(app), identity.AppDisplayNameIn(app, "zh-CN")} {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		quoted := regexp.QuoteMeta(name)
		title = regexp.MustCompile(`(?i)\s+(?:in|on|via|over|through)\s+`+quoted+`\b`).ReplaceAllString(title, "")
		title = regexp.MustCompile(`(?i)在\s*`+quoted+`\s*(?:上|里|中)?`).ReplaceAllString(title, "")
	}
	return timelineTitlePastTense(strings.Join(strings.Fields(title), " "))
}
