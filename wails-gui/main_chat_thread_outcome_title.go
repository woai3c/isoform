package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// threadOutcomeTitleLimit bounds a Left off card's title (chat-review v1.0.10 H6).
const threadOutcomeTitleLimit = 60

var threadOutcomeReportedSpeech = []string{
	"your assistant said that ", "your assistant said ", "your assistant says ",
	"the assistant said that ", "the assistant said ", "the assistant says ",
	"你的助手说", "助手说",
}

// applyThreadOutcomeWorkTitle makes a Left off card's title name the work in
// at most 60 characters. A title that is the assistant's reported speech, or
// too long, becomes the work's name and its state moves under it, before
// "Next: ...".
func applyThreadOutcomeWorkTitle(item *mainChatProactive, work string) {
	item.Headline, item.Body = threadOutcomeTitleAndBody(item.Headline, item.Body, work)
	for lang, copy := range item.Localized {
		copy.Headline, copy.Body = threadOutcomeTitleAndBody(copy.Headline, copy.Body, work)
		item.Localized[lang] = copy
	}
}

func threadOutcomeTitleAndBody(headline, body, work string) (string, string) {
	headline = strings.TrimSpace(headline)
	speech, reported := stripReportedSpeech(headline)
	if !reported && utf8.RuneCountInString(headline) <= threadOutcomeTitleLimit {
		return headline, body
	}
	title := clipAtWord(strings.TrimSpace(work), threadOutcomeTitleLimit)
	state := threadOutcomeState(speech)
	if title == "" {
		title = clipAtWord(strings.TrimRight(state, ".。"), threadOutcomeTitleLimit)
		state = ""
	}
	if state != "" && !strings.Contains(body, state) {
		body = strings.TrimSpace(state + " " + body)
	}
	return title, body
}

func stripReportedSpeech(text string) (string, bool) {
	lower := strings.ToLower(text)
	for _, prefix := range threadOutcomeReportedSpeech {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(text[len(prefix):]), true
		}
	}
	return text, false
}

// threadOutcomeState keeps the first sentence and drops a trailing ", and ..."
// clause: "the release is still building, not yet published, and public
// commits will ..." reads "The release is still building, not yet published."
func threadOutcomeState(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, ".。!?"); i >= 0 {
		text = text[:i]
	}
	if i := strings.Index(text, ", and "); i > 0 {
		text = text[:i]
	}
	text = strings.TrimRight(strings.TrimSpace(text), ",，")
	if text == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(text)
	text = string(unicode.ToUpper(first)) + text[size:]
	if last, _ := utf8.DecodeLastRuneInString(text); last < utf8.RuneSelf {
		return text + "."
	}
	return text + "。"
}

func clipAtWord(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	if i := strings.LastIndexAny(cut, " ,"); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,，")
}
