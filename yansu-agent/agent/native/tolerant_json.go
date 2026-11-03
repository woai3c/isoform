// Package native holds the in-process Anthropic-native agent loop and
// the helpers that support it. tolerant_json implements the multi-stage
// parser described in the refactor doc §4.4.12: LLM-produced JSON is
// frequently malformed (trailing commas, smart quotes, markdown fences,
// truncated output). Instead of bouncing the model with an error on
// every minor deviation, we try harder to extract something usable.
//
// Stages, from strict to loose:
//
//	S1  strict encoding/json
//	S2  strip markdown code fences, retry
//	S3  general repair (smart quotes, trailing commas, unquoted keys,
//	    single-quoted strings, Python literals, comments, unclosed
//	    braces/brackets)
//	S4  right-trim truncation repair (drop trailing bytes until parse
//	    succeeds)
//	S5  heuristic schema-aware scalar extraction via regex (last resort)
package native

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ParseStage identifies which repair tier succeeded for metrics/UX.
type ParseStage string

const (
	StageS1 ParseStage = "S1"
	StageS2 ParseStage = "S2"
	StageS3 ParseStage = "S3"
	StageS4 ParseStage = "S4"
	StageS5 ParseStage = "S5"
)

// ParseResult holds the outcome of the tolerant parse.
type ParseResult struct {
	Value any
	Raw   []byte
	Stage ParseStage
	// Warning is non-empty when the parse succeeded via a repair stage
	// (S2 and up); surface it to the model so it can learn from its
	// mistakes rather than silently accept broken output.
	Warning string
}

// ParseOptions controls which stages are enabled. A zero value enables all.
type ParseOptions struct {
	// MaxStage caps the highest stage that may be attempted. Empty means
	// no cap (all stages enabled). Useful in debug mode to observe raw
	// model output quality.
	MaxStage ParseStage
	// Schema, when non-nil, is used by stage S5 to guide heuristic
	// extraction (only scalar types: string, number, boolean).
	Schema map[string]string // field name -> "string"|"number"|"boolean"
}

// Parse attempts to coerce raw text into a JSON value, walking through
// the repair stages in order. It returns the first successful stage.
func Parse(raw string, opts ParseOptions) (*ParseResult, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("tolerant_json: empty input")
	}

	stageAllowed := func(s ParseStage) bool {
		if opts.MaxStage == "" {
			return true
		}
		order := map[ParseStage]int{StageS1: 1, StageS2: 2, StageS3: 3, StageS4: 4, StageS5: 5}
		return order[s] <= order[opts.MaxStage]
	}

	// S1: strict JSON.
	if stageAllowed(StageS1) {
		if v, ok := strictParse(trimmed); ok {
			return &ParseResult{Value: v, Raw: []byte(trimmed), Stage: StageS1}, nil
		}
	}

	// S2: strip markdown fences, retry strict.
	if stageAllowed(StageS2) {
		if s2 := stripMarkdownFence(trimmed); s2 != trimmed {
			if v, ok := strictParse(s2); ok {
				return &ParseResult{
					Value:   v,
					Raw:     []byte(s2),
					Stage:   StageS2,
					Warning: "input JSON was wrapped in markdown fences; fences stripped",
				}, nil
			}
		}
	}

	// S3: general-purpose repair.
	if stageAllowed(StageS3) {
		s3 := repairJSON(stripMarkdownFence(trimmed))
		if v, ok := strictParse(s3); ok {
			return &ParseResult{
				Value:   v,
				Raw:     []byte(s3),
				Stage:   StageS3,
				Warning: "input JSON was malformed and auto-repaired (stage S3); please emit valid JSON next time",
			}, nil
		}
	}

	// S4: truncation repair — right-trim bytes until parse succeeds.
	if stageAllowed(StageS4) {
		s4 := repairJSON(stripMarkdownFence(trimmed))
		if v, ok := truncationRepair(s4); ok {
			return &ParseResult{
				Value:   v,
				Raw:     []byte(s4),
				Stage:   StageS4,
				Warning: "input JSON was truncated and auto-repaired (stage S4); output may be incomplete",
			}, nil
		}
	}

	// S5: heuristic regex extraction. Only runs when a schema is provided.
	if stageAllowed(StageS5) && len(opts.Schema) > 0 {
		if v, ok := heuristicExtract(raw, opts.Schema); ok {
			return &ParseResult{
				Value:   v,
				Raw:     []byte(raw),
				Stage:   StageS5,
				Warning: "input JSON could not be parsed; fields extracted heuristically (stage S5) — reliability is low",
			}, nil
		}
	}

	return nil, fmt.Errorf("tolerant_json: all parse stages failed")
}

