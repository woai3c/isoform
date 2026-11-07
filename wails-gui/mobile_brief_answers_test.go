package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func closingAnswerFixture(t *testing.T, answers []mobileBriefCardWords) MobileBriefV1 {
	t.Helper()
	brief, picks := writtenBriefFixture()
	raw := writtenReply(t, &mobileBriefNarrativeReplyClosing{Text: "Want me to draft the note to Sam?", Card: "offer-2", Answers: answers},
		mobileBriefNarrativeReplyParagraph{Text: writtenShoe, Refs: []string{"e7"}})
	paragraphs, words, err := mobileBriefNarrativeFromReply(brief, picks, raw, "English")
	if err != nil {
		t.Fatal(err)
	}
	brief.Narrative, brief.NarrativeStatus = paragraphs, "ok"
	brief.Cards = mobileBriefApplyCardWords(brief, mobileBriefPatternCards(brief, "en"), words, "en")
	return brief
}

func legacyOwnVoiceBriefFixture() MobileBriefV1 {
	return MobileBriefV1{
		LocalDate: "2026-10-09", NarrativeStatus: "ok",
		Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "legacy", Label: "Launch plan"}},
		Sections: MobileBriefSections{Moved: []MobileBriefItem{{
			Key: "legacy-launch", Title: "Launch plan", Status: "needs_review", Kind: "one_time_work",
			AttentionReason: "Your Mac needs attention.", NextStep: "Review it on your Mac.", AttentionRank: 2, Refs: []string{"e1"},
		}}},
		Narrative: []MobileBriefPara{
			{Text: "Review the launch plan on your Mac.", Refs: []string{"e1"}},
			{Text: "Want to review the launch plan on your Mac?", Refs: []string{"e1"}},
		},
	}
}

func TestMobileBriefPrePROwnVoiceDigestGolden(t *testing.T) {
	t.Run("digests", func(t *testing.T) {
		brief := legacyOwnVoiceBriefFixture()
		item := brief.Sections.Moved[0]
		for _, tc := range []struct{ name, got, want string }{
			{"closing item", mobileBriefCardFacts(brief, item, "closing"), "0193a796996567a65480383b2ec918bd1ba7682b7ecd1cd1a71540fd60c06815"},
			{"written item", mobileBriefCardFacts(brief, item, "moved"), "d58a121e0caf5d01006ac6df3f6e766d78c7416b62c2156391ae9e6205a3a0c5"},
			{"paragraph", mobileBriefCardFacts(brief, MobileBriefItem{Title: brief.Narrative[0].Text, Refs: []string{"e1"}}, "paragraph"), "01c8f49a43cf98981c94566097f809dc0b2b02d67a52c41c321a128f00d564ef"},
			{"closing", mobileBriefClosingDigest(brief), "e2075d0ae95a48c6ab1d002c3d5cd3e90da3ae551c691d2aafe50ed6066567dc"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.got != tc.want {
					t.Fatalf("legacy digest = %q, want pre-PR golden %q", tc.got, tc.want)
				}
			})
		}
	})

	t.Run("answer survives hygiene", func(t *testing.T) {
		brief := legacyOwnVoiceBriefFixture()
		digest := "e2075d0ae95a48c6ab1d002c3d5cd3e90da3ae551c691d2aafe50ed6066567dc"
		answer := MobileBriefCard{ID: "legacy-answer", Kind: mobileBriefCardAction, Mode: mobileBriefCardSend,
			Label: "Leave it", Message: "No, leave it.", Refs: []string{"e1"}, Writer: mobileBriefCardWriterModel, Facts: digest, AnswerTo: digest}
		if !mobileBriefAnswerMatches(brief, answer) {
			t.Fatal("pre-PR answer no longer matches its closing")
		}
		brief.Cards = []MobileBriefCard{answer}
		if got := mobileBriefHygiene(brief); len(got.Cards) != 1 || got.Cards[0].ID != answer.ID {
			t.Fatalf("pre-PR answer was dropped: %+v", got.Cards)
		}
	})

	t.Run("written card words remain reusable", func(t *testing.T) {
		brief := legacyOwnVoiceBriefFixture()
		patterns := mobileBriefPatternCards(brief, "English")
		var stored MobileBriefCard
		for _, card := range patterns {
			if card.ID == "item:legacy-launch" {
				stored = card
			}
		}
		if stored.ID == "" || stored.Facts != "d58a121e0caf5d01006ac6df3f6e766d78c7416b62c2156391ae9e6205a3a0c5" {
			t.Fatalf("legacy item card facts changed: %+v", stored)
		}
		stored.Writer, stored.Message = mobileBriefCardWriterModel, "Keep the legacy written words."
		got := mobileBriefCardsWithWritten(patterns, []MobileBriefCard{stored})
		for _, card := range got {
			if card.ID == stored.ID {
				if card.Writer != mobileBriefCardWriterModel || card.Message != stored.Message {
					t.Fatalf("written words were not reused: %+v", card)
				}
				return
			}
		}
		t.Fatal("legacy item card disappeared")
	})
}

