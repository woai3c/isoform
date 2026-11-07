package main

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	mobileBriefMinWords = 150
	mobileBriefMaxWords = 300
)

// Chinese has no word separators. Two Han characters count as one reading
// unit, alongside whitespace-delimited words, for a comparable speech budget.
func mobileBriefWordCount(text string) int {
	words, han, inWord := 0, 0, false
	for _, r := range text {
		switch true {
		case unicode.Is(unicode.Han, r):
			han++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if !inWord {
				words++
			}
			inWord = true
		case unicode.IsSpace(r) || unicode.IsPunct(r):
			inWord = false
		}
	}
	return words + (han+1)/2
}

func mobileBriefNarrativeWords(paragraphs []MobileBriefPara) int {
	texts := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		texts = append(texts, paragraph.Text)
	}
	return mobileBriefWordCount(strings.Join(texts, "\n"))
}

// Shorten Highlights first, one whole line at a time. Forward paragraphs are
// never sliced mid-sentence. The caller uses the compact source-backed template
// if forward work alone exceeds the budget. Copy before editing stored text.
func trimMobileBriefNarrative(brief MobileBriefV1, paragraphs []MobileBriefPara) []MobileBriefPara {
	out := append([]MobileBriefPara{}, paragraphs...)
	highlightRefs := map[string]bool{}
	for _, item := range mobileBriefChiefSections(brief).Highlights {
		for _, ref := range item.Refs {
			highlightRefs[ref] = true
		}
	}
	for i := len(out) - 1; i >= 0 && mobileBriefNarrativeWords(out) > mobileBriefMaxWords; i-- {
		if out[i].Text == mobileBriefClosingText(brief, "en") || out[i].Text == mobileBriefClosingText(brief, "zh-CN") ||
			i == len(out)-1 && strings.ContainsAny(out[i].Text, "?？") {
			continue
		}
		isHighlight := len(out[i].Refs) > 0
		for _, ref := range out[i].Refs {
			isHighlight = isHighlight && highlightRefs[ref]
		}
		if !isHighlight {
			continue
		}
		lines := strings.Split(out[i].Text, "\n")
		for len(lines) > 0 && mobileBriefNarrativeWords(out) > mobileBriefMaxWords {
			lines = lines[:len(lines)-1]
			out[i].Text = strings.Join(lines, "\n")
		}
		if len(lines) == 0 {
			out = append(out[:i], out[i+1:]...)
		}
	}
	return out
}

func prepareMobileBriefNarrative(brief MobileBriefV1, paragraphs []MobileBriefPara, language string) []MobileBriefPara {
	out, fits := fitMobileBriefNarrative(brief, paragraphs, language)
	if !fits {
		if brief.rankedEdition {
			return nil
		}
		return mobileBriefNarrativeTemplate(brief, language)
	}
	return out
}

// The model caller needs the fit result to retain its honest unavailable
// status when it substitutes a template; legacy playback needs only the text.
func fitMobileBriefNarrative(brief MobileBriefV1, paragraphs []MobileBriefPara, language string) ([]MobileBriefPara, bool) {
	out := []MobileBriefPara{}
	for _, paragraph := range paragraphs {
		paragraph.Text = normalizeBriefText(strings.TrimSpace(paragraph.Text), language, mobileBriefTextReference(brief), false)
		if paragraph.Text == "" {
			continue
		}
		// Over-long text is trimmed to whole sentences, never silenced (design R74 v1.0.1).
		paragraph.Text = mobileBriefTrimSentences(paragraph.Text, mobileBriefNarrativeMaxBytes)
		out = append(out, paragraph)
	}
	out = trimMobileBriefNarrative(brief, out)
	return out, mobileBriefNarrativeWords(out) <= mobileBriefMaxWords
}

// mobileBriefSentenceEnd splits text into sentences: each ends at a terminal mark
// (a full stop only when a space or the end follows, so "1.5" stays whole).
var mobileBriefSentenceEnd = regexp.MustCompile(`[^.!?。！？]*(?:[.!?。！？]+(?:\s|$)|[^.!?。！？]+$)`)

// mobileBriefTrimSentences keeps whole sentences of text within limit bytes (design
// R74 v1.0.1): over-long text is never cut mid-sentence and never silenced for length.
// If even the first sentence is over the limit, it is cut at the last word boundary
// that fits, the only cut that can leave the limit.
func mobileBriefTrimSentences(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	kept := ""
	for _, sentence := range mobileBriefSentenceEnd.FindAllString(text, -1) {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		next := sentence
		if kept != "" {
			next = kept + " " + sentence
		}
		if len(next) > limit {
			break
		}
		kept = next
	}
	if kept != "" {
		return kept
	}
	cut := text[:limit]
	if i := strings.LastIndexAny(cut, " \n\t"); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut)
}

// mobileBriefTrimParagraphs trims every paragraph to the per-paragraph limit, so the
// shown words and the spoken words are the same trimmed text.
func mobileBriefTrimParagraphs(paragraphs []MobileBriefPara) []MobileBriefPara {
	out := make([]MobileBriefPara, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph.Text = mobileBriefTrimSentences(paragraph.Text, mobileBriefNarrativeMaxBytes)
		if paragraph.Text != "" {
			out = append(out, paragraph)
		}
	}
	return out
}
