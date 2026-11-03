package pii

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var redactionPlaceholder = regexp.MustCompile(`\[REDACTED:[^\]\r\n]+\]`)

// DisplayText redacts current PII and renders stored category placeholders as
// the locale-neutral ASCII marker "[...]". It never recovers masked text.
// Keep storage/model-input redaction tags intact; use this only in read models.
func DisplayText(text string) string {
	text, _ = RedactText(text)
	return DisplayRedactions(text)
}

// HasRedaction reports stored placeholders as well as newly detected PII.
func HasRedaction(text string) bool {
	_, count := RedactText(text)
	return count > 0 || redactionPlaceholder.MatchString(text)
}

// DisplayRedactions changes only existing masks, for serialized Markdown whose
// exact in-process bytes are still needed for provenance and edit validation.
func DisplayRedactions(text string) string {
	return redactionPlaceholder.ReplaceAllString(text, "[...]")
}

// ModelOutputInstructions applies to prose, titles, summaries and prepared
// questions alike. Masks are evidence of withheld details, never their values.
const ModelOutputInstructions = "Redaction tags are private details: use plain words for [REDACTED:account_number] and other kinds (an account number; a private detail) in the requested language, or omit them. Keep tags out of all output fields; do not guess hidden values or damaged words.\n"

// Accept incomplete tags too: a streamed or length-bounded model answer can
// stop before the closing bracket. Unknown kinds use the private-detail copy.
var modelRedactionPlaceholder = regexp.MustCompile(`(?i)\[REDACTED(?::[^\]\r\n]*)?\]?`)

// DisplayModelText is the common presentation boundary for text derived from
// redacted input. Storage and model context retain their original masks. This
// function only removes information; it cannot recover a redacted value.
func DisplayModelText(text, language string) string {
	if !strings.Contains(strings.ToUpper(text), "[REDACTED") {
		return text
	}
	copy := modelRedactionCopy["en"]
	lower := strings.ToLower(language)
	if strings.HasPrefix(lower, "zh") || strings.Contains(lower, "chinese") {
		copy = modelRedactionCopy["zh-CN"]
	}
	var out strings.Builder
	last := 0
	for _, match := range modelRedactionPlaceholder.FindAllStringIndex(text, -1) {
		start, end := match[0], match[1]
		tag := strings.TrimSuffix(text[start:end], "]")
		_, kind, _ := strings.Cut(tag, ":")
		phrase := copy[strings.ToLower(kind)]
		if phrase == "" {
			phrase = copy["private_detail"]
		}
		// Legacy partial-word masks have destroyed the missing letters. Drop
		// adjoining Latin word fragments rather than inventing their completion.
		for start > last {
			r, size := utf8.DecodeLastRuneInString(text[last:start])
			if !displayFragmentRune(r) {
				break
			}
			start -= size
		}
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if !displayFragmentRune(r) {
				break
			}
			end += size
		}
		if start < last {
			start = last
		}
		out.WriteString(text[last:start])
		if out.Len() > 0 && (start == last || start > 0 && strings.ContainsRune(",;:.!?", rune(text[start-1]))) && phrase[0] < utf8.RuneSelf {
			out.WriteByte(' ')
		}
		out.WriteString(phrase)
		last = end
	}
	out.WriteString(text[last:])
	return out.String()
}

func displayFragmentRune(r rune) bool {
	return unicode.Is(unicode.Latin, r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_'
}

// Go-side copy follows the en/zh-CN catalog pattern used by doingNowCopy.
// Frontend common.redactions mirrors it for the final streaming render guard.
var modelRedactionCopy = map[string]map[string]string{
	"en": {
		"private_detail": "a private detail", "account_number": "an account number", "code": "a private code",
		"private_email": "an email address", "private_phone": "a phone number",
		"private_address": "a private address", "street_address": "a street address",
		"private_person": "a person", "private_date": "a private date",
		"private_url": "a private link", "private_link": "a private link",
		"private_org": "an organization", "credit_card": "a card number",
		"cvv": "a security code", "one_time_password": "a one-time code",
		"password": "a password", "secret": "a private credential", "api_key": "an API key",
		"government_id": "an identity number", "license_plate": "a license plate",
		"device_id": "a device identifier", "session_id": "a session identifier",
	},
	"zh-CN": {
		"private_detail": "一项隐私信息", "account_number": "一个账号", "code": "一个私密代码",
		"private_email": "一个邮箱地址", "private_phone": "一个电话号码",
		"private_address": "一个私人地址", "street_address": "一个街道地址",
		"private_person": "某个人", "private_date": "一个私人日期",
		"private_url": "一个私人链接", "private_link": "一个私人链接",
		"private_org": "某个机构", "credit_card": "一个银行卡号",
		"cvv": "一个安全码", "one_time_password": "一个一次性验证码",
		"password": "一个密码", "secret": "一项私密凭据", "api_key": "一个 API 密钥",
		"government_id": "一个证件号码", "license_plate": "一个车牌号",
		"device_id": "一个设备标识", "session_id": "一个会话标识",
	},
}
