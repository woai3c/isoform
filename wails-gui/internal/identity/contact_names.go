package identity

import (
	"github.com/mozillazg/go-unidecode"
	"strings"
	"unicode"
)

// ContactNamesMatch is a suggestion signal, never authority for an automatic
// merge. Romanization lets a seen Chinese name match a Latin address-book name;
// rotating the first token also accommodates family-name order.
func (r *Resolver) ContactNamesMatch(left, right []string) bool {
	keys := map[string]bool{}
	for _, name := range left {
		for _, key := range contactNameKeys(name) {
			keys[key] = true
		}
	}
	for _, name := range right {
		for _, key := range contactNameKeys(name) {
			if keys[key] {
				return true
			}
		}
	}
	return false
}

func contactNameKeys(name string) []string {
	tokens := strings.Fields(strings.ToLower(unidecode.Unidecode(name)))
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, s)
	}
	key := compact(strings.Join(tokens, ""))
	if key == "" {
		return nil
	}
	out := []string{key}
	if len(tokens) > 1 {
		out = append(out, compact(strings.Join(append(tokens[1:], tokens[0]), "")))
	}
	return out
}
