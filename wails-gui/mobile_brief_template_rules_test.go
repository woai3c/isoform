package main

import (
	"fmt"
	"strings"
	"time"
)

// The rules the template follows (mobileBriefNarrativeTemplate): its order,
// its ages, its one question and its voice. Before backlog item 65 they also
// held the model's text; the written brief now has its own checks
// (mobileBriefNarrativeFromReply), and these stay to keep the template honest.

// The model may copy a clock only from a cited upcoming calendar item.
// A future event elsewhere in the brief cannot justify a past clock claim.
func validateMobileBriefNarrativeVoice(brief MobileBriefV1, paragraphs []MobileBriefPara, languageName string) error {
	if !mobileBriefNarrativeWithinBudget(paragraphs) {
		return mobileBriefNarrativeRuleErr(mobileBriefRuleBudget, "narrative exceeds reading budget")
	}
	partialRefs := mobileBriefPartialRefs(brief)
	closing, _ := mobileBriefClosing(brief, languageName)
	questions := 0
	// One question in all: the closing's offers, or, without them, one
	// pointed question about an observation that ends before its outcome.
	for _, paragraph := range paragraphs {
		questions += strings.Count(paragraph.Text, "?") + strings.Count(paragraph.Text, "？")
		if strings.ContainsAny(paragraph.Text, "?？") {
			supported := paragraph.Text == closing.Text
			for _, ref := range paragraph.Refs {
				supported = supported || partialRefs[ref]
			}
			if !supported {
				return mobileBriefNarrativeRuleErr(mobileBriefRuleQuestion, "question without an incomplete observation")
			}
		}
	}
	if questions > 1 {
		return mobileBriefNarrativeRuleErr(mobileBriefRuleQuestion, "narrative asks more than one question")
	}
	if err := validateMobileBriefNarrativeAges(brief, paragraphs, languageName); err != nil {
		return err
	}
	for _, paragraph := range paragraphs {
		if mobileBriefBannedOutcomePhrase(paragraph.Text) {
			return mobileBriefNarrativeRuleErr(mobileBriefRuleBanned, "narrative contains banned uncertainty copy")
		}
	}
	if err := validateMobileBriefNarrativeOrder(brief, paragraphs); err != nil {
		return err
	}
	clocksByRef := mobileBriefFutureClocks(brief, languageName)
	for _, paragraph := range paragraphs {
		if mobileBriefVoiceBroken(paragraph.Text) {
			return mobileBriefNarrativeRuleErr(mobileBriefRuleVoice, "narrative violates the voice rules")
		}
		if !mobileBriefClocksSupported(paragraph.Text, paragraph.Refs, clocksByRef) {
			return mobileBriefNarrativeRuleErr(mobileBriefRuleClock, "narrative contains an unsupported clock time")
		}
	}
	return nil
}

// mobileBriefPartialRefs are the refs of observations that end before their
// outcome: the only ones a question outside the closing may be about.
func mobileBriefPartialRefs(brief MobileBriefV1) map[string]bool {
	partialRefs := map[string]bool{}
	for _, item := range brief.Sections.Moved {
		if item.Outcome != nil && item.Outcome.State == "incomplete" {
			for _, ref := range item.Refs {
				partialRefs[ref] = true
			}
		}
	}
	for _, item := range brief.MeetingAttendance {
		if item.State == "unknown" {
			for _, ref := range item.Refs {
				partialRefs[ref] = true
			}
		}
	}
	return partialRefs
}

// Carried-over work the text speaks of names its age or its due day in a
// paragraph that cites it (backlog item 53); a later paragraph may mention it
// again without repeating the age. A reply that never names one falls back to
// the template, which always says it.
func validateMobileBriefNarrativeAges(brief MobileBriefV1, paragraphs []MobileBriefPara, languageName string) error {
	closing, _ := mobileBriefClosing(brief, languageName)
	if len(mobileBriefAgesUnnamed(paragraphs, mobileBriefAgeTokens(brief, languageName), closing.Text)) > 0 {
		return mobileBriefNarrativeRuleErr(mobileBriefRuleAge, "narrative omits the age of carried-over work")
	}
	return nil
}

// mobileBriefAgeTokens are, for each carried-over item's primary ref, the
// words that name its age or its due day.
func mobileBriefAgeTokens(brief MobileBriefV1, languageName string) map[string][]string {
	lang := mobileBriefVoiceLang(languageName)
	reference, loc := mobileBriefTextReference(brief), mobileBriefNarrativeLocation(brief)
	tokens := map[string][]string{}
	for _, section := range mobileBriefNarrativeSections(brief) {
		if section.name == "Highlights" || section.name == "Day ahead" {
			continue
		}
		for _, item := range section.items {
			since := mobileBriefParseTime(item.Since)
			if len(item.Refs) == 0 || !mobileBriefCarriedOver(since, reference, loc) {
				continue
			}
			if _, ok := tokens[item.Refs[0]]; !ok {
				tokens[item.Refs[0]] = mobileBriefDayTokens([]time.Time{since, mobileBriefParseTime(item.DueAt)}, reference, loc, lang)
			}
		}
	}
	return tokens
}

