package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mobileBriefChipTestItem(ref, id, title, kind string) MobileBriefItem {
	return MobileBriefItem{ID: id, Key: id, Title: title, Kind: kind, Status: "active", Refs: []string{ref}}
}

func mobileBriefChipTestRef(ref, sourceKind, sourceID string) MobileBriefRef {
	return MobileBriefRef{Ref: ref, SourceKind: sourceKind, SourceID: sourceID, Label: sourceID}
}

func mobileBriefChipTestBrief(narrative []MobileBriefPara, sections MobileBriefSections, evidence []MobileBriefRef) MobileBriefV1 {
	return MobileBriefV1{
		Version: 1, BriefID: "2026-10-23-morning", Slot: mobileBriefSlotMorning,
		LocalDate: "2026-10-23", Timezone: "UTC", AsOf: "2026-10-23T07:00:00Z",
		NarrativeStatus: "ok", Narrative: narrative, Sections: sections, Evidence: evidence,
	}
}

func mobileBriefChipTestParagraphCards(cards []MobileBriefCard) []MobileBriefCard {
	var out []MobileBriefCard
	for _, card := range cards {
		if card.Kind == mobileBriefCardQuestion || card.Kind == mobileBriefCardItem && card.Paragraph != nil && card.Label != "" {
			out = append(out, card)
		}
	}
	return out
}

func mobileBriefChipTestCapBrief() MobileBriefV1 {
	waiting := mobileBriefChipTestItem("e-wait", "waiting", "Maya", "waiting_on_reply")
	waiting.Status = "waiting"
	commitment := mobileBriefChipTestItem("e-commit", "commitment", "Q4 plan", "commitment")
	working := mobileBriefChipTestItem("e-working", "working", "Launch plan", "goal")
	attentionCommitment := mobileBriefChipTestItem("e-attention-1", "attention-commitment", "Board review", "commitment")
	attentionCommitment.AttentionReason, attentionCommitment.AttentionRank = "Needs a decision", 1
	attentionDeliverable := mobileBriefChipTestItem("e-attention-2", "attention-deliverable", "Release memo", "deliverable")
	attentionDeliverable.AttentionReason, attentionDeliverable.AttentionRank = "Ready to review", 2
	return mobileBriefChipTestBrief([]MobileBriefPara{
		{Text: "Maya is waiting for a reply.", Refs: []string{"e-wait"}},
		{Text: "The Q4 plan needs time.", Refs: []string{"e-commit"}},
		{Text: "The launch plan is moving.", Refs: []string{"e-working"}},
		{Text: "The board review needs a decision.", Refs: []string{"e-attention-1"}},
		{Text: "The release memo is ready.", Refs: []string{"e-attention-2"}},
	}, MobileBriefSections{
		WaitingOnYou:  []MobileBriefItem{waiting},
		Commitments:   []MobileBriefItem{commitment, attentionCommitment},
		WorkingToward: []MobileBriefItem{working},
		Deliverables:  []MobileBriefItem{attentionDeliverable},
	}, []MobileBriefRef{
		mobileBriefChipTestRef("e-wait", mobileBriefWaitingReplySource, "maya"),
		mobileBriefChipTestRef("e-commit", "purpose", "q4-plan"),
		mobileBriefChipTestRef("e-working", "purpose", "launch-plan"),
		mobileBriefChipTestRef("e-attention-1", "purpose", "board-review"),
		mobileBriefChipTestRef("e-attention-2", "deliverable", "release-memo"),
	})
}

func mobileBriefChipTestDedupeBrief() MobileBriefV1 {
	first := mobileBriefChipTestItem("e-first", "first", "Weekly update", "deliverable")
	second := mobileBriefChipTestItem("e-second", "second", "Quarterly report", "deliverable")
	third := mobileBriefChipTestItem("e-third", "third", "Quarterly report", "deliverable")
	return mobileBriefChipTestBrief([]MobileBriefPara{
		{Text: "The weekly update is ready.", Refs: []string{"e-first"}},
		{Text: "The weekly update still needs attention.", Refs: []string{"e-first"}},
		{Text: "The quarterly report is ready.", Refs: []string{"e-second"}},
		{Text: "The other quarterly report is ready.", Refs: []string{"e-third"}},
	}, MobileBriefSections{Deliverables: []MobileBriefItem{first, second, third}}, []MobileBriefRef{
		mobileBriefChipTestRef("e-first", "deliverable", "weekly-update"),
		mobileBriefChipTestRef("e-second", "deliverable", "quarterly-report-a"),
		mobileBriefChipTestRef("e-third", "deliverable", "quarterly-report-b"),
	})
}

