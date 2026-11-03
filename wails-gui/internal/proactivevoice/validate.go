package proactivevoice

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/engagement"
)

// Rejection contains only a fixed check name, safe for count-only logging.
type Rejection string

const (
	RejectShape   Rejection = "parse_shape"
	RejectRefs    Rejection = "refs"
	RejectFacts   Rejection = "facts"
	RejectLimits  Rejection = "limits"
	RejectBanned  Rejection = "banned_claims"
	RejectLinks   Rejection = "links_markup"
	RejectSecrets Rejection = "digits_money_secrets"
	RejectDraft   Rejection = "draft"
	RejectClosing Rejection = "closing_line"
	RejectPromise Rejection = "promises_passed_days"
	RejectActions Rejection = "action_table"
	RejectEcho    Rejection = "echo"
	RejectNames   Rejection = "names_denylist"
	RejectAbstain Rejection = "abstain"
)

// Validate runs checks 1 through 13 in the reviewed order. No rejected partial
// output escapes: callers get either one complete accepted object or its kind.
func Validate(job Job, raw []byte, contactDenylist []string) (Output, Rejection) {
	out, ok := parseOutput(raw)
	if !ok {
		return Output{}, RejectShape
	}
	if out.Abstain {
		if out.Headline != "" || out.Body != "" || out.Draft != nil || len(out.Actions) != 0 || len(out.Evidence) != 0 || out.Reason == nil || !slices.Contains([]string{"no_source", "would_need_guess", "kind_not_supported", "over_limit", "unsafe_content"}, *out.Reason) {
			return Output{}, RejectShape
		}
		return Output{}, RejectAbstain
	}
	if out.Reason != nil || !slices.Equal(out.Actions, job.Actions) || len(out.Evidence) == 0 || strings.TrimSpace(out.Headline) == "" || strings.TrimSpace(out.Body) == "" {
		return Output{}, RejectShape
	}
	texts := []string{out.Headline, out.Body}
	if out.Draft != nil {
		texts = append(texts, out.Draft.Text)
	}
	refs := evidenceIndex(job)
	cited := make(map[string]evidence, len(out.Evidence))
	for _, ref := range out.Evidence {
		entry, exists := refs[ref]
		if !exists || entry.forbidden || !evidenceMentioned(entry, texts) {
			return Output{}, RejectRefs
		}
		if _, duplicate := cited[ref]; duplicate {
			return Output{}, RejectRefs
		}
		cited[ref] = entry
	}
	if !validFacts(job, out, cited, contactDenylist) {
		return Output{}, RejectFacts
	}
	if !within(out.Headline, job.Limits.Headline) || !within(out.Body, job.Limits.Body) || (out.Draft != nil && (job.Limits.Draft == nil || !within(out.Draft.Text, *job.Limits.Draft))) {
		return Output{}, RejectLimits
	}
	for _, text := range texts {
		if strings.ContainsAny(text, "!！~") || containsEmoji(text) || hasAny(text, []string{"urgent", "critical", "紧急", "严重", "关键", "温馨提示", "别忘了", "加油"}) || hasAny(text, []string{"no reply", "no response", "did not reply", "never replied", "ignored", "没有回复", "没回复", "未回复", "忽略了", "忘了"}) {
			return Output{}, RejectBanned
		}
	}
	for _, text := range texts[:2] {
		if hasAny(text, []string{"sent", "booked", "saved", "scheduled", "replied", "cancelled", "canceled", "rescheduled", "已发送", "已保存", "已预约", "已回复", "取消了", "改期"}) {
			return Output{}, RejectBanned
		}
	}
	for _, text := range texts {
		if engagement.ContainsTextLink(text) || hasAny(text, []string{"http", "https", "www", "mailto"}) || emailPattern.MatchString(text) || strings.ContainsAny(text, "<>[]{}`\\") {
			return Output{}, RejectLinks
		}
	}
	for _, text := range texts {
		if isoDatePattern.MatchString(text) || groupedNumberPattern.MatchString(text) || money(text) || !validDigitRuns(text, job, cited) {
			return Output{}, RejectSecrets
		}
	}
	if out.Draft != nil && hasAny(out.Draft.Text, secretWords) {
		return Output{}, RejectSecrets
	}
	for _, text := range texts[:2] {
		for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(".!?。！？\n", r) }) {
			if hasAny(sentence, secretWords) && !digitPattern.MatchString(sentence) {
				return Output{}, RejectSecrets
			}
		}
	}
	if out.Draft != nil && (!job.DraftAllowed || job.Kind != "waiting_on_reply" || job.MailFlag != "none" || job.DraftTo != "addr_1" || out.Draft.To != job.DraftTo || out.Draft.Subject != job.DraftSubject || strings.TrimSpace(out.Draft.Text) == "" || placeholderPattern.MatchString(out.Draft.Text)) {
		return Output{}, RejectDraft
	}
	if job.DraftAllowed && out.Draft == nil {
		return Output{}, RejectDraft
	}
	closing := job.ClosingLine
	if closing == "" {
		closing = ClosingLine(job.OutputLanguage)
	}
	if out.Draft != nil {
		if job.ClosingLine == "" || !strings.HasSuffix(out.Body, job.ClosingLine) {
			return Output{}, RejectClosing
		}
	} else if strings.Contains(out.Body, closing) || strings.Contains(out.Body, ClosingLine("en")) || strings.Contains(out.Body, ClosingLine("zh-CN")) {
		return Output{}, RejectClosing
	}
	userWordsCited := false
	for _, words := range job.UserWords {
		if _, ok := cited[words.Ref]; !ok {
			continue
		}
		userWordsCited = true
		if words.PromisedDay != nil && words.PromisedDay.Status == "passed" && !hasAny(out.Body, []string{"passed", "has passed", "已过", "已经过去"}) {
			return Output{}, RejectPromise
		}
	}
	for _, text := range texts[1:] {
		if !userWordsCited && (hasAny(text, promiseWords) || beforeDayPattern.MatchString(text)) {
			return Output{}, RejectPromise
		}
	}
	if !slices.Equal(out.Actions, Actions(job.Kind, job.DraftAllowed)) || !Supported(job.Kind) {
		return Output{}, RejectActions
	}
	if echoes(job, out) {
		return Output{}, RejectEcho
	}
	if !job.NamesAllowed {
		for _, name := range contactDenylist {
			if containsContactName(strings.Join(texts, "\n"), name) {
				return Output{}, RejectNames
			}
		}
	}
	return out, ""
}

