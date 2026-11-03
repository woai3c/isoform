package pii

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// codeRun matches a standalone run of digits, in groups split by single
// spaces, dashes or dots ("482913", "482 913", "1234-5678"). Runs of 4 or
// more digits in all are one-time codes, PINs, and the account, order and
// tracking numbers RedactText has no label for.
var codeRun = regexp.MustCompile(`(?:^|[^0-9A-Za-z])([0-9]+(?:[ .\-][0-9]+)*)(?:$|[^0-9A-Za-z])`)

// codeMinDigits is the fewest digits a run needs to count as a code.
const codeMinDigits = 4

// shortCodeDigits is the length of a short run (a PIN, a CVV, a short
// verification code) that counts as a code only when a code keyword stands
// near it in the same sentence.
const shortCodeDigits = 3

// englishCodeKeywords are the words that mark a nearby run of exactly
// shortCodeDigits digits as a short PIN or verification code. "security
// code" needs no entry of its own: "code" already covers it.
var englishCodeKeywords = []string{
	"pin", "passcode", "password", "code", "otp", "cvv", "cvc", "csc",
	"verification", "one-time", "2fa", "mfa", "token",
}

// chineseCodeKeywords mark a short run the same way; Chinese has no word
// boundaries, so they match as substrings of a nearby word.
var chineseCodeKeywords = []string{"验证码", "密码", "动态码", "校验码", "安全码"}

// alnumRun matches a run of ten or more letters and digits; one that mixes
// both is an identifier (a tracking number, a ticket, a session id).
var alnumRun = regexp.MustCompile(`[A-Za-z0-9]{10,}`)

// RedactCodes replaces every standalone run of 4 or more digits, also when
// split into groups by spaces, dashes or dots, every run of ten or more
// characters mixing letters with digits, and every standalone run of exactly
// shortCodeDigits digits that looks like a short code: not a dotted version
// or decimal, not part of a larger number, and with a code keyword near it
// before a sentence end or a comma.
func RedactCodes(text string) string {
	return redactCodes(text, func(start, end int, run string) bool { return shortCodeLike(text, start, end, run) })
}

// RedactCodesKeeping redacts codes as RedactCodes does everywhere outside the
// keep spans (byte ranges, sorted and not overlapping, such as dates a caller
// shows as written), which it leaves as they are. A short run's keyword
// context is read across the kept spans as if they were blank, so a kept date
// between a keyword and a code neither hides the keyword nor takes a word of
// its window. Each gap is also checked on its own as RedactCodes would, so
// nothing RedactCodes hides in a gap is shown.
func RedactCodesKeeping(text string, keep [][2]int) string {
	blank := []byte(text)
	for _, span := range keep {
		for i := span[0]; i < span[1]; i++ {
			blank[i] = ' '
		}
	}
	context := string(blank)
	var b strings.Builder
	last := 0
	redactGap := func(start, end int) {
		gap := text[start:end]
		b.WriteString(redactCodes(gap, func(s, e int, run string) bool {
			return shortCodeLike(gap, s, e, run) || shortCodeLike(context, start+s, start+e, run)
		}))
	}
	for _, span := range keep {
		redactGap(last, span[0])
		b.WriteString(text[span[0]:span[1]])
		last = span[1]
	}
	redactGap(last, len(text))
	return b.String()
}

// redactCodes is RedactCodes with the short-run check supplied by the caller,
// given the run's byte range in text.
func redactCodes(text string, shortCode func(start, end int, run string) bool) string {
	var b strings.Builder
	pos := 0
	for rest := text; ; {
		loc := codeRun.FindStringSubmatchIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			break
		}
		run := rest[loc[2]:loc[3]]
		b.WriteString(rest[:loc[2]])
		if n := countDigits(run); n >= codeMinDigits || (n == shortCodeDigits && shortCode(pos+loc[2], pos+loc[3], run)) {
			b.WriteString("[REDACTED:code]")
		} else {
			b.WriteString(run)
		}
		// The character after the run may open the next one.
		pos += loc[3]
		rest = rest[loc[3]:]
	}
	text = b.String()
	return alnumRun.ReplaceAllStringFunc(text, func(run string) string {
		if strings.IndexFunc(run, unicode.IsDigit) >= 0 && strings.IndexFunc(run, unicode.IsLetter) >= 0 {
			return "[REDACTED:code]"
		}
		return run
	})
}

