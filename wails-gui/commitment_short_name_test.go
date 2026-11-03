package main

import (
	"testing"
	"time"
)

func TestCommitmentShortNamePreservesSourceAndConditions(t *testing.T) {
	source := "Make sure I do some sort of calf race or exercise for AFO like every day"
	want := "Calf race or exercise for AFO every day"
	if got := commitmentShortName(source); got != want {
		t.Fatalf("name = %q", got)
	}
	if got := commitmentShortName("Pay rent only if the invoice arrives"); got != "Pay rent only if the invoice arrives" {
		t.Fatalf("lost condition: %q", got)
	}
	for source, want := range map[string]string{
		"Remind me to call Sam about the lease tomorrow":              "Call Sam about the lease tomorrow",
		"I need to send the invoice to Acme by Friday or something":   "Send the invoice to Acme by Friday",
		"Make sure I finish the release notes before the launch.":     "Finish the release notes before the launch",
		"Okay, don't forget to water the plants like every week.":     "Water the plants every week",
		"I've got to renew my passport only if the trip is confirmed": "Renew my passport only if the trip is confirmed",
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
