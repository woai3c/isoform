package main

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func mobileBriefHasHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func mobileBriefClosingParagraph(brief MobileBriefV1) MobileBriefPara {
	if n := len(brief.Narrative); n > 0 && strings.ContainsAny(brief.Narrative[n-1].Text, "?？") {
		return brief.Narrative[n-1]
	}
	return MobileBriefPara{}
}

// Bind answers to the question and all its original source facts. Topic
// navigation and display ref renumbering do not change this identity.
func mobileBriefClosingDigest(brief MobileBriefV1) string {
	para := mobileBriefClosingParagraph(brief)
	if para.Text == "" || len(para.Refs) == 0 {
		return ""
	}
	evidence := mobileBriefEvidenceByRef(brief)
	for _, ref := range para.Refs {
		if evidence[ref].SourceID == "" {
			return ""
		}
	}
	var facts []string
	for _, line := range mobileBriefSectionLines(brief.Sections) {
		for _, item := range line.items {
			for _, ref := range para.Refs {
				if slices.Contains(item.Refs, ref) {
					if item.EvidenceUnavailable {
						return ""
					}
					facts = append(facts, mobileBriefCardFacts(brief, item, "closing"))
					break
				}
			}
		}
	}
	extra := strings.Join(uniqueSortedStrings(facts), ":")
	if para.Hold != nil {
		extra += ":" + mobileBriefModelDigest(para.Hold)
	}
	return mobileBriefCardFacts(brief, MobileBriefItem{Title: para.Text, Refs: para.Refs}, extra)
}

func mobileBriefAnswerMatches(brief MobileBriefV1, card MobileBriefCard) bool {
	digest := mobileBriefClosingDigest(brief)
	return digest != "" && card.AnswerTo == digest && card.Facts == digest && card.Kind == mobileBriefCardAction &&
		card.Mode == mobileBriefCardSend && card.Target == nil && (card.Writer == mobileBriefCardWriterModel || card.Writer == mobileBriefCardWriterClosing) &&
		strings.Join(uniqueSortedStrings(card.Refs), "\x00") == strings.Join(uniqueSortedStrings(mobileBriefClosingParagraph(brief).Refs), "\x00")
}

// No pattern action stands in for a missing answer. In particular, an offer
// for another pick never becomes an answer just because it has a button label.
func mobileBriefClosingAnswers(brief MobileBriefV1, candidates []MobileBriefCard) []MobileBriefCard {
	var answers []MobileBriefCard
	seen := map[string]bool{}
	for _, card := range candidates {
		message, label := strings.TrimSpace(card.Message), strings.TrimSpace(card.Label)
		if len(answers) == 2 {
			break
		}
		if !mobileBriefAnswerMatches(brief, card) || label == "" || utf8.RuneCountInString(label) > mobileBriefCardLabelRunes ||
			strings.ContainsAny(label+message, "\n\r") || message == "" || len(message) > mobileBriefCardMessageBytes ||
			mobileBriefBannedOutcomePhrase(message) || mobileBriefBannedOutcomePhrase(label) ||
			redactThreadOutcomeText(label) != label || reEmoji.MatchString(label+message) || mobileBriefInterfaceSentence.MatchString(message) ||
			threadOutcomeTypingNarration.MatchString(message) || redactThreadOutcomeText(message) != message || seen[message] {
			continue
		}
		// A short negative answer can refer to the question without repeating
		// its subject. Other answers must name the question's subject.
		decline := slices.Contains([]string{"not now", "no, leave it", "no, leave it for now", "no, not now", "暂不", "不用，先放着", "不用了", "不，先不处理"}, strings.TrimRight(strings.ToLower(message), ".。"))
		if decline {
			// The decline reads "Not now" (暂不) whatever the writer chose
			// (today-review v1.1.2 #6, v1.1.4 #6).
			card.Label = "Not now"
			if mobileBriefHasHan(label + message) {
				card.Label = "暂不"
			}
		}
		subject, ok := mobileBriefCardSubject(brief, card.Refs)
		if !decline && (!ok || !mobileBriefNamesSubject(message, mobileBriefCardTitle(subject)) && !mobileBriefNamesSomeone(message, mobileBriefCardTitle(subject))) {
			continue
		}
		seen[message] = true
		card.Refs = append([]string(nil), card.Refs...)
		answers = append(answers, card)
	}
	return answers
}
