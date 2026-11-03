package identity

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// trailingServerTagPattern matches Discord's "Server Tag: <CLAN>" clan
// badge that the AX cluster header concatenates after the username.
// Synthetic examples:
//
//   - "alex_halcyon Server Tag: HLC"
//   - "maya_chen Server Tag: X\"
//   - "Alice Server Tag: ABCD"
//
// The badge value is server-defined and can include letters, digits,
// punctuation, and even backslashes — so we match it as "one or more
// non-whitespace characters" anchored to the end. Anchored to EOL so
// usernames mid-string that happen to contain "Server Tag:" stay intact.
var trailingServerTagPattern = regexp.MustCompile(`\s+Server\s+Tag:\s*\S+\s*$`)

// trailingDatePattern matches a bare "YYYY/M/D" date suffix (optionally
// followed by a comma) that Discord renders next to the username when a
// message is from an earlier day and no time-of-day is shown. Examples:
//
//   - "alex_halcyon 2031/6/30,"
//   - "halcyon 2031/6/29"
//
// Anchored to the end so a username that contains digits mid-string is
// preserved.
var trailingDatePattern = regexp.MustCompile(`\s+\d{4}/\d{1,2}/\d{1,2},?\s*$`)

// trailingTimestampPattern matches common time-of-day suffixes that chat
// parsers sometimes leave attached to a sender name when the upstream AX
// tree concatenates everything in the cluster header. Covers:
//
//   - bare "10:25" / "8:01"
//   - "10:25 PM" / "8:01 am"
//   - "Today at 11:51 PM" / "Yesterday at 8:01 AM"
//   - 中文 "今天 10:25" / "昨天10:34" / "前天 08:12"
//   - absolute "2031/7/1 08:15"
//
// Anchored to the END of the string so it never eats a username that
// happens to contain digits in the middle ("user1234").
var trailingTimestampPattern = regexp.MustCompile(
	`(?i)\s+(?:(?:\d{4}/\d{1,2}/\d{1,2}|今天|昨天|前天|today|yesterday)\s*(?:at\s+)?)?\d{1,2}:\d{2}(?:\s*[ap]m)?\s*$`)

// trailingRoleBadges enumerates inline role labels chat platforms render
// next to the username (Discord OP / BOT / Server Owner, Slack APP, …).
// Stripped only when they appear as a suffix preceded by whitespace, so
// a legitimate user literally named "Bot" stays intact.
var trailingRoleBadges = []string{
	"OP Original Poster",
	"Original Poster",
	"Server Owner",
	"BOT",
	"Bot",
	"APP",
	"App",
}

// trailingMessageBleedPattern matches a message body fragment that bled
// into the sender field via a buggy parser — heuristically detected as
// "looks like a sentence ending in punctuation". Only triggers when the
// remaining prefix is still a plausible username (no spaces or short).
// Conservative: we'd rather leave a slightly noisy name than chop a
// legitimate "Mary Jane Doe" down to "Mary".
var trailingSentenceBleedPattern = regexp.MustCompile(`\s+[A-Z][^.!?]{4,80}[.!?]\s*$`)

