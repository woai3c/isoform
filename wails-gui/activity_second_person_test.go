package main

import (
	"strings"
	"testing"
)

func TestSecondPersonLineTalksToThePerson(t *testing.T) {
	self := []string{"bo"}
	cases := []struct{ in, want string }{
		{"The user browsed their GitHub feed.", "You browsed your GitHub feed."},
		{"The user asked Claude Code to fix the test.", "You asked Claude Code to fix the test."},
		{"The person's role there was to oversee the release.", "Your role there was to oversee the release."},
		{"Bo asked for both.", "You asked for both."},
		{"Reviewed PR #12. The user is waiting on CI.", "Reviewed PR #12. You are waiting on CI."},
		{"Opened the doc. This looks like a launch plan.", "Opened the doc."},
		{"- **Done:** the user merged the fix.", "- **Done:** You merged the fix."},
		{"用户在写周报。", "你在写周报。"},
		{"Bobby reviewed Alice's draft.", "Bobby reviewed Alice's draft."},
		{"### Technical\n- The user has a PR open", "### Technical\n- You have a PR open"},
	}
	for _, c := range cases {
		if got := secondPersonLine(c.in, self); got != c.want {
			t.Errorf("secondPersonLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEveryActivityPromptCarriesTheSecondPersonRule(t *testing.T) {
	prompts := map[string]string{
		"events":    eventExtractionInstruction,
		"coherence": coherenceVerifyInstruction,
		"moments":   buildMomentsPrompt(nil, nil, ""),
		"weekly":    buildWeeklySummaryPrompt(&WeeklySummaryData{}, ""),
		"timeline":  buildTimelineGroupSummaryPrompt(ActivityTimelineGroup{AppName: "Notes", AppKey: "notes"}, "", ""),
		"composer":  buildComposerPrompt(nil, ActivityTimelineRunRow{AppName: "Notes", AppKey: "notes"}, ActivityTimelineGroupSummary{}, composerTransition{}, ""),
		"outcome":   buildThreadOutcomePrompt(nil, "en"),
	}
	for name, prompt := range prompts {
		if !strings.Contains(prompt, activitySecondPersonRule) {
			t.Errorf("%s prompt has no second-person rule", name)
		}
	}
}
