package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func mobileBriefVoiceFixture() MobileBriefV1 {
	asOf := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	return buildMobileBriefV1(mobileBriefInput{
		Slot: mobileBriefSlotEvening, WindowStart: asOf.Add(-10 * time.Hour), AsOf: asOf,
		Location: time.UTC, Timezone: "UTC",
		Moved: []mobileBriefCandidate{{
			SourceKind: "history", SourceID: "review", Title: "Pricing draft at 9:15 AM",
			Status: "needs_review", Detail: "Private draft text at 09:15", At: asOf.Add(-7 * time.Hour),
		}, {
			SourceKind: "history", SourceID: "outcome", Title: "Sent the pricing proposal at 10:00 AM",
			Status: "verified", At: asOf.Add(-6 * time.Hour),
		}},
		OpenLoops: []mobileBriefCandidate{{
			SourceKind: "purpose", SourceID: "pricing", Title: "Agree on the pricing plan",
			Kind: "commitment", Status: "active", Detail: "Private waiting condition", At: asOf.Add(-time.Hour),
		}},
		Meetings: []mobileBriefCandidate{{
			SourceKind: mobileBriefCalendarSource, SourceID: "design", Title: "Design review",
			Status: "upcoming", Kind: "meeting", At: asOf.Add(time.Hour),
		}, {
			SourceKind: mobileBriefCalendarSource, SourceID: "past", Title: "Team sync at 11:30 AM",
			Status: "done", Kind: "meeting", At: asOf.Add(-6 * time.Hour),
		}},
	})
}

func TestMobileBriefVoiceFixtureOmitsPastClockTimes(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	prompt := mobileBriefTestPrompt(brief, "English")
	paragraphs := mobileBriefNarrativeTemplate(brief, "English")
	if len(paragraphs) != 4 {
		t.Fatalf("template paragraphs = %d, want forward work, Highlights, then closing offers", len(paragraphs))
	}
	// The hold needs the current reference as machine metadata. Keep checking
	// every source clock and every paragraph for past-time narration.
	text := strings.Replace(prompt, "asOf="+brief.AsOf, "asOf=<current reference>", 1)
	for _, paragraph := range paragraphs {
		text += "\n" + paragraph.Text
		if len(paragraph.Refs) == 0 {
			t.Fatal("a template paragraph must carry evidence")
		}
	}
	for _, past := range []string{"9:15 AM", "09:15", "11:30 AM", "11:30", "10:00 AM", "4:00 PM", "5:00 PM", "T17:00:00Z"} {
		if strings.Contains(text, past) {
			t.Errorf("brief voice carries past clock time %q", past)
		}
	}
	// The design review is later today: a clock, never the brief's own weekday.
	if !strings.Contains(text, "at 6:00 PM") || strings.Contains(text, "Friday") {
		t.Error("future preparation time must stay in the reader's local format")
	}
	for _, held := range []string{"Private draft text", "Private waiting condition"} {
		if strings.Contains(text, held) {
			t.Errorf("item details entered the voice: %q", held)
		}
	}
	if err := validateMobileBriefNarrativeVoice(brief, paragraphs, "English"); err != nil {
		t.Fatalf("the template violates its voice guard: %v", err)
	}
}

func TestMobileBriefNarrativePromptChiefOfStaffRulesAndOrder(t *testing.T) {
	prompt := mobileBriefTestPrompt(mobileBriefVoiceFixture(), "English")
	for _, rule := range []string{
		"chief of staff", `Write as "I" to "you"`, "computer and phone",
		"give no clock time for anything past or on now", "never write 9am, 9:00am or 12:30pm", `never "I saw", "I noticed" or "I can't tell"`,
		"no lists, headings, markdown, quotation marks, emoji or exclamation marks", "never guess beyond the facts given",
		"| Design review | at 6:00 PM",
	} {
		if !strings.Contains(prompt, rule) {
			t.Errorf("prompt is missing rule %q", rule)
		}
	}
}