func countDigits(text string) int {
	n := 0
	for _, r := range text {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// shortCodeLike reports whether a run of exactly shortCodeDigits digits is
// a short code: not a dotted version, decimal or price ("3.12", "1.2.3"),
// not part of a larger number grouped by commas ("450,000"), and with a
// code keyword near it.
func shortCodeLike(text string, start, end int, run string) bool {
	if strings.Contains(run, ".") {
		return false
	}
	if start >= 2 && text[start-1] == ',' && text[start-2] >= '0' && text[start-2] <= '9' {
		return false
	}
	if end+1 < len(text) && text[end] == ',' && text[end+1] >= '0' && text[end+1] <= '9' {
		return false
	}
	return codeContextNear(text, start, end)
}

// codeContextNear reports whether a code keyword stands near the run
// text[start:end]: within the four words before it or the three words after
// it, stopping at a sentence end or a comma. The context comes from the
// original text, so an earlier "[REDACTED:code]" does not itself count as
// the keyword "code", and only the window words are scanned, never the
// whole line.
func codeContextNear(text string, start, end int) bool {
	return hasCodeKeyword(wordsBefore(text[:start], 4)) || hasCodeKeyword(wordsAfter(text[end:], 3))
}

// contextStop reports whether r ends the keyword context for a short code:
// a sentence end or a comma.
func contextStop(r rune) bool {
	switch r {
	case '.', '!', '?', '\n', ',', '，', '。', '！', '？':
		return true
	}
	return false
}

// wordsBefore returns up to n words from the end of text, in text order,
// scanning back only until a sentence end or a comma. wordsAfter returns up
// to n words from the start of text the same way. A word is a run between
// whitespace.
func wordsBefore(text string, n int) []string {
	var words []string
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		for l, r := 0, len(word)-1; l < r; l, r = l+1, r-1 {
			word[l], word[r] = word[r], word[l]
		}
		words = append(words, string(word))
		word = word[:0]
	}
	for i := len(text); i > 0 && len(words) < n; {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		i -= size
		if contextStop(r) {
			flush()
			break
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		word = append(word, r)
	}
	flush()
	for l, r := 0, len(words)-1; l < r; l, r = l+1, r-1 {
		words[l], words[r] = words[r], words[l]
	}
	return words
}

func wordsAfter(text string, n int) []string {
	var words []string
	start := -1
	for i := 0; i < len(text) && len(words) < n; {
		r, size := utf8.DecodeRuneInString(text[i:])
		if contextStop(r) || unicode.IsSpace(r) {
			if start >= 0 {
				words = append(words, text[start:i])
				start = -1
			}
			if contextStop(r) {
				break
			}
		} else if start < 0 {
			start = i
		}
		i += size
	}
	if start >= 0 && len(words) < n {
		words = append(words, text[start:])
	}
	return words
}

// hasCodeKeyword reports whether words hold a code keyword: an English word
// equal to a keyword (punctuation at the edges ignored), or a Chinese
// keyword inside a word. "code" after a word that names a non-secret code
// ("status code", "exit code") does not count.
func hasCodeKeyword(words []string) bool {
	for i, word := range words {
		for _, keyword := range chineseCodeKeywords {
			if strings.Contains(word, keyword) {
				return true
			}
		}
		word = strings.ToLower(strings.TrimFunc(word, notLetterOrDigit))
		if word == "code" && i > 0 && codeBlocker(words[i-1]) {
			continue
		}
		for _, keyword := range englishCodeKeywords {
			if word == keyword {
				return true
			}
		}
	}
	return false
}

// codeBlocker reports whether word keeps a following "code" from counting as
// a keyword: it names a status, error or area code, not a verification code.
func codeBlocker(word string) bool {
	word = strings.ToLower(strings.TrimFunc(word, notLetterOrDigit))
	switch word {
	case "status", "exit", "error", "http", "response", "return", "area", "zip", "postal":
		return true
	}
	return false
}

// notLetterOrDigit reports whether r is punctuation or another non-word
// character, the kind TrimFunc drops from the edges of a word.
func notLetterOrDigit(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// pathTokenLike reports whether a link path segment looks like a token: very
// long, a long run of digits, or long and mixing letters with digits or
// cases.
func pathTokenLike(segment string) bool {
	n := utf8.RuneCountInString(segment)
	if n >= 24 {
		return true
	}
	var lower, upper, digit, other bool
	for _, r := range segment {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	if digit && !lower && !upper && !other && n >= 8 {
		return true
	}
	if n < 12 {
		return false
	}
	return ((lower || upper) && digit) || (lower && upper)
}

// RedactURL keeps what a link says about where the user was and drops what
// could let someone else in: the scheme, the host and the path up to the
// first segment that looks like a token, with personal details and codes in
// the kept path redacted. The query and fragment are dropped: they carry
// tokens, reset codes and search terms. The path stays percent-encoded, so an
// encoded "?" or "#" never turns back into one. Ports and user names are
// dropped. It returns "" for anything that is not an absolute link.
func RedactURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Scheme == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	var kept []string
	cut := false
	for _, segment := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		if segment == "" {
			continue
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			decoded = segment
		}
		if pathTokenLike(segment) || pathTokenLike(decoded) {
			cut = true
			break
		}
		kept = append(kept, segment)
	}
	path := ""
	if len(kept) > 0 {
		path = "/" + strings.Join(kept, "/")
	}
	if cut {
		path += "/..."
	}
	path, _ = RedactText(path)
	return strings.ToLower(u.Scheme) + "://" + host + RedactCodes(path)
}