func strictParse(s string) (any, bool) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	return v, true
}

var fenceRe = regexp.MustCompile("(?is)^\\s*```(?:json|JSON|json5|JSON5)?\\s*\n?(.*?)\n?\\s*```\\s*$")

// stripMarkdownFence removes a surrounding ```json ... ``` wrapper if present.
func stripMarkdownFence(s string) string {
	m := fenceRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	return strings.TrimSpace(m[1])
}

// repairJSON applies a battery of token-level fixes that cover the vast
// majority of model-produced malformed JSON we observe.
func repairJSON(s string) string {
	s = normalizeQuotes(s)
	s = stripComments(s)
	s = replacePythonLiterals(s)
	s = quoteBareKeys(s)
	s = convertSingleQuotedStrings(s)
	s = dropTrailingCommas(s)
	s = closeUnfinishedStringAtEnd(s)
	s = balanceBrackets(s)
	return strings.TrimSpace(s)
}

// normalizeQuotes converts smart/curly/reversed quotes to ASCII ".
func normalizeQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '“', '”', '„', '‟', '″', '«', '»':
			b.WriteRune('"')
		case '‘', '’', '‚', '‛', '′':
			b.WriteRune('\'')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripComments removes // line comments and /* block */ comments that
// appear outside of strings. It respects escapes inside strings.
func stripComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		// Enter/exit strings.
		if c == '"' {
			// Copy the whole string literal verbatim.
			b.WriteByte(c)
			i++
			for i < n {
				d := s[i]
				b.WriteByte(d)
				i++
				if d == '\\' && i < n {
					b.WriteByte(s[i])
					i++
					continue
				}
				if d == '"' {
					break
				}
			}
			continue
		}
		// Line comment.
		if c == '/' && i+1 < n && s[i+1] == '/' {
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				return b.String()
			}
			i += j
			continue
		}
		// Block comment.
		if c == '/' && i+1 < n && s[i+1] == '*' {
			j := strings.Index(s[i:], "*/")
			if j < 0 {
				return b.String()
			}
			i += j + 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

var pyLiteralRe = regexp.MustCompile(`(?m)(^|[\[\{,:\s])(True|False|None)(\b)`)

func replacePythonLiterals(s string) string {
	return pyLiteralRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := pyLiteralRe.FindStringSubmatch(match)
		switch sub[2] {
		case "True":
			return sub[1] + "true" + sub[3]
		case "False":
			return sub[1] + "false" + sub[3]
		case "None":
			return sub[1] + "null" + sub[3]
		}
		return match
	})
}

// bareKeyRe matches an unquoted identifier followed by a colon that is
// the start of a JSON object member. We only match when it's directly
// preceded by `{` or `,` (with optional whitespace) so we don't touch
// content inside strings.
var bareKeyRe = regexp.MustCompile(`([\{,]\s*)([A-Za-z_][A-Za-z0-9_\-]*)\s*:`)

func quoteBareKeys(s string) string {
	return runOutsideStrings(s, func(chunk string) string {
		return bareKeyRe.ReplaceAllString(chunk, `$1"$2":`)
	})
}

// convertSingleQuotedStrings rewrites 'text' to "text", escaping any
// double quotes that appear inside the original string. Only triggered
// on clearly-delimited single-quoted tokens outside of real strings.
func convertSingleQuotedStrings(s string) string {
	return runOutsideStrings(s, func(chunk string) string {
		var b strings.Builder
		b.Grow(len(chunk))
		n := len(chunk)
		for i := 0; i < n; i++ {
			c := chunk[i]
			if c != '\'' {
				b.WriteByte(c)
				continue
			}
			// Find the closing '.
			end := -1
			for j := i + 1; j < n; j++ {
				if chunk[j] == '\\' && j+1 < n {
					j++
					continue
				}
				if chunk[j] == '\'' {
					end = j
					break
				}
			}
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			inner := chunk[i+1 : end]
			b.WriteByte('"')
			for k := 0; k < len(inner); k++ {
				ic := inner[k]
				switch ic {
				case '\\':
					if k+1 < len(inner) {
						b.WriteByte('\\')
						b.WriteByte(inner[k+1])
						k++
					}
				case '"':
					b.WriteByte('\\')
					b.WriteByte('"')
				default:
					b.WriteByte(ic)
				}
			}
			b.WriteByte('"')
			i = end
		}
		return b.String()
	})
}

