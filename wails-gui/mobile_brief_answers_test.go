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
	if err := store.save(brief, time.Now()); err != nil {
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