func parseOutput(raw []byte) (Output, bool) {
	var out Output
	if len(raw) < 2 || len(raw) > 32*1024 || raw[0] != '{' || raw[len(raw)-1] != '}' || !utf8.Valid(raw) {
		return out, false
	}
	fields, ok := objectFields(raw, []string{"headline", "body", "draft", "actions", "evidence", "abstain", "reason"}, true)
	if !ok {
		return out, false
	}
	for _, key := range []string{"headline", "body", "actions", "evidence", "abstain"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return out, false
		}
	}
	if !bytes.Equal(bytes.TrimSpace(fields["draft"]), []byte("null")) {
		draftFields, valid := objectFields(fields["draft"], []string{"to", "subject", "text"}, false)
		if !valid {
			return out, false
		}
		for _, val := range draftFields {
			if bytes.Equal(bytes.TrimSpace(val), []byte("null")) {
				return out, false
			}
		}
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Actions) > 3 {
		return Output{}, false
	}
	return out, true
}

func objectFields(raw []byte, keys []string, ordered bool) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := make(map[string]json.RawMessage, len(keys))
	index := 0
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || index >= len(keys) || !slices.Contains(keys, key) || (ordered && keys[index] != key) {
			return nil, false
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields[key] = value
		index++
	}
	if _, err = decoder.Token(); err != nil || index != len(keys) {
		return nil, false
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}

func within(text string, limit Limit) bool {
	if limit.Max <= 0 {
		return false
	}
	switch limit.Unit {
	case "characters":
		return utf8.RuneCountInString(text) <= limit.Max
	case "words":
		return len(strings.Fields(text)) <= limit.Max
	default:
		return false
	}
}

// Whole-token boundaries are Unicode aware for Latin words; Chinese phrases
// deliberately match inside Chinese prose, whose words have no spaces.
func whole(text, term string) bool {
	if term == "" {
		return false
	}
	text, term = compactSpace(strings.ToLower(text)), compactSpace(strings.ToLower(term))
	if term == "" {
		return false
	}
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(term)
		first, _ := utf8.DecodeRuneInString(term)
		last, _ := utf8.DecodeLastRuneInString(term)
		before, after := rune(0), rune(0)
		if start > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:start])
		}
		if end < len(text) {
			after, _ = utf8.DecodeRuneInString(text[end:])
		}
		if (!latinToken(first) || !latinToken(before)) && (!latinToken(last) || !latinToken(after)) {
			return true
		}
		offset = start + 1
	}
	return false
}

