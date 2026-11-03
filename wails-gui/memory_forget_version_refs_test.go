package main

import "testing"

func TestMemoryForgetHistoricalReferences(t *testing.T) {
	const id = "corrected"
	oldRef := taskContextSourceRef("memory_markdown", id+":old", 100, "old private fact")
	newRef := taskContextSourceRef("memory_markdown", id+":live", 200, "independent correction")
	ids := map[string]bool{memoryForgetHistoricalSelector(id, "old private fact"): true}
	for _, test := range []struct {
		name string
		refs []string
		want bool
	}{
		{"old frozen version", []string{oldRef}, true},
		{"current frozen version", []string{newRef}, false},
		{"current version resolves bare source", []string{id, "memory:" + id, newRef}, false},
		{"old version remains when both versions cited", []string{id, newRef, oldRef}, true},
		{"legacy unqualified source", []string{"memory:" + id}, true},
		{"legacy bare source", []string{id}, true},
		{"legacy filename", []string{id + ".md"}, true},
		{"other source kind", []string{"knowledge:" + id}, false},
		{"other identity", []string{"memory:" + id + "-other"}, false},
		{"unrelated qualified ref does not resolve ambiguity", []string{id, taskContextSourceRef("memory", "different", 200, "independent correction")}, true},
		{"malformed version remains ambiguous", []string{"memory:" + id + ":not-a-time:1234567890abcdef"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			matched := false
			for _, ref := range test.refs {
				matched = matched || memoryForgetHistoricalRefMatches(ref, test.refs, ids)
			}
			if matched != test.want {
				t.Fatalf("historical match = %v, want %v", matched, test.want)
			}
		})
	}
}

func TestMemoryForgetHistoricalSelectorPreservesIdentity(t *testing.T) {
	for _, id := range []string{"corrected", "corrected:part"} {
		selector := memoryForgetHistoricalSelector(id, "\n old private fact \n")
		ref := taskContextSourceRef("memory", id, 100, "old private fact")
		if !memoryForgetHistoricalRefMatches(ref, []string{ref}, map[string]bool{selector: true}) {
			t.Fatalf("selector %q did not match its exact version %q", selector, ref)
		}
	}
}
