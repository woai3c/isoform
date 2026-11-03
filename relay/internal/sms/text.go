package sms

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// MaxBodyRunes bounds one text the hub asks the relay to send when it
	// fits the GSM 7-bit alphabet: at most three 153-character segments.
	MaxBodyRunes = 320
	// MaxUnicodeBodyRunes bounds a text that needs Unicode (Chinese, emoji,
	// curly quotes): also at most three 67-character segments, so one
	// counted text never costs more than three segments.
	MaxUnicodeBodyRunes = 160
	// MaxReplyRunes bounds one inbound reply forwarded to the hub; longer
	// replies are cut. Providers already cap a text at about 1600 characters.
	MaxReplyRunes = 1600
)

// e164Pattern is a plus sign, a non-zero country digit and 7 to 14 more digits.
var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// NormalizeNumber returns the E.164 form of a number typed with spaces,
// dashes, dots or parentheses, and false when it is not E.164.
func NormalizeNumber(raw string) (string, bool) {
	number := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', ' ':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(raw))
	return number, e164Pattern.MatchString(number)
}

// Keyword is a carrier keyword a reply may be.
type Keyword int

const (
	KeywordNone Keyword = iota
	KeywordStop
	KeywordStart
	KeywordHelp
)

// ClassifyKeyword matches a whole reply against the carrier keywords, ignoring
// case, surrounding space, control and invisible format characters, and
// trailing punctuation. A keyword inside a longer reply is not one.
func ClassifyKeyword(body string) Keyword {
	word := strings.ToUpper(strings.TrimRight(cleanReply(body), ".!"))
	switch word {
	case "STOP", "STOPALL", "UNSUBSCRIBE", "CANCEL", "END", "QUIT", "OPTOUT", "REVOKE":
		return KeywordStop
	case "START", "UNSTOP":
		return KeywordStart
	case "HELP", "INFO":
		return KeywordHelp
	default:
		return KeywordNone
	}
}

