package main

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A document whose name shares the words of an open commitment is linked to
// it: "Q3 deck.key" works toward "Send Sam the Q3 deck". The match is a
// plain comparison of words, never a model call, and only open commitments
// take part. It is worked out when needed from what is stored, so nothing
// is left behind when a document or a commitment goes away.
const (
	// fileLinkMinShared is how many words a document and a commitment must
	// share; one word is enough only when it is long and is all the
	// document's name says.
	fileLinkMinShared   = 2
	fileLinkLoneMinRune = 5
	fileLinkMinRunes    = 3
)

// fileLinkStopWords are words too common in file names or commitments to
// tie the two together.
var fileLinkStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "about": true,
	"this": true, "that": true, "your": true, "our": true, "you": true, "are": true,
	"final": true, "draft": true, "copy": true, "untitled": true, "document": true,
	"doc": true, "docs": true, "file": true, "files": true, "new": true, "notes": true,
	"note": true, "version": true, "edit": true, "edited": true, "review": true,
	"send": true, "update": true, "updated": true, "finish": true, "make": true,
	"write": true, "get": true, "screenshot": true, "screen": true, "shot": true,
	"image": true, "img": true, "scan": true, "export": true, "download": true,
	"pdf": true, "docx": true, "xlsx": true, "pptx": true, "key": true, "pages": true,
	"numbers": true, "txt": true, "md": true, "png": true, "jpg": true, "jpeg": true,
}

// fileLinkWords are the significant words of a text: lowercased runs of
// letters or digits, at least three runes or carrying a digit ("q3"), not
// pure numbers, not stop words; a run of Han characters counts as one word
// when it is at least two characters.
func fileLinkWords(text string) map[string]bool {
	words := map[string]bool{}
	var b strings.Builder
	han := false
	flush := func() {
		word := b.String()
		b.Reset()
		if word == "" {
			return
		}
		runes := utf8.RuneCountInString(word)
		hasDigit, hasLetter := false, false
		for _, r := range word {
			if unicode.IsDigit(r) {
				hasDigit = true
			} else {
				hasLetter = true
			}
		}
		switch {
		case !hasLetter:
			return
		case han && runes < 2:
			return
		case !han && runes < fileLinkMinRunes && !hasDigit:
			return
		case fileLinkStopWords[word]:
			return
		}
		words[word] = true
	}
	for _, r := range strings.ToLower(text) {
		isHan := unicode.Is(unicode.Han, r)
		switch {
		case isHan:
			if !han {
				flush()
			}
			han = true
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if han {
				flush()
			}
			han = false
			b.WriteRune(r)
		default:
			flush()
			han = false
		}
	}
	flush()
	return words
}

// fileCommitmentScore is how many words a document's name shares with a
// commitment, or 0 when that is not enough to link them.
func fileCommitmentScore(nameWords, commitmentWords map[string]bool) int {
	shared, longest := 0, 0
	for word := range nameWords {
		if commitmentWords[word] {
			shared++
			longest = max(longest, utf8.RuneCountInString(word))
		}
	}
	switch {
	case shared >= fileLinkMinShared:
		return shared
	case shared == 1 && len(nameWords) == 1 && longest >= fileLinkLoneMinRune:
		return 1
	default:
		return 0
	}
}

// fileCommitment is the commitment a document is linked to.
type fileCommitment struct {
	ID    string
	Title string
}

// linkFilesToCommitments links each document to the open commitment its name
// shares the most words with, the most recently changed one on a tie. It
// returns the links by document id.
func linkFilesToCommitments(rows []FileActivityRow, details []PurposeOpenLoopDetail) map[string]fileCommitment {
	type commitment struct {
		fileCommitment
		words     map[string]bool
		changedAt int64
	}
	var open []commitment
	for _, detail := range details {
		if detail.OpenLoop.Kind != PurposeKindCommitment || !purposeStateOpen(detail.Current.LifecycleState) {
			continue
		}
		title := strings.TrimSpace(detail.Current.Title)
		words := fileLinkWords(title)
		if title == "" || len(words) == 0 {
			continue
		}
		open = append(open, commitment{
			fileCommitment: fileCommitment{ID: detail.OpenLoop.ID, Title: title},
			words:          words,
			changedAt:      detail.Current.OccurredAt,
		})
	}
	if len(open) == 0 {
		return nil
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].changedAt != open[j].changedAt {
			return open[i].changedAt > open[j].changedAt
		}
		return open[i].ID < open[j].ID
	})
	links := map[string]fileCommitment{}
	for _, row := range rows {
		stem := strings.TrimSuffix(row.Name, filepath.Ext(row.Name))
		words := fileLinkWords(stem)
		if len(words) == 0 {
			continue
		}
		best, bestScore := fileCommitment{}, 0
		for _, c := range open {
			if score := fileCommitmentScore(words, c.words); score > bestScore {
				best, bestScore = c.fileCommitment, score
			}
		}
		if bestScore > 0 {
			links[row.ID] = best
		}
	}
	return links
}
