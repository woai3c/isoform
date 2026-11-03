// Package tools holds the Anthropic-native agent loop's tool layer.
//
// edit_match implements the seven-level fuzzy matcher described in the
// refactor doc §4.3.4. Edit is the tool most likely to fail when the
// model slightly misremembers whitespace, line endings, or copy-pastes
// the Read output with a numbering prefix — so each level relaxes one
// dimension while still returning enough metadata (the level it
// matched at, the original byte range) that the caller can decide
// whether to warn the model or reject the match.
package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// MatchLevel names the level at which a fuzzy match succeeded.
type MatchLevel string

const (
	MatchL0 MatchLevel = "L0" // rejection: input contained a Read line-number prefix
	MatchL1 MatchLevel = "L1" // byte-exact
	MatchL2 MatchLevel = "L2" // after EOL normalization
	MatchL3 MatchLevel = "L3" // + BOM strip + line rtrim
	MatchL4 MatchLevel = "L4" // + dedent common indent, reindent new_string
	MatchL5 MatchLevel = "L5" // whitespace-insensitive (collapse runs)
	MatchL6 MatchLevel = "L6" // anchor (first+last line) + body token-sequence
)

// MatchResult records where in the original file content a successful
// match was found, along with the replacement string that preserves
// the original region's indentation when applicable.
type MatchResult struct {
	Level           MatchLevel
	Start           int    // byte offset into original file
	End             int    // byte offset into original file (exclusive)
	ReplaceWith     string // string to substitute for file[Start:End]
	OccurrenceCount int    // how many matches at this level (1 means unambiguous)
}

// ErrAmbiguous is returned when multiple matches were found at the first
// non-zero level and replace_all was not requested.
var ErrAmbiguous = fmt.Errorf("ambiguous match: multiple candidates found")

// ErrNotFound is returned when no level found a match.
var ErrNotFound = fmt.Errorf("no matching region found")

// ErrLinePrefix is the specific L0 rejection signal.
var ErrLinePrefix = fmt.Errorf("old_string contains line-number prefixes; do not include the `NNN\\t` prefix emitted by Read")

// FindMatches walks L1 → L6 until it finds at least one match. When the
// first non-zero level returns multiple matches, FindMatches returns
// every one of them so the caller can choose (replace_all) or report
// ambiguity (default). FindMatches never crosses levels.
func FindMatches(file, oldString string) ([]MatchResult, MatchLevel, error) {
	if err := rejectLinePrefix(oldString); err != nil {
		return nil, MatchL0, err
	}

	// L1: byte-exact.
	if ms := exactMatches(file, oldString); len(ms) > 0 {
		markLevel(ms, MatchL1)
		return ms, MatchL1, nil
	}

	// L2: normalize CRLF on both sides.
	fileL2 := normalizeEOL(file)
	oldL2 := normalizeEOL(oldString)
	if fileL2 != file || oldL2 != oldString {
		if ms := exactMatches(fileL2, oldL2); len(ms) > 0 {
			// Map the L2 offsets back onto the original file. Because
			// we only replaced CRLF with LF, all prior offsets are
			// unchanged: the search result indexes into fileL2. We
			// carry the replacement as computed on the normalized
			// file; caller substitutes on fileL2. For simplicity we
			// report offsets against the normalized file; the caller
			// is expected to use the normalized file as the source of
			// truth after an L2 hit.
			markLevel(ms, MatchL2)
			return ms, MatchL2, nil
		}
	}

	// L3: strip BOM + rtrim each line on both sides, but only for
	// matching. The replacement is still performed on the original
	// (EOL-normalized) file to keep byte offsets meaningful.
	fileL3 := stripBOM(fileL2)
	oldL3 := stripBOM(oldL2)
	fileTrim := rtrimLines(fileL3)
	oldTrim := rtrimLines(oldL3)
	if ms := exactMatches(fileTrim, oldTrim); len(ms) > 0 {
		// Locate the span on the original normalized file by mapping
		// through the trim transform.
		mapped := mapTrimmedMatches(fileL3, fileTrim, ms)
		if len(mapped) > 0 {
			markLevel(mapped, MatchL3)
			return mapped, MatchL3, nil
		}
	}

	// L4: dedent common indent from oldL3; search in fileL3.
	if ms, ok := dedentMatches(fileL3, oldL3); ok {
		markLevel(ms, MatchL4)
		return ms, MatchL4, nil
	}

	// L5: whitespace-insensitive (collapse runs to single space).
	if ms, ok := whitespaceLooseMatches(fileL3, oldL3); ok {
		markLevel(ms, MatchL5)
		return ms, MatchL5, nil
	}

	// L6: anchor (first/last line) + body token sequence.
	if ms, ok := anchorBodyMatches(fileL3, oldL3); ok {
		markLevel(ms, MatchL6)
		return ms, MatchL6, nil
	}

	return nil, "", ErrNotFound
}