func mobileBriefChipTestAttentionDedupeBrief(withParagraph bool) MobileBriefV1 {
	attention := mobileBriefChipTestItem("e-attention", "attention", "Maya", "waiting_on_reply")
	attention.Status, attention.AttentionReason, attention.AttentionRank = "waiting", "Waiting for my reply", 1
	extras := []MobileBriefItem{
		mobileBriefChipTestItem("e-extra-1", "extra-1", "Launch memo", "deliverable"),
		mobileBriefChipTestItem("e-extra-2", "extra-2", "Board deck", "deliverable"),
		mobileBriefChipTestItem("e-extra-3", "extra-3", "Weekly update", "deliverable"),
	}
	var narrative []MobileBriefPara
	if withParagraph {
		narrative = []MobileBriefPara{
			{Text: "Maya is waiting for a reply.", Refs: []string{"e-attention"}},
			{Text: "The launch memo is ready.", Refs: []string{"e-extra-1"}},
			{Text: "The board deck is ready.", Refs: []string{"e-extra-2"}},
			{Text: "The weekly update is ready.", Refs: []string{"e-extra-3"}},
		}
	}
	return mobileBriefChipTestBrief(narrative, MobileBriefSections{
		WaitingOnYou: []MobileBriefItem{attention},
		Deliverables: extras,
	}, []MobileBriefRef{
		mobileBriefChipTestRef("e-attention", mobileBriefWaitingReplySource, "maya"),
		mobileBriefChipTestRef("e-extra-1", "deliverable", "launch-memo"),
		mobileBriefChipTestRef("e-extra-2", "deliverable", "board-deck"),
		mobileBriefChipTestRef("e-extra-3", "deliverable", "weekly-update"),
	})
}

func TestMobileBriefChipsCapThreeAfterAttentionRanking(t *testing.T) {
	brief := mobileBriefChipTestCapBrief()
	chips := mobileBriefChipTestParagraphCards(mobileBriefPatternCards(brief, "en"))
	if len(chips) != 3 {
		t.Fatalf("chips = %+v, want exactly three", chips)
	}
	wantParagraphs := []int{3, 4, 0}
	seen := map[int]bool{}
	for index, card := range chips {
		if card.Paragraph == nil {
			t.Fatalf("chip has no paragraph: %+v", card)
		}
		paragraph := *card.Paragraph
		if paragraph != wantParagraphs[index] {
			t.Fatalf("chip %d paragraph = %d, want %d: %+v", index, paragraph, wantParagraphs[index], chips)
		}
		if seen[paragraph] {
			t.Fatalf("more than one chip for paragraph %d: %+v", paragraph, chips)
		}
		seen[paragraph] = true
	}
	for _, paragraph := range []int{1, 2} {
		if seen[paragraph] {
			t.Fatalf("paragraph %d unexpectedly received a chip: %+v", paragraph, chips)
		}
	}
}

func TestMobileBriefChipsUniqueLabels(t *testing.T) {
	brief := mobileBriefChipTestDedupeBrief()
	chips := mobileBriefChipTestParagraphCards(mobileBriefPatternCards(brief, "en"))
	if len(chips) != 2 {
		t.Fatalf("chips = %+v, want one for each distinct label/source", chips)
	}
	if chips[0].Paragraph == nil || *chips[0].Paragraph != 0 || chips[1].Paragraph == nil || *chips[1].Paragraph != 2 {
		t.Fatalf("chips = %+v, want paragraphs 0 and 2", chips)
	}
	if chips[0].Label != "Open Weekly update" || chips[1].Label != "Open Quarterly report" {
		t.Fatalf("labels = %q, %q", chips[0].Label, chips[1].Label)
	}
}