func TestMobileBriefNewOwnVoiceDigestSurvivesReload(t *testing.T) {
	asOf := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot: mobileBriefSlotOnRequest, WindowStart: asOf.Add(-time.Hour), AsOf: asOf, Location: time.UTC,
		Moved: []mobileBriefCandidate{{
			SourceKind: "history", SourceID: "new-voice", Title: "Launch plan", Kind: "one_time_work", Status: "needs_review", At: asOf,
		}},
	})
	if !brief.ownVoiceFacts {
		t.Fatal("new brief was not marked before card creation")
	}
	brief.Sections.Moved[0].AttentionReason = "Your Mac needs attention."
	brief.Sections.Moved[0].NextStep = "Review it on your Mac."
	refs := []string{brief.Evidence[0].Ref}
	brief.Narrative = []MobileBriefPara{
		{Text: "Review the launch plan on your Mac.", Refs: refs},
		{Text: "Want to review the launch plan on your Mac?", Refs: refs},
	}
	digest := mobileBriefClosingDigest(brief)
	brief.Cards = []MobileBriefCard{{ID: "new-answer", Kind: mobileBriefCardAction, Mode: mobileBriefCardSend,
		Label: "Leave it", Message: "No, leave it.", Refs: refs, Writer: mobileBriefCardWriterModel, Facts: digest, AnswerTo: digest}}
	assembled := mobileBriefWithOwnVoice(brief)
	if got := assembled.Narrative[len(assembled.Narrative)-1].Text; got != "Want to review the launch plan on your computer?" {
		t.Fatalf("new closing = %q", got)
	}
	data, err := marshalStoredMobileBrief(assembled)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded MobileBriefV1
	if err := unmarshalStoredMobileBrief(data, &reloaded); err != nil {
		t.Fatal(err)
	}
	if !reloaded.ownVoiceFacts {
		t.Fatal("new brief lost its digest marker on reload")
	}
	answers := cardsByKind(reloaded.Cards, mobileBriefCardAction)
	if len(answers) != 1 || !mobileBriefAnswerMatches(reloaded, answers[0]) {
		t.Fatalf("new answer did not survive storage: %+v", answers)
	}
}

func TestMobileBriefClosingAnswersMatchQuestion(t *testing.T) {
	brief := closingAnswerFixture(t, []mobileBriefCardWords{
		{Label: "Draft the note to Sam", Message: "Yes, draft the note to Sam about the shoe insert follow-up."},
		{Label: "Leave it", Message: "No, leave it."},
		{Label: "Ignored third answer", Message: "Draft another note to Sam."},
	})
	answers := cardsByKind(mobileBriefPatternCards(brief, "en"), mobileBriefCardAction)
	if len(answers) != 2 || answers[0].Message != "Yes, draft the note to Sam about the shoe insert follow-up." || answers[1].Message != "No, leave it." {
		t.Fatalf("matching answers: %+v", answers)
	}
	for _, card := range answers {
		if !mobileBriefAnswerMatches(brief, card) || card.AnswerTo == "" || card.Target != nil {
			t.Fatalf("unbound answer: %+v", card)
		}
	}
	store := newMobileBriefStore(t.TempDir())
	// Saved as of the brief's own day: a later clock would sweep the dated
	// fixture out of the store before it is read back.
	if err := store.save(brief, mobileBriefAsOfTime(brief)); err != nil {
		t.Fatal(err)
	}
	loaded, ok := store.load(brief.BriefID)
	if !ok || len(mobileBriefClosingAnswers(loaded, loaded.Cards)) != 2 {
		t.Fatal("answers lost on reload")
	}
}

func TestMobileBriefClosingAnswerNeverUsesUnrelatedAction(t *testing.T) {
	for _, words := range [][]mobileBriefCardWords{
		nil,
		{{Label: "Prep for Deep work", Message: "Prepare for Deep work."}},
		{{Label: "No, prep instead", Message: "No, prepare for Deep work."}},
	} {
		brief := closingAnswerFixture(t, words)
		// Even an old saved action must not be promoted into an answer.
		unrelated := MobileBriefCard{ID: "offer-1", Kind: mobileBriefCardAction, Mode: mobileBriefCardSend,
			Label: "Prep for Deep work", Message: "Prepare for Deep work.", Refs: []string{"e7"}, Writer: mobileBriefCardWriterModel}
		brief.Cards = append(brief.Cards, unrelated)
		if _, ok := (&App{}).todayChatCardNow(t.Context(), brief, unrelated.ID, todayChatSources{}); ok {
			t.Fatal("resolver accepted an unrelated action as an answer")
		}
		cards := mobileBriefCardsWithWritten(mobileBriefPatternCards(brief, "en"), brief.Cards)
		if got := cardsByKind(cards, mobileBriefCardAction); len(got) != 0 {
			t.Fatalf("unrelated or missing answers became buttons: %+v", got)
		}
		if len(cardsByKind(cards, mobileBriefCardOpener)) != 1 {
			t.Fatal("Talk about this brief was lost")
		}
	}
}

