//go:build windows

package activitymon

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// cleanFlatText post-processes the raw newline-joined text the UIA
// subprocess emits, removing the three duplication patterns Chromium's
// UIA bridge produces that the PowerShell window-N dedup can't catch.
//
//  1. Unicode normalization. Cursor / VSCode (and other Electron apps
//     using icon fonts) suffix their UIA Name with a Private Use Area
//     glyph for the row icon, so the file-tree row "notes" appears once
//     as "notes" and once as "notes". Strip PUA, variation
//     selectors, and zero-width characters before comparing.
//
//  2. Intra-line "X X" / "X\nX" duplication. Chromium sometimes returns
//     the same accessible name concatenated to itself in a single Name
//     string, e.g. "Source Control - 1 pending changes Source Control -
//     1 pending changes". A per-line scanner detects "two equal halves
//     with optional space/punct between" and keeps one half.
//
//  3. Far-apart line-level duplicates. Slack / Notion / Discord expose
//     a chat message as multiple sibling nodes (timestamp container,
//     body Link, profile Link, hover overlay, ...), each carrying the
//     SAME message body as their Name. Across a long message list the
//     duplicates can be 10-30 entries apart — past the PS subprocess's
//     window-8 budget. We track the last 32 emitted lines and drop any
//     exact match.
//
// The function is pure: same input -> same output. Cheap (a few string
// allocations per snapshot) and idempotent. Returns the original text
// unchanged when none of the patterns trigger.
// windowEntry tracks one line kept in the cleanFlatText output. It
// owns its own dedup key (`primary`) plus zero or more "subsumed
// fragment" keys (`extras`) — pieces of accessible-name structure
// that Chromium exposes again as standalone sibling Names in the
// same message subtree.
type windowEntry struct {
	primary string
	extras  []string
}

func cleanFlatText(raw string) string {
	if raw == "" {
		return raw
	}

	const recentWindow = 32

	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	entries := make([]windowEntry, 0, recentWindow)
	// keyToCount lets multiple entries register the same key (e.g.
	// two "Priya  H:MM PM" headers in a row both want "Priya" as a
	// subsumed fragment). The fragment stays known-duplicate until the
	// LAST holder is evicted.
	keyToCount := make(map[string]int, recentWindow*2)

	known := func(k string) bool {
		_, ok := keyToCount[k]
		return ok
	}
	add := func(k string) {
		keyToCount[k]++
	}
	remove := func(k string) {
		keyToCount[k]--
		if keyToCount[k] <= 0 {
			delete(keyToCount, k)
		}
	}

	for _, line := range lines {
		// Pass 1: strip decorative Unicode + collapse intra-line dups.
		cleaned := normalizeUIAFragment(line)
		cleaned = stripIntraLineDup(cleaned)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}

		// Pass 2: produce the primary dedup key. The KEY is what we
		// compare; the OUTPUT preserves the cleaned (un-normalized)
		// line so timestamp / reaction context isn't lost.
		primary := dedupKey(cleaned)
		if known(primary) {
			continue
		}
		// Also drop if this line matches a *fragment* an earlier
		// compound line already subsumed (sender / body extracted
		// from "Name  TIME" or "Name: body. TIME.").
		if cleaned != primary && known(cleaned) {
			continue
		}

		out = append(out, cleaned)
		entry := windowEntry{
			primary: primary,
			extras:  extractSubsumedFragments(cleaned),
		}
		add(entry.primary)
		for _, k := range entry.extras {
			add(k)
		}
		entries = append(entries, entry)
		if len(entries) > recentWindow {
			evicted := entries[0]
			entries = entries[1:]
			remove(evicted.primary)
			for _, k := range evicted.extras {
				remove(k)
			}
		}
	}
	// Final pass: drop flat-form long lines whose whitespace-stripped
	// content reappears as the concatenation of subsequent formatted
	// child lines. Chromium UIA exposes long bodies (LLM responses,
	// multi-paragraph posts) BOTH as a single super-long Name with no
	// line breaks AND as separate child Names with proper layout. The
	// flat version slips past per-line dedup because no individual
	// formatted line equals it.
	return strings.Join(collapseFlattenedDups(out), "\n")
}

