package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// today-review v1.1.4 #1: what the person said to keep out of the brief stays out.
func TestBriefV114StatedExclusionHolds(t *testing.T) {
	terms := mobileBriefStatedExclusions([]MemoryEntry{
		{Content: "Keep PT out of my morning brief."},
		{Title: "简报里不要再提健身了。"},
	})
	if !slices.Contains(terms, "PT") || !slices.Contains(terms, "健身") {
		t.Fatalf("stated exclusions = %q", terms)
	}
	if !mobileBriefExcluded(terms, "PT appointment at 4") || !mobileBriefExcluded(terms, "周四健身课") || mobileBriefExcluded(terms, "Optimize the build") {
		t.Fatal("exclusion matched the wrong words")
	}
	in := mobileBriefInput{Commitments: []mobileBriefCandidate{{Title: "PT appointment"}, {Title: "Atlas launch"}}, Meetings: []mobileBriefCandidate{{Title: "健身"}}}
	out := mobileBriefWithoutExcluded(in, terms)
	if len(out.Commitments) != 1 || out.Commitments[0].Title != "Atlas launch" || len(out.Meetings) != 0 {
		t.Fatalf("excluded items reached the writer: %+v", out)
	}
	brief := MobileBriefV1{Narrative: []MobileBriefPara{{Text: "Your PT appointment is at 4."}, {Text: "Atlas launches Friday."}}}
	blank := mobileBriefBlankExcluded(brief, terms)
	if strings.Contains(blank.Narrative[0].Text, "PT") || blank.Narrative[1].Text != "Atlas launches Friday." {
		t.Fatalf("stored brief kept the excluded paragraph: %+v", blank.Narrative)
	}
	if got := mobileBriefStatedExclusions(nil); len(got) != 0 {
		t.Fatalf("no note, no exclusion: %q", got)
	}
}

func TestBriefV118StoredPTExclusionRemovesReferencesAndAudio(t *testing.T) {
	terms := []string{"PT"}
	brief := MobileBriefV1{
		NarrativeStatus: "ok",
		Narrative:       []MobileBriefPara{{Text: "Your calf exercise for AFO was due yesterday.", Refs: []string{"pt"}}, {Text: "The launch is ready.", Refs: []string{"work"}}, {Text: "Want help with both?", Refs: []string{"pt", "work"}}},
		Sections:        MobileBriefSections{Commitments: []MobileBriefItem{{Title: "Calf raises for AFO", Refs: []string{"pt"}}, {Title: "Launch", Refs: []string{"work"}}}},
		Evidence:        []MobileBriefRef{{Ref: "pt", Label: "PT"}, {Ref: "work", Label: "Launch"}},
		Cards:           []MobileBriefCard{{Label: "Plan exercises", Refs: []string{"pt"}}},
		Offers:          []MobileBriefOffer{{Label: "Plan exercises", Refs: []string{"pt"}}},
		Audio:           &MobileBriefAudio{},
	}
	got := mobileBriefHygiene(mobileBriefBlankExcluded(brief, terms))
	if len(got.Narrative) != 1 || got.Narrative[0].Text != "The launch is ready." || len(got.Sections.Commitments) != 1 || len(got.Evidence) != 1 || len(got.Cards) != 0 || len(got.Offers) != 0 || got.Audio != nil {
		t.Fatalf("excluded source remained in delivered brief: %+v", got)
	}
	if !mobileBriefExcluded(terms, "physical therapy") || !mobileBriefExcluded(terms, "calf exercise for AFO") || mobileBriefExcluded(terms, "AFO launch") {
		t.Fatal("PT aliases matched incorrectly")
	}
	input := mobileBriefInput{Commitments: []mobileBriefCandidate{{Title: "calf exercise for AFO"}, {Title: "Launch"}}}
	if kept := mobileBriefWithoutExcluded(input, terms); len(kept.Commitments) != 1 || kept.Commitments[0].Title != "Launch" {
		t.Fatalf("excluded input reached writer: %+v", kept.Commitments)
	}
}

// today-review v1.1.4 #3: no classifier labels, and "today" only with a record from today.
func TestBriefV114PlainParagraphAndTodayProof(t *testing.T) {
	for _, tc := range []struct {
		text           string
		closing, today bool
		want           string
	}{
		{"Atlas is due Friday. This one is optional.", false, true, "Atlas is due Friday."},
		{"Dana replied. It is urgent.", false, true, "Dana replied."},
		{"The memo is due today. Dana replied.", false, false, "Dana replied."},
		{"The memo is due today. Dana replied.", false, true, "The memo is due today. Dana replied."},
		{"Atlas is ready. I can draft the note.", false, true, "Atlas is ready."},
		{"Want me to draft the note?", true, true, "Want me to draft the note?"},
		{"这是一条建议。Atlas 周五到期。", false, true, "Atlas 周五到期。"},
		{"备忘录今天到期。Dana 回复了。", false, false, "Dana 回复了。"},
	} {
		if got := mobileBriefPlainParagraph(tc.text, tc.closing, tc.today); got != tc.want {
			t.Errorf("%q (closing=%v today=%v) = %q, want %q", tc.text, tc.closing, tc.today, got, tc.want)
		}
	}
	brief := MobileBriefV1{LocalDate: "2026-10-23", Timezone: "UTC",
		Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "a", ObservedAt: "2026-10-23T09:00:00Z"}, {Ref: "e2", SourceKind: "history", SourceID: "b", ObservedAt: "2026-10-20T09:00:00Z"}}}
	evidence := mobileBriefEvidenceByRef(brief)
	items := mobileBriefItemsByRef(brief)
	if !mobileBriefParagraphProvesToday(brief, MobileBriefPara{Refs: []string{"e1"}}, items, evidence) {
		t.Fatal("a record from today did not prove today")
	}
	if mobileBriefParagraphProvesToday(brief, MobileBriefPara{Refs: []string{"e2"}}, items, evidence) {
		t.Fatal("an older record proved today")
	}
}