// ValidBody checks a text the hub wants sent: valid UTF-8, not only space,
// no control characters other than a newline, and at most MaxBodyRunes
// characters, or MaxUnicodeBodyRunes when it needs Unicode.
func ValidBody(body string) bool {
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" {
		return false
	}
	limit := MaxBodyRunes
	if !gsm7(body) {
		limit = MaxUnicodeBodyRunes
	}
	if utf8.RuneCountInString(body) > limit {
		return false
	}
	for _, r := range body {
		if r != '\n' && unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// hubPrefixes are the starts a hub-written text may have: the brand, then
// an ASCII colon (English) or a full-width one (Chinese).
var hubPrefixes = []string{"Isoform:", "Isoform："}

// linkPattern finds a link: anything with a scheme, a www. address, a
// domain followed by a path, or a bare domain under a top-level domain that
// links commonly use. File names such as report.pdf or build.sh are not
// links. The Mac strips the same pattern from what it puts in a text
// (wails-gui/internal/engagement/text.go), so a text it writes never trips it.
var linkPattern = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|\bwww\.)[\x21-\x7e]*|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:[a-z]{2,24}/[\x21-\x7e]*|(?:` + linkTLDs + `)\b(?:/[\x21-\x7e]*)?)`)

// linkTLDs are the top-level domains a bare domain is read as a link under.
const linkTLDs = "com|net|org|edu|gov|io|co|me|ly|app|dev|link|info|biz|xyz|us|uk|ca|au|de|fr|ru|cn|top|site|online|shop|club|gl|to|cc|tk|ml|ga|cf|gq|live|click|page|gg|tv|ws|cloud|tech|store|icu|vip|work|pro|one|ai"

// phoneRunPattern finds a run of digits with the separators a phone number
// is written with; a run of seven digits or more is read as a phone number.
var phoneRunPattern = regexp.MustCompile(`\+?\(?[0-9][0-9 ().+-]*[0-9]\)?`)

// phoneDigits is the fewest digits a run needs to be read as a phone number.
const phoneDigits = 7

// HasPhoneNumber reports whether text carries a run of seven digits or more.
func HasPhoneNumber(text string) bool {
	for _, run := range phoneRunPattern.FindAllString(text, -1) {
		if digitCount(run) >= phoneDigits {
			return true
		}
	}
	return false
}

func digitCount(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// HubBody checks what a hub-written text says, beyond ValidBody: it starts
// with the brand, as the campaign registration promises, and carries no link
// and no phone number. Every text goes out under one shared registration, so
// a hub that sends links or numbers puts every hub's texts at risk of carrier
// filtering.
func HubBody(body string) bool {
	branded := false
	for _, prefix := range hubPrefixes {
		if strings.HasPrefix(body, prefix) {
			branded = true
		}
	}
	return branded && !linkPattern.MatchString(body) && !HasPhoneNumber(body)
}

// gsm7Alphabet is the GSM 03.38 default alphabet and its extension table,
// the characters a text can carry without switching to Unicode.
const gsm7Alphabet = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?" +
	"¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà^{}\\[~]|€"

// gsm7 reports whether every character is in the GSM 7-bit alphabet.
func gsm7(body string) bool {
	for _, r := range body {
		if !strings.ContainsRune(gsm7Alphabet, r) {
			return false
		}
	}
	return true
}

// Segments estimates how many billed segments a text takes: 160 GSM
// characters (or 70 Unicode ones) fit in one, and longer texts split into
// 153 (or 67) per segment.
func Segments(body string) int {
	single, part, units := 160, 153, utf8.RuneCountInString(body)
	if !gsm7(body) {
		single, part, units = 70, 67, len(utf16.Encode([]rune(body)))
	}
	if units <= single {
		return 1
	}
	return (units + part - 1) / part
}

// cleanReply makes an inbound reply safe to forward: invalid bytes, control
// characters other than a newline, and invisible format characters (such as
// zero-width spaces) are dropped, space is trimmed, and the text is cut at
// MaxReplyRunes.
func cleanReply(body string) string {
	body = strings.ToValidUTF8(body, "")
	body = strings.Map(func(r rune) rune {
		if (r != '\n' && unicode.IsControl(r)) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, body)
	body = strings.TrimSpace(body)
	if utf8.RuneCountInString(body) > MaxReplyRunes {
		body = string([]rune(body)[:MaxReplyRunes])
	}
	return body
}

// The fixed texts the relay itself sends. Every hub-written text goes out as
// the hub wrote it; these are the only ones the relay composes.
const (
	stopReply  = "Isoform: you are unsubscribed and will get no more texts. Reply START to resume."
	startReply = "Isoform: texts are back on. Up to %d msgs/day. Msg & data rates may apply. Reply HELP for help, STOP to cancel."
	helpReply  = "Isoform: alerts from Isoform on your computer. Up to %d msgs/day. Msg & data rates may apply. Reply STOP to cancel. Help: %s"
)

// codeTexts are the verification texts by language; English is the default.
var codeTexts = map[string]string{
	"en":    "Isoform: your code is %s. Do not share it. It expires in 10 min. Up to %d msgs/day. Msg & data rates may apply. Reply HELP for help, STOP to cancel.",
	"zh-CN": "Isoform：验证码 %s，请勿告诉他人，10分钟内有效。每天最多%d条，可能产生短信费。回复HELP获取帮助，STOP退订。",
}

func codeText(lang, code string, dailyCap int) string {
	template, ok := codeTexts[lang]
	if !ok {
		template = codeTexts["en"]
	}
	return fmt.Sprintf(template, code, dailyCap)
}

func helpText(dailyCap int, contact string) string {
	return fmt.Sprintf(helpReply, dailyCap, contact)
}

func startText(dailyCap int) string { return fmt.Sprintf(startReply, dailyCap) }