// extractSubsumedFragments returns the sub-pieces of a line that
// Chromium is known to expose again as standalone sibling Names within
// the same message subtree. Empty result when the line doesn't match a
// known compound shape (the common case — we deliberately only fire on
// strong Slack-style patterns to keep false-positive risk tiny).
//
// Recognized shapes (both Slack-specific but observed in flat-text
// output from every Chromium chat client we've seen):
//
//   - "Name  HH:MM" (or HH:MM AM/PM) — the standard message header.
//     The same "Name" appears alone as the author profile link a few
//     siblings later.
//
//   - "Name: body. HH:MM PM." — the compact follow-up form Slack uses
//     when the same author posts within the same minute. The same
//     "Name" and "body" appear independently as sibling Names too.
func extractSubsumedFragments(line string) []string {
	var out []string
	if sender := matchHeaderName(line); sender != "" {
		out = append(out, sender)
	}
	if sender, body := matchCompoundMessage(line); sender != "" {
		out = append(out, sender)
		if body != "" {
			out = append(out, body)
		}
	}
	// URL lines are never compound — Slack message-permalink URLs end in
	// `.png`/`.jpg` which would falsely trip the filename matcher.
	if !looksLikeURL(line) {
		// Slack message body + attachment filename concatenated as one
		// accessible name. The filename and (when non-trivial) the body
		// also appear as separate sibling Names.
		if body, filename := splitTrailingFilename(line); body != "" {
			out = append(out, body)
			out = append(out, filename)
		}
		// "image.png 4 repliesLast reply 2 days agoView thread"
		// — Slack threaded attachment summary. The reply count, last-
		// reply tail, and "View thread" each appear independently below.
		if head, frags := splitTrailingThreadMetadata(line); len(frags) > 0 {
			if head != "" {
				out = append(out, head)
				// Head itself may be a body+filename compound (e.g.
				// "image.png 4 replies..."). Recurse one level.
				if hbody, hfile := splitTrailingFilename(head); hbody != "" {
					out = append(out, hbody, hfile)
				}
			}
			out = append(out, frags...)
		}
		// "<body>2 files Download all" — Slack multi-attachment
		// indicator. Body, "<N> files", and "Download all" each appear
		// independently as sibling Names below.
		if head, frags := splitTrailingFilesIndicator(line); len(frags) > 0 {
			if head != "" {
				out = append(out, head)
			}
			out = append(out, frags...)
		}
	}
	return out
}

// looksLikeURL reports whether the line is (or starts with) an http/https
// URL. Used by extractSubsumedFragments to short-circuit filename / thread
// pattern matchers — Slack's permalink URLs contain ".png" segments that
// the filename matcher would otherwise treat as compound bodies.
func looksLikeURL(line string) bool {
	return strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://")
}

// matchHeaderName recognizes Slack-style message headers — a name
// followed by two-or-more spaces followed by something containing an
// HH:MM time pattern. Accepts: "Priya  10:51 AM", "Maya Chen  Today at
// 3:22 PM", "Foo  Yesterday at 8:01 AM" — anything where the tail
// after the double space contains a recognizable time anywhere.
// Returns the name on match, empty otherwise.
func matchHeaderName(line string) string {
	idx := strings.Index(line, "  ")
	if idx <= 0 || idx > 40 {
		return ""
	}
	if findTimePattern(line[idx:]) < 0 {
		return ""
	}
	return strings.TrimSpace(line[:idx])
}

