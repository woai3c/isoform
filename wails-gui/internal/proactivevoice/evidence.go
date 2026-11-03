package proactivevoice

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type evidence struct {
	facts     []string
	text      []string
	names     []string
	titles    []string
	forbidden bool
}

func evidenceIndex(job Job) map[string]evidence {
	refs := make(map[string]evidence)
	add := func(ref string, entry evidence) {
		if previous, duplicate := refs[ref]; duplicate {
			previous.forbidden = true
			refs[ref] = previous
			return
		}
		refs[ref] = entry
	}
	for _, person := range job.People {
		e := evidence{text: []string{person.RoleLabel}}
		if person.Name != nil {
			e.names = []string{*person.Name}
			e.facts = append(e.facts, *person.Name)
		}
		add(person.Ref, e)
	}
	for _, item := range job.Items {
		e := evidence{}
		switch item.Type {
		case "mail_thread", "calendar_event", "commitment", "meeting", "purpose_item", "last_touch", "brief", "capture_status":
		default:
			e.forbidden = true
		}
		for _, display := range []*Display{item.WaitingSince, item.Start, item.StartsIn, item.AttendeeTotal, item.Due, item.FollowupsCount, item.LastTouch, item.Gap, item.Slot, item.What, item.OffFor} {
			if display != nil {
				e.facts = append(e.facts, display.Display)
			}
		}
		for _, title := range []*string{item.Title, item.Subject} {
			if title != nil {
				e.titles = append(e.titles, *title)
				e.facts = append(e.facts, *title)
			}
		}
		if item.FromName != nil {
			e.names = append(e.names, *item.FromName)
			e.facts = append(e.facts, *item.FromName)
		}
		if item.Place != nil {
			e.facts = append(e.facts, *item.Place)
		}
		for _, text := range []*string{item.BodyExcerpt, item.Progress, item.NextStep} {
			if text != nil {
				e.text = append(e.text, *text)
			}
		}
		if item.Text != "" {
			e.text = append(e.text, item.Text)
		}
		for _, followup := range item.Followups {
			e.text = append(e.text, followup.Text)
			add(followup.Ref, evidence{text: []string{followup.Text}})
		}
		for _, top := range item.TopItems {
			e.text = append(e.text, top.Text)
			add(top.Ref, evidence{text: []string{top.Text}})
		}
		add(item.Ref, e)
	}
	for _, words := range job.UserWords {
		e := evidence{facts: []string{words.When.Display}, text: []string{words.Text, words.Attribution}}
		if words.PromisedDay != nil {
			e.facts = append(e.facts, words.PromisedDay.Display)
		}
		add(words.Ref, e)
	}
	for _, sample := range job.WritingSamples {
		add(sample.Ref, evidence{forbidden: true})
	}
	return refs
}

// Refs are not printed in prose. A citation is used when one of its concrete
// facts or meaningful source words occurs in the output, not merely its id.
func evidenceMentioned(entry evidence, texts []string) bool {
	all := strings.Join(texts, "\n")
	for _, fact := range append(append([]string{}, entry.facts...), entry.text...) {
		if whole(all, fact) {
			return true
		}
		for _, token := range wordTokens(fact) {
			if !ordinaryWord(token) && utf8.RuneCountInString(token) >= 2 && whole(all, token) {
				return true
			}
		}
		// Chinese has no whitespace word boundaries. A shared two-character
		// sequence establishes use; facts themselves are checked separately.
		runes := []rune(fact)
		for i := 0; i+1 < len(runes); i++ {
			if unicode.In(runes[i], unicode.Han) && unicode.In(runes[i+1], unicode.Han) && strings.Contains(all, string(runes[i:i+2])) {
				return true
			}
		}
	}
	return false
}

func factValues(job Job, cited map[string]evidence) []string {
	facts := []string{job.Now.Display, job.DraftSubject}
	for _, entry := range cited {
		facts = append(facts, entry.facts...)
	}
	return facts
}

func factFields(job Job, cited map[string]evidence) string {
	return strings.Join(factValues(job, cited), "\n")
}

// Treat any punctuation, symbol or invisible format character between a
// number and a word as part of the quantity. An unrecognized separator must
// not reduce an unsupported quantity to a supported standalone number.
const quantitySeparator = `[\s\pZ\pP\pS\p{Cf}]`