// NormalizeSenderName cleans a raw chat sender string before it enters
// the identity layer. Two failures motivate it: Discord AX heading
// titles concatenating `<username> <body> <role> <timestamp>` into one
// node, and chat platforms that decorate the username with a trailing
// timestamp on same-day messages. Without normalization the resolver
// auto-buckets each (name + timestamp) tuple as a separate Anonymous
// principal, polluting the Who you know page.
//
// Strategy: strip trailing timestamps, then strip trailing role badges,
// then optionally strip a trailing message-body sentence. Repeats until
// stable so combinations like `Username Body. OP Original Poster 12:12`
// all collapse to `Username`. Returns the original input unchanged when
// normalization would empty the string — never silently destroys data.
func NormalizeSenderName(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return raw
	}
	for changed := true; changed; {
		changed = false

		if next := trailingServerTagPattern.ReplaceAllString(cleaned, ""); next != cleaned {
			next = strings.TrimSpace(next)
			if next != "" {
				cleaned = next
				changed = true
				continue
			}
		}

		if next := trailingTimestampPattern.ReplaceAllString(cleaned, ""); next != cleaned {
			next = strings.TrimSpace(next)
			if next != "" {
				cleaned = next
				changed = true
				continue
			}
		}

		if next := trailingDatePattern.ReplaceAllString(cleaned, ""); next != cleaned {
			next = strings.TrimSpace(next)
			if next != "" {
				cleaned = next
				changed = true
				continue
			}
		}

		for _, badge := range trailingRoleBadges {
			lowered := strings.ToLower(cleaned)
			suffix := " " + strings.ToLower(badge)
			if strings.HasSuffix(lowered, suffix) {
				next := strings.TrimSpace(cleaned[:len(cleaned)-len(badge)])
				if next != "" {
					cleaned = next
					changed = true
					break
				}
			}
		}
		if changed {
			continue
		}

		if next := trailingSentenceBleedPattern.ReplaceAllString(cleaned, ""); next != cleaned {
			next = strings.TrimSpace(next)
			if next != "" && !strings.ContainsAny(next, " \t") {
				// Only accept the sentence-bleed strip when the resulting
				// prefix is a single token — anything else is likely a
				// real multi-word display name we'd rather preserve.
				cleaned = next
				changed = true
				continue
			}
		}
	}

	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return raw
	}
	return cleaned
}

// proseBleedPattern detects sentence-shaped text bleeding through the
// middle of a string: a sentence mark followed by whitespace and more
// words. A real display name is a label, not prose, so this only ever
// matches when message body / a system notice leaked into the field.
var proseBleedPattern = regexp.MustCompile(`[.!?！？。][\s"'’)\]】」]*\S`)

// PlausibleAutoContactName reports whether name looks like a real chat
// sender's display name, as opposed to a chunk of message body or a
// system notice that bled into the sender field. It gates auto-creation
// of anonymous Contact buckets: a false result means "do not persist
// this as a person".
//
// It exists because upstream AX/OCR parsers occasionally capture the
// wrong node — for example, a Discord new-member welcome card whose
// heading title is "<user> Server Tag: X\ Sample welcome text!", which
// NormalizeSenderName (only strips TRAILING noise) could not salvage and
// which then became a permanent, un-mergeable Anonymous row on the Who
// you know page.
//
// Deliberately biased toward REJECTING: a missed auto-bucket costs at
// most one unattributed sighting, whereas a bogus principal pollutes the
// Who you know page indefinitely and resists cleanup. Single-token handles
// that happen to end in "!" (e.g. "Winner!") are intentionally allowed —
// the prose checks only fire on multi-word strings.
func PlausibleAutoContactName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	// Real usernames / display names are short. Discord caps both at 32;
	// other chat platforms are similar. Anything materially longer is
	// almost certainly captured message text.
	if utf8.RuneCountInString(name) > 48 {
		return false
	}
	// Line breaks never appear in a display name but do when a multi-line
	// message body is captured into the field.
	if strings.ContainsAny(name, "\n\r") {
		return false
	}
	// "Server Tag: <CLAN>" is a Discord badge rendered after the username.
	// When it survives normalization it sits mid-string with body text on
	// either side — the name is not trustworthy.
	if strings.Contains(name, "Server Tag:") {
		return false
	}
	// Prose detection only applies to multi-word strings so single-token
	// names ("x.y", "Winner!") are preserved.
	if strings.ContainsAny(name, " \t　") {
		if proseBleedPattern.MatchString(name) {
			return false
		}
		if r, _ := utf8.DecodeLastRuneInString(name); r == '!' || r == '?' || r == '！' || r == '？' {
			return false
		}
	}
	return true
}