func TestMobileBriefChipsDedupeWithAttentionCards(t *testing.T) {
	withoutParagraph := mobileBriefChipTestAttentionDedupeBrief(false)
	withParagraph := mobileBriefChipTestAttentionDedupeBrief(true)
	baseCards := mobileBriefPatternCards(withoutParagraph, "en")
	cards := mobileBriefPatternCards(withParagraph, "en")
	var base, linked MobileBriefCard
	for _, card := range baseCards {
		if card.ID == "item:attention" {
			base = card
		}
	}
	for _, card := range cards {
		if card.ID == "item:attention" {
			linked = card
		}
	}
	if base.ID == "" || linked.ID == "" {
		t.Fatalf("attention item card missing: base=%+v linked=%+v", base, linked)
	}
	if linked.Paragraph == nil || *linked.Paragraph != 0 || linked.Label != "Draft the reply to Maya" {
		t.Fatalf("attention card was not linked as the paragraph chip: %+v", linked)
	}
	if linked.ID != base.ID || linked.Message != base.Message || !reflect.DeepEqual(linked.Refs, base.Refs) || linked.Facts != base.Facts {
		t.Fatalf("linking changed the item card: base=%+v linked=%+v", base, linked)
	}
	chips := mobileBriefChipTestParagraphCards(cards)
	if len(chips) != 3 {
		t.Fatalf("chips = %+v, want linked card plus two more", chips)
	}
}

func mobileBriefChipTestDropBrief() MobileBriefV1 {
	moved := mobileBriefChipTestItem("e-moved", "moved", "Moved task", "commitment")
	people := mobileBriefChipTestItem("e-people", "people", "Dana", "person")
	learned := mobileBriefChipTestItem("e-learned", "learned", "Preference", "memory")
	openLoop := mobileBriefChipTestItem("e-open-loop", "open-loop", "Open loop", "commitment")
	unnamed := mobileBriefChipTestItem("e-unnamed", "unnamed", "", "deliverable")
	return mobileBriefChipTestBrief([]MobileBriefPara{
		{Text: "The moved task is still open.", Refs: []string{"e-moved"}},
		{Text: "I should follow up with Dana.", Refs: []string{"e-people"}},
		{Text: "The preference was recorded.", Refs: []string{"e-learned"}},
		{Text: "The open loop is still open.", Refs: []string{"e-open-loop"}},
		{Text: "The unnamed deliverable is ready.", Refs: []string{"e-unnamed"}},
		{Text: "This paragraph has no typed subject.", Refs: []string{"e-none"}},
	}, MobileBriefSections{
		Moved:        []MobileBriefItem{moved},
		People:       []MobileBriefItem{people},
		Learned:      []MobileBriefItem{learned},
		OpenLoops:    []MobileBriefItem{openLoop},
		Deliverables: []MobileBriefItem{unnamed},
	}, []MobileBriefRef{
		mobileBriefChipTestRef("e-moved", "purpose", "moved-task"),
		mobileBriefChipTestRef("e-people", "purpose", "dana"),
		mobileBriefChipTestRef("e-learned", "purpose", "preference"),
		mobileBriefChipTestRef("e-open-loop", "purpose", "open-loop"),
		mobileBriefChipTestRef("e-unnamed", "deliverable", "unnamed"),
		mobileBriefChipTestRef("e-none", "purpose", "untyped"),
	})
}

func assertNoGenericMobileBriefChipLabels(t *testing.T, cards []MobileBriefCard, language string) {
	t.Helper()
	generic := map[string]bool{
		"What should I consider next?": true,
		"如何继续推进？":                      true,
		"Ask about this topic":         true,
		"这个话题怎么样":                      true,
		"Status update":                true,
	}
	for _, card := range cards {
		if generic[card.Label] || strings.Contains(strings.ToLower(card.Label), "this topic") || strings.Contains(card.Label, "这个话题") {
			t.Fatalf("%s generic label survived: %+v", language, card)
		}
	}
}

