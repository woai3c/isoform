package main

// defaultComposerSuggestions is the first-launch / empty-state fallback shown
// below the composer before any real batch has been generated. Kept tiny and
// brand-voice friendly: first person, short, no questions, no preamble.
//
// TODO(user): swap these for the final brand-voice strings. The only hard
// constraint is: non-empty, ≤ 80 runes each, ideally 3–5 entries.
var defaultComposerSuggestions = []string{
	"Summarize what I worked on today",
	"Help me plan the next hour",
	"What should I focus on right now?",
	"Draft a quick message for me",
	"Explain something I've been wondering about",
}

// defaultComposerSuggestionsBatch returns the fallback batch shown when no
// generated batch is cached yet. The sentinel ID lets the frontend (or future
// telemetry) distinguish seeded defaults from a real model-generated batch.
func defaultComposerSuggestionsBatch() *ComposerSuggestionsBatch {
	return &ComposerSuggestionsBatch{
		ID:          "default",
		GeneratedAt: 0,
		Suggestions: append([]string(nil), defaultComposerSuggestions...),
	}
}