// today-review v1.1.4 #4: the closing question's item gets no chip of its own.
func TestBriefV114OneOfferPerItem(t *testing.T) {
	item := MobileBriefItem{ID: "deliverable", Kind: "deliverable", Title: "weekly update", Refs: []string{"e1"}}
	brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "update"}},
		Sections:  MobileBriefSections{Deliverables: []MobileBriefItem{item}},
		Narrative: []MobileBriefPara{{Text: "The weekly update is ready.", Refs: []string{"e1"}}}}
	if chips := mobileBriefParagraphChipCards(mobileBriefLimitParagraphChips(brief, mobileBriefParagraphCards(brief, "en", nil, 3))); len(chips) != 1 {
		t.Fatalf("fixture needs one chip without a closing: %+v", chips)
	}
	brief.Narrative = append(brief.Narrative, MobileBriefPara{Text: "Want me to open the weekly update?", Refs: []string{"e1"}})
	if chips := mobileBriefParagraphChipCards(mobileBriefLimitParagraphChips(brief, mobileBriefParagraphCards(brief, "en", nil, 3))); len(chips) != 0 {
		t.Fatalf("closing's item also got a chip: %+v", chips)
	}
}

// today-review v1.1.4 #5: chips are written short names: at most 32
// English or 16 Chinese characters, never cut off.
func TestBriefV114ChipLabelsAreShortAndWhole(t *testing.T) {
	for _, tc := range []struct {
		label, lang string
		keep        bool
	}{
		{"Prep for 11:00 AM with Dan Smith", "en", true},
		{"Prep for 11:00 AM with Dana Whitfield", "en", false},
		{"准备明天产品团队季度规划评审会议", "zh", true},
		{"准备明天产品团队季度规划评审会议吧", "zh", false},
		{"Draft the reply to Dana…", "en", false},
		{"Draft the reply to Dana...", "en", false},
	} {
		got := mobileBriefQuestionChipLabel(tc.label, tc.lang)
		if (got != "") != tc.keep || got != "" && got != tc.label {
			t.Errorf("%q: got %q, keep=%v", tc.label, got, tc.keep)
		}
	}
}

// today-review v1.1.4 #6: the decline answer reads "Not now" / "暂不".
func TestBriefV114DeclineReadsNotNow(t *testing.T) {
	for _, tc := range []struct{ closing, label, message, want string }{
		{"Want me to draft the reply to Dana?", "Leave it", "No, leave it.", "Not now"},
		{"要我起草给 Dana 的回复吗？", "先放着", "不用，先放着", "暂不"},
	} {
		brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "dana"}},
			Narrative: []MobileBriefPara{{Text: "Dana is waiting.", Refs: []string{"e1"}}, {Text: tc.closing, Refs: []string{"e1"}}}}
		digest := mobileBriefClosingDigest(brief)
		card := MobileBriefCard{ID: "answer-1", Kind: mobileBriefCardAction, Mode: mobileBriefCardSend, Label: tc.label, Message: tc.message,
			AnswerTo: digest, Facts: digest, Writer: mobileBriefCardWriterModel, Refs: []string{"e1"}}
		answers := mobileBriefClosingAnswers(brief, []MobileBriefCard{card})
		if len(answers) != 1 || answers[0].Label != tc.want {
			t.Fatalf("decline = %+v, want %q", answers, tc.want)
		}
	}
}

// today-review v1.1.4 #9: topic cards use the brief's private filter.
func TestTopicV114CardsLeaveOutPrivateDetail(t *testing.T) {
	views := topicCardsWithoutPrivate([]TopicView{
		{ID: "ftb", Name: "ftb.ca.gov Web Pay"},
		{ID: "bank", Name: "Chase bank"},
		{ID: "atlas", Name: "Atlas", Latest: &TopicMoment{Text: "Paid the tax bill"}, Next: &TopicMoment{Text: "Ship the beta"}},
		{ID: "riffado", Name: "Riffado", Latest: &TopicMoment{Text: "Shipped the mixer"}},
	})
	if len(views) != 2 || views[0].ID != "atlas" || views[0].Latest != nil || views[0].Next == nil || views[1].Latest == nil {
		t.Fatalf("private topics or lines shown: %+v", views)
	}
}

// today-review v1.1.4 #8: a topic records the distinct days it had activity.
func TestTopicV114ActivityDays(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	days := topicAddActivityDay(nil, now.Add(-time.Hour).UnixMilli(), now)
	days = topicAddActivityDay(days, now.Add(-2*time.Hour).UnixMilli(), now)
	days = topicAddActivityDay(days, now.Add(-48*time.Hour).UnixMilli(), now)
	days = topicAddActivityDay(days, now.Add(-30*24*time.Hour).UnixMilli(), now)
	days = topicAddActivityDay(days, 0, now)
	if len(days) != 2 || days[0] != "2026-10-23" || days[1] != "2026-10-21" {
		t.Fatalf("activity days = %q", days)
	}
}