// dropTrailingCommas strips `,}` and `,]` sequences, repeating until no
// more remain (to cover deeply-nested malformations).
func dropTrailingCommas(s string) string {
	return runOutsideStrings(s, func(chunk string) string {
		trailingCommaRe := regexp.MustCompile(`,(\s*[}\]])`)
		for {
			next := trailingCommaRe.ReplaceAllString(chunk, `$1`)
			if next == chunk {
				return chunk
			}
			chunk = next
		}
	})
}

// closeUnfinishedStringAtEnd handles the common truncation case where
// the last token is an unterminated string. We scan once and, if the
// total number of unescaped double quotes is odd, append a single `"`.
func closeUnfinishedStringAtEnd(s string) string {
	inString := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		if c == '\\' {
			esc = true
			continue
		}
		if c == '"' {
			inString = !inString
		}
	}
	if inString {
		return s + `"`
	}
	return s
}

// balanceBrackets counts unmatched `{`, `[` and appends the missing
// closers at the end (in reverse order of opening). Works outside of
// string literals.
func balanceBrackets(s string) string {
	var stack []byte
	inString := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(s)
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i])
	}
	return b.String()
}

// runOutsideStrings applies f to every chunk of s that is not a JSON
// string literal, leaving string literals intact.
func runOutsideStrings(s string, f func(string) string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := len(s)
	start := 0
	for i := 0; i < n; {
		c := s[i]
		if c == '"' {
			// Flush pending non-string portion.
			b.WriteString(f(s[start:i]))
			// Copy the string literal verbatim.
			b.WriteByte('"')
			i++
			for i < n {
				d := s[i]
				b.WriteByte(d)
				i++
				if d == '\\' && i < n {
					b.WriteByte(s[i])
					i++
					continue
				}
				if d == '"' {
					break
				}
			}
			start = i
			continue
		}
		i++
	}
	if start < n {
		b.WriteString(f(s[start:]))
	}
	return b.String()
}

// truncationRepair peels bytes off the end of s until the remainder
// parses as JSON, or we run out of input. It is only called after
// repairJSON has already had a chance to balance brackets; this catches
// the rarer case where the truncation cut mid-value and balancing
// alone can't fix it.
func truncationRepair(s string) (any, bool) {
	s = strings.TrimSpace(s)
	// Walk backwards one byte at a time. For long inputs this is O(n) parse
	// attempts which is fine in practice (tool inputs are small). We stop at
	// zero to avoid pathological empty-parse attempts.
	for end := len(s); end > 0; end-- {
		trunc := strings.TrimRightFunc(s[:end], unicode.IsSpace)
		if trunc == "" {
			return nil, false
		}
		// Also strip dangling separators (comma, colon) at the truncation
		// point before balancing — they'd otherwise produce `{"a":1,}` which
		// repairJSON would then have to fix again.
		trimmed := strings.TrimRight(trunc, ",:")
		candidate := balanceBrackets(closeUnfinishedStringAtEnd(trimmed))
		if v, ok := strictParse(candidate); ok {
			return v, true
		}
	}
	return nil, false
}

// heuristicExtract pulls a minimal object out of raw using per-field
// regex patterns. Only scalar types are supported (string, number,
// boolean). Missing fields are omitted; callers should run schema
// validation afterward to enforce required fields.
func heuristicExtract(raw string, schema map[string]string) (any, bool) {
	out := map[string]any{}
	for field, typ := range schema {
		switch typ {
		case "string":
			re := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*"((?:[^"\\]|\\.)*)"`, regexp.QuoteMeta(field)))
			if m := re.FindStringSubmatch(raw); len(m) == 2 {
				out[field] = unescapeJSONString(m[1])
			}
		case "number":
			re := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*(-?\d+(?:\.\d+)?(?:[eE][+\-]?\d+)?)`, regexp.QuoteMeta(field)))
			if m := re.FindStringSubmatch(raw); len(m) == 2 {
				var n float64
				fmt.Sscanf(m[1], "%f", &n)
				out[field] = n
			}
		case "boolean":
			re := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*(true|false)`, regexp.QuoteMeta(field)))
			if m := re.FindStringSubmatch(raw); len(m) == 2 {
				out[field] = m[1] == "true"
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func unescapeJSONString(s string) string {
	out, err := strconvUnquote(`"` + s + `"`)
	if err != nil {
		return s
	}
	return out
}

// strconvUnquote mirrors strconv.Unquote for JSON strings without
// dragging in strconv; we keep it simple since inputs here are already
// wrapped in double quotes.
func strconvUnquote(s string) (string, error) {
	var raw any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return "", err
	}
	if str, ok := raw.(string); ok {
		return str, nil
	}
	return "", fmt.Errorf("not a string")
}