func TestMobileBriefNarrativeVoiceOnlyAllowsCitedFutureClocks(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	var futureRef, pastRef string
	for _, ref := range brief.Evidence {
		switch ref.SourceID {
		case "design":
			futureRef = ref.Ref
		case "review":
			pastRef = ref.Ref
		}
	}
	for _, tc := range []struct {
		name, text, ref string
		valid           bool
	}{
		{"future", "You have Design review at 6:00 PM. Prepare what you need.", futureRef, true},
		{"no clock", "Review your pricing draft before you choose the next step.", pastRef, true},
		{"past", "Your pricing draft was ready at 9:15 AM.", pastRef, false},
		{"wrong ref", "You have Design review at 6:00 PM.", pastRef, false},
		{"invented time", "You have Design review at 7:00 PM.", futureRef, false},
		{"unsupported format", "You have Design review at 18:00.", futureRef, false},
		{"exclamation", "Review your pricing draft!", pastRef, false},
		{"platform name", "Review the draft on your Mac.", pastRef, false},
		{"decorative symbol", "Review your pricing draft \u2726", pastRef, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMobileBriefNarrativeVoice(brief, []MobileBriefPara{{Text: tc.text, Refs: []string{tc.ref}}}, "English")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestMobileBriefNarrativeTemplateQualifiesAttendance(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"attended", "Team sync"},
		{"skipped", "no matching call on this computer"},
		{"unknown", "couldn't tell whether you attended your Team sync"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			brief := MobileBriefV1{
				AsOf: "2026-10-16T17:00:00Z", Timezone: "UTC",
				Evidence:          []MobileBriefRef{{Ref: "e1", SourceKind: mobileBriefAttendanceSource, SourceID: "sync", Label: "Team sync"}},
				MeetingAttendance: []MobileBriefAttendance{{EventID: "sync", Title: "Team sync at 11:30 AM", State: tc.state, Scope: "computer", Refs: []string{"e1"}}},
			}
			paragraphs := mobileBriefNarrativeTemplate(brief, "English")
			// Today review v1.1.6 allows uncertain attendance to be omitted.
			if tc.state == "unknown" {
				if len(paragraphs) != 0 {
					t.Fatalf("uncertain attendance was not omitted: %+v", paragraphs)
				}
				if prompt := mobileBriefTestPrompt(brief, "English"); strings.Contains(prompt, "Team sync") {
					t.Fatal("past attendance reached the prompt")
				}
				return
			}
			// An attended call is a Highlight with nothing left to offer; the
			// others close with a follow-up offer.
			want := 2
			if tc.state == "attended" {
				want = 1
			}
			if len(paragraphs) != want || !strings.Contains(paragraphs[0].Text, tc.want) {
				t.Fatalf("attendance template = %+v", paragraphs)
			}
			if mobileBriefDisplayClock.MatchString(paragraphs[0].Text) {
				t.Fatal("attendance template contains a past clock time")
			}
			// Since backlog item 65 attendance is the template's alone; the
			// written brief speaks of the day ahead.
			if prompt := mobileBriefTestPrompt(brief, "English"); strings.Contains(prompt, "Team sync") {
				t.Fatal("past attendance reached the prompt")
			}
		})
	}
}

func TestMobileBriefNarrativeTemplateNoModelKeepsVoiceAndPrivacy(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	paragraphs, status, _ := composeMobileBriefNarrative(context.Background(), nil, brief, nil)
	if status != "unavailable" || len(paragraphs) != 4 {
		t.Fatalf("no-model brief: status=%q paragraphs=%+v", status, paragraphs)
	}
	for _, language := range []string{"English", "Simplified Chinese"} {
		paragraphs := mobileBriefNarrativeTemplate(brief, language)
		if len(paragraphs) == 0 {
			t.Fatalf("%s template is empty", language)
		}
		if err := validateMobileBriefNarrativeVoice(brief, paragraphs, language); err != nil {
			t.Fatalf("%s template violates the voice: %v", language, err)
		}
	}
	// Old briefs can retain browsing/file items, but neither template nor
	// model may revive them as review or plan context.
	brief.Sections.Moved = append(brief.Sections.Moved, MobileBriefItem{Title: "Private filename", Refs: []string{"file"}})
	brief.Sections.OpenLoops = append(brief.Sections.OpenLoops, MobileBriefItem{Title: "Private search", Kind: "commitment", Refs: []string{"web"}})
	brief.Evidence = append(brief.Evidence,
		MobileBriefRef{Ref: "file", SourceKind: mobileBriefFileSource, SourceID: "f", Label: "Private filename"},
		MobileBriefRef{Ref: "web", SourceKind: mobileBriefWebSource, SourceID: "w", Label: "Private search"},
	)
	text := mobileBriefTestPrompt(brief, "English")
	for _, paragraph := range mobileBriefNarrativeTemplate(brief, "English") {
		text += paragraph.Text
	}
	for _, held := range []string{"Private filename", "Private search"} {
		if strings.Contains(text, held) {
			t.Fatalf("held-back source returned to the voice: %q", held)
		}
	}
}