func latinToken(r rune) bool {
	return (unicode.IsLetter(r) && !unicode.In(r, unicode.Han)) || unicode.IsDigit(r) || r == '_'
}

func hasAny(text string, words []string) bool {
	for _, word := range words {
		if whole(text, word) {
			return true
		}
	}
	return false
}

func containsContactName(text, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if whole(text, name) {
		return true
	}
	// A first-name greeting still discloses a known person. Ignore initials,
	// which would otherwise match ordinary single-letter words.
	for _, token := range wordTokens(name) {
		if utf8.RuneCountInString(token) > 1 && whole(text, token) {
			return true
		}
	}
	return false
}

func containsEmoji(text string) bool {
	for _, r := range text {
		if r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF ||
			r >= 0x2194 && r <= 0x2199 || r >= 0x21A9 && r <= 0x21AA ||
			r >= 0x231A && r <= 0x231B || r >= 0x23E9 && r <= 0x23F3 ||
			r >= 0x23F8 && r <= 0x23FA || r >= 0x25AA && r <= 0x25AB ||
			r >= 0x25FB && r <= 0x25FE || r >= 0x2934 && r <= 0x2935 ||
			r >= 0x2B05 && r <= 0x2B07 || r >= 0x2B1B && r <= 0x2B1C {
			return true
		}
		switch r {
		case 0x00A9, 0x00AE, 0x203C, 0x2049, 0x20E3, 0x2122, 0x2139,
			0x2328, 0x23CF, 0x24C2, 0x25B6, 0x25C0, 0x2B50, 0x2B55,
			0x3030, 0x303D, 0x3297, 0x3299, 0xFE0F:
			return true
		}
	}
	return false
}

var (
	emailPattern         = regexp.MustCompile(`(?i)[^\s@]+@[^\s@]+`)
	isoDatePattern       = regexp.MustCompile(`\b[0-9]{4}[-/][0-9]{2}[-/][0-9]{2}\b`)
	groupedNumberPattern = regexp.MustCompile(`[0-9]+(?:,[0-9]{3})+(?:\.[0-9]+)?`)
	digitPattern         = regexp.MustCompile(`[0-9]`)
	digitRunPattern      = regexp.MustCompile(`[0-9][0-9 ,.-]*[0-9]|[0-9]`)
	placeholderPattern   = regexp.MustCompile(`(?i)[\[\]{}]|_{3,}|\b(?:TODO|TBD|TBC|INSERT HERE|FILL IN|YOUR NAME|YOUR ANSWER|PLACEHOLDER|BLANK)\b|待填写|请填写|待补充|占位`)
	beforeDayPattern     = regexp.MustCompile(`(?i)\b(?:by|before)\s+(?:(?:next|this|the)\s+)?(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday|today|tomorrow|tonight|january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec|[0-9]+(?:st|nd|rd|th)?)\b`)
)

var secretWords = []string{"验证码", "密码", "password", "one-time code", "OTP"}
var promiseWords = []string{"will", "I'll", "I’ll", "would", "going to", "plan to", "promise", "should", "shall", "tomorrow", "tonight", "next week", "我会", "会把", "会给", "会先", "打算", "计划", "答应", "说会", "明天", "今晚", "下周", "之前", "前给"}

func money(text string) bool {
	return strings.ContainsAny(text, "$€£¥￥%") || hasAny(text, []string{"USD", "EUR", "GBP", "CNY", "RMB", "dollars", "euros", "yuan", "cents", "percent", "元", "块", "美元", "美金", "欧元", "英镑", "日元", "人民币", "百分之"})
}

func validDigitRuns(text string, job Job, cited map[string]evidence) bool {
	// Keep this guard even though check 3 also rejects these numbers: alternate
	// digits and numeral symbols must never disappear from the secret scan.
	if hasUnsupportedNumber(text) {
		return false
	}
	allowed := factFields(job, cited)
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, text)
	for _, run := range digitRunPattern.FindAllString(text, -1) {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, run)
		if len(digits) >= 4 && !hasAny(allowed, []string{digits}) {
			return false
		}
	}
	return true
}

func hasUnsupportedNumber(text string) bool {
	for _, r := range text {
		if unicode.IsNumber(r) && (r < '0' || r > '9') {
			return true
		}
	}
	return false
}

// StripLinks uses the same URL matcher as notification delivery. Addresses are
// separately removed because mail destinations must be replaced by addr_1.
func StripLinks(text string) string {
	return emailPattern.ReplaceAllString(engagement.StripTextLinks(text), " ")
}
