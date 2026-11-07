package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Source timestamps remain available for provenance. Display labels omit
// clock times, including ones embedded in a calendar title or summary.
var mobileBriefDisplayClock = regexp.MustCompile(`(?i)\b(?:[0-2]?[0-9][:：][0-5][0-9](?::[0-5][0-9])?\s*(?:a\.?m\.?|p\.?m\.?)?|(?:0?[1-9]|1[0-2])\s*(?:a\.?m\.?|p\.?m\.?))\b|(?:凌晨|早上|上午|中午|下午|晚上)?[0-2]?[0-9](?:点|時|时)(?:[0-5]?[0-9]分?|半)?`)

var mobileBriefDisplayClockRange = regexp.MustCompile(`(?:` + mobileBriefDisplayClock.String() + `)\s*(?:to|[-–—]|至)\s*(?:` + mobileBriefDisplayClock.String() + `)`)

var mobileBriefClockJoiner = regexp.MustCompile(`(?i)\b(?:at|from|to|between)\s*(?:[-–—,;.]\s*)?$`)
var mobileBriefDeviceName = regexp.MustCompile(`(?i)(?:\b(your|this|my|the user's)\s+(MacBook Pro|MacBook Air|MacBook|Mac|iMac|iPhone)\b|(你的|这台|这个|我的|用户的)\s*(MacBook Pro|MacBook Air|MacBook|Mac|iMac|iPhone)\b[ \t]*)`)
var mobileBriefQuotedText = regexp.MustCompile(`"[^"\r\n]*"|“[^”\r\n]*”|「[^」\r\n]*」|『[^』\r\n]*』|《[^》\r\n]*》`)

type mobileBriefTextRange struct {
	start int
	end   int
}

