package main

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// One lesson, once (design review v1.0.1 W40): near-duplicate know-how shows
// as one card, and learning it again adds a moment to the copy already saved.

// KnowHowMoment is one time a lesson was learned, for Where it came from.
type KnowHowMoment struct {
	At        int64  `json:"at"`
	Assistant string `json:"assistant"` // "claude" or "codex" for assistant history
	App       string `json:"app"`
	SessionID string `json:"sessionId"`
}

const knowHowRelearnedKey = "relearned"

var knowHowLessonStopWords = map[string]bool{"the": true, "a": true, "an": true, "my": true, "your": true, "our": true}

func knowHowLessonTokens(title string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make(map[string]bool, len(words))
	for _, word := range words {
		if !knowHowLessonStopWords[word] {
			tokens[word] = true
		}
	}
	return tokens
}

func sameKnowHowLessonTokens(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	shared := 0
	for word := range a {
		if b[word] {
			shared++
		}
	}
	smaller := len(a)
	if len(b) < smaller {
		smaller = len(b)
	}
	// One title is the other with a word dropped ("deploys after repo rename").
	if shared == smaller && smaller >= 4 {
		return true
	}
	return float64(shared)/float64(len(a)+len(b)-shared) >= 0.8
}

func sameKnowHowLesson(a, b string) bool {
	return sameKnowHowLessonTokens(knowHowLessonTokens(a), knowHowLessonTokens(b))
}

func knowHowEntryMoments(entry KnowledgeEntry, card KnowHowCard) []KnowHowMoment {
	first := KnowHowMoment{At: card.SourceAt, Assistant: card.SourceAssistant}
	if first.At == 0 {
		first.At = card.SavedAt
	}
	// History and CLAUDE.md entries carry a project folder as their app.
	if card.Source != assistantHistorySource && card.Source != assistantHistoryClaudeMd && len(card.Apps) > 0 {
		first.App = card.Apps[0]
	}
	if len(entry.SessionIDs) > 0 {
		first.SessionID = entry.SessionIDs[0]
	}
	moments := []KnowHowMoment{first}
	if doc, ok := parseOKFDocument(entry.Content); ok {
		for _, raw := range okfStringSlice(doc.Frontmatter, knowHowRelearnedKey) {
			if moment, ok := parseKnowHowMoment(raw); ok {
				moments = append(moments, moment)
			}
		}
	}
	return moments
}

// A relearned moment is stored as "timestamp|assistant|app|session".
func formatKnowHowMoment(m KnowHowMoment) string {
	return strings.Join([]string{okfTimestamp(time.UnixMilli(m.At)), m.Assistant, m.App, m.SessionID}, "|")
}

func parseKnowHowMoment(raw string) (KnowHowMoment, bool) {
	parts := strings.SplitN(raw, "|", 4)
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	at := parseOKFTimestampMs(parts[0])
	if at <= 0 {
		return KnowHowMoment{}, false
	}
	return KnowHowMoment{At: at, Assistant: parts[1], App: parts[2], SessionID: parts[3]}, true
}

// mergeKnowHowLessons folds near-duplicate cards into the first one saved.
// Cards arrive newest first and leave in the same order.
func mergeKnowHowLessons(cards []KnowHowCard) []KnowHowCard {
	type lesson struct {
		card   KnowHowCard
		tokens map[string]bool
	}
	lessons := make([]lesson, 0, len(cards))
	for i := len(cards) - 1; i >= 0; i-- {
		card := cards[i]
		tokens := knowHowLessonTokens(card.Title)
		merged := false
		for j := range lessons {
			if !sameKnowHowLessonTokens(lessons[j].tokens, tokens) {
				continue
			}
			into := &lessons[j].card
			into.MergedIDs = append(into.MergedIDs, card.ID)
			into.MergedIDs = append(into.MergedIDs, card.MergedIDs...)
			into.Moments = append(into.Moments, card.Moments...)
			into.SessionCount += card.SessionCount
			if into.ReuseCount >= 0 && card.ReuseCount >= 0 {
				into.ReuseCount += card.ReuseCount
			} else {
				into.ReuseCount = -1
			}
			if card.SavedAt > into.SavedAt {
				into.SavedAt = card.SavedAt
			}
			into.Apps = compactStrings(append(into.Apps, card.Apps...))
			merged = true
			break
		}
		if !merged {
			lessons = append(lessons, lesson{card: card, tokens: tokens})
		}
	}
	out := make([]KnowHowCard, 0, len(lessons))
	for i := len(lessons) - 1; i >= 0; i-- {
		card := lessons[i].card
		sort.SliceStable(card.Moments, func(a, b int) bool { return card.Moments[a].At < card.Moments[b].At })
		out = append(out, card)
	}
	return out
}

// relearnKnowHow adds a moment to an already-saved copy of the same lesson
// and returns its ID, so learning it again adds nothing new. except is the
// entry being written, which is an update of itself rather than a copy.
func relearnKnowHow(dir, title, except string, moment KnowHowMoment) (string, bool) {
	tokens := knowHowLessonTokens(title)
	if len(tokens) == 0 {
		return "", false
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var match *KnowledgeEntry
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasSuffix(name, ".md") || strings.TrimSuffix(name, ".md") == except {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		entry := parseKnowledgeFile(name, string(data))
		if !sameKnowHowLessonTokens(tokens, knowHowLessonTokens(entry.Title)) {
			continue
		}
		if match == nil || parseOKFTimestampMs(entry.Date) < parseOKFTimestampMs(match.Date) {
			e := entry
			match = &e
		}
	}
	if match == nil {
		return "", false
	}
	unlock := memoryFileLocks.Acquire("knowledge:" + match.ID)
	defer unlock()
	path := filepath.Join(dir, match.ID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	doc, ok := parseOKFDocument(string(data))
	if !ok {
		return "", false
	}
	if moment.At <= 0 {
		moment.At = time.Now().UnixMilli()
	}
	seen := okfStringSlice(doc.Frontmatter, knowHowRelearnedKey)
	for _, raw := range seen {
		if prior, ok := parseKnowHowMoment(raw); ok && (prior.At == moment.At || (moment.SessionID != "" && prior.SessionID == moment.SessionID)) {
			return match.ID, true
		}
	}
	doc.Frontmatter[knowHowRelearnedKey] = append(seen, formatKnowHowMoment(moment))
	if err := os.WriteFile(path, []byte(renderOKFDocument(doc.Frontmatter, doc.Body)), 0600); err != nil {
		log.Printf("[know-how] relearned moment not saved: %v", err)
		return "", false
	}
	return match.ID, true
}