var (
	numberPattern        = regexp.MustCompile(`[0-9]+(?:[:.][0-9]+)*`)
	quantityPattern      = regexp.MustCompile(`(?i)(?:[0-9]+(?:[:.][0-9]+)*|\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty)\b)(` + quantitySeparator + `*)((?:(?:business|working|calendar|square|cubic|nautical|light)` + quantitySeparator + `+)?[\pL\pM]+(?:[\p{Pd}\p{Cf}][\pL\pM]+)*)`)
	chineseNumberPattern = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]+)*|[零〇一二两三四五六七八九十百千万亿]+)` + quantitySeparator + `*(?:微秒|毫秒|秒钟|秒|分钟|分|刻钟|小时|钟头|天|星期|周年|周|个月|月|季度|季|年|个人|个|人|次|条|封|项|件|份|本|张|位|场|通|段|页|日)`)
	quotedPattern        = regexp.MustCompile(`"[^"\n]*"|“[^”\n]*”|「[^」\n]*」`)
	dayWords             = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December", "Jan", "Feb", "Mar", "Apr", "Jun", "Jul", "Aug", "Sep", "Sept", "Oct", "Nov", "Dec", "today", "tonight", "tomorrow", "yesterday", "day after tomorrow", "day before yesterday", "next week", "last week", "this week", "next month", "last month", "this month", "next year", "last year", "this year", "今天", "今日", "今早", "今晚", "明天", "明日", "明早", "明晚", "昨天", "昨日", "昨晚", "后天", "前天", "下周", "上周", "本周", "这周", "下个月", "上个月", "这个月", "明年", "去年", "今年", "周一", "周二", "周三", "周四", "周五", "周六", "周日", "周天", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六", "星期日", "星期天"}
	numberWords          = []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}
)

const dateWeekdays = `monday|tuesday|wednesday|thursday|friday|saturday|sunday|mon|tue|wed|thu|fri|sat|sun`
const dateMonths = `january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec`

var datePhrasePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:(?:` + dateWeekdays + `),?\s+)?(?:` + dateMonths + `)\s+[0-9]{1,2}(?:st|nd|rd|th)?(?:,?\s+[0-9]{4})?\b`),
	regexp.MustCompile(`(?i)\b(?:(?:` + dateWeekdays + `),?\s+)?[0-9]{1,2}(?:st|nd|rd|th)?\s+(?:` + dateMonths + `)(?:,?\s+[0-9]{4})?\b`),
	regexp.MustCompile(`(?i)\b(?:next|last|this)\s+(?:` + dateWeekdays + `|week|month|year|morning|afternoon|evening)\b`),
	regexp.MustCompile(`(?:[0-9]{4}\s*年\s*)?[0-9一二三四五六七八九十]+\s*月\s*[0-9一二三四五六七八九十]+\s*日(?:[（(](?:周|星期)[一二三四五六日天][）)])?`),
	regexp.MustCompile(`(?:上|下|本|这)周[一二三四五六日天]`),
}

// A compound date or count must occur in one display/title field. Flattening
// evidence would let the model combine one item's weekday with another date.
func supportedFactPhrase(job Job, cited map[string]evidence, phrase string) bool {
	for _, fact := range factValues(job, cited) {
		if whole(fact, phrase) {
			return true
		}
	}
	return false
}

func validQuantities(text string, job Job, cited map[string]evidence) bool {
	// Capture the entire following word, not a list of known English units:
	// a supported count cannot authorize an invented noun or an abbreviation.
	for _, match := range quantityPattern.FindAllStringSubmatch(text, -1) {
		phrase, separator, unit := match[0], match[1], match[2]
		first, _ := utf8.DecodeRuneInString(unit)
		if unicode.In(first, unicode.Han) || !whole(text, phrase) || money(unit) {
			continue
		}
		if quantityGrammar(unit) && quantityGrammarSeparator(separator) {
			continue
		}
		// A new sentence beginning with ordinary contract prose is not a
		// quantity suffix. Unit nouns still require evidence after a period.
		if strings.TrimSpace(separator) == "." && unicode.IsUpper(first) && quantitySentenceStart(unit) {
			continue
		}
		if !supportedFactPhrase(job, cited, phrase) {
			return false
		}
	}
	// Chinese units have no word separators. Long units precede their shorter
	// prefixes so an allowed 个 or 分 cannot authorize 个月 or 分钟.
	for _, phrase := range chineseNumberPattern.FindAllString(text, -1) {
		if !supportedChineseQuantity(job, cited, phrase) {
			return false
		}
	}
	return true
}

func quantitySentenceStart(word string) bool {
	// The proper-name prose allowlist also contains count nouns such as Day
	// and Meetings. Only unambiguous sentence starters may end a quantity.
	switch strings.ToLower(word) {
	case "i", "you", "we", "it", "they", "the", "a", "an", "this", "that", "these", "those", "here", "there", "please":
		return true
	default:
		return false
	}
}

func quantityGrammarSeparator(separator string) bool {
	// Preserve plain prose such as "Oct 15, and". Other punctuation is
	// ambiguous quantity syntax and must be grounded with the complete phrase.
	switch strings.TrimSpace(separator) {
	case "", ",":
		return true
	default:
		return false
	}
}

func supportedChineseQuantity(job Job, cited map[string]evidence, phrase string) bool {
	for _, fact := range factValues(job, cited) {
		for _, candidate := range chineseNumberPattern.FindAllString(fact, -1) {
			// Match parsed quantities rather than Han substrings: 十个 cannot
			// borrow its number from 二十个, nor 个 its unit from 个月.
			if compactSpace(candidate) == compactSpace(phrase) {
				return true
			}
		}
	}
	return false
}

func quantityGrammar(word string) bool {
	// A standalone date/time or count may be followed by ordinary grammar,
	// as in "Oct 15 and" or "20 is the count". These words are not units.
	switch strings.ToLower(word) {
	case "and", "or", "but", "is", "are", "was", "were", "has", "have", "had", "to", "for", "of", "in", "at", "on", "from", "with", "by", "before", "after", "since", "that", "which", "when", "then", "ago", "later":
		return true
	default:
		return false
	}
}

func validFacts(job Job, out Output, cited map[string]evidence, contactDenylist []string) bool {
	texts := []string{out.Headline, out.Body}
	if out.Draft != nil {
		texts = append(texts, out.Draft.Text)
	}
	all := strings.Join(texts, "\n")
	allowed := factFields(job, cited)
	quotedSources := allowed
	for _, entry := range cited {
		quotedSources += "\n" + strings.Join(entry.text, "\n")
	}
	// Quotes make a title or verbatim excerpt identifiable even when every
	// word is lowercase. The echo exemption does not authorize invented
	// quoted text: it must still occur in the cited evidence.
	for _, quoted := range quotedPattern.FindAllString(all, -1) {
		chars := []rune(quoted)
		if len(chars) > 2 && !whole(quotedSources, string(chars[1:len(chars)-1])) {
			return false
		}
	}
	// Check every token independently so 12 never matches 312, and compound
	// units prevent a supported "15 minutes" becoming "15 hours".
	for _, text := range texts {
		// Display numbers are assembled with ASCII decimal digits. Alternate
		// numeric scripts, fractions and numeral symbols cannot bypass them.
		if hasUnsupportedNumber(text) {
			return false
		}
		for _, pattern := range datePhrasePatterns {
			for _, phrase := range pattern.FindAllString(text, -1) {
				if !supportedFactPhrase(job, cited, phrase) {
					return false
				}
			}
		}
		for _, token := range numberPattern.FindAllString(text, -1) {
			if !whole(allowed, token) {
				return false
			}
		}
		for _, word := range append(append([]string{}, dayWords...), numberWords...) {
			if whole(text, word) && !whole(allowed, word) {
				return false
			}
		}
		if !validQuantities(text, job, cited) {
			return false
		}
	}
	// Every known title/place/name in the job must belong to a cited ref.
	for ref, entry := range evidenceIndex(job) {
		for _, value := range append(append([]string{}, entry.names...), entry.titles...) {
			if whole(all, value) {
				if _, ok := cited[ref]; !ok {
					return false
				}
			}
		}
	}
	named := 0
	knownNames := make([]string, 0, len(job.People))
	for _, person := range job.People {
		if person.Name == nil || *person.Name == "" {
			continue
		}
		name := *person.Name
		knownNames = append(knownNames, name)
		used := whole(all, name)
		// The reviewed draft uses a given name in its greeting, while the
		// headline uses the complete display name from the same person ref.
		for _, token := range wordTokens(name) {
			used = used || whole(all, token)
		}
		if used {
			if _, ok := cited[person.Ref]; !ok {
				return false
			}
			named++
		}
	}
	if named > job.MaxNames {
		return false
	}
	// A contact not represented by a people entry is never a model fact.
	// Names-off handling stays at check 13 to preserve the specified order.
	if job.NamesAllowed {
		for _, contact := range contactDenylist {
			if whole(all, contact) && !hasAny(strings.Join(knownNames, "\n"), []string{contact}) {
				return false
			}
		}
	}
	// English proper-name candidates that were invented do not gain authority
	// merely by appearing in unstructured evidence. Ordinary nouns and the
	// fixed prose of the contract are exempt.
	for _, token := range wordTokens(all) {
		first, _ := utf8.DecodeRuneInString(token)
		if unicode.IsUpper(first) && first < 128 && !ordinaryWord(token) && !whole(allowed, token) && !hasAny(strings.Join(knownNames, "\n"), []string{token}) {
			// Defer explicitly supplied contact names to the names denylist.
			if !job.NamesAllowed && hasAny(strings.Join(contactDenylist, "\n"), []string{token}) {
				continue
			}
			return false
		}
	}
	return true
}

func compactSpace(text string) string { return strings.Join(strings.Fields(text), " ") }

func wordTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '’' })
}

var ordinaryWords = strings.Fields(strings.ToLower("I I'll I'm I’m I've I'd You Your Yours We Our It Its The A An And Or But On In At To Of For From By With Without As Is Are Was Were Be Been Being Has Have Had Do Does Did Not No Only This That These Those There Here Where When What How Why Nothing Anything Something Everything Someone Everyone Now Please Thanks Thank Sorry Open Read Turn Review See Reply Answer Ask Check Draft Here Among Before After Since Then Still More All Some Any Each My Me Us Them Their Her Him His She He They Would Will Should Shall Could Can Cannot Couldn’t Couldnt Get Let Need Take Keep Make Look Way Usually Back Out Up Down Into About Over Under Again Progress Recorded Waiting Ready Meeting Meetings Event Calendar Mail Headers Thread Notes Chat Records Screen Capture Audio Computer Phone Settings Menu Bar Source Consent Present Content Information Available Prepared Help Status Work Brief Morning Afternoon Evening Follow Followups Follow-ups Next Smallest Step Stands Last Day Passed Patience Taken Long Design Review Budget Video Link Place Attending Total Count Report Drafted None Kind Copy Isoform Body Headline Evidence Actions Text Reason Abstain"))

func ordinaryWord(word string) bool {
	word = strings.ToLower(word)
	for _, known := range ordinaryWords {
		if word == known {
			return true
		}
	}
	return false
}

func echoes(job Job, out Output) bool {
	sources := []string{}
	exempt := []string{}
	for _, person := range job.People {
		if person.Name != nil {
			exempt = append(exempt, *person.Name)
		}
	}
	for _, item := range job.Items {
		for _, field := range []*string{item.Title, item.Subject, item.FromName} {
			if field != nil {
				exempt = append(exempt, *field)
			}
		}
		for _, field := range []*string{item.BodyExcerpt, item.Title, item.Subject, item.Progress, item.NextStep, item.Place, item.FromName} {
			if field != nil {
				sources = append(sources, *field)
			}
		}
		sources = append(sources, item.Text)
		for _, field := range item.TopItems {
			sources = append(sources, field.Text)
		}
		for _, field := range item.Followups {
			sources = append(sources, field.Text)
		}
	}
	for _, words := range job.UserWords {
		sources = append(sources, words.Text)
	}
	for _, sample := range job.WritingSamples {
		sources = append(sources, sample.Text)
	}
	targets := []struct{ text, language string }{{out.Headline, job.OutputLanguage}, {out.Body, job.OutputLanguage}}
	if out.Draft != nil {
		language := job.OutputLanguage
		if job.DraftLanguage != nil {
			language = *job.DraftLanguage
		}
		targets = append(targets, struct{ text, language string }{out.Draft.Text, language})
	}
	for _, target := range targets {
		text := quotedPattern.ReplaceAllString(target.text, "\x00")
		for _, value := range exempt {
			if value != "" {
				text = exemptWholeValue(text, value)
			}
		}
		for _, segment := range strings.Split(text, "\x00") {
			if target.language == "zh-CN" {
				chars := echoChars(segment)
				for _, source := range sources {
					original := string(echoChars(source))
					for i := 0; i+12 <= len(chars); i++ {
						if strings.Contains(original, string(chars[i:i+12])) {
							return true
						}
					}
				}
				continue
			}
			words := strings.Fields(strings.ToLower(segment))
			for i := range words {
				words[i] = strings.TrimFunc(words[i], unicode.IsPunct)
			}
			for _, source := range sources {
				sourceWords := strings.Fields(strings.ToLower(source))
				for i := range sourceWords {
					sourceWords[i] = strings.TrimFunc(sourceWords[i], unicode.IsPunct)
				}
				joined := " " + strings.Join(sourceWords, " ") + " "
				for i := 0; i+6 <= len(words); i++ {
					if strings.Contains(joined, " "+strings.Join(words[i:i+6], " ")+" ") {
						return true
					}
				}
			}
		}
	}
	return false
}

// A copied name or title exempts that exact value, not its letters inside a
// different word (for example, the name Lin inside the ordinary word Link).
func exemptWholeValue(text, value string) string {
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	var result strings.Builder
	previous := 0
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], value)
		if index < 0 {
			break
		}
		start, end := offset+index, offset+index+len(value)
		before, after := rune(0), rune(0)
		if start > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:start])
		}
		if end < len(text) {
			after, _ = utf8.DecodeRuneInString(text[end:])
		}
		if (!latinToken(first) || !latinToken(before)) && (!latinToken(last) || !latinToken(after)) {
			result.WriteString(text[previous:start])
			result.WriteByte(0)
			previous = end
		}
		offset = end
	}
	result.WriteString(text[previous:])
	return result.String()
}

func echoChars(text string) []rune {
	return []rune(strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, text))
}
