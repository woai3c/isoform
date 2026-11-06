package main

import "testing"

func TestBeliefInSecondPerson(t *testing.T) {
	cases := map[string]string{
		"Accepts partial pull requests when unresolved items are called out":             "You accept partial pull requests when unresolved items are called out",
		"Verifies release checksums":                                                     "You verify release checksums",
		"Turns on Auto-fix":                                                              "You turn on Auto-fix",
		"Queues a full check after each merge":                                           "You queue a full check after each merge",
		"Compares recent runs with a baseline":                                           "You compare recent runs with a baseline",
		"Never lets uncertain replies replay automatically":                              "You never let uncertain replies replay automatically",
		"Always checks the results":                                                      "You always check the results",
		"Hands work to other agents and coordinates them through a hub":                  "You hand work to other agents and coordinate them through a hub",
		"Uses design reviews to find launch blockers before release":                     "You use design reviews to find launch blockers before release",
		"Wants public repo commits to carry the real fix messages":                       "You want public repo commits to carry the real fix messages",
		"Fast-merges urgent fixes after focused checks pass":                             "You fast-merge urgent fixes after focused checks pass",
		"Guards mornings for deep work":                                                  "You guard mornings for deep work",
		"Puts every plan on the calendar":                                                "You put every plan on the calendar",
		"Keeps the backup hub read-only":                                                 "You keep the backup hub read-only",
		"Relies on short standups":                                                       "You rely on short standups",
		"Pushes fixes to a small branch first":                                           "You push fixes to a small branch first",
		"Has a standing Friday review":                                                   "You have a standing Friday review",
		"“Prefers bun over npm”":                                                         "You prefer bun over npm",
		"I prefer bun over npm":                                                          "You prefer bun over npm",
		"I am a morning writer":                                                          "You are a morning writer",
		"My mornings are for deep work":                                                  "Your mornings are for deep work",
		"You keep emails to three sentences":                                             "You keep emails to three sentences",
		"Tools for the Lisbon trip":                                                      "Tools for the Lisbon trip",
		"News about Maya Okafor":                                                         "News about Maya Okafor",
		"Process notes":                                                                  "Process notes",
		"Keeps 8 to 11 AM free for writing and code review, and declines meetings then.": "You keep 8 to 11 AM free for writing and code review, and decline meetings then.",
		"You keep 8 to 11 AM free, and declines meetings then":                           "You keep 8 to 11 AM free, and decline meetings then",
		"Drafts a reply and sends it before noon":                                        "You draft a reply and send it before noon",
		"Reviews pull requests and is quick to merge":                                    "You review pull requests and are quick to merge",
		"Keeps notes and plans in one doc":                                               "You keep notes and plans in one doc",
		"Guards mornings, then answers email after lunch":                                "You guard mornings, then answer email after lunch",
		"习惯在终端里用语音输入":                                                                    "习惯在终端里用语音输入",
		"":                                                                               "",
	}
	for in, want := range cases {
		if got := beliefInSecondPerson(in); got != want {
			t.Errorf("beliefInSecondPerson(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMobileGoalTitleShortensACommitment(t *testing.T) {
	dictated := "Make sure I do some sort of calf race or exercise for AFO like every day"
	if got := mobileGoalTitle(PurposeKindCommitment, dictated); got != "AFO calf raises, every day" {
		t.Fatalf("commitment title = %q", got)
	}
	if got := mobileGoalTitle(PurposeKindGoal, "Run a half marathon in March"); got != "Run a half marathon in March" {
		t.Fatalf("goal title = %q", got)
	}
}

// Launch walk Y28: a belief without a subject still reads "You …".
func TestBeliefInSecondPersonWritesSubjectlessBeliefsAsYou(t *testing.T) {
	cases := map[string]string{
		"Approves disk cleanup only after reviewing a size audit of candidates":   "You approve disk cleanup only after reviewing a size audit of candidates",
		"Requires the focused tests to pass before pushing":                       "You need the focused tests to pass before pushing",
		"Building Isoform desktop app and iPhone companion with Claude and Codex": "You're building Isoform desktop app and iPhone companion with Claude and Codex",
		"Meeting notes stay in the hub":                                           "Meeting notes stay in the hub",
	}
	for in, want := range cases {
		if got := beliefInSecondPerson(in); got != want {
			t.Errorf("beliefInSecondPerson(%q) = %q, want %q", in, got, want)
		}
	}
}