// mobileBriefAgesUnnamed are the carried-over refs the paragraphs (other than
// the closing) cite without naming their age or due day in any of them.
func mobileBriefAgesUnnamed(paragraphs []MobileBriefPara, tokens map[string][]string, closing string) map[string]bool {
	cited, named := map[string]bool{}, map[string]bool{}
	for _, paragraph := range paragraphs {
		if paragraph.Text == closing {
			continue
		}
		text := strings.ToLower(paragraph.Text)
		for _, ref := range paragraph.Refs {
			words, ok := tokens[ref]
			if !ok {
				continue
			}
			cited[ref] = true
			for _, word := range words {
				named[ref] = named[ref] || word != "" && strings.Contains(text, strings.ToLower(word))
			}
		}
	}
	unnamed := map[string]bool{}
	for ref := range cited {
		if !named[ref] {
			unnamed[ref] = true
		}
	}
	return unnamed
}

// mobileBriefDayTokens are the ways a paragraph may name a day: the relative
// words, the weekday and the written date, in the brief's language.
func mobileBriefDayTokens(times []time.Time, reference time.Time, loc *time.Location, lang string) []string {
	var out []string
	for _, at := range times {
		if at.IsZero() {
			continue
		}
		local := at.In(loc)
		out = append(out, mobileBriefDayWords(at, reference, loc, lang))
		if lang == "zh" {
			out = append(out, []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[local.Weekday()],
				[]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[local.Weekday()],
				fmt.Sprintf("%d月%d日", local.Month(), local.Day()))
			continue
		}
		out = append(out, local.Weekday().String(), local.Format("January 2"), local.Format("Jan 2"))
	}
	return out
}

// Citations keep retrospective lines confined to one short final block. A
// provider that ignores the ordering or length rules falls back to the template.
func validateMobileBriefNarrativeOrder(brief MobileBriefV1, paragraphs []MobileBriefPara) error {
	allowed := map[string]bool{}
	for _, ref := range mobileBriefNarrativeInputEvidence(brief) {
		allowed[ref.Ref] = true
	}
	sections := mobileBriefNarrativeSections(brief)
	ranks, highlightRefs, hasForward := mobileBriefNarrativeRanks(brief, sections, allowed)
	lastRank, seenForward, seenHighlights := -1, false, false
	for i, paragraph := range paragraphs {
		if i == len(paragraphs)-1 && (paragraph.Text == mobileBriefClosingText(brief, "en") || paragraph.Text == mobileBriefClosingText(brief, "zh-CN")) {
			continue
		}
		rank := mobileBriefParagraphRank(paragraph, ranks, sections)
		highlight, forward := false, false
		for _, ref := range paragraph.Refs {
			if highlightRefs[ref] {
				highlight = true
			} else {
				forward = true
			}
		}
		if rank < lastRank {
			return mobileBriefNarrativeRuleErr(mobileBriefRuleOrder, "narrative sections are out of order")
		}
		lastRank = rank
		if highlight {
			if forward || seenHighlights || i != len(paragraphs)-1 && i != len(paragraphs)-2 || hasForward && !seenForward {
				return mobileBriefNarrativeRuleErr(mobileBriefRuleHighlights, "Highlights must be a separate final block after forward work")
			}
			lines := strings.Split(paragraph.Text, "\n")
			if len(lines) > mobileBriefChiefLimit {
				return mobileBriefNarrativeRuleErr(mobileBriefRuleHighlights, "too many Highlights lines")
			}
			for _, line := range lines {
				if strings.TrimSpace(line) == "" || len(line) > mobileBriefHighlightBytes || mobileBriefDisplayClock.MatchString(line) {
					return mobileBriefNarrativeRuleErr(mobileBriefRuleHighlights, "Highlights must be short lines without clock times")
				}
			}
			seenHighlights = true
		} else {
			seenForward = true
		}
	}
	return nil
}

// mobileBriefNarrativeRanks places each narratable ref in the brief's order
// (Decide, Review, Plan, Waiting on you, Due, Day ahead, Highlights): the
// first section that holds it.
func mobileBriefNarrativeRanks(brief MobileBriefV1, sections []mobileBriefSectionLine, allowed map[string]bool) (map[string]int, map[string]bool, bool) {
	ranks := map[string]int{}
	highlightRefs := map[string]bool{}
	hasForward := false
	for rank, section := range sections {
		for _, item := range section.items {
			if item.ChatMessageID != "" || !mobileBriefItemNarratable(item, allowed) {
				continue
			}
			for _, ref := range item.Refs {
				if section.name == "Highlights" {
					highlightRefs[ref] = true
				} else {
					hasForward = true
				}
				if _, ok := ranks[ref]; !ok {
					ranks[ref] = rank
				}
			}
		}
	}
	return ranks, highlightRefs, hasForward
}

// mobileBriefParagraphRank is a paragraph's place in the order: that of its
// earliest-placed ref. A ref outside the sections (a Purpose Map goal or
// commitment) is forward work placed with what is due, before the day ahead.
func mobileBriefParagraphRank(paragraph MobileBriefPara, ranks map[string]int, sections []mobileBriefSectionLine) int {
	unplaced := len(sections)
	for i, section := range sections {
		if section.name == "Due" {
			unplaced = i
		}
	}
	rank := len(sections)
	for _, ref := range paragraph.Refs {
		if r, ok := ranks[ref]; ok {
			rank = min(rank, r)
		} else {
			rank = min(rank, unplaced)
		}
	}
	return rank
}