func mobileBriefProtectedTextRanges(text string, protected []string) []mobileBriefTextRange {
	ranges := mobileBriefQuotedText.FindAllStringIndex(text, -1)
	wordByte := func(value byte) bool {
		return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
	}
	for _, value := range protected {
		value = strings.TrimSpace(value)
		if value == "" || !mobileBriefDeviceName.MatchString(value) {
			continue
		}
		matcher, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(value))
		if err != nil {
			continue
		}
		for _, found := range matcher.FindAllStringIndex(text, -1) {
			start, end := found[0], found[1]
			if (start == 0 || !wordByte(text[start-1])) && (end == len(text) || !wordByte(text[end])) {
				ranges = append(ranges, []int{start, end})
			}
		}
	}
	if len(ranges) == 0 {
		return nil
	}
	ordered := make([]mobileBriefTextRange, 0, len(ranges))
	for _, r := range ranges {
		ordered = append(ordered, mobileBriefTextRange{start: r[0], end: r[1]})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].start == ordered[j].start {
			return ordered[i].end > ordered[j].end
		}
		return ordered[i].start < ordered[j].start
	})
	merged := ordered[:0]
	for _, r := range ordered {
		if len(merged) > 0 && r.start <= merged[len(merged)-1].end {
			if r.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = r.end
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

func mobileBriefNextDeviceWord(value string) (string, int, int) {
	start := 0
	for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
		start++
	}
	if start < len(value) && value[start] >= '0' && value[start] <= '9' {
		return value[start : start+1], start, start + 1
	}
	end := start
	for end < len(value) && (value[end] >= 'a' && value[end] <= 'z' || value[end] >= 'A' && value[end] <= 'Z') {
		end++
	}
	return value[start:end], start, end
}

func mobileBriefBlockedDeviceWord(word string) bool {
	if word == "" {
		return false
	}
	if word[0] >= '0' && word[0] <= '9' {
		return true
	}
	switch strings.ToLower(word) {
	case "mini", "pro", "studio", "air", "app", "store":
		return true
	}
	return false
}

func replaceMobileBriefOwnVoice(value string) string {
	matches := mobileBriefDeviceName.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var out strings.Builder
	at := 0
	for _, match := range matches {
		out.WriteString(value[at:match[0]])
		pronoun, device, chinese := "", "", false
		if match[2] >= 0 {
			pronoun, device = value[match[2]:match[3]], value[match[4]:match[5]]
		} else {
			pronoun, device, chinese = value[match[6]:match[7]], value[match[8]:match[9]], true
		}
		end := match[1]
		suffix, suffixStart, suffixEnd := mobileBriefNextDeviceWord(value[end:])
		consumed := false
		if strings.EqualFold(device, "Mac") && strings.EqualFold(suffix, "mini") {
			end += suffixEnd
			consumed = true
		} else if chinese && strings.EqualFold(device, "Mac") && strings.HasPrefix(value[end+suffixStart:], "电脑") {
			end += suffixStart + len("电脑")
			consumed = true
		}
		if consumed {
			suffix, _, _ = mobileBriefNextDeviceWord(value[end:])
		}
		if mobileBriefBlockedDeviceWord(suffix) {
			out.WriteString(value[match[0]:match[1]])
			at = match[1]
			continue
		}
		switch {
		case chinese && strings.EqualFold(device, "iPhone"):
			out.WriteString(pronoun + "手机")
		case chinese:
			out.WriteString(pronoun + "电脑")
		case strings.EqualFold(device, "iPhone"):
			out.WriteString(pronoun + " phone")
		default:
			out.WriteString(pronoun + " computer")
		}
		at = end
	}
	out.WriteString(value[at:])
	return out.String()
}

// rewriteMobileBriefOwnVoice changes only Isoform's own device references.
// Complete quotations and the person's own names stay byte-for-byte intact.
func rewriteMobileBriefOwnVoice(text string, protected ...string) (string, bool) {
	if !mobileBriefDeviceName.MatchString(text) {
		return text, true
	}
	ranges := mobileBriefProtectedTextRanges(text, protected)
	var out strings.Builder
	clean := true
	at := 0
	writeOwn := func(value string) {
		if mobileBriefDeviceName.MatchString(value) && strings.ContainsAny(value, "\"“”「」『』《》") {
			clean = false
		}
		out.WriteString(replaceMobileBriefOwnVoice(value))
	}
	for _, r := range ranges {
		writeOwn(text[at:r.start])
		out.WriteString(text[r.start:r.end])
		at = r.end
	}
	writeOwn(text[at:])
	return out.String(), clean
}

func splitMobileBriefOwnVoiceSentences(text string, protected []string) []string {
	ranges := mobileBriefProtectedTextRanges(text, protected)
	if len(ranges) == 0 {
		return splitIntoSentences(text)
	}
	runes := []rune(text)
	offsets := make([]int, 0, len(runes)+1)
	for start := range text {
		offsets = append(offsets, start)
	}
	offsets = append(offsets, len(text))
	masked := append([]rune(nil), runes...)
	rangeAt := 0
	for i, start := range offsets[:len(offsets)-1] {
		for rangeAt < len(ranges) && start >= ranges[rangeAt].end {
			rangeAt++
		}
		if rangeAt == len(ranges) {
			break
		}
		if start < ranges[rangeAt].start {
			continue
		}
		switch masked[i] {
		case '.', '!', '?':
			masked[i] = 'x'
		case '。', '！', '？':
			masked[i] = 'Ｘ'
		case '\n':
			masked[i] = ' '
		}
	}
	var sentences []string
	at := 0
	for _, sentence := range splitIntoSentences(string(masked)) {
		end := at + utf8.RuneCountInString(sentence)
		if end > len(runes) {
			return splitIntoSentences(text)
		}
		sentences = append(sentences, text[offsets[at]:offsets[end]])
		at = end
	}
	if at != len(runes) {
		return splitIntoSentences(text)
	}
	return sentences
}

func mobileBriefOwnVoiceText(text string, protected ...string) string {
	if !mobileBriefDeviceName.MatchString(text) {
		return text
	}
	var kept strings.Builder
	for _, sentence := range splitMobileBriefOwnVoiceSentences(text, protected) {
		rewritten, clean := rewriteMobileBriefOwnVoice(sentence, protected...)
		if clean {
			kept.WriteString(rewritten)
		}
	}
	return strings.TrimSpace(kept.String())
}

func mobileBriefWithoutClockTimes(value string) string {
	if !mobileBriefDisplayClock.MatchString(value) {
		return value
	}
	value = mobileBriefDisplayClockRange.ReplaceAllString(value, "")
	value = mobileBriefDisplayClock.ReplaceAllString(value, "")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSpace(mobileBriefClockJoiner.ReplaceAllString(value, ""))
	return strings.Trim(value, " \t-–—,;")
}

func mobileBriefVoiceLabel(value string) string {
	value = mobileBriefWithoutClockTimes(value)
	value = reEmoji.ReplaceAllString(value, "")
	value = strings.NewReplacer("!", ".", "！", "。").Replace(value)
	return mobileBriefPromptLine(value)
}

// Legacy rows remain the evidence ledger. Reproject old stored briefs too,
// with forward work first and the bounded retrospective last.
func mobileBriefNarrativeSections(brief MobileBriefV1) []mobileBriefSectionLine {
	sections := mobileBriefChiefSections(brief)
	var review []MobileBriefItem
	for _, item := range sections.Review {
		if item.Kind != mobileBriefAttendanceSource || item.Status != "unknown" {
			review = append(review, item)
		}
	}
	var plan []MobileBriefItem
	for _, item := range sections.Plan {
		if !mobileBriefFutureCalendarItem(brief, item) {
			plan = append(plan, item)
		}
	}
	for _, item := range sections.WorkingToward {
		// Commitment cards own reminders, including their main-chat holdback;
		// independent progress remains available in the bounded Purpose Map.
		if item.Kind != "commitment" {
			plan = append(plan, item)
		}
	}
	return []mobileBriefSectionLine{
		{"Decide", sections.Decide},
		{"Review", review},
		{"Plan", plan},
		{"Waiting on you", sections.WaitingOnYou},
		{"Due", sections.Commitments},
		{"Day ahead", sections.DayAhead},
		{"Highlights", sections.Highlights},
	}
}

func mobileBriefNarrativeInputEvidence(brief MobileBriefV1) []MobileBriefRef {
	// Evidence used only by a commitment already reported in main chat stays
	// out, as in the stored document; see mobileBriefNarrativeEvidenceForDocument.
	narratable := mobileBriefNarrativeEvidenceForDocument(brief)
	allowed := make(map[string]bool, len(narratable))
	for _, ref := range narratable {
		allowed[ref.Ref] = true
	}
	used := map[string]bool{}
	for _, section := range mobileBriefNarrativeSections(brief) {
		for _, item := range section.items {
			if !mobileBriefItemNarratable(item, allowed) || item.ChatMessageID != "" {
				continue
			}
			for _, ref := range item.Refs {
				used[ref] = true
			}
		}
	}
	// The Purpose Map sections reach the model too, so their anchored refs
	// are citable (mobile_brief_anchor.go).
	all := mobileBriefEvidenceByRef(brief)
	for _, items := range [][]MobileBriefItem{brief.Sections.WorkingToward, brief.Sections.Commitments, brief.Sections.WaitingOnYou} {
		for _, item := range items {
			if len(item.Refs) == 0 || !mobileBriefItemNarratable(item, allowed) || item.ChatMessageID != "" || mobileBriefItemAnchor(item, all) == "" {
				continue
			}
			for _, ref := range item.Refs {
				used[ref] = true
			}
		}
	}
	if len(used) > 0 {
		for _, item := range mobileBriefNarrativePeople(brief) {
			for _, ref := range item.Refs {
				used[ref] = true
			}
		}
	}
	out := make([]MobileBriefRef, 0, len(used))
	for _, ref := range narratable {
		if used[ref.Ref] {
			out = append(out, ref)
		}
	}
	return out
}

// People context stays limited to existing principal sightings. Mail headers
// and arbitrary people-item details never enter this join, and a sighting
// cannot create a paragraph or an obligation by itself.
func mobileBriefNarrativePeople(brief MobileBriefV1) []MobileBriefItem {
	allowed := map[string]bool{}
	for _, ref := range brief.Evidence {
		if ref.SourceKind == "principal" {
			allowed[ref.Ref] = true
		}
	}
	var out []MobileBriefItem
	for _, item := range brief.Sections.People {
		if len(item.Refs) == 0 || !mobileBriefItemNarratable(item, allowed) || mobileBriefVoiceLabel(item.Title) == "" {
			continue
		}
		out = append(out, item)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func mobileBriefFutureCalendarItem(brief MobileBriefV1, item MobileBriefItem) bool {
	if item.Status != "upcoming" {
		return false
	}
	asOf, asOfErr := time.Parse(time.RFC3339, brief.AsOf)
	at, atErr := time.Parse(time.RFC3339, item.At)
	if asOfErr != nil || atErr != nil || at.Before(asOf) {
		return false
	}
	for _, ref := range brief.Evidence {
		if ref.SourceKind != mobileBriefCalendarSource {
			continue
		}
		for _, itemRef := range item.Refs {
			if itemRef == ref.Ref {
				return true
			}
		}
	}
	return false
}

// mobileBriefFutureClocks are the clock times each ref may be written with:
// only an upcoming calendar item's own time, as the brief displays it.
func mobileBriefFutureClocks(brief MobileBriefV1, languageName string) map[string]map[string]bool {
	clocksByRef := map[string]map[string]bool{}
	loc := mobileBriefNarrativeLocation(brief)
	clock := mobileBriefClockLayout(languageName)
	for _, section := range mobileBriefNarrativeSections(brief) {
		for _, item := range section.items {
			if !mobileBriefFutureCalendarItem(brief, item) {
				continue
			}
			for _, ref := range item.Refs {
				if clocksByRef[ref] == nil {
					clocksByRef[ref] = map[string]bool{}
				}
				for _, token := range mobileBriefDisplayClock.FindAllString(mobileBriefLocalTime(item.At, loc, clock), -1) {
					clocksByRef[ref][strings.TrimSpace(token)] = true
				}
			}
		}
	}
	return clocksByRef
}

// mobileBriefClocksSupported reports whether every clock time in text belongs
// to one of the refs it is cited with.
func mobileBriefClocksSupported(text string, refs []string, clocksByRef map[string]map[string]bool) bool {
	for _, token := range mobileBriefDisplayClock.FindAllString(text, -1) {
		allowed := false
		for _, ref := range refs {
			allowed = allowed || clocksByRef[ref][strings.TrimSpace(token)]
		}
		if !allowed {
			return false
		}
	}
	return true
}

// mobileBriefVoiceBroken reports an exclamation, an emoji or an own-device reference.
func mobileBriefVoiceBroken(text string, protected ...string) bool {
	rewritten, clean := rewriteMobileBriefOwnVoice(text, protected...)
	return !clean || rewritten != text || strings.ContainsAny(text, "!！") || reEmoji.MatchString(text)
}

func mobileBriefQuotedContent(brief MobileBriefV1) []string {
	var quoted []string
	add := func(value string) {
		if strings.TrimSpace(value) != "" {
			quoted = append(quoted, value)
		}
	}
	for _, ref := range brief.Evidence {
		add(ref.Label)
	}
	for _, line := range append(mobileBriefSectionLines(brief.Sections), mobileBriefSectionLine{"upcoming", brief.Upcoming}) {
		for _, item := range line.items {
			add(item.Title)
			if item.Context != nil {
				add(item.Context.SourceLabel)
				add(item.Context.Preview)
			}
		}
	}
	for _, item := range brief.MeetingAttendance {
		add(item.Title)
	}
	for _, paragraph := range append(append([]MobileBriefPara{}, brief.Narrative...), brief.WeeklyRecap...) {
		add(paragraph.TopicName)
	}
	if brief.DayShape != nil {
		if brief.DayShape.TodayDayShape != nil {
			for _, event := range brief.DayShape.Events {
				add(event.Title)
				add(event.ShortName)
				add(event.ShortestName)
			}
		}
		for _, app := range brief.DayShape.TopApps {
			add(app.Name)
		}
	}
	if brief.YourWeek != nil {
		for _, row := range brief.YourWeek.Time {
			add(row.Name)
		}
		for _, row := range brief.YourWeek.Done {
			add(row.Title)
		}
		for _, row := range brief.YourWeek.Open {
			add(row.Title)
		}
		for _, row := range brief.YourWeek.Learned {
			if row.Kind == yourWeekLearnedMemory {
				add(row.Text)
			}
		}
		if brief.YourWeek.Ask != nil {
			add(brief.YourWeek.Ask.Subject)
		}
	}
	if brief.WeekQuestion != nil {
		add(brief.WeekQuestion.Recommendation)
	}
	return quoted
}

// mobileBriefWithOwnVoice is the generation-time boundary for every brief
// sentence Isoform writes. Source-owned fields and quotations are not inputs.
func mobileBriefWithOwnVoice(brief MobileBriefV1) MobileBriefV1 {
	protected := mobileBriefQuotedContent(brief)
	rewrite := func(value string) string {
		return mobileBriefOwnVoiceText(value, protected...)
	}
	lost := func(before, after string) bool {
		return strings.TrimSpace(before) != "" && after == ""
	}
	rewriteHold := func(hold *TodayDayShapeHold) *TodayDayShapeHold {
		if hold == nil {
			return nil
		}
		out := *hold
		out.Label = rewrite(out.Label)
		return &out
	}

	brief.Narrative = append([]MobileBriefPara(nil), brief.Narrative...)
	for i := range brief.Narrative {
		if !mobileBriefIsOpenAnswerParagraph(brief, brief.Narrative[i]) {
			brief.Narrative[i].Text = rewrite(brief.Narrative[i].Text)
			brief.Narrative[i].Hold = rewriteHold(brief.Narrative[i].Hold)
		}
	}
	brief.WeeklyRecap = append([]MobileBriefPara(nil), brief.WeeklyRecap...)
	brief.Cards = append([]MobileBriefCard(nil), brief.Cards...)
	brief.Offers = append([]MobileBriefOffer(nil), brief.Offers...)
	brief.MeetingAttendance = append([]MobileBriefAttendance(nil), brief.MeetingAttendance...)
	brief.WeekLead = rewrite(brief.WeekLead)
	if brief.WeekLead == "" {
		brief.WeekLeadFacts = ""
	}
	if brief.Generation != nil {
		generation := *brief.Generation
		generation.Message = rewrite(generation.Message)
		brief.Generation = &generation
	}
	if brief.Audio != nil {
		audio := *brief.Audio
		audio.StatusMessage = rewrite(audio.StatusMessage)
		brief.Audio = &audio
	}
	if brief.YourWeek != nil {
		week := *brief.YourWeek
		week.Headline = rewrite(week.Headline)
		week.Open = append([]MobileBriefWeekOpen(nil), week.Open...)
		for i := range week.Open {
			label := rewrite(week.Open[i].Label)
			if lost(week.Open[i].Label, label) {
				week.Open[i].CardID = ""
			}
			week.Open[i].Label = label
		}
		week.Learned = append([]MobileBriefWeekLearned(nil), week.Learned...)
		for i := range week.Learned {
			if week.Learned[i].Kind == yourWeekLearnedMute {
				week.Learned[i].Text = rewrite(week.Learned[i].Text)
			}
		}
		if week.Ask != nil {
			ask := *week.Ask
			ask.Text, ask.Message = rewrite(ask.Text), rewrite(ask.Message)
			ask.Hold = rewriteHold(ask.Hold)
			if lost(week.Ask.Text, ask.Text) || lost(week.Ask.Message, ask.Message) {
				week.Ask = nil
			} else {
				week.Ask = &ask
			}
		}
		brief.YourWeek = &week
	}
	if brief.DayShape != nil {
		day := *brief.DayShape
		if day.TodayDayShape != nil {
			shape := *day.TodayDayShape
			shape.Hold = rewriteHold(shape.Hold)
			day.TodayDayShape = &shape
		}
		brief.DayShape = &day
	}
	learnedIndex := 0
	for i := range brief.WeeklyRecap {
		if brief.WeeklyRecap[i].Role == yourWeekParagraphLearned {
			kind := yourWeekLearnedMemory
			if brief.YourWeek != nil && learnedIndex < len(brief.YourWeek.Learned) {
				kind = brief.YourWeek.Learned[learnedIndex].Kind
			}
			learnedIndex++
			if kind == yourWeekLearnedMemory {
				continue
			}
		}
		if brief.WeeklyRecap[i].Role == yourWeekParagraphAsk && brief.YourWeek != nil && brief.YourWeek.Ask == nil {
			brief.WeeklyRecap[i].Text = ""
			continue
		}
		brief.WeeklyRecap[i].Text = rewrite(brief.WeeklyRecap[i].Text)
		brief.WeeklyRecap[i].Hold = rewriteHold(brief.WeeklyRecap[i].Hold)
	}

	cards := make([]MobileBriefCard, 0, len(brief.Cards))
	for _, card := range brief.Cards {
		label, message := rewrite(card.Label), rewrite(card.Message)
		if lost(card.Label, label) || lost(card.Message, message) {
			continue
		}
		card.Label, card.Message = label, message
		cards = append(cards, card)
	}
	brief.Cards = cards
	offers := make([]MobileBriefOffer, 0, len(brief.Offers))
	for _, offer := range brief.Offers {
		label, prompt, text := rewrite(offer.Label), rewrite(offer.Prompt), rewrite(offer.Text)
		if lost(offer.Label, label) || lost(offer.Prompt, prompt) || lost(offer.Text, text) {
			continue
		}
		offer.Label, offer.Prompt, offer.Text = label, prompt, text
		offers = append(offers, offer)
	}
	brief.Offers = offers

	for _, items := range []*[]MobileBriefItem{
		&brief.Sections.Noticed, &brief.Sections.Decide, &brief.Sections.Review,
		&brief.Sections.Plan, &brief.Sections.WaitingOnYou, &brief.Sections.Commitments,
		&brief.Sections.WorkingToward, &brief.Sections.DayAhead, &brief.Sections.Highlights,
		&brief.Sections.Moved, &brief.Sections.Doing, &brief.Sections.Watching,
		&brief.Sections.Meetings, &brief.Sections.People, &brief.Sections.OpenLoops,
		&brief.Sections.Learned, &brief.Sections.Deliverables, &brief.Sections.Schedules, &brief.Upcoming,
	} {
		*items = append([]MobileBriefItem(nil), (*items)...)
		for i := range *items {
			item := &(*items)[i]
			item.Detail = rewrite(item.Detail)
			item.NextStep = rewrite(item.NextStep)
			item.AttentionReason = rewrite(item.AttentionReason)
			if item.Outcome != nil {
				outcome := *item.Outcome
				outcome.Summary = rewrite(outcome.Summary)
				outcome.NextStep = rewrite(outcome.NextStep)
				outcome.Question = rewrite(outcome.Question)
				outcome.UserActed = rewrite(outcome.UserActed)
				item.Outcome = &outcome
			}
		}
	}
	for i := range brief.MeetingAttendance {
		brief.MeetingAttendance[i].Reason = rewrite(brief.MeetingAttendance[i].Reason)
	}
	brief = mobileBriefHygiene(brief)
	brief.ConversationKey = mobileBriefConversationKey(brief)
	return brief
}

// mobileBriefNarrativeTemplate is the no-model path. It uses the same short
// groups, titles and evidence refs, never source content. The only item detail
// is a vague Purpose's goal or map step, as its own sentence; the narrative
// model never sees the template. Each group contributes one next step rather
// than a source inventory.
func mobileBriefNarrativeTemplate(brief MobileBriefV1, languageName string) []MobileBriefPara {
	// A pathological source title cannot consume the forward budget. Rebuild
	// complete template actions with shorter titles rather than cutting prose.
	for titleBytes := mobileBriefHighlightBytes; ; titleBytes /= 2 {
		out := mobileBriefNarrativeTemplateTitles(brief, languageName, titleBytes)
		for i := range out {
			out[i].Text = normalizeBriefText(out[i].Text, languageName, mobileBriefTextReference(brief), false)
		}
		out = trimMobileBriefNarrative(brief, out)
		var spoken []MobileBriefPara
		for _, paragraph := range out {
			spoken = append(spoken, MobileBriefPara{Text: mobileBriefParagraphSpeech(brief, paragraph, languageName)})
		}
		if mobileBriefNarrativeWords(out) <= mobileBriefMaxWords && mobileBriefNarrativeWords(spoken) <= mobileBriefMaxWords || titleBytes <= 10 {
			return out
		}
	}
}

func mobileBriefNarrativeTemplateTitles(brief MobileBriefV1, languageName string, titleBytes int) []MobileBriefPara {
	evidence := mobileBriefNarrativeInputEvidence(brief)
	kept := make(map[string]bool, len(evidence))
	for _, ref := range evidence {
		kept[ref.Ref] = true
	}
	lang := "en"
	if strings.Contains(strings.ToLower(languageName), "chinese") || strings.HasPrefix(strings.ToLower(languageName), "zh") {
		lang = "zh"
	}
	out := []MobileBriefPara{}
	questionUsed := false
	var forward []MobileBriefPara
	var highlights []string
	var highlightRefs []string
	for _, section := range mobileBriefNarrativeSections(brief) {
		// Each forward group states one item: the one that most needs
		// attention, so the brief says what its closing offers.
		items := append([]MobileBriefItem{}, section.items...)
		sort.SliceStable(items, func(i, j int) bool {
			tier := func(item MobileBriefItem) int {
				if item.AttentionRank > 0 {
					return item.AttentionRank
				}
				return mobileBriefAttentionStalled + 1
			}
			return tier(items[i]) < tier(items[j])
		})
		for _, item := range items {
			if len(item.Refs) == 0 || !mobileBriefItemNarratable(item, kept) || item.ChatMessageID != "" {
				continue
			}
			titleLimit := titleBytes
			if section.name == "Highlights" {
				if item.Kind == mobileBriefThreadOutcomeSource {
					titleLimit = 140
				} else {
					titleLimit /= 2
				}
			}
			title := mobileBriefText(mobileBriefVoiceLabel(item.Title), titleLimit)
			if title == "" || mobileBriefBannedOutcomePhrase(title) {
				continue
			}
			text := mobileBriefTemplateItem(brief, item, section.name, title, lang, languageName)
			if strings.ContainsAny(text, "?？") {
				if questionUsed {
					continue
				}
				questionUsed = true
			}
			if section.name == "Highlights" {
				if text != "" && len(highlights) < mobileBriefChiefLimit {
					if len(highlights) == 0 {
						text = pick(lang, "Highlights: ", "亮点：") + text
					}
					highlights = append(highlights, mobileBriefText(text, mobileBriefHighlightBytes))
					highlightRefs = append(highlightRefs, item.Refs...)
				}
				continue
			}
			if text == "" || mobileBriefBannedOutcomePhrase(text) || len(text) > mobileBriefNarrativeMaxBytes {
				continue
			}
			forward = append(forward, MobileBriefPara{Text: text, Refs: append([]string{}, item.Refs...)})
			break
		}
	}
	// Two short forward items per paragraph leave room for every forward
	// group, while keeping the existing four-paragraph wire contract.
	for _, paragraph := range forward {
		if len(out) > 0 && len(out[len(out)-1].Text)+len(paragraph.Text)+1 <= mobileBriefNarrativeMaxBytes &&
			len(strings.Split(out[len(out)-1].Text, "\n")) < 2 {
			last := &out[len(out)-1]
			last.Text += "\n" + paragraph.Text
			last.Refs = append(last.Refs, paragraph.Refs...)
		} else if len(out) < mobileBriefNarrativeMaxParagraphs-2 {
			out = append(out, paragraph)
		}
	}
	if len(highlights) > 0 {
		out = append(out, MobileBriefPara{Text: strings.Join(highlights, "\n"), Refs: uniqueSortedStrings(highlightRefs)})
	}
	closing, _ := mobileBriefClosing(brief, languageName)
	for !mobileBriefNarrativeWithinBudget(append(out, closing)) && len(out) > 0 {
		out = out[:len(out)-1]
	}
	if closing.Text != "" {
		out = append(out, closing)
	}
	return out
}

func mobileBriefClosingText(brief MobileBriefV1, language string) string {
	closing, _ := mobileBriefClosing(brief, language)
	return closing.Text
}

func mobileBriefTemplateItem(brief MobileBriefV1, item MobileBriefItem, section, title, lang, languageName string) string {
	reference, loc := mobileBriefTextReference(brief), mobileBriefNarrativeLocation(brief)
	since := mobileBriefSinceWords(mobileBriefParseTime(item.Since), reference, loc, lang)
	sentence := func(text string) string {
		text = strings.TrimSpace(text)
		if text == "" || strings.HasSuffix(text, ".") || strings.HasSuffix(text, "。") || strings.HasSuffix(text, ".”") || strings.HasSuffix(text, "。”") {
			return text
		}
		return text + pick(lang, ".", "。")
	}
	// English says titles plainly for speech; Chinese keeps “” around them.
	subject := mobileBriefItemNoun(title, item.Kind, lang)
	if section == "Day ahead" || item.Kind == mobileBriefAttendanceSource {
		subject = mobileBriefNounSubject(title, lang)
	}
	capital := mobileBriefCapitalize(subject)
	// Every carried-over line says how old it is (backlog item 53).
	age := ""
	if since != "" {
		age = pick(lang, " It has been open "+since+".", "这件事"+since+"一直在待办里。")
	}
	if item.Kind == mobileBriefThreadOutcomeSource {
		text := sentence(redactThreadOutcomeText(title))
		if section == "Highlights" {
			return text
		}
		if item.Outcome != nil && item.Outcome.State == "incomplete" {
			return text + age
		}
		if item.NextStep != "" {
			text += pick(lang, " ", "") + sentence(mobileBriefVoiceLabel(redactThreadOutcomeText(item.NextStep)))
		}
		return text + age
	}
	if item.Kind == mobileBriefAttendanceSource {
		switch item.Status {
		case "skipped":
			return pick(lang, fmt.Sprintf("%s has no matching call on this computer.", capital), fmt.Sprintf("这台电脑上%s没有对应通话。", subject))
		case "unknown":
			// The meeting by its cleaned name, as the person would say it; no
			// line asks about something a person can't place (v1.1.5 #6).
			return pick(lang, fmt.Sprintf("Isoform couldn't tell whether you attended your %s.", mobileBriefLowerFirst(strings.TrimSpace(title))), fmt.Sprintf("Isoform 没法确定你是否参会：%s。", mobileBriefNounSubject(title, lang)))
		case "attended":
			return title
		}
	}
	if item.Kind == "waiting_on_reply" {
		if since != "" {
			return pick(lang, fmt.Sprintf("%s has been waiting on your reply %s.", title, since), fmt.Sprintf("%s%s一直在等你回复。", title, since))
		}
		return pick(lang, fmt.Sprintf("%s is waiting on your reply.", title), fmt.Sprintf("%s在等你回复。", title))
	}
	if item.Status == "no_recent_touch" {
		last := mobileBriefParseTime(item.LastTouch)
		if last.IsZero() {
			last = mobileBriefParseTime(item.Since)
		}
		if day := mobileBriefDayWords(last, reference, loc, lang); day != "" {
			return pick(lang, fmt.Sprintf("There is no recorded touch with %s since %s.", title, day), fmt.Sprintf("%s以来没有和%s的联系记录。", day, title))
		}
		return pick(lang, fmt.Sprintf("There is no recorded touch with %s in three weeks.", title), fmt.Sprintf("近三周没有和%s的联系记录。", title))
	}
	// A vague title gets its goal or next step as a sentence of its own.
	context := ""
	if value, next := mobileBriefSubjectContext(brief, item, title, true); value != "" {
		context = pick(lang, " The goal is "+mobileBriefLowerFirst(value)+".", "目标是"+value+"。")
		if next {
			context = pick(lang, " The next step is to "+mobileBriefLowerFirst(value)+".", "下一步是"+value+"。")
		}
	}
	stalled := ""
	// Progress that stopped later than the item opened is worth its own line;
	// the same day twice is not.
	if last := mobileBriefParseTime(item.LastTouch); item.AttentionRank == mobileBriefAttentionStalled && item.AttentionReason != "" &&
		mobileBriefCivilDays(last, mobileBriefParseTime(item.Since), loc) > 0 {
		stalled = pick(lang, " ", "") + sentence(item.AttentionReason)
	}
	task, isTask := mobileBriefTaskPhrase(title, item.Kind, lang)
	if item.Kind == "commitment" {
		isTask = false
	}
	open := func(next string) string {
		if due := mobileBriefDueWords(mobileBriefParseTime(item.DueAt), reference, loc, lang); due != "" {
			if isTask {
				return fmt.Sprintf("Your task to %s %s.", task, due) + context + stalled
			}
			return pick(lang, fmt.Sprintf("%s %s.", capital, due), fmt.Sprintf("%s%s。", subject, due)) + context + stalled
		}
		if since != "" {
			if isTask {
				return fmt.Sprintf("Your task to %s has been open %s.", task, since) + context + stalled
			}
			return pick(lang, fmt.Sprintf("%s has been open in your plan %s.", capital, since), fmt.Sprintf("%s%s一直在你的计划里。", subject, since)) + context + stalled
		}
		return next + context + stalled
	}
	switch section {
	case "Decide", "Due":
		if isTask {
			return open(fmt.Sprintf("Your task to %s is still open.", task))
		}
		return open(pick(lang, fmt.Sprintf("%s is still open in your plan.", capital), fmt.Sprintf("%s仍在你的计划中。", subject)))
	case "Review":
		if since != "" {
			return pick(lang, fmt.Sprintf("%s has been ready for your review %s.", capital, since), fmt.Sprintf("%s%s一直等你审阅。", subject, since))
		}
		return pick(lang, fmt.Sprintf("%s is ready for your review.", capital), fmt.Sprintf("%s等你审阅。", subject))
	case "Plan":
		if isTask {
			return open(fmt.Sprintf("Your next task is to %s.", task))
		}
		return open(pick(lang, fmt.Sprintf("%s is next in your plan.", capital), fmt.Sprintf("%s在接下来的计划中。", subject)))
	case "Waiting on you":
		if since != "" {
			return pick(lang, fmt.Sprintf("%s has been waiting %s.", capital, since), fmt.Sprintf("%s%s一直在等待中。", subject, since)) + context
		}
		return pick(lang, fmt.Sprintf("%s is waiting on a next step.", capital), fmt.Sprintf("%s在等待下一步。", subject)) + context
	case "Day ahead":
		when := mobileBriefWhen(item, reference, loc, lang)
		if when == "" {
			return pick(lang, fmt.Sprintf("%s is on your calendar.", capital), fmt.Sprintf("日历中有%s。", subject))
		}
		return pick(lang, fmt.Sprintf("%s is %s.", capital, when), fmt.Sprintf("%s在%s。", subject, when))
	case "Highlights":
		return title
	default:
		return ""
	}
}
