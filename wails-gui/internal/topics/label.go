package topics

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// hanRun is a run of Han characters; hanBreak the little words a name may
// sit behind inside one.
var (
	hanRun   = regexp.MustCompile(`\p{Han}+`)
	hanBreak = regexp.MustCompile(`关于|对于|的|了|是|在|和|与|及|或|对|给|跟|向|把|被|从|为`)
)

// Label problems reported by CheckLabel.
const (
	LabelPerson  = "person"  // a word names a person
	LabelLink    = "link"    // a link, a path, an address or a handle
	LabelEmpty   = "empty"   // nothing left after stop words, codes and links
	LabelGeneric = "generic" // a single plain word ("brief", "hat")
)

// CheckLabel applies the rules Rank uses to a label written elsewhere (an
// interest a model named, a search query): no person's name, the people the
// user knows or a name-like word, no link, path, address or handle, and more
// than one plain word. It returns "" when the label may be searched for.
func CheckLabel(text string, names []string) string {
	if strings.Contains(text, "://") || strings.ContainsAny(text, "/\\@") {
		return LabelLink
	}
	if MentionsPerson(text, names) {
		return LabelPerson
	}
	var words []token
	for _, run := range phraseRuns(text) {
		words = append(words, run...)
	}
	if len(words) == 0 {
		return LabelEmpty
	}
	if len(words) == 1 && !productLike(words) {
		return LabelGeneric
	}
	return ""
}

// MentionsPerson reports text that names a person: a word of the people the
// user knows, a given name, or a Han run written like a name. It reads any
// text, a label or a sentence.
func MentionsPerson(text string, names []string) bool {
	nameSet := nameWords(names)
	// A known name that is also a stop word ("Will", "Claude") never
	// reaches the runs, so the raw words are compared too.
	rawNames := map[string]bool{}
	for _, name := range names {
		// An address's domain names no one; a handle ("@yetone") does.
		if at := strings.IndexByte(name, '@'); at > 0 && strings.Contains(name[at:], ".") {
			name = name[:at]
		}
		for _, word := range rawWords(name) {
			if len([]rune(word)) >= 2 && !numeric(word) {
				rawNames[word] = true
			}
		}
	}
	for _, word := range rawWords(text) {
		if nameSet[word] || rawNames[word] {
			return true
		}
	}
	// Han text has no spaces: a known Han name counts anywhere in it, also
	// spelled with spaces between its characters.
	joined := stripHanSpaces(text)
	for word := range nameSet {
		if hanToken(word) && strings.Contains(joined, word) {
			return true
		}
	}
	// Every Han run, also one too long to become a word, is checked for a
	// name at its start and after each little word inside it ("关于王伟的报道").
	for _, run := range hanRun.FindAllString(joined, -1) {
		for _, part := range hanBreak.Split(run, -1) {
			if hanNameLike(part) {
				return true
			}
		}
	}
	for _, run := range phraseRuns(text) {
		for _, person := range personTokens(run, nameSet, SourceSearch) {
			if person {
				return true
			}
		}
	}
	return false
}

// rawWords are the lower-cased runs of letters and digits in text.
func rawWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// StripPerson rewrites text so it keeps no person's name: every word the
// person check marks — a known name, a given name and the words beside one —
// goes, and whitespace folds. When what is left still names a person (a name
// fused inside a Han run), nothing readable enough remains and "" is
// returned. A line that went through StripPerson never keeps a name.
func StripPerson(text string, names []string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	nameSet := nameWords(names)
	marked := map[string]bool{}
	for _, run := range phraseRuns(text) {
		for i, person := range personTokens(run, nameSet, SourceSearch) {
			if person {
				marked[run[i].key] = true
			}
		}
	}
	// MentionsPerson also compares raw words against the names as written.
	for _, name := range names {
		// An address's domain names no one; a handle ("@yetone") does.
		if at := strings.IndexByte(name, '@'); at > 0 && strings.Contains(name[at:], ".") {
			name = name[:at]
		}
		for _, word := range rawWords(name) {
			if len([]rune(word)) >= 2 && !numeric(word) {
				marked[word] = true
			}
		}
	}
	var kept []string
	for _, field := range strings.Fields(text) {
		drop := false
		for _, word := range rawWords(field) {
			if marked[word] {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, field)
		}
	}
	out := strings.Join(kept, " ")
	// A known Han name sits anywhere in the text, also spelled with spaces.
	for word := range nameSet {
		if hanToken(word) {
			out = strings.Join(strings.Fields(strings.ReplaceAll(out, word, " ")), " ")
		}
	}
	if MentionsPerson(out, names) {
		return ""
	}
	return out
}

// stripHanSpaces removes the spaces between two Han characters.
func stripHanSpaces(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsSpace(r) {
			j := i + 1
			for j < len(runes) && unicode.IsSpace(runes[j]) {
				j++
			}
			prevHan := b.Len() > 0 && unicode.Is(unicode.Han, lastRune(b.String()))
			if prevHan && j < len(runes) && unicode.Is(unicode.Han, runes[j]) {
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}