// matchCompoundMessage recognizes "Name: body ... HH:MM[ AM/PM] ..."
// style compounds. We only require ": " near the start AND an HH:MM
// pattern somewhere in the rest — the trailing time gate is what keeps
// "Note: ..." / "TODO: ..." / "Re: ..." annotations from matching.
// Anything between the body and the time (and after the time, like
// " 1 reaction.") is treated as decoration. Returns (name, body), both
// empty on miss.
func matchCompoundMessage(line string) (string, string) {
	colonIdx := strings.Index(line, ": ")
	if colonIdx <= 0 || colonIdx > 40 {
		return "", ""
	}
	rest := line[colonIdx+2:]
	timeIdx := findTimePattern(rest)
	if timeIdx < 0 {
		return "", ""
	}
	body := strings.TrimSpace(rest[:timeIdx])
	body = strings.TrimRight(body, " .")
	body = strings.TrimSpace(body)
	name := strings.TrimSpace(line[:colonIdx])
	return name, body
}

// findTimePattern scans s for an HH:MM (optionally with " AM"/" PM")
// substring and returns the byte offset where the time starts, or -1
// when no time pattern is present.
//
// Looser than looksLikeTime: looksLikeTime checks the entire string is
// a time; this only needs a time substring (so "Today at 3:22 PM" and
// "示例回复. 3:06 PM. 1 reaction." both match). Used by the compound
// and header recognizers to accept Slack's various date-context tails.
func findTimePattern(s string) int {
	n := len(s)
	for i := 0; i < n-3; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		// Reject when preceded by an ASCII digit — we want a digit
		// CLUSTER boundary so "p1779947476..." doesn't match.
		if i > 0 {
			prev := s[i-1]
			if prev >= '0' && prev <= '9' {
				continue
			}
		}
		if matchTimeAt(s, i) > 0 {
			return i
		}
	}
	return -1
}

// matchTimeAt returns the byte offset of the character past the time
// pattern starting at pos, or 0 when no time is present at that
// position. Mirrors looksLikeTime but for an arbitrary substring.
func matchTimeAt(s string, pos int) int {
	n := len(s)
	if pos >= n || s[pos] < '0' || s[pos] > '9' {
		return 0
	}
	p := pos + 1
	if p < n && s[p] >= '0' && s[p] <= '9' {
		p++
	}
	if p >= n || s[p] != ':' {
		return 0
	}
	p++
	if p+1 >= n || s[p] < '0' || s[p] > '9' || s[p+1] < '0' || s[p+1] > '9' {
		return 0
	}
	p += 2
	// Reject HH:MM followed by another digit (so "12:345" doesn't match).
	if p < n && s[p] >= '0' && s[p] <= '9' {
		return 0
	}
	// Optional " AM" / " PM"
	if p+2 < n && s[p] == ' ' && (s[p+1] == 'A' || s[p+1] == 'P') && s[p+2] == 'M' {
		p += 3
	}
	return p
}

// looksLikeTime reports whether s is an "HH:MM" or "H:MM" string,
// optionally followed by " AM" / " PM".
func looksLikeTime(s string) bool {
	n := len(s)
	if n < 4 || n > 8 {
		return false
	}
	pos := 0
	if s[pos] < '0' || s[pos] > '9' {
		return false
	}
	pos++
	if pos < n && s[pos] >= '0' && s[pos] <= '9' {
		pos++
	}
	if pos >= n || s[pos] != ':' {
		return false
	}
	pos++
	if pos+1 >= n || s[pos] < '0' || s[pos] > '9' || s[pos+1] < '0' || s[pos+1] > '9' {
		return false
	}
	pos += 2
	if pos == n {
		// bare "HH:MM"
		return true
	}
	// The only legal continuation is exactly " AM" or " PM" (3 chars).
	if pos+3 != n {
		return false
	}
	return s[pos] == ' ' && (s[pos+1] == 'A' || s[pos+1] == 'P') && s[pos+2] == 'M'
}

