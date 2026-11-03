package main

import (
	"reflect"
	"testing"
)

func TestNormalizeKeywordKey(t *testing.T) {
	cases := map[string]string{
		"Isoform/something":    "something",
		"something":            "something",
		"Deliverables-Hub":     "deliverables hub",
		"deliverables hub":     "deliverables hub",
		"deliverables_hub":     "deliverables hub",
		"stream-card.tsx":      "stream card",
		"sth-deliverables-hub": "sth deliverables hub",
		"  React  ":            "react",
	}
	for in, want := range cases {
		if got := normalizeKeywordKey(in); got != want {
			t.Errorf("normalizeKeywordKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeLLMKeywords(t *testing.T) {
	in := []string{
		"Deliverables Hub", // duplicate-by-key of "deliverables-hub" below
		"deliverables-hub",
		"1984",   // pure digits -> drop
		"Review", // generic stopword -> drop
		"cmux",
		"sth-event",
		"git",
		"commit", // 5th survivor (cap is 5)
		"docs",   // 6th -> capped out
	}
	// Title-overlapping keywords (e.g. "Deliverables Hub") are intentionally kept:
	// tags double as clickable filters, so overlap with the title is desired.
	got := sanitizeLLMKeywords(in)
	want := []string{"Deliverables Hub", "cmux", "sth-event", "git", "commit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitizeLLMKeywords = %#v, want %#v", got, want)
	}
}

func TestDeriveDisplayKeywords_KeepsOnlyInContextDropsLeakage(t *testing.T) {
	title := "Review PR #1984: unified Deliverables Hub"
	desc := "Reviewed a PR adding a unified Deliverables Hub to the desktop app."
	ents := []Entity{
		{Type: entityPR, Name: "Isoform/something#1984"},       // off-context -> dropped
		{Type: entityRepo, Name: "Isoform/something"},          // off-context -> dropped
		{Type: entityProject, Name: "something"},               // off-context -> dropped
		{Type: entityChannel, Name: "1984"},                    // pure digits -> dropped
		{Type: entityTopic, Name: "Deliverables Hub"},          // in title -> kept
		{Type: entityProject, Name: "deliverables-hub"},        // same key as above -> merged
		{Type: entityTopic, Name: "tennis"},                    // off-context leakage -> dropped
		{Type: entityProject, Name: "alcaraz_roundup_2026.md"}, // off-context leakage -> dropped
	}
	got := deriveDisplayKeywords(ents, title, desc)

	if len(got) > maxDisplayKeywords {
		t.Fatalf("got %d keywords, want <= %d: %#v", len(got), maxDisplayKeywords, got)
	}
	// Exactly the in-context identifier survives; the human-readable surface form
	// ("Deliverables Hub") wins over the slug variant.
	want := []string{"Deliverables Hub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deriveDisplayKeywords = %#v, want %#v", got, want)
	}
	// Order-independent lock for #2: NO off-context entity may appear, regardless
	// of type (weak topics AND strong repo/project/pr/file are all excluded when
	// not present in the title/description). This is what stops cross-activity
	// leakage from padding the result.
	for _, leak := range []string{"tennis", "alcaraz_roundup_2026.md", "Isoform/something#1984", "Isoform/something", "1984"} {
		if kwContains(got, leak) {
			t.Errorf("off-context / numeric entity %q leaked into keywords: %#v", leak, got)
		}
	}
}

func TestDeriveDisplayKeywords_StrongOffContextIsNotPadded(t *testing.T) {
	// A terse title that names NO entity. Every entity is off-context, including a
	// strong repo. Precision-first: rather than pad with an unrelated strong repo,
	// the fallback returns nothing (the LLM path supplies real keywords).
	got := deriveDisplayKeywords(
		[]Entity{
			{Type: entityRepo, Name: "Isoform/unrelated-repo"},
			{Type: entityPerson, Name: "Someone Else"},
			{Type: entityProject, Name: "stray-project"},
		},
		"Took a break",
		"Stepped away from the desk.",
	)
	if len(got) != 0 {
		t.Fatalf("expected no keywords for a title with no in-context entity, got %#v", got)
	}
}

func TestDisplayKeywordsFor_PrefersLLMThenFallsBack(t *testing.T) {
	ents := []Entity{{Type: entityProject, Name: "sth-event"}}
	// LLM provided -> use (sanitized).
	ev := extractedEvent{Title: "Debug logic", Keywords: []string{"sth-event", "vitest"}}
	if got := displayKeywordsFor(ev, ents); !reflect.DeepEqual(got, []string{"sth-event", "vitest"}) {
		t.Errorf("LLM path = %#v", got)
	}
	// No LLM keywords -> derive from entities. The entity must be in-context
	// (named in the title) to survive the precision-first fallback.
	ev2 := extractedEvent{Title: "Optimize sth-event filter logic"}
	if got := displayKeywordsFor(ev2, ents); !kwContains(got, "sth-event") {
		t.Errorf("fallback path = %#v, want sth-event", got)
	}
}

func kwContains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
