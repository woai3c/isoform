package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// beliefWords are the lowercased words of a belief in second person, so
// "Wants X" and "You want X" read as the same idea.
func beliefWords(text string) map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(beliefInSecondPerson(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		words[word] = true
	}
	return words
}

// nearDuplicateBeliefs reports whether two beliefs say the same thing in nearly
// the same words: at least four words each and nine in ten shared, or identical.
func nearDuplicateBeliefs(a, b string) bool {
	left, right := beliefWords(a), beliefWords(b)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	shared := 0
	for word := range left {
		if right[word] {
			shared++
		}
	}
	union := len(left) + len(right) - shared
	if shared == union {
		return true
	}
	if len(left) < 4 || len(right) < 4 {
		return false
	}
	return float64(shared)/float64(union) >= 0.85
}

// nearDuplicateMemoryID finds the stored belief a new one nearly repeats, so
// the new evidence strengthens the belief already there (About you v1.0.1 Y33).
func nearDuplicateMemoryID(title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}
	dir, err := memoryDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if nearDuplicateBeliefs(title, parseMemoryEntryDocument(string(data)).title()) {
			return strings.TrimSuffix(entry.Name(), ".md")
		}
	}
	return ""
}
