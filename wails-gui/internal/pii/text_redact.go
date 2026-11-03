package pii

import (
	"strings"
	"sync/atomic"
)

// redactTextCalls counts RedactText scans, so tests can prove that hot
// readers reuse redactions instead of scanning again on every read.
var redactTextCalls atomic.Int64

// RedactTextCalls reports how many times RedactText has scanned text.
func RedactTextCalls() int64 { return redactTextCalls.Load() }

// redactHit is an internal match record used by RedactText.
type redactHit struct {
	start, end int
	category   string
}

// RedactText scans plain text for PII using the same regex patterns used for
// OCR observations, and replaces each match with a category tag like
// "[REDACTED:api_key]". Returns the redacted text and a count of redactions.
//
// Unlike DetectWithRegex which operates on OCR observations with bounding
// boxes, this works on flat text (e.g. AX tree output) and is intended to
// be called before persisting such text to disk/database.
func RedactText(text string) (string, int) {
	redactTextCalls.Add(1)
	if text == "" {
		return text, 0
	}

	var hits []redactHit
	for _, pat := range regexPatterns {
		matches := findRegexMatches(pat, text)
		for _, m := range matches {
			var start, end int
			if pat.extract > 0 && len(m) > pat.extract*2+1 {
				start, end = m[pat.extract*2], m[pat.extract*2+1]
			} else {
				start, end = m[0], m[1]
			}
			if start < 0 || end <= start {
				continue
			}
			if pat.category == "account_number" && !accountMatchAllowed(text, m) {
				continue
			}
			matched := text[start:end]
			if pat.validate != nil && !pat.validate(matched) {
				continue
			}
			hits = append(hits, redactHit{start: start, end: end, category: pat.category})
		}
	}

	if len(hits) == 0 {
		return text, 0
	}

	hits = resolveRedactOverlaps(hits)

	var sb strings.Builder
	sb.Grow(len(text))
	last := 0
	redactCount := 0
	for _, h := range hits {
		if h.start < last {
			continue
		}
		sb.WriteString(text[last:h.start])
		sb.WriteString("[REDACTED:")
		sb.WriteString(h.category)
		sb.WriteString("]")
		last = h.end
		redactCount++
	}
	sb.WriteString(text[last:])
	return sb.String(), redactCount
}

// resolveRedactOverlaps sorts hits by start offset ascending, then length
// descending, and drops overlapping entries. Keeps the first (longer) match
// when two overlap at the same start.
func resolveRedactOverlaps(hits []redactHit) []redactHit {
	if len(hits) <= 1 {
		return hits
	}
	// Insertion sort by start ascending, then length descending.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0; j-- {
			a, b := hits[j-1], hits[j]
			lenA, lenB := a.end-a.start, b.end-b.start
			if a.start > b.start || (a.start == b.start && lenA < lenB) {
				hits[j-1], hits[j] = b, a
				continue
			}
			break
		}
	}
	out := hits[:0]
	var lastEnd int
	for _, h := range hits {
		if h.start < lastEnd {
			continue
		}
		out = append(out, h)
		lastEnd = h.end
	}
	return out
}
