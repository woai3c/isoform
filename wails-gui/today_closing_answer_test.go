package main

import (
	"testing"
	"time"
)

func TestClosingAnswerPostsAndRejectsStaleChoice(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	refs := brief.Narrative[0].Refs
	brief.Narrative = []MobileBriefPara{{Text: "What should we do with the Launch plan?", Refs: refs}}
	digest := mobileBriefClosingDigest(brief)
	if digest == "" {
		t.Fatal("closing fixture has no digest")
	}
	brief.Cards = []MobileBriefCard{{ID: "answer-one", Kind: mobileBriefCardAction, Mode: mobileBriefCardSend, Label: "Leave it", Message: "No, leave it.", Refs: refs, Writer: mobileBriefCardWriterModel, Facts: digest, AnswerTo: digest}}
	bindConversationBriefFixture(t, app, &brief)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	answer, err := app.PostTodayClosingAnswer(brief.BriefID, "answer-one", "No, leave it.", "")
	if err != nil || answer.Label != "Not now" {
		t.Fatalf("answer=%+v err=%v", answer, err)
	}
	main, err := app.store.LoadMainSession(t.Context())
	if err != nil || main == nil {
		t.Fatal("main chat missing")
	}
	rows, err := app.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil || len(rows) != 1 || rows[0].Content != "No, leave it." {
		t.Fatalf("chat rows=%+v err=%v", rows, err)
	}
	if _, err := app.PostTodayClosingAnswer(brief.BriefID, "answer-one", "No, leave it.", ""); err == nil {
		t.Fatal("stale answer accepted")
	}
	rows, _ = app.store.LoadStreamItems(t.Context(), main.LocalID)
	if len(rows) != 1 {
		t.Fatalf("stale answer posted a row: %+v", rows)
	}
	delivered, ok := app.todayChatDeliveredBrief(brief.BriefID)
	reopened := app.todayClosingAnswer(delivered)
	if !ok || reopened == nil || reopened.Label != "Not now" {
		t.Fatal("saved answer did not reopen")
	}
	if err := app.DeleteMemoryEntry("launch"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PostTodayClosingAnswer(brief.BriefID, "answer-one", "No, leave it.", "answer-one"); err == nil {
		t.Fatal("withdrawn source accepted")
	}
}

func TestClosingHoldQuestionCannotBePosted(t *testing.T) {
	brief := MobileBriefV1{Narrative: []MobileBriefPara{{Text: "Should I hold an hour for the memo?"}}}
	if todayNonHoldClosing(brief) {
		t.Fatal("hold offer accepted as a closing answer")
	}
}

func TestClosingAnswerPostsFromProvenBriefWhilePaused(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	refs := brief.Narrative[0].Refs
	brief.Narrative = []MobileBriefPara{{Text: "What should we do with the Launch plan?", Refs: refs}}
	digest := mobileBriefClosingDigest(brief)
	brief.Cards = []MobileBriefCard{{ID: "answer-one", Kind: mobileBriefCardAction, Mode: mobileBriefCardSend, Label: "Leave it", Message: "No, leave it.", Refs: refs, Writer: mobileBriefCardWriterModel, Facts: digest, AnswerTo: digest}}
	bindConversationBriefFixture(t, app, &brief)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	answer, err := app.PostTodayClosingAnswer(brief.BriefID, "answer-one", "No, leave it.", "")
	if err != nil || answer.CardID != "answer-one" {
		t.Fatalf("paused answer=%+v err=%v", answer, err)
	}
	if err := app.DeleteMemoryEntry("launch"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PostTodayClosingAnswer(brief.BriefID, "answer-one", "No, leave it.", "answer-one"); err == nil {
		t.Fatal("withdrawn source accepted")
	}
}
