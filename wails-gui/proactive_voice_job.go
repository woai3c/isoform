package main

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"wails-gui/internal/pii"
	"wails-gui/internal/proactivevoice"
)

const proactiveVoiceJobLimit = 8 * 1024

// Candidate code runs may use typographic punctuation, symbols or invisible
// separators. Normalize the whole separator class only inside numeric runs;
// unrelated prose punctuation and short quantities remain unchanged.
const proactiveVoiceNumericSeparator = `[\s\pZ\pP\pS\p{Cf}]`

var proactiveVoiceEmail = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_\x60{|}~-]+@[a-z0-9.-]+\.[a-z]{2,}`)
var proactiveVoiceRawDate = regexp.MustCompile(`[0-9]{4}[-/][0-9]{2}[-/][0-9]{2}`)
var proactiveVoiceRawTimestamp = regexp.MustCompile(`(?i)[0-9]{4}[-/][0-9]{2}[-/][0-9]{2}[t ][0-9]{2}:[0-9]{2}`)
var proactiveVoiceDigitGroups = regexp.MustCompile(`[0-9]+(?:` + proactiveVoiceNumericSeparator + `+[0-9]+)+`)
var proactiveVoiceDigitSeparators = regexp.MustCompile(proactiveVoiceNumericSeparator + `+`)
var proactiveVoiceRedactedDigitGroups = regexp.MustCompile(`(?:[0-9]+` + proactiveVoiceNumericSeparator + `*)*\[REDACTED:[a-z_]+\](?:` + proactiveVoiceNumericSeparator + `*[0-9]+)*`)
var proactiveVoiceDay = regexp.MustCompile(`(?i)\b(today|tomorrow|tonight|yesterday|monday|tuesday|wednesday|thursday|friday|saturday|sunday|mon|tue|wed|thu|fri|sat|sun|january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|oct|nov|dec|next week|last week|this week|next month|last month|this month|next year|last year|this year)\b|今天|明天|后天|今晚|昨天|前天|下周|上周|本周|这周|下个月|上个月|这个月|明年|去年|今年|周[一二三四五六日天]|星期|[0-9一二三四五六七八九十]+月|\d{4}[-/]\d{2}[-/]\d{2}|\b\d{1,2}[-/]\d{1,2}\b`)

// These conservative lexical lists implement decision 6. Budget/project names
// alone are not amounts; explicit financial or medical content is withheld.
var proactiveVoiceRestricted = regexp.MustCompile(`(?i)[$€£¥￥%]|\b(USD|EUR|GBP|CNY|RMB|dollars?|euros?|yuan|cents?|percent|payment|invoice|billing|bank account|credit card|diagnosis|diagnosed|medication|prescription|therapy|therapist|cancer|diabetes|medical|health|hospital|patient|blood pressure)\b|人民币|美元|美金|欧元|英镑|日元|百分之|付款|账单|银行卡|信用卡|医疗|健康|诊断|处方|药物|癌症|糖尿病|心理治疗|医院|\d\s*[元块]`)

// Health content is screened separately so a money-ask flag can never
// override the decision to drop a moment that also contains health details.
var proactiveVoiceHealth = regexp.MustCompile(`(?i)\b(diagnosis|diagnosed|medication|prescription|therapy|therapist|cancer|diabetes|medical|health|hospital|patient|blood pressure|doctor|dentist|dental|treatment|symptom|disease|pregnancy|pregnant|vaccine|blood test|antidepressant)\b|医疗|健康|诊断|处方|药物|癌症|糖尿病|心理治疗|医院|医生|牙医|治疗|症状|疾病|怀孕|疫苗|血压|血检`)

func proactiveVoiceMailFlag(subject, body string) string {
	text := subject + " " + body
	for _, rule := range []struct{ kind, pattern string }{
		{"money", `(?i)[$€£¥￥]|\b(pay|payment|transfer|wire|money|invoice|billing|USD|EUR|GBP|CNY|RMB|dollars?)\b|付款|转账|汇款|支付|账单|人民币|美元`},
		{"code", `(?i)\b(OTP|one-time code|verification code|security code)\b|验证码|动态码|一次性代码`},
		{"password", `(?i)\b(password|passcode|credential)\b|密码|口令`},
		{"link", `(?i)\b(link|click|visit|http|https|www|mailto)\b|链接|点击|网址`},
	} {
		if regexp.MustCompile(rule.pattern).MatchString(text) {
			return rule.kind
		}
	}
	if proactivevoice.StripLinks(text) != text {
		return "link"
	}
	return "none"
}

func proactiveVoiceDate(t time.Time, lang string) string {
	if lang == "zh-CN" {
		days := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
		return fmt.Sprintf("%d 月 %d 日（%s）", t.Month(), t.Day(), days[t.Weekday()])
	}
	return t.Format("Monday, Jan 2")
}

func proactiveVoiceClean(s string, names []string, limit int) string {
	return proactiveVoiceCleanValue(s, names, limit, true)
}

func proactiveVoiceCleanValue(s string, names []string, limit int, redactCodes bool) string {
	s, _ = pii.RedactText(s)
	s = proactiveVoiceEmail.ReplaceAllString(s, "")
	s = proactivevoice.StripLinks(s)
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		s = proactiveVoiceRemoveName(s, name)
	}
	s = strings.Join(strings.Fields(s), " ")
	if redactCodes {
		s = proactiveVoiceRedactCodes(s)
	}
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit])
	}
	return strings.TrimSpace(s)
}

// Normalize decimal glyphs and numeric grouping only at the provider boundary.
// Stored user text remains unchanged. RedactCodes then handles generic codes
// and PINs even when no OTP label was recognizable to RedactText.
func proactiveVoiceRedactCodes(text string) string {
	text = strings.Map(func(r rune) rune {
		if r <= unicode.MaxASCII {
			return r
		}
		for _, digits := range unicode.Digit.R16 {
			if r >= rune(digits.Lo) && r <= rune(digits.Hi) && (r-rune(digits.Lo))%rune(digits.Stride) == 0 {
				return '0' + (r-rune(digits.Lo))/rune(digits.Stride)%10
			}
		}
		for _, digits := range unicode.Digit.R32 {
			if r >= rune(digits.Lo) && r <= rune(digits.Hi) && (r-rune(digits.Lo))%rune(digits.Stride) == 0 {
				return '0' + (r-rune(digits.Lo))/rune(digits.Stride)%10
			}
		}
		return r
	}, text)
	// Keep PII scanning first so generic-code masking cannot break a labeled
	// API key. A PII match may consume only the first group of a code (for
	// example, the CVV rule sees 482 in "security code: 482-913"). Treat all
	// numeric continuations of that redaction as the same secret.
	text = proactiveVoiceRedactedDigitGroups.ReplaceAllStringFunc(text, func(group string) string {
		if strings.IndexFunc(group, unicode.IsDigit) >= 0 {
			return "[REDACTED:code]"
		}
		return group
	})
	text = proactiveVoiceDigitGroups.ReplaceAllStringFunc(text, func(group string) string {
		if len(proactiveVoiceDigitSeparators.ReplaceAllString(group, "")) < 4 {
			return group
		}
		return proactiveVoiceDigitSeparators.ReplaceAllString(group, " ")
	})
	return pii.RedactCodes(text)
}

// assembleProactiveVoiceJob is pure. The source adapter owns current read-time
// access; this second boundary owns minimization and the provider contract.
// The address map stays local and is never marshaled with the job.
func assembleProactiveVoiceJob(kind, lang string, namesAllowed bool, now time.Time, source proactiveVoiceSource) (proactivevoice.Job, map[string]string, string) {
	if lang != "zh-CN" {
		lang = "en"
	}
	job := proactivevoice.Job{Contract: proactivevoice.Contract, Kind: kind, MomentID: "mom_" + uuid.NewString(), Locale: lang, OutputLanguage: lang, Surface: "main_chat", NamesAllowed: namesAllowed, MaxNames: 3, Now: proactivevoice.Display{Display: proactiveVoiceDate(now, lang) + " " + now.Format("15:04")}, MailFlag: "none", Limits: proactivevoice.DefaultLimits(lang, false), Coverage: map[string]string{}, Actions: proactivevoice.Actions(kind, false), KindGuidance: proactivevoice.Guidance(kind), People: []proactivevoice.Person{}, Items: []proactivevoice.Item{}, UserWords: []proactivevoice.UserWords{}}
	addresses := map[string]string{}
	if !proactivevoice.Supported(kind) {
		return job, addresses, "kind_not_supported"
	}
	if !proactiveVoiceAppsAllowed(source.SourceApps) {
		return job, addresses, "unsafe_content"
	}
	// The v1 brief adapter's top items are source titles, not independent
	// generic summaries. Withholding names also withholds those titles.
	if kind == "brief_ready" && !namesAllowed {
		return job, addresses, "no_source"
	}
	if kind == "waiting_on_reply" {
		job.MailFlag = proactiveVoiceMailFlag(source.RawMailSubject, source.RawMailBody)
		mailText := source.RawMailSubject + " " + source.RawMailBody
		if proactiveVoiceHealth.MatchString(mailText) || (job.MailFlag != "money" && proactiveVoiceRestricted.MatchString(mailText)) {
			return job, addresses, "unsafe_content"
		}
	}
	deny := []string{}
	if !namesAllowed {
		deny = proactiveVoiceWithheldNames(source)
	}
	clean := func(s string, limit int) string { return proactiveVoiceClean(s, deny, limit) }
	if namesAllowed && source.UserName != "" {
		job.User.Name = proactivevoice.Text(clean(source.UserName, 80))
	}
	refs := map[string]string{}
	for _, p := range source.People {
		if len(job.People) == 6 {
			break
		}
		old := p.Ref
		p.Ref = fmt.Sprintf("p%d", len(job.People)+1)
		p.RoleLabel = clean(p.RoleLabel, 50)
		if p.RoleLabel == "" {
			p.RoleLabel = "a colleague"
			if lang == "zh-CN" {
				p.RoleLabel = "同事"
			}
		}
		if p.Name != nil && namesAllowed {
			p.Name = proactivevoice.Text(clean(*p.Name, 80))
		} else {
			p.Name = nil
		}
		refs[old] = p.Ref
		job.People = append(job.People, p)
	}
	expected := map[string]string{"waiting_on_reply": "mail_thread", "meeting_prep": "calendar_event", "commitment_slipping": "commitment", "brief_ready": "brief", "capture_paused": "capture_status"}[kind]
	for _, raw := range source.Items {
		if len(job.Items) == 6 {
			break
		}
		if raw.Type != expected {
			continue
		}
		// Clone all pointer/slice fields so withholding cannot mutate evidence.
		encoded, _ := json.Marshal(raw)
		var item proactivevoice.Item
		_ = json.Unmarshal(encoded, &item)
		item = proactiveVoiceTypedItem(item)
		refs[item.Ref] = fmt.Sprintf("m%d", len(job.Items)+1)
		item.Ref = refs[item.Ref]
		item.Text, item.Label, item.Owner, item.With = "", "", "", ""
		item.Followups, item.FollowupsCount, item.LastTouch, item.Gap = nil, nil, nil, nil
		if kind == "waiting_on_reply" {
			job.Coverage["mail"] = "headers only"
			item.Subject, item.BodyExcerpt = nil, nil
			if source.MailBodyConsent && job.MailFlag == "none" {
				job.Coverage["mail"] = "headers and the body of this thread"
				item.Subject = proactivevoice.Text(source.RawMailSubject)
				item.BodyExcerpt = proactivevoice.Text(source.RawMailBody)
			}
		}
		for _, field := range []*string{item.Title, item.Subject, item.BodyExcerpt, item.NextStep, item.Progress, item.Place} {
			if field != nil && proactiveVoiceRestrictedText(*field) {
				return job, addresses, "unsafe_content"
			}
			if field != nil && proactiveVoiceRawDate.MatchString(*field) {
				return job, addresses, "would_need_guess"
			}
		}
		for _, field := range []**string{&item.FromName, &item.Title, &item.Subject, &item.Place} {
			if *field == nil {
				continue
			}
			if !namesAllowed {
				*field = nil
				continue
			}
			value := clean(**field, 120)
			*field = proactivevoice.Text(value)
		}
		for _, field := range []**string{&item.BodyExcerpt, &item.NextStep, &item.Progress} {
			if *field != nil {
				*field = proactivevoice.Text(clean(**field, 600))
			}
		}
		for _, field := range []*proactivevoice.Display{item.WaitingSince, item.Start, item.StartsIn, item.AttendeeTotal, item.Due, item.Slot, item.What, item.OffFor} {
			if field != nil {
				if proactiveVoiceRestrictedText(field.Display) {
					return job, addresses, "unsafe_content"
				}
				if proactiveVoiceRawDate.MatchString(field.Display) {
					return job, addresses, "would_need_guess"
				}
				// These displays are computed by the source adapter, not source
				// prose. Preserve adjacent date/time facts such as "Oct 16 10:00".
				field.Display = proactiveVoiceCleanValue(field.Display, deny, 100, false)
			}
		}
		people := []string{}
		for _, ref := range item.PeopleRefs {
			if mapped := refs[ref]; mapped != "" && len(people) < job.MaxNames {
				people = append(people, mapped)
			}
		}
		item.PeopleRefs = people
		if len(item.TopItems) > 2 {
			item.TopItems = item.TopItems[:2]
		}
		for i := range item.TopItems {
			top := &item.TopItems[i]
			if proactiveVoiceRestrictedText(top.Text) {
				return job, addresses, "unsafe_content"
			}
			if proactiveVoiceRawDate.MatchString(top.Text) {
				return job, addresses, "would_need_guess"
			}
			top.Ref = fmt.Sprintf("b%d", i+1)
			top.Text = clean(top.Text, 180)
		}
		job.Items = append(job.Items, item)
	}
	if len(job.Items) == 0 {
		return job, addresses, "no_source"
	}
	switch kind {
	case "waiting_on_reply":
		item := job.Items[0]
		if item.FromName == nil || *item.FromName == "" || item.WaitingSince == nil {
			return job, addresses, "no_source"
		}
		if item.Subject != nil && *item.Subject != "" {
			headline := fmt.Sprintf("%s has been waiting on your answer about %s since %s.", *item.FromName, *item.Subject, item.WaitingSince.Display)
			if lang == "zh-CN" {
				headline = fmt.Sprintf("%s 从%s起在等你回复“%s”。", *item.FromName, item.WaitingSince.Display, *item.Subject)
			}
			if utf8.RuneCountInString(headline) <= job.Limits.Headline.Max {
				job.HeadlineSubject = item.Subject
			}
		}
		if job.MailFlag == "none" && source.DraftRecipient != "" && strings.TrimSpace(source.RawMailSubject) != "" {
			address, err := mail.ParseAddress(source.DraftRecipient)
			if err == nil {
				if proactiveVoiceRestrictedText(source.DraftSubject) {
					return job, addresses, "unsafe_content"
				}
				job.DraftAllowed = true
				job.DraftTo = "addr_1"
				addresses[job.DraftTo] = address.Address
				job.DraftSubject = clean(source.DraftSubject, 160)
				if job.DraftSubject == "" {
					job.DraftSubject = "Re: " + source.RawMailSubject
				}
				draftLang := source.DraftLanguage
				if draftLang != "zh-CN" {
					draftLang = "en"
				}
				job.DraftLanguage = &draftLang
				job.ClosingLine = proactivevoice.ClosingLine(lang)
				job.Limits = proactivevoice.DefaultLimits(lang, true)
				if source.MailBodyConsent {
					for _, sample := range source.WritingSamples {
						if len(job.WritingSamples) == 3 {
							break
						}
						if proactiveVoiceRestrictedText(sample) {
							continue
						}
						if proactiveVoiceRawDate.MatchString(sample) {
							continue
						}
						job.WritingSamples = append(job.WritingSamples, proactivevoice.WritingSample{Ref: fmt.Sprintf("s%d", len(job.WritingSamples)+1), Text: clean(sample, 400)})
					}
				}
			}
		}
	case "meeting_prep":
		job.Coverage["calendar"] = "titles and times"
	case "commitment_slipping":
		if job.Items[0].Progress == nil || *job.Items[0].Progress != "none recorded" {
			return job, addresses, "would_need_guess"
		}
		job.Coverage["notes_and_chat"] = "your own words only"
	case "brief_ready":
		if len(job.Items[0].TopItems) == 0 {
			return job, addresses, "no_source"
		}
	}
	if job.DraftAllowed {
		for _, belief := range source.CommunicationBeliefs {
			if len(job.CommunicationBeliefs) == 6 {
				break
			}
			if belief.ID == "" || proactiveVoiceRestrictedText(belief.Text) || proactiveVoiceRawDate.MatchString(belief.Text) {
				continue
			}
			text := clean(belief.Text, 240)
			if text != "" {
				job.CommunicationBeliefs = append(job.CommunicationBeliefs, proactivevoice.CommunicationBelief{ID: belief.ID, Text: text})
			}
		}
	}
	if len(job.CommunicationBeliefs) > 0 {
		job.KindGuidance += " communication_beliefs shape only the draft's language, length and greeting. They are not evidence citations, never appear in evidence, and add no facts to headline, body or draft."
	}
	job.Actions = proactivevoice.Actions(kind, job.DraftAllowed)
	for _, words := range source.UserWords {
		if len(job.UserWords) == 5 {
			break
		}
		if words.Source != "chat" && words.Source != "notes" && !(words.Source == "mail_sent" && source.MailBodyConsent) {
			continue
		}
		if proactiveVoiceRestrictedText(words.Text) {
			return job, addresses, "unsafe_content"
		}
		if proactiveVoiceDay.MatchString(words.Text) && words.PromisedDay == nil {
			continue
		}
		localizedText, safeDate := proactiveVoiceDisplayUserDates(words.Text, words.PromisedDay, lang)
		if !safeDate {
			continue
		}
		words.Text = localizedText
		about := []string{}
		for _, p := range job.People {
			if p.Name != nil && proactiveVoiceExactContains(words.Text, *p.Name) && *p.Name != "" {
				about = append(about, p.Ref)
			}
		}
		for _, it := range job.Items {
			for _, title := range []*string{it.Title, it.Subject, it.FromName} {
				if title != nil && *title != "" && proactiveVoiceExactContains(words.Text, *title) {
					about = append(about, it.Ref)
					break
				}
			}
		}
		if len(about) == 0 {
			continue
		}
		attribution := map[string]string{"chat": "You told me", "notes": "Your notes say", "mail_sent": "You wrote in mail"}[words.Source]
		if lang == "zh-CN" {
			attribution = map[string]string{"chat": "你在聊天里告诉我", "notes": "你的笔记写道", "mail_sent": "你在邮件里写过"}[words.Source]
		}
		w := proactivevoice.UserWords{Ref: fmt.Sprintf("w%d", len(job.UserWords)+1), Source: words.Source, Text: clean(words.Text, 600), When: proactivevoice.Display{Display: proactiveVoiceDate(words.When, lang)}, Attribution: attribution, About: about}
		if words.PromisedDay != nil {
			status := "still ahead"
			day := *words.PromisedDay
			end := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, day.Location())
			if !now.Before(end) {
				status = "passed"
			}
			w.PromisedDay = &proactivevoice.PromisedDay{Display: proactiveVoiceDate(day, lang), Status: status}
		}
		job.UserWords = append(job.UserWords, w)
	}
	if len(job.UserWords) > 0 {
		job.Coverage["notes_and_chat"] = "your own words only"
	}
	encoded, err := json.Marshal(job)
	if err != nil || len(encoded) > proactiveVoiceJobLimit {
		return job, addresses, "over_limit"
	}
	if proactiveVoiceRawDate.Match(encoded) {
		return job, addresses, "would_need_guess"
	}
	return job, addresses, ""
}

// Only a date already resolved by the source adapter can replace an ISO date
// in the user's words. Other dates and timestamps are ambiguous evidence and
// the optional user-words item is omitted instead of guessing their meaning.
func proactiveVoiceDisplayUserDates(text string, promised *time.Time, lang string) (string, bool) {
	if !proactiveVoiceRawDate.MatchString(text) {
		return text, true
	}
	if promised == nil || proactiveVoiceRawTimestamp.MatchString(text) {
		return "", false
	}
	valid := true
	localized := proactiveVoiceRawDate.ReplaceAllStringFunc(text, func(raw string) string {
		day, err := time.ParseInLocation("2006-01-02", strings.ReplaceAll(raw, "/", "-"), promised.Location())
		if err != nil || day.Year() != promised.Year() || day.Month() != promised.Month() || day.Day() != promised.Day() {
			valid = false
			return ""
		}
		return proactiveVoiceDate(day, lang)
	})
	return localized, valid
}

func proactiveVoiceRestrictedText(text string) bool {
	return proactiveVoiceRestricted.MatchString(text) || proactiveVoiceHealth.MatchString(text)
}

// Rebuild the union from the type's fields, rather than trusting that an input
// Item only populated fields appropriate to that type. In particular, mail
// subject/body fields cannot ride along on a calendar or capture item.
func proactiveVoiceTypedItem(raw proactivevoice.Item) proactivevoice.Item {
	item := proactivevoice.Item{Ref: raw.Ref, Type: raw.Type}
	switch raw.Type {
	case "mail_thread":
		item.FromName, item.WaitingSince = raw.FromName, raw.WaitingSince
	case "calendar_event":
		item.Title, item.Start, item.StartsIn = raw.Title, raw.Start, raw.StartsIn
		item.Place, item.PeopleRefs, item.AttendeeTotal = raw.Place, raw.PeopleRefs, raw.AttendeeTotal
	case "commitment":
		item.Title, item.Due, item.Progress, item.NextStep = raw.Title, raw.Due, raw.Progress, raw.NextStep
	case "brief":
		item.Slot, item.TopItems = raw.Slot, raw.TopItems
	case "capture_status":
		item.What, item.OffFor = raw.What, raw.OffFor
	}
	return item
}

// Known contact names include first-name greetings, not only complete display
// names. Titles and places are withheld in free text as well as their fields.
func proactiveVoiceWithheldNames(source proactiveVoiceSource) []string {
	var deny []string
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[strings.ToLower(value)] {
			seen[strings.ToLower(value)] = true
			deny = append(deny, value)
		}
	}
	addName := func(name string) {
		add(name)
		for _, part := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			if utf8.RuneCountInString(part) > 1 {
				add(part)
			}
		}
	}
	for _, name := range source.ContactNames {
		addName(name)
	}
	addName(source.UserName)
	for _, person := range source.People {
		if person.Name != nil {
			addName(*person.Name)
		}
	}
	add(source.RawMailSubject)
	for _, item := range source.Items {
		if item.FromName != nil {
			addName(*item.FromName)
		}
		for _, field := range []*string{item.Title, item.Subject, item.Place} {
			if field != nil {
				add(*field)
			}
		}
	}
	return deny
}

// Match the exact display value, never a name inside a longer Latin token.
// Han names remain substring matches because Chinese has no word spaces.
func proactiveVoiceExactContains(text, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	latin := func(r rune) bool {
		return unicode.IsDigit(r) || r == '_' || unicode.IsLetter(r) && !unicode.In(r, unicode.Han)
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], value)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(value)
		before, after := rune(0), rune(0)
		if start > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:start])
		}
		if end < len(text) {
			after, _ = utf8.DecodeRuneInString(text[end:])
		}
		if (!latin(first) || !latin(before)) && (!latin(last) || !latin(after)) {
			return true
		}
		offset = start + 1
	}
	return false
}

func proactiveVoiceRemoveName(text, name string) string {
	pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(name))
	matches := pattern.FindAllStringIndex(text, -1)
	var clean strings.Builder
	previous := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		// Match against a small unchanged context so Unicode case folding does
		// not shift the byte indexes used to remove the original name.
		contextStart, contextEnd := start, end
		if start > 0 {
			_, width := utf8.DecodeLastRuneInString(text[:start])
			contextStart -= width
		}
		if end < len(text) {
			_, width := utf8.DecodeRuneInString(text[end:])
			contextEnd += width
		}
		if !proactiveVoiceExactContains(text[contextStart:contextEnd], text[start:end]) {
			continue
		}
		clean.WriteString(text[previous:start])
		previous = end
	}
	clean.WriteString(text[previous:])
	return clean.String()
}
