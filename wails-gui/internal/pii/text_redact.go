package pii

import (
	"crypto/sha256"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
)

// redactTextCalls counts RedactText scans (memo misses), so tests can prove
// that hot readers reuse redactions instead of scanning again on every read.
var redactTextCalls atomic.Int64

// RedactTextCalls reports how many times RedactText has scanned text.
func RedactTextCalls() int64 { return redactTextCalls.Load() }

// redactRuleVersion identifies the rules RedactText applies. It is a
// fingerprint of the compiled pattern list, so a rule change (a new pattern,
// a different category or capture group) produces a new version and every
// memoized redaction under the old one is dropped on its next read.
var redactRuleVersion atomic.Uint64

func init() { redactRuleVersion.Store(fingerprintRedactRules()) }

func fingerprintRedactRules() uint64 {
	h := fnv.New64a()
	for _, pat := range regexPatterns {
		h.Write([]byte(pat.category))
		h.Write([]byte{0, byte(pat.extract), 0})
		if pat.re != nil {
			h.Write([]byte(pat.re.String()))
		}
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// The memo keeps the redaction of recently scanned texts, keyed on a digest
// of the text. It is bounded by entry count and by bytes; when either bound
// is reached it starts again, so memory never grows past these limits.
// Texts longer than redactMemoMaxText are scanned every time.
const (
	redactMemoMaxEntries = 4096
	redactMemoMaxBytes   = 8 << 20
	redactMemoMaxText    = 64 << 10
)

type redactMemoKey struct {
	digest  [sha256.Size]byte
	version uint64
}

type redactMemoValue struct {
	text  string
	count int
}

var redactMemo struct {
	mu      sync.Mutex
	entries map[redactMemoKey]redactMemoValue
	bytes   int
}

func redactMemoLookup(key redactMemoKey) (redactMemoValue, bool) {
	redactMemo.mu.Lock()
	defer redactMemo.mu.Unlock()
	v, ok := redactMemo.entries[key]
	return v, ok
}

func redactMemoStore(key redactMemoKey, value redactMemoValue) {
	size := len(value.text) + sha256.Size
	redactMemo.mu.Lock()
	defer redactMemo.mu.Unlock()
	if redactMemo.entries == nil || len(redactMemo.entries) >= redactMemoMaxEntries || redactMemo.bytes+size > redactMemoMaxBytes {
		redactMemo.entries = map[redactMemoKey]redactMemoValue{}
		redactMemo.bytes = 0
	}
	if _, exists := redactMemo.entries[key]; exists {
		return
	}
	redactMemo.entries[key] = value
	redactMemo.bytes += size
}

// resetRedactMemo drops every memoized redaction. Tests use it to start clean.
func resetRedactMemo() {
	redactMemo.mu.Lock()
	defer redactMemo.mu.Unlock()
	redactMemo.entries = nil
	redactMemo.bytes = 0
}

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
	if text == "" {
		return text, 0
	}
	if len(text) > redactMemoMaxText {
		return redactTextScan(text)
	}
	key := redactMemoKey{digest: sha256.Sum256([]byte(text)), version: redactRuleVersion.Load()}
	if v, ok := redactMemoLookup(key); ok {
		return v.text, v.count
	}
	out, count := redactTextScan(text)
	redactMemoStore(key, redactMemoValue{text: out, count: count})
	return out, count
}

// redactTextScan runs the pattern scan on text without consulting the memo.
func redactTextScan(text string) (string, int) {
	redactTextCalls.Add(1)

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
