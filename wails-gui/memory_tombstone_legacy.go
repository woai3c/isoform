package main

import "strings"

// Version 1 is the unversioned format shipped on feat/iphone-companion.
// Its hashed body and point namespaces remain readable. Activity sessions are
// deliberately not matched: another fact from the same session is independent.
// Legacy ID keys remain conservative because the old opaque format cannot
// distinguish a deleted record ID from an archived parent's provenance ID.
func memoryLegacyTombstoneContentKeys(content string) []string {
	keys := make([]string, 0)
	add := func(kind, text string) {
		normalized := strings.Join(NormalizeForMatch(text), " ")
		if normalized != "" {
			keys = append(keys, memoryForgetHash(kind+":"+normalized))
		}
	}
	add("body", splitMarkdownTranslationParts(content).Body)
	for _, point := range parseMemoryPoints(content) {
		add("point", point)
	}
	return keys
}