func TestMobileBriefClosingAnswerDropsAfterQuestionOrFactsChange(t *testing.T) {
	for _, change := range []string{"question", "facts", "removed closing", "digest"} {
		t.Run(change, func(t *testing.T) {
			brief := closingAnswerFixture(t, []mobileBriefCardWords{{Label: "Draft note to Sam", Message: "Yes, draft the shoe insert follow-up note to Sam."}})
			card := cardsByKind(brief.Cards, mobileBriefCardAction)[0]
			switch change {
			case "question":
				brief.Narrative[len(brief.Narrative)-1].Text = "Want me to add a follow-up for Sam instead?"
			case "facts":
				for i := range brief.Sections.OpenLoops {
					if strings.Contains(brief.Sections.OpenLoops[i].Title, "Sam") {
						brief.Sections.OpenLoops[i].Title = "Sam shoe follow-up resolved"
					}
				}
			case "removed closing":
				brief.Narrative = brief.Narrative[:len(brief.Narrative)-1]
			case "digest":
				brief.Cards[len(brief.Cards)-1].AnswerTo = "stale"
				card.AnswerTo = "stale"
				brief.Cards = []MobileBriefCard{card}
			}
			if got := mobileBriefClosingAnswers(brief, brief.Cards); len(got) != 0 {
				t.Fatalf("stale answers survived %s: %+v", change, got)
			}
			// The resolver checks identity even before its frozen-input check.
			app := &App{}
			if _, ok := app.todayChatCardNow(t.Context(), brief, card.ID, todayChatSources{}); ok {
				t.Fatal("resolver kept a stale answer")
			}
		})
	}
}

func TestMobileBriefClosingAnswerOptionalWireField(t *testing.T) {
	brief := closingAnswerFixture(t, []mobileBriefCardWords{{Label: "Leave it", Message: "No, leave it."}})
	card := cardsByKind(brief.Cards, mobileBriefCardAction)[0]
	raw, err := json.Marshal(card)
	if err != nil || !strings.Contains(string(raw), `"answerTo":"`+card.AnswerTo+`"`) {
		t.Fatalf("missing answer digest: %s %v", raw, err)
	}
	legacy, _ := json.Marshal(MobileBriefCard{ID: "offer-1"})
	if strings.Contains(string(legacy), "answerTo") {
		t.Fatal("empty digest was not omitted")
	}
}

func TestTodayChatClosingAnswerPreparesWithoutWritesAndWithdrawsWithSource(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	refs := brief.Sections.OpenLoops[0].Refs
	brief.Narrative = []MobileBriefPara{{Text: "The Launch plan is open.", Refs: refs}, {Text: "Want me to plan the next steps for the Launch plan?", Refs: refs}}
	brief.NarrativeStatus = "ok"
	digest := mobileBriefClosingDigest(brief)
	brief.Cards = mobileBriefApplyCardWords(brief, mobileBriefPatternCards(brief, "en"), []mobileBriefCardWords{
		{ID: "answer:" + digest + ":0", Label: "Plan the next steps", Message: "Yes, plan the next steps for the Launch plan.", AnswerTo: digest},
	}, "en")
	bindConversationBriefFixture(t, app, &brief)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before, err := app.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	view := app.mobileBriefWithCards(app.mobileBriefForDelivery(brief))
	answers := cardsByKind(view.Cards, mobileBriefCardAction)
	if len(answers) != 1 {
		t.Fatalf("matching answer not delivered: %+v", view.Cards)
	}
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: answers[0].ID, BriefID: brief.BriefID}
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil || draft.Message != answers[0].Message {
		t.Fatalf("answer draft: %+v %v", draft, err)
	}
	after, err := app.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("preparing an answer wrote history")
	}
	if _, err := app.SaveMemoryEntry("launch", "# Launch plan\nThe release is already complete."); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareTodayChatDraft(ref); err == nil {
		t.Fatal("an answer survived a source correction")
	}
	app.closedEyes.enabled.Store(true)
	if got := app.mobileBriefWithCards(brief); len(got.Cards) != 0 {
		t.Fatal("answers or questions survived Closed Eyes")
	}
}