// ApplyMatch returns the new file contents for a single match.
// The caller decides whether to reindent newString based on the level.
func ApplyMatch(file string, m MatchResult) string {
	return file[:m.Start] + m.ReplaceWith + file[m.End:]
}

// ReindentNewString prefixes each non-empty line of newString with the
// leading-whitespace prefix extracted from matched, so an L4 match
// (which dedented the target region) still produces properly indented
// output. The first line is treated like any other line — the prefix
// is applied unconditionally because the match always starts at the
// beginning of a line in L4/L6 (we only enter those levels after
// dedent, which operates on whole-line boundaries).
func ReindentNewString(newString, matched string) string {
	prefix := leadingWhitespace(firstLine(matched))
	if prefix == "" {
		return newString
	}
	lines := strings.Split(newString, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue // don't create trailing whitespace
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// ---------- level implementations ----------

var linePrefixRe = regexp.MustCompile(`(?m)^\s*\d+\t`)

func rejectLinePrefix(old string) error {
	trimmed := strings.TrimSpace(old)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(old, "\n")
	nonEmpty := 0
	matched := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		if linePrefixRe.MatchString(l + "\n") {
			matched++
		}
	}
	if nonEmpty > 0 && matched == nonEmpty {
		return ErrLinePrefix
	}
	return nil
}

func exactMatches(hay, needle string) []MatchResult {
	if needle == "" {
		return nil
	}
	var out []MatchResult
	start := 0
	for {
		idx := strings.Index(hay[start:], needle)
		if idx < 0 {
			break
		}
		abs := start + idx
		out = append(out, MatchResult{Start: abs, End: abs + len(needle), ReplaceWith: ""})
		start = abs + len(needle)
	}
	return out
}

func normalizeEOL(s string) string {
	if !strings.ContainsAny(s, "\r") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func stripBOM(s string) string {
	// UTF-8 BOM: EF BB BF.
	if len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF {
		return s[3:]
	}
	return s
}

func rtrimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, func(r rune) bool {
			return r == ' ' || r == '\t'
		})
	}
	return strings.Join(lines, "\n")
}

func mapTrimmedMatches(original, trimmed string, matches []MatchResult) []MatchResult {
	// Trim-only transforms don't change byte offsets except for dropped
	// trailing whitespace. We walk both strings in lockstep with a
	// position map: for each byte in trimmed, remember its index in
	// original. Then translate match offsets through the map.
	mp := make([]int, len(trimmed)+1)
	oi, ti := 0, 0
	for oi < len(original) && ti < len(trimmed) {
		if original[oi] == trimmed[ti] {
			mp[ti] = oi
			oi++
			ti++
			continue
		}
		// Skip whitespace in the original that was rtrimmed away.
		if original[oi] == ' ' || original[oi] == '\t' {
			oi++
			continue
		}
		// Mismatch we can't reconcile — bail.
		return nil
	}
	mp[ti] = oi
	for ; ti < len(trimmed); ti++ {
		mp[ti] = len(original)
	}
	mp[len(trimmed)] = len(original)

	out := make([]MatchResult, 0, len(matches))
	for _, m := range matches {
		if m.Start >= len(mp) || m.End >= len(mp) {
			continue
		}
		out = append(out, MatchResult{Start: mp[m.Start], End: mp[m.End]})
	}
	return out
}