// normalizeUIAFragment cleans a flat-text fragment in two ways:
//
//  1. Removes the categories of Unicode characters that show up in
//     Chromium UIA accessible names but carry no semantic value:
//     - Private Use Area (icon font glyphs from Octicon/Codicon)
//     - Variation Selectors (emoji style toggles, not the emoji itself)
//     - Zero-Width Joiner / Non-Joiner / Space / BOM
//     - Other format (Cf) characters (LRM/RLM/PDI/ALM etc.)
//
//  2. Folds the non-breaking-space family (U+00A0, U+2007, U+2009,
//     U+202F, U+205F, U+3000) down to plain ASCII space. Slack's
//     accessibility tree uses non-breaking space as the separator
//     between sender name and the timestamp ("Priya  10:51 AM"),
//     not regular space — so downstream pattern matchers ("Name  TIME")
//     would silently miss every Slack header without this fold. The
//     fold also helps line-level equality dedup since two visually
//     identical lines that differ only by NBSP vs ASCII space now hash
//     to the same key.
//
// The emoji code points themselves are preserved — only their
// invisible modifiers go away.
func normalizeUIAFragment(s string) string {
	if s == "" {
		return s
	}
	if !hasNormalizableChar(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isStripRune(r) {
			continue
		}
		if isNBSPLike(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isNBSPLike reports whether r is one of the non-breaking-space family
// runes that Chromium / Slack use interchangeably with ASCII space.
func isNBSPLike(r rune) bool {
	switch r {
	case 0x00A0, // NO-BREAK SPACE
		0x2007, // FIGURE SPACE
		0x2009, // THIN SPACE
		0x202F, // NARROW NO-BREAK SPACE
		0x205F, // MEDIUM MATHEMATICAL SPACE
		0x3000: // IDEOGRAPHIC SPACE
		return true
	}
	return false
}

func hasNormalizableChar(s string) bool {
	for _, r := range s {
		if isStripRune(r) || isNBSPLike(r) {
			return true
		}
	}
	return false
}

func isStripRune(r rune) bool {
	switch {
	case r == 0xFEFF: // BOM
		return true
	case r >= 0x200B && r <= 0x200F: // ZWSP, ZWNJ, ZWJ, LRM, RLM
		return true
	case r >= 0x2028 && r <= 0x202E: // line/paragraph sep + bidi format
		return true
	case r >= 0x2060 && r <= 0x2064: // WJ + invisible math
		return true
	case r >= 0x206A && r <= 0x206F: // deprecated format
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return true
	case r >= 0xE000 && r <= 0xF8FF: // BMP Private Use Area (icon fonts)
		return true
	case r >= 0xF0000 && r <= 0xFFFFD: // Supplementary PUA-A
		return true
	case r >= 0x100000 && r <= 0x10FFFD: // Supplementary PUA-B
		return true
	case unicode.Is(unicode.Cc, r): // C0/C1 control characters
		// Keep \t (	) — sometimes meaningful as a separator.
		return r != '\t'
	}
	return false
}

// stripIntraLineDup detects the "X X" or "X<sep>X" Chromium pattern
// where a single node's accessible name is the same phrase concatenated
// to itself, optionally separated by a single space, slash, dash, or
// period. Returns one copy of the phrase when matched, the original
// line otherwise.
//
// Examples it catches:
//
//	"Source Control - 1 pending changes Source Control - 1 pending changes"
//	"article-zero-to-agent-cli.mdarticle-zero-to-agent-cli.md"
//	"hello / hello"
//
// Examples it does NOT catch (intentional — risk of false positives):
//
//	"hello hello world" (asymmetric, not exactly "X X")
//	"yes yes" (too short — likely a real repeated word, not noise)
func stripIntraLineDup(line string) string {
	if len(line) < 8 {
		// Too short to confidently identify duplication noise vs.
		// legitimate repeated words ("ok ok", "yes yes" can be real).
		return line
	}

	n := len(line)
	// Try exact halves first (no separator).
	if n%2 == 0 {
		half := n / 2
		left, right := line[:half], line[half:]
		if left == right {
			return left
		}
	}
	// Try one-character separators commonly seen in Chromium output.
	if n%2 == 1 {
		half := (n - 1) / 2
		left, right := line[:half], line[half+1:]
		sep := line[half]
		if left == right && (sep == ' ' || sep == '-' || sep == '/' || sep == '.' || sep == ',') {
			return left
		}
	}
	return line
}

// dedupKey produces the comparison key for cleanFlatText. Beyond exact
// equality, it normalizes two patterns Slack's UIA tree emits where the
// same message body appears as multiple sibling Name strings:
//
//  1. Leading "H:MM" or "HH:MM" timestamp concatenated to the body
//     (Slack's "follow-up message in a series" pattern — sent within
//     the same minute, same author). The body is then ALSO exposed as
//     a sibling Name without the timestamp.
//
//  2. Trailing single/double-digit reaction count concatenated to a
//     non-Latin body (Slack reaction badges expose "<body><count>",
//     e.g. "示例回复1", while the body alone appears as a separate Name).
//     The strip only fires when the digit cluster is preceded by a
//     non-ASCII-letter rune so identifiers like "level1" / "v2" /
//     "step3" stay intact.
//
// When normalization would empty the key (a bare "12:34" line) we fall
// back to the original — a standalone timestamp shouldn't collapse
// every later body to itself.
func dedupKey(line string) string {
	stripped := stripLeadingTime(line)
	stripped = stripTrailingReactionDigits(stripped)
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return line
	}
	return stripped
}

// stripLeadingTime removes a "H:MM" or "HH:MM" timestamp at the start
// of the line, optionally followed by " AM"/" PM" and a single space.
// Returns the input unchanged when no time pattern matches at position 0.
//
// Examples:
//
//	"10:15hello"     -> "hello"
//	"6:34示例"        -> "示例"
//	"10:34 PM body"  -> "body"
//	"Hello 10:15"    -> "Hello 10:15"  (not at start)
//	"12345"          -> "12345"        (no colon)
//	"10:34"          -> ""             (caller falls back to original)
func stripLeadingTime(s string) string {
	n := len(s)
	if n < 4 {
		return s
	}
	pos := 0
	if s[pos] < '0' || s[pos] > '9' {
		return s
	}
	pos++
	if pos < n && s[pos] >= '0' && s[pos] <= '9' {
		pos++
	}
	if pos >= n || s[pos] != ':' {
		return s
	}
	pos++
	if pos+1 >= n || s[pos] < '0' || s[pos] > '9' || s[pos+1] < '0' || s[pos+1] > '9' {
		return s
	}
	pos += 2
	if pos+2 < n && s[pos] == ' ' && (s[pos+1] == 'A' || s[pos+1] == 'P') && s[pos+2] == 'M' {
		pos += 3
	}
	if pos < n && s[pos] == ' ' {
		pos++
	}
	return s[pos:]
}

// stripTrailingReactionDigits drops up to two trailing decimal digits
// when they look "tacked on" to a non-ASCII body — typically a Slack
// reaction count appended to a CJK message. The rune immediately
// preceding the digit cluster must be a non-ASCII non-digit codepoint,
// so anything in the ASCII range (letters, digits, hyphens, slashes,
// dots — all of which are common identifier characters) is left alone.
//
// Why such a conservative gate: messages like "filler-01" or "v2.1"
// or "level3" have legitimate trailing digits that mean something, and
// the false-positive cost of collapsing distinct lines outweighs the
// benefit of catching the rare Latin-script reaction-count concat. The
// observed Slack pattern uses CJK message bodies (well outside ASCII),
// so the non-ASCII gate covers the cases users actually hit.
func stripTrailingReactionDigits(s string) string {
	n := len(s)
	if n < 2 {
		return s
	}
	end := n
	for end > 0 && s[end-1] >= '0' && s[end-1] <= '9' {
		end--
	}
	digitCount := n - end
	if digitCount == 0 || digitCount > 2 {
		return s
	}
	if end == 0 {
		return s
	}
	r, _ := utf8.DecodeLastRuneInString(s[:end])
	if r < 0x80 {
		// ASCII (letter, digit, punctuation, symbol) — too risky to strip.
		return s
	}
	if unicode.IsDigit(r) {
		return s
	}
	return s[:end]
}

// trailingFilenameRE matches the file-attachment shapes Slack emits
// glued to the end of a message body without any separator:
//
//   - "image.png" / "image_2.jpg" / "IMG_1234.heic" — generic
//     short filenames Slack auto-assigns.
//   - "截屏YYYY-MM-DD 下午H.MM.SS.png" / "Screen Shot YYYY-MM-DD at
//     H.MM.SS AM.png" — macOS screenshot filenames in zh-CN and en.
//   - Generic "<filename>.<ext>" for common attachment types when
//     the filename has no whitespace.
//
// Each alternative is anchored with $ so it only matches at the end of
// the line.
var trailingFilenameRE = regexp.MustCompile(
	`(?i)` +
		// macOS Chinese screenshot
		`(截屏\d{4}-\d{2}-\d{2} 下午\d+\.\d+\.\d+\.(?:png|jpg|jpeg))$` +
		`|` +
		// macOS English screenshot
		`(Screen Shot \d{4}-\d{2}-\d{2} at \d+\.\d+\.\d+ [AP]M\.(?:png|jpg|jpeg))$` +
		`|` +
		// Slack-named image / IMG / screenshot
		`((?:image|img|screenshot|photo|file)[_\-]?\d*\.(?:png|jpg|jpeg|gif|webp|heic))$` +
		`|` +
		// Bare ASCII filename + known extension (whitespace-free)
		`([a-zA-Z0-9_\-]+\.(?:pdf|docx?|xlsx?|pptx?|csv|txt|md|html?|zip|tar|gz|rar|7z|mp4|mov|webm|mp3|wav))$`,
)

// splitTrailingFilename splits a line that ends with a recognizable
// attachment filename into (body, filename). Returns "", "" when no
// pattern matches or when the entire line IS the filename (so there is
// nothing to register as a separate body fragment).
func splitTrailingFilename(line string) (body, filename string) {
	loc := trailingFilenameRE.FindStringIndex(line)
	if loc == nil {
		return "", ""
	}
	if loc[0] == 0 {
		// Whole line is the filename — no body to register, dedup
		// against later standalone copies of THIS line handled by the
		// normal exact-match path.
		return "", ""
	}
	body = strings.TrimSpace(line[:loc[0]])
	filename = strings.TrimSpace(line[loc[0]:])
	if body == "" || filename == "" {
		return "", ""
	}
	return body, filename
}

// trailingThreadMetadataRE matches the Slack "threaded attachment
// summary" shape: a head segment followed by "<N> replies", an
// optional "Last reply <stamp>", and "View thread", all concatenated
// without separators (Chromium emits this as ONE accessible Name).
var trailingThreadMetadataRE = regexp.MustCompile(
	`^(.*?)(\d+)\s*repliesLast reply (.+?) agoView thread$`,
)

// splitTrailingThreadMetadata returns (head, fragments) when line is a
// Slack threaded attachment summary, otherwise ("", nil).
//
// Fragments are the three standalone pieces Chromium ALSO exposes as
// sibling Names ("4 replies", "Last reply 2 days ago", "View thread")
// so a later occurrence of any of them gets dedup-suppressed.
func splitTrailingThreadMetadata(line string) (string, []string) {
	m := trailingThreadMetadataRE.FindStringSubmatch(line)
	if m == nil {
		return "", nil
	}
	head := strings.TrimSpace(m[1])
	frags := []string{
		m[2] + " replies",
		"Last reply " + m[3] + " ago",
		"View thread",
	}
	return head, frags
}

// trailingFilesIndicatorRE matches Slack's multi-attachment indicator:
// "<text><N> files Download all" emitted as a single Name when the
// user attached more than one file. "<N> files" and "Download all"
// appear separately below.
var trailingFilesIndicatorRE = regexp.MustCompile(
	`^(.*?)(\d+)\s*files Download all$`,
)

func splitTrailingFilesIndicator(line string) (string, []string) {
	m := trailingFilesIndicatorRE.FindStringSubmatch(line)
	if m == nil {
		return "", nil
	}
	head := strings.TrimSpace(m[1])
	return head, []string{m[2] + " files", "Download all"}
}

// collapseFlattenedDups drops "flat compound" lines that duplicate a
// formatted block emitted as separate sibling lines below. Chromium's
// UIA tree systematically exposes long bodies (LLM message responses,
// multi-paragraph posts) twice: once as a single super-long Name with
// all content concatenated without preserved newlines, and once as
// individual child Names with proper line layout. Per-line dedup
// cannot catch this because no single child line equals the flat
// compound.
//
// Strategy: for each "long" line (>= flattenedDupMinRunes, skipping
// URLs) chunk its whitespace-stripped form into 20-rune fragments.
// Concatenate the whitespace-stripped forms of up to
// flattenedDupLookahead following lines. If at least
// flattenedDupMatchRatio of the chunks appear somewhere in the
// concatenation, the long line is a flattened sibling — drop it.
//
// The fuzzy chunk match handles the fact that Chromium's flat form is
// not byte-identical to the concatenated formatted lines — Slack often
// repeats a date label inside the flat string (".. 示例行程后天（7月30日）
// — 天气转好 ..") while the formatted version only emits the date once.
// Strict substring matching misses those because of the inserted
// repetition; chunk-based matching tolerates it.
//
// Strict gates keep false positives away:
//
//   - URL prefix gate prevents long-URL false hits.
//   - Long unique paragraphs whose chunks do not appear in following
//     lines stay (no concat coverage).
//   - Short headers and sender names cannot trigger (below threshold).
const (
	flattenedDupMinRunes   = 100
	flattenedDupLookahead  = 200
	flattenedDupChunkRunes = 20
	flattenedDupMatchRatio = 0.85
)

func collapseFlattenedDups(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}
	dropped := make([]bool, len(lines))

	for i, line := range lines {
		if utf8.RuneCountInString(line) < flattenedDupMinRunes {
			continue
		}
		if looksLikeURL(line) {
			continue
		}

		target := stripAllWhitespace(line)
		if target == "" {
			continue
		}
		chunks := splitIntoRuneChunks(target, flattenedDupChunkRunes)
		if len(chunks) < 5 {
			continue
		}

		// Build the whitespace-stripped concat of the next N lines.
		var concat strings.Builder
		concat.Grow(len(target) * 2)
		end := i + 1 + flattenedDupLookahead
		if end > len(lines) {
			end = len(lines)
		}
		for j := i + 1; j < end; j++ {
			if dropped[j] {
				continue
			}
			concat.WriteString(stripAllWhitespace(lines[j]))
			if len(concat.String()) > len(target)*4 {
				break
			}
		}
		concatStr := concat.String()

		matched := 0
		for _, c := range chunks {
			if strings.Contains(concatStr, c) {
				matched++
			}
		}
		ratio := float64(matched) / float64(len(chunks))
		if ratio >= flattenedDupMatchRatio {
			dropped[i] = true
		}
	}

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if dropped[i] {
			continue
		}
		out = append(out, line)
	}
	return out
}

// splitIntoRuneChunks slices s into fixed-rune-length chunks. The
// final chunk is dropped when it is shorter than the requested size
// (partial chunks would inflate the match ratio with content that
// might happen to repeat elsewhere by chance).
func splitIntoRuneChunks(s string, chunkSize int) []string {
	if chunkSize <= 0 {
		return nil
	}
	runes := []rune(s)
	if len(runes) < chunkSize {
		return nil
	}
	out := make([]string, 0, len(runes)/chunkSize)
	for i := 0; i+chunkSize <= len(runes); i += chunkSize {
		out = append(out, string(runes[i:i+chunkSize]))
	}
	return out
}

// stripAllWhitespace returns s with every Unicode whitespace rune
// removed. Used by collapseFlattenedDups to compare flat-compound and
// formatted-block content irrespective of layout.
func stripAllWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