func mobileBriefChipTestModelCard(t *testing.T, chip mobileBriefCardWords) (MobileBriefCard, MobileBriefCard) {
	t.Helper()
	brief, picks := writtenBriefFixture()
	raw, err := json.Marshal(struct {
		Paragraphs []mobileBriefNarrativeReplyParagraph `json:"paragraphs"`
		Closing    *mobileBriefNarrativeReplyClosing    `json:"closing,omitempty"`
		Cards      []mobileBriefCardWords               `json:"cards"`
	}{
		Paragraphs: []mobileBriefNarrativeReplyParagraph{{
			Text: writtenShoe, Refs: []string{"e7"}, Chip: &chip,
		}},
		Closing: &mobileBriefNarrativeReplyClosing{Text: "Want me to prep for the Northwind check-in?", Card: "offer-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paragraphs, words, err := mobileBriefNarrativeFromReply(brief, picks, string(raw), "English")
	if err != nil {
		t.Fatalf("model reply rejected: rule=%s err=%v", mobileBriefNarrativeRule(err), err)
	}
	brief.Narrative, brief.NarrativeStatus = paragraphs, "ok"
	// The raw task title is too long for a written chip (today-review
	// v1.1.4 #5); give the subject its short name so a pattern chip exists.
	for i := range brief.Sections.WorkingToward {
		if brief.Sections.WorkingToward[i].Key == "workingToward.shoe" {
			brief.Sections.WorkingToward[i].Title = "Sam shoe insert"
		}
	}
	pattern := mobileBriefPatternCards(brief, "en")
	var patternCard MobileBriefCard
	for _, card := range pattern {
		if card.Kind == mobileBriefCardQuestion && card.Paragraph != nil {
			patternCard = card
			break
		}
	}
	if patternCard.ID == "" {
		t.Fatalf("pattern paragraph card missing: %+v", pattern)
	}
	for _, card := range mobileBriefApplyCardWords(brief, pattern, words, "English") {
		if card.ID == patternCard.ID {
			return card, patternCard
		}
	}
	t.Fatalf("applied paragraph card missing: %s", patternCard.ID)
	return MobileBriefCard{}, patternCard
}

func TestMobileBriefChipsDropInsteadOfGeneric(t *testing.T) {
	for _, tc := range []struct {
		value, lang string
	}{
		{"What should I consider next?", "en"},
		{"如何继续推进？", "zh"},
		{"Ask about this topic", "en"},
		{"这个话题怎么样", "zh"},
		{"Status update", "en"},
	} {
		if got := mobileBriefQuestionChipLabel(tc.value, tc.lang); got != "" {
			t.Fatalf("generic label %q survived in %s: %q", tc.value, tc.lang, got)
		}
	}
	drop := mobileBriefChipTestDropBrief()
	if title := mobileBriefCardTitle(drop.Sections.Deliverables[0]); title != "" {
		t.Fatalf("unnamed subject has a card title: %q", title)
	}
	for _, language := range []string{"en", "zh"} {
		cards := mobileBriefPatternCards(drop, language)
		if chips := mobileBriefChipTestParagraphCards(cards); len(chips) != 0 {
			t.Fatalf("%s dropped subjects received chips: %+v", language, chips)
		}
		assertNoGenericMobileBriefChipLabels(t, cards, language)
		capCards := mobileBriefPatternCards(mobileBriefChipTestCapBrief(), language)
		assertNoGenericMobileBriefChipLabels(t, capCards, language)
	}
}

func TestMobileBriefChipModelWords(t *testing.T) {
	valid := mobileBriefCardWords{
		Label:   "What's left on shoe insert?",
		Message: "What's the next step for the shoe insert follow-up?",
	}
	got, pattern := mobileBriefChipTestModelCard(t, valid)
	if got.Writer != mobileBriefCardWriterModel || got.Label != valid.Label || got.Message != valid.Message {
		t.Fatalf("valid model chip = %+v, want model words %+v", got, valid)
	}
	for _, tc := range []struct {
		name  string
		words mobileBriefCardWords
	}{
		{"send message", mobileBriefCardWords{
			Label: valid.Label, Message: "Send the shoe insert follow-up?",
		}},
		{"label does not name subject", mobileBriefCardWords{
			Label: "What's next?", Message: valid.Message,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, fallback := mobileBriefChipTestModelCard(t, tc.words)
			if got.Writer != mobileBriefCardWriterPattern || got.Label != fallback.Label || got.Message != fallback.Message {
				t.Fatalf("invalid model chip = %+v, want pattern fallback %+v", got, fallback)
			}
		})
	}
	if pattern.Writer != mobileBriefCardWriterPattern {
		t.Fatalf("pattern source writer = %q", pattern.Writer)
	}
}