// dedentMatches accommodates the common case where the model wrote
// old_string without the leading indentation that exists in the file.
// Strategy: dedent old_string (strip its own common indent, if any),
// then walk the file line-by-line looking for a run of N lines whose
// common leading whitespace can be stripped to yield the dedented
// old_string exactly. The ReplaceWith value is the matched region's
// original bytes so the caller can reindent new_string based on it.
func dedentMatches(file, old string) ([]MatchResult, bool) {
	oldLines := strings.Split(old, "\n")
	oldCommon := commonLeadingWhitespace(oldLines)
	dedentedOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		dedentedOld[i] = strings.TrimPrefix(l, oldCommon)
	}

	fileLines := strings.Split(file, "\n")
	offsets := make([]int, len(fileLines)+1)
	for i, l := range fileLines {
		offsets[i+1] = offsets[i] + len(l) + 1 // +1 for the trailing \n
	}

	n := len(oldLines)
	if n == 0 || n > len(fileLines) {
		return nil, false
	}

	var out []MatchResult
	for i := 0; i+n <= len(fileLines); i++ {
		window := fileLines[i : i+n]
		windowCommon := commonLeadingWhitespace(window)
		if windowCommon == "" && oldCommon == "" {
			// No dedent on either side means L1 would have matched already.
			continue
		}
		if windowCommon == oldCommon {
			// Same indent — L1 handled it.
			continue
		}
		ok := true
		for j := 0; j < n; j++ {
			line := window[j]
			if !strings.HasPrefix(line, windowCommon) {
				ok = false
				break
			}
			if strings.TrimPrefix(line, windowCommon) != dedentedOld[j] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		start := offsets[i]
		end := offsets[i+n] - 1
		if end > len(file) {
			end = len(file)
		}
		out = append(out, MatchResult{Start: start, End: end, ReplaceWith: file[start:end]})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// whitespaceLooseMatches collapses all runs of whitespace (within a
// line) to a single space on both sides and searches. When a match is
// found, the byte span on the original file is computed by walking
// forward from the collapsed-offset anchor.
func whitespaceLooseMatches(file, old string) ([]MatchResult, bool) {
	collapsedFile, fileMap := collapseWhitespace(file)
	collapsedOld, _ := collapseWhitespace(old)
	if collapsedOld == "" {
		return nil, false
	}

	var out []MatchResult
	start := 0
	for {
		idx := strings.Index(collapsedFile[start:], collapsedOld)
		if idx < 0 {
			break
		}
		abs := start + idx
		// Map collapsed offsets back to original.
		origStart := fileMap[abs]
		origEnd := fileMap[abs+len(collapsedOld)]
		out = append(out, MatchResult{Start: origStart, End: origEnd, ReplaceWith: ""})
		start = abs + len(collapsedOld)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// anchorBodyMatches handles the "≥3 lines with whitespace drift in the
// middle" case. First and last lines must match precisely (after EOL
// normalization); middle lines may differ in whitespace and may have a
// ±1 line count drift.
func anchorBodyMatches(file, old string) ([]MatchResult, bool) {
	oldLines := strings.Split(old, "\n")
	if len(oldLines) < 3 {
		return nil, false
	}
	first, last := oldLines[0], oldLines[len(oldLines)-1]
	if strings.TrimSpace(first) == "" || strings.TrimSpace(last) == "" {
		return nil, false
	}

	fileLines := strings.Split(file, "\n")
	offsets := make([]int, len(fileLines)+1)
	for i, l := range fileLines {
		offsets[i+1] = offsets[i] + len(l) + 1
	}

	normalizedOld := normalizeLineWS(old)
	n := len(oldLines)

	var out []MatchResult
	for i := 0; i < len(fileLines); i++ {
		if normalizeLineWS(fileLines[i]) != normalizeLineWS(first) {
			continue
		}
		// Try body lengths (n-1, n, n+1).
		for _, delta := range []int{-1, 0, 1} {
			size := n + delta
			if size < 3 {
				continue
			}
			end := i + size - 1
			if end >= len(fileLines) {
				continue
			}
			if normalizeLineWS(fileLines[end]) != normalizeLineWS(last) {
				continue
			}
			candidate := strings.Join(fileLines[i:end+1], "\n")
			if normalizeLineWS(candidate) != normalizedOld {
				continue
			}
			startByte := offsets[i]
			endByte := offsets[end+1] - 1
			if endByte > len(file) {
				endByte = len(file)
			}
			out = append(out, MatchResult{Start: startByte, End: endByte, ReplaceWith: file[startByte:endByte]})
			break
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	// Require anchor uniqueness (each first/last is unique across the file).
	if len(out) > 1 {
		return nil, false
	}
	return out, true
}

func markLevel(ms []MatchResult, level MatchLevel) {
	for i := range ms {
		ms[i].Level = level
		ms[i].OccurrenceCount = len(ms)
	}
}

func commonLeadingWhitespace(lines []string) string {
	var common string
	found := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lw := leadingWhitespace(l)
		if !found {
			common = lw
			found = true
			continue
		}
		common = sharedPrefix(common, lw)
		if common == "" {
			break
		}
	}
	return common
}

func sharedPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

func leadingWhitespace(line string) string {
	for i, r := range line {
		if r != ' ' && r != '\t' {
			return line[:i]
		}
	}
	return line
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func normalizeLineWS(s string) string {
	// Collapse runs of whitespace within a line to a single space, and
	// trim leading/trailing whitespace.
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		fields := strings.Fields(l)
		lines[i] = strings.Join(fields, " ")
	}
	return strings.Join(lines, "\n")
}

// collapseWhitespace returns s with runs of whitespace (within a line)
// replaced by a single space, plus a map from collapsed-index to
// original-index so match offsets can be mapped back.
func collapseWhitespace(s string) (string, []int) {
	var b strings.Builder
	b.Grow(len(s))
	mp := make([]int, 0, len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' {
			// Emit a single space.
			mp = append(mp, i)
			b.WriteByte(' ')
			i++
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			continue
		}
		mp = append(mp, i)
		b.WriteByte(c)
		i++
	}
	mp = append(mp, len(s))
	return b.String(), mp
}
