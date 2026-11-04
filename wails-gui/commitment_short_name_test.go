package main

import (
	"testing"
	"time"
	"unicode/utf8"
)

func TestCommitmentShortNamePreservesSourceAndConditions(t *testing.T) {
	source := "Make sure I do some sort of calf race or exercise for AFO like every day"
	want := "AFO calf raises, every day"
	if got := commitmentShortName(source); got != want {
		t.Fatalf("name = %q", got)
	}
	if got := commitmentShortName("Pay rent only if the invoice arrives"); got != "Pay rent only if the invoice arrives" {
		t.Fatalf("lost condition: %q", got)
	}
	for source, want := range map[string]string{
		"Remind me to call Sam about the lease tomorrow":              "Call Sam about the lease tomorrow",
		"I need to send the invoice to Acme by Friday or something":   "Send the invoice to Acme by Friday",
		"Make sure I finish the release notes before the launch.":     "Finish the release notes",
		"Okay, don't forget to water the plants like every week.":     "Water the plants, every week",
		"I've got to renew my passport only if the trip is confirmed": "Renew my passport",
		"Make sure I file the permit if the city asks":                "File the permit if the city asks",
		"Ship Isoform 1.0": "Ship Isoform 1.0",
		"Pay rent.":        "Pay rent.",
	} {
		if got := commitmentShortName(source); got != want {
			t.Errorf("commitmentShortName(%q) = %q, want %q", source, got, want)
		}
	}
	input, err := userPurposeCreateInput(userPurposeCreate{Key: "commitment", Origin: "desktop", Kind: PurposeKindCommitment, Text: source}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if input.Title != want || input.DesiredOutcome != source {
		t.Fatalf("source or name lost: %+v", input)
	}
	expected := userPurposeEvidence("user-goal:commitment", "desktop", PurposeStanceSupport, source, input.OccurredAt)
	if input.Evidence[0].SourceHash != expected.SourceHash {
		t.Fatal("source evidence no longer records the original words")
	}
}

func TestCommitmentShortNameKeepsFortyCharactersCutAtAWord(t *testing.T) {
	for source, want := range map[string]string{
		"Remind me to email Priya the revised budget for the community garden before the board meeting": "Email Priya the revised budget",
		"I need to finish reviewing the quarterly onboarding survey results with Marisol":               "Finish reviewing the quarterly",
		"Schedule the annual furnace inspection and replace the filters in the basement":                "Schedule the annual furnace inspection",
		"Supercalifragilisticexpialidociousandmorewordsjoinedtogether":                                  "Supercalifragilisticexpialidociousandmor",
		"给社区花园预算做最后一次核对，然后在周五之前发给普丽亚并抄送理事会所有成员，确认大家都看过":                                                 "给社区花园预算做最后一次核对，然后在周五之前发给普丽亚并抄送理事会所有成员",
	} {
		got := commitmentShortName(source)
		if got != want {
			t.Errorf("commitmentShortName(%q) = %q, want %q", source, got, want)
		}
		if n := utf8.RuneCountInString(got); n > commitmentShortNameLimit {
			t.Errorf("commitmentShortName(%q) has %d characters", source, n)
		}
	}
	source := "Make sure I send the signed lease renewal to Priya and keep a copy for the folder"
	input, err := userPurposeCreateInput(userPurposeCreate{Key: "lease", Origin: "desktop", Kind: PurposeKindCommitment, Text: source}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(input.Title) > commitmentShortNameLimit || input.Title != "Send the signed lease renewal to Priya" || input.DesiredOutcome != source {
		t.Fatalf("short name or full words lost: title %q, detail %q", input.Title, input.DesiredOutcome)
	}
}

// chat-review v1.0.10 H4: the live AFO example reads as a plain name, with
// the person's words kept in full as the detail.
func TestCommitmentShortNameRewordsTheAFOExample(t *testing.T) {
	source := "Make sure I do some sort of calf race or exercise for AFO like every day"
	if got := commitmentShortName(source); got != "AFO calf raises, every day" {
		t.Fatalf("commitmentShortName = %q", got)
	}
	if got := commitmentShortName("Stretch my hamstrings daily"); got != "Stretch my hamstrings, daily" {
		t.Fatalf("cadence = %q", got)
	}
}
