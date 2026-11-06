package main

import "testing"

// v1.0.11 walk: a commit message is git work, and a preference for a product
// reads as care. Both fixes are shown here so a regression fails loudly.
func TestCommitMessagesAreWorkNotCommunication(t *testing.T) {
	got := classifyMemoryArea("Wants the real fix titles in commit messages, not generic summaries", "user_insight")
	if got == "communication" {
		t.Fatalf("commit message belief classified as %q", got)
	}
	if got := classifyMemoryArea("Sends replies to customers by email", "user_insight"); got != "communication" {
		t.Fatalf("email reply belief classified as %q, want communication", got)
	}
}

func TestPreferenceForProductReadsAsCare(t *testing.T) {
	cases := map[string]string{
		"Preferences for Isoform Spoken Briefs and Contextual Chat": "You care about Isoform Spoken Briefs and Contextual Chat",
		"Preferences for short release notes and quiet hours":       "You prefer short release notes and quiet hours",
	}
	for in, want := range cases {
		if got := beliefInSecondPerson(in); got != want {
			t.Fatalf("beliefInSecondPerson(%q) = %q, want %q", in, got, want)
		}
	}
}