func TestMobileBriefWithoutClockTimes(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"Team sync at 11:30 AM", "Team sync"},
		{"Design review 09:30 to 10:00", "Design review"},
		{"Review at 9 am", "Review"},
		{"下午3点讨论方案", "讨论方案"},
		{"08:00 to 09:30", ""},
		{"09:30 to 10:00, 30 min", "30 min"},
		{"Q3 pricing review", "Q3 pricing review"},
		{"Who the note came from", "Who the note came from"},
	} {
		if got := mobileBriefWithoutClockTimes(tc.input); got != tc.want {
			t.Errorf("without clocks %q = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMobileBriefNarrativePeopleAreBoundedContextOnly(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	for _, name := range []string{"Dana", "Priya", "Chen", "Mei"} {
		brief.Sections.People = append(brief.Sections.People, MobileBriefItem{Title: name, Detail: "Private conversation", Refs: []string{name}})
		brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: name, SourceKind: "principal", SourceID: name, Label: name})
	}
	brief.Sections.People = append(brief.Sections.People, MobileBriefItem{Title: "Private mail subject", Refs: []string{"mail"}})
	brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: "mail", SourceKind: "mail", SourceID: "thread", Label: "Private mail subject"})
	// Since backlog item 65 a sighting is no part of the written brief: the
	// prompt is the same with or without them.
	if prompt := mobileBriefTestPrompt(brief, "English"); prompt != mobileBriefTestPrompt(mobileBriefVoiceFixture(), "English") {
		t.Fatalf("people context reached the prompt:\n%s", prompt)
	}
	brief.Sections = MobileBriefSections{People: brief.Sections.People}
	if paragraphs, status, _ := composeMobileBriefNarrative(context.Background(), nil, brief, nil); len(paragraphs) != 0 || status != "skipped" {
		t.Fatalf("sightings alone created a brief: %+v %s", paragraphs, status)
	}
}

// The chief of staff prompt and the Purpose Map meet in one prompt. Working-toward
// items reach the model with their bounded progress and map step; a commitment
// already reported in main chat, an item backed by browsing, and the content-free
// fallback for purged evidence never do.
func TestMobileBriefNarrativePromptCarriesThePurposeMapWithItsHoldbacks(t *testing.T) {
	brief := mobileBriefVoiceFixture()
	brief.Evidence = append(brief.Evidence,
		MobileBriefRef{Ref: "p1", SourceKind: "purpose", SourceID: "goal-a", Label: "Ship pricing page"},
		MobileBriefRef{Ref: "a1", SourceKind: "activity", SourceID: "run-a", Label: "Drafted pricing copy"},
		MobileBriefRef{Ref: "p2", SourceKind: "purpose", SourceID: "commitment-b", Label: "Reply to Dana"},
		MobileBriefRef{Ref: "p3", SourceKind: "purpose", SourceID: "goal-c", Label: "Browsing goal"},
		MobileBriefRef{Ref: "w1", SourceKind: mobileBriefWebSource, SourceID: "site", Label: "private-site.example"},
	)
	brief.Sections.WorkingToward = []MobileBriefItem{
		{Title: "Ship pricing page", Detail: "Drafted pricing copy", NextStep: "Review the headline", Refs: []string{"p1", "a1"}},
		{Title: "Browsing goal", Detail: "Visited private-site.example", Refs: []string{"p3", "w1"}},
		{Title: "Purged goal", Detail: "Evidence no longer on this computer.", EvidenceUnavailable: true},
	}
	brief.Sections.Commitments = []MobileBriefItem{
		{Title: "Reply to Dana", Status: "due_today", Refs: []string{"p2"}, ChatMessageID: "message-1", ChatSessionID: "chat-1"},
	}
	prompt := mobileBriefTestPrompt(brief, "English")
	// A goal reaches the prompt only as a pick that moved, never with its
	// progress text (mobile_brief_picks.go).
	for _, withheld := range []string{"Browsing goal", "private-site.example", "Purged goal", "Evidence no longer on this computer", "Reply to Dana", "Drafted pricing copy"} {
		if strings.Contains(prompt, withheld) {
			t.Errorf("prompt discloses %q", withheld)
		}
	}
	for _, ref := range mobileBriefNarrativeInputEvidence(brief) {
		if ref.Ref == "w1" || ref.Ref == "p2" {
			t.Errorf("evidence %s must not be citable", ref.Ref)
		}
	}
}
