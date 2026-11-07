package main

import (
	"context"
	"strings"
	"testing"
)

// R74 (design v1.0.0): every brief opens with a sentence written from what it holds.
// Nothing in it: "A quiet evening. Nothing needs you." Items but nothing picked: the
// two largest sections, in the brief's order. Something needing the person: "{n} need you."

func TestEmptyBriefOpensWithTheQuietSentence(t *testing.T) {
	if got := mobileBriefOpeningSentence(MobileBriefV1{Slot: mobileBriefSlotEvening}, "English"); got != "A quiet evening. Nothing needs you." {
		t.Fatalf("empty evening = %q", got)
	}
	if got := mobileBriefOpeningSentence(MobileBriefV1{Slot: mobileBriefSlotMorning}, "English"); got != "A quiet morning. Nothing needs you." {
		t.Fatalf("empty morning = %q", got)
	}
	if got := mobileBriefOpeningSentence(MobileBriefV1{Slot: mobileBriefSlotEvening}, "Chinese"); got != "安静的晚上。没有需要你处理的事。" {
		t.Fatalf("empty zh evening = %q", got)
	}
}

func TestItemsButNothingPickedNamesTheTwoLargestSections(t *testing.T) {
	brief := MobileBriefV1{Slot: mobileBriefSlotEvening, Sections: MobileBriefSections{
		Meetings: make([]MobileBriefItem, 8),
		Moved:    make([]MobileBriefItem, 8),
		Learned:  make([]MobileBriefItem, 2),
	}}
	want := "Nothing needs you this evening. Your calendar had 8 meetings today, and 8 things moved."
	if got := mobileBriefOpeningSentence(brief, "English"); got != want {
		t.Fatalf("items sentence = %q, want %q", got, want)
	}
	if got := mobileBriefOpeningSentence(brief, "Chinese"); !strings.Contains(got, "8场会议") || !strings.Contains(got, "8件事推进了") || strings.Contains(got, "学到") {
		t.Fatalf("zh items sentence = %q", got)
	}
}

func TestSomethingNeedingYouStartsWithTheCount(t *testing.T) {
	brief := MobileBriefV1{Slot: mobileBriefSlotEvening, Sections: MobileBriefSections{Review: make([]MobileBriefItem, 3), Moved: make([]MobileBriefItem, 1)}}
	got := mobileBriefOpeningSentence(brief, "English")
	if !strings.HasPrefix(got, "3 need you this evening.") || !strings.Contains(got, "1 things moved") {
		t.Fatalf("need-you sentence = %q", got)
	}
	if got := mobileBriefOpeningSentence(MobileBriefV1{Slot: mobileBriefSlotEvening, Sections: MobileBriefSections{Review: make([]MobileBriefItem, 3)}}, "Chinese"); !strings.HasPrefix(got, "3件事需要你处理。") {
		t.Fatalf("zh need-you sentence = %q", got)
	}
}

// The template opening is a sentence the publication floor does not cut (the floor is for model text).
func TestTemplateOpeningPublishes(t *testing.T) {
	brief := MobileBriefV1{Slot: mobileBriefSlotEvening, NarrativeStatus: "skipped", Narrative: mobileBriefNarrativeTemplate(MobileBriefV1{Slot: mobileBriefSlotEvening}, "English")}
	published := mobileBriefForPublication(brief)
	if published.NarrativeStatus == "quiet" || len(published.Narrative) != 1 {
		t.Fatalf("published status=%q paragraphs=%d, want the opening kept", published.NarrativeStatus, len(published.Narrative))
	}
}

// Quiet only for nothing in the brief.
func TestQuietOnlyWhenNothingIsInIt(t *testing.T) {
	if published := mobileBriefForPublication(MobileBriefV1{Slot: mobileBriefSlotEvening, NarrativeStatus: "skipped"}); published.NarrativeStatus != "quiet" {
		t.Fatalf("status = %q, want quiet for a brief with no paragraphs", published.NarrativeStatus)
	}
}

// A template opening is never spoken.
func TestTemplateOpeningIsNotSpoken(t *testing.T) {
	brief := MobileBriefV1{Slot: mobileBriefSlotEvening, NarrativeStatus: "skipped", Narrative: []MobileBriefPara{{Text: "A quiet evening. Nothing needs you."}}}
	if text, _ := mobileBriefSpeech(brief, "en"); text != "" {
		t.Fatalf("a template opening was spoken: %q", text)
	}
}

// withoutOpening drops the sentence a brief with nothing to name opens with, so a
// test that counts reminders counts only what the brief names (design R74).
func withoutOpening(paragraphs []MobileBriefPara) []MobileBriefPara {
	var out []MobileBriefPara
	for _, p := range paragraphs {
		if !strings.HasPrefix(p.Text, "A quiet ") && !strings.HasPrefix(p.Text, "Nothing needs you") && !strings.HasPrefix(p.Text, "安静的") && !strings.Contains(p.Text, "需要你处理") {
			out = append(out, p)
		}
	}
	return out
}

// testModelCompose is the model's reply a test brief gets in place of a writer call: a full-length
// paragraph (at least the publication floor, under the speech byte bound) citing the brief's own
// evidence, as a model brief does (design R74).
func testModelCompose(_ context.Context, _ *App, brief MobileBriefV1, _ []MobileBriefCard) ([]MobileBriefPara, string, []mobileBriefCardWords) {
	var refs []string
	if len(brief.Evidence) > 0 {
		refs = []string{brief.Evidence[0].Ref}
	}
	text := strings.TrimSpace(strings.Repeat("All clear, one quick check left today. ", 15))
	return []MobileBriefPara{{Text: text, Refs: refs}}, "ok", nil
}

// The stored brief gets one log line: picks, who wrote the words and the sentence
// count, never the words (design R74 v1.0.0).
func TestBriefLogSummaryIsOneLineWithoutWords(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	brief.NarrativeStatus = "ok"
	brief.Narrative = []MobileBriefPara{{Text: "SECRETWORD one. Mac 1.0.41 is public. Review it?"}}
	line := mobileBriefLogSummary(brief)
	if strings.Contains(line, "SECRETWORD") || strings.Contains(line, "\n") {
		t.Fatalf("log line carries words or spans lines: %q", line)
	}
	if !strings.Contains(line, "writer=model") || !strings.Contains(line, "sentences=3") {
		t.Fatalf("log line = %q, want writer=model sentences=3", line)
	}
	brief.NarrativeStatus = "unavailable"
	if line := mobileBriefLogSummary(brief); !strings.Contains(line, "writer=template") {
		t.Fatalf("template words not named: %q", line)
	}
	brief.Narrative = nil
	if line := mobileBriefLogSummary(brief); !strings.Contains(line, "writer=none") || !strings.Contains(line, "sentences=0") {
		t.Fatalf("no words not named: %q", line)
	}
}
