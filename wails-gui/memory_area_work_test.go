package main

import "testing"

// Y28 (design, 2026-11-06): How you communicate holds only messages sent to
// people. Building, shipping, running agents and commit messages are Work, and
// a body word never moves a belief into communication.
func TestBuildingAndAgentBeliefsAreWorkNotCommunication(t *testing.T) {
	for _, title := range []string{
		"You're building Isoform desktop app and iPhone companion with Claude and Codex",
		"You have agents map the code and docs before writing new code",
		"You let Auto-fix push CI fixes to PR branches without waiting for a reply",
		"Wants the real fix titles in commit messages, not generic summaries",
	} {
		if got := classifyMemoryArea(title, "user_insight"); got != "work" {
			t.Fatalf("%q classified as %q, want work", title, got)
		}
	}
}

func TestBodyWordsNeverPlaceABeliefInCommunication(t *testing.T) {
	body := "Builds the release notes\n- Sends messages and replies to the team in mail"
	if got := classifyMemoryArea(body, "user_insight"); got == "communication" {
		t.Fatalf("a body word put the belief in %q", got)
	}
	fm := memoryFrontmatter{Area: "communication"}
	if got := memoryAreaOnReadWithWords("", fm, func() string { return body }); got == "communication" {
		t.Fatalf("stored communication area kept for a build belief: %q", got)
	}
}
