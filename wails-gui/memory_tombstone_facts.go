package main

import (
	"sort"
	"strings"
	"unicode"
)

// Local matching uses complete assertions, never their individual words or
// source sessions. Headings are labels, not facts. Model-prepared facts can add
// paraphrase coverage, but forgetting always has this offline baseline.
func memoryDeterministicFactKeys(content string) []string {
	parts := splitMarkdownTranslationParts(content)
	text := parts.Body
	if doc, ok := parseOKFDocument(content); ok {
		text = doc.Body
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
		// An ordered-list ordinal is formatting, never an assertion.
		marker := 0
		for marker < len(line) && line[marker] >= '0' && line[marker] <= '9' {
			marker++
		}
		if marker > 0 && marker+1 < len(line) && (line[marker] == '.' || line[marker] == ')') && unicode.IsSpace(rune(line[marker+1])) {
			line = strings.TrimSpace(line[marker+1:])
		}
		// Sentence boundaries cover plain-text notes as well as Memory Points.
		// Keep decimal points and conditions inside an assertion intact.
		var current strings.Builder
		runes := []rune(line)
		add := func() {
			assertion := current.String()
			normalized := strings.Join(NormalizeForMatch(assertion), " ")
			current.Reset()
			if memoryFactMeaningful(assertion) {
				seen[memoryForgetHash("fact:"+normalized)] = true
			}
		}
		for i, r := range runes {
			boundary := false
			switch r {
			case '。', '！', '？':
				boundary = true
			case '.':
				boundary = memoryFactPeriodBoundary(runes, i, current.String())
			case '!', '?':
				boundary = i+1 == len(runes) || unicode.IsSpace(runes[i+1])
			}
			if boundary {
				add()
			} else {
				current.WriteRune(r)
			}
		}
		add()
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

// A period in a title or initial does not end an assertion. In particular,
// "Works with Dr. Chen" must not emit the reusable prefix "Works with Dr".
func memoryFactPeriodBoundary(line []rune, index int, assertion string) bool {
	if index+1 < len(line) && !unicode.IsSpace(line[index+1]) {
		return false
	}
	start := index
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	token := strings.Trim(string(line[start:index]), "\"'`()[]{}*_")
	if memoryFactAbbreviation(token) {
		return false
	}
	letters := 0
	for _, r := range token {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	// Single initials and tiny standalone fragments stay with the following
	// text. A complete short assertion such as "Uses Go." may still end here.
	if letters == 1 {
		// A lone letter is an initial only after a name ("John F. Kennedy") or
		// at the start of the line; after a lowercase word it ends a complete
		// assertion ("Takes vitamin D.").
		if memoryFactInitialAfterName(line, start) {
			return false
		}
		return true
	}
	if letters == 2 && !memoryFactMeaningful(assertion) {
		return false
	}
	return true
}

// memoryFactInitialAfterName reports whether the token that begins at start has
// no previous word, or the previous word is capitalized or itself an initial.
func memoryFactInitialAfterName(line []rune, start int) bool {
	end := start
	for end > 0 && unicode.IsSpace(line[end-1]) {
		end--
	}
	if end == 0 {
		return true
	}
	begin := end
	for begin > 0 && !unicode.IsSpace(line[begin-1]) {
		begin--
	}
	previous := strings.Trim(string(line[begin:end]), "\"'`()[]{}*_,;:")
	for _, r := range previous {
		if unicode.IsLetter(r) {
			return unicode.IsUpper(r)
		}
	}
	return true
}

func memoryFactAbbreviation(value string) bool {
	value = strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "."))
	switch value {
	case "dr", "mr", "mrs", "ms", "prof", "rev", "sr", "jr", "st", "hon", "capt", "col", "gen", "lt", "sgt", "vs", "etc", "approx", "inc", "ltd", "corp":
		return true
	}
	// Dotted initials also cover e.g., i.e., U.S., and similar abbreviations
	// without turning their individual letters into independent fact keys.
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		letters := []rune(part)
		if len(letters) != 1 || !unicode.IsLetter(letters[0]) {
			return false
		}
	}
	return true
}

func memoryFactMeaningful(assertion string) bool {
	if memoryFactAbbreviation(assertion) {
		return false
	}
	words := NormalizeForMatch(assertion)
	letters, compactLetters := 0, 0
	for _, word := range words {
		for _, r := range word {
			if unicode.IsLetter(r) {
				letters++
			}
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
				compactLetters++
			}
		}
	}
	// CJK assertions need no whitespace; two compact-script letters can carry
	// a complete short fact while a lone Latin initial cannot.
	return letters > 0 && (len(words) >= 2 || letters >= 4 || compactLetters >= 2)
}
