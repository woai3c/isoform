package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMorningBriefRankedEditionSurvivesStorageAndNeverUsesTemplate(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	brief.Slot, brief.rankedEdition, brief.NarrativeStatus = mobileBriefSlotMorning, true, "ok"
	brief.Narrative = []MobileBriefPara{{Text: "Review the draft today.", Refs: brief.Sections.OpenLoops[0].Refs}}
	published := mobileBriefForPublication(brief)
	if published.NarrativeStatus != "ok" || len(published.Narrative) != 1 {
		t.Fatalf("short ranked brief was suppressed: %+v", published)
	}
	stored, err := marshalStoredMobileBrief(published)
	if err != nil {
		t.Fatal(err)
	}
	var restored MobileBriefV1
	if err := unmarshalStoredMobileBrief(stored, &restored); err != nil || !restored.rankedEdition {
		t.Fatalf("ranked edition was not retained: %v", err)
	}
	wire, err := json.Marshal(restored)
	if err != nil || strings.Contains(string(wire), "rankedEdition") {
		t.Fatalf("internal edition marker appeared on the wire: %s, %v", wire, err)
	}
	if speech, template := mobileBriefSpeech(restored, "en"); speech == "" || template {
		t.Fatalf("short ranked brief was not spoken: %q, template=%v", speech, template)
	}

	missing := restored
	missing.Narrative, missing.NarrativeStatus = nil, "unavailable"
	if got := (&App{}).mobileBriefWithTemplate(missing); len(got.Narrative) != 0 {
		t.Fatal("read-time template replaced the unavailable ranked brief")
	}
	if speech, template := mobileBriefSpeech(missing, "en"); speech != "" || template {
		t.Fatalf("audio replaced the unavailable ranked brief: %q, template=%v", speech, template)
	}
	tooLong := []MobileBriefPara{{Text: strings.Repeat("x", mobileBriefNarrativeMaxBytes+1), Refs: brief.Narrative[0].Refs}}
	if got := prepareMobileBriefNarrative(restored, tooLong, "en"); len(got) != 0 {
		t.Fatal("budget substituted a template for an invalid ranked brief")
	}
}
