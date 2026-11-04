package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Design review v1.0.1 W40: one lesson, once. Fictional near-duplicates.
func TestKnowHowNearDuplicateTitles(t *testing.T) {
	same := [][2]string{
		{"Juniper Bakery deploys broke after repo rename", "Juniper Bakery deploys broke after the repo rename"},
		{"Juniper Bakery deploys broke after repo rename", "Juniper Bakery deploys after repo rename"},
	}
	for _, pair := range same {
		if !sameKnowHowLesson(pair[0], pair[1]) {
			t.Errorf("%q and %q should be one lesson", pair[0], pair[1])
		}
	}
	different := [][2]string{
		{"Juniper Bakery deploys broke after repo rename", "Juniper Bakery deploys broke after the Node upgrade"},
		{"Restart the socket", "Restart the printer"},
	}
	for _, pair := range different {
		if sameKnowHowLesson(pair[0], pair[1]) {
			t.Errorf("%q and %q are different lessons", pair[0], pair[1])
		}
	}
}

func TestKnowHowMergesCopiesAlreadySaved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	app.knowHow.counter = &knowHowFakeCounter{}
	day := time.Date(2026, 10, 30, 14, 10, 0, 0, time.UTC)
	writeKnowHowFixture(t, "first", buildOKFKnowledgeMarkdown("first", "Juniper Bakery deploys broke after repo rename", "Point the site at the new repo", day, "activity", []string{"Terminal"}, []string{"s1"}, []string{"Relink the repo"}, "fix"))
	writeKnowHowFixture(t, "second", buildOKFKnowledgeMarkdown("second", "Juniper Bakery deploys broke after the repo rename", "", day.Add(24*time.Hour), "activity", []string{"Safari"}, []string{"s2"}, []string{"Relink"}, "fix"))
	writeKnowHowFixture(t, "third", buildOKFKnowledgeMarkdown("third", "Juniper Bakery deploys after repo rename", "", day.Add(48*time.Hour), "activity", nil, []string{"s3"}, []string{"Relink"}, "fix"))
	writeKnowHowFixture(t, "other", buildOKFKnowledgeMarkdown("other", "Halcyon invoices use the customer's time zone", "", day, "activity", nil, nil, []string{"Ask Northwind"}, "decision"))

	cards, err := app.ListKnowHow()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(cards), cards)
	}
	var lesson KnowHowCard
	for _, card := range cards {
		if card.ID == "first" {
			lesson = card
		}
	}
	if lesson.Title != "Juniper Bakery deploys broke after repo rename" {
		t.Fatalf("the first copy saved keeps the card: %+v", cards)
	}
	if len(lesson.MergedIDs) != 2 || len(lesson.Moments) != 3 {
		t.Fatalf("merged %v moments %+v", lesson.MergedIDs, lesson.Moments)
	}
	if lesson.Moments[0].SessionID != "s1" || lesson.Moments[2].SessionID != "s3" || lesson.Moments[0].App != "Terminal" {
		t.Fatalf("moments oldest first with their session: %+v", lesson.Moments)
	}
}

func TestKnowHowLearningAgainAddsTheMoment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	app.knowHow.counter = &knowHowFakeCounter{}
	day := time.Date(2026, 10, 30, 14, 10, 0, 0, time.UTC)
	writeKnowHowFixture(t, "first", buildOKFKnowledgeMarkdown("first", "Juniper Bakery deploys broke after repo rename", "", day, "activity", nil, []string{"s1"}, []string{"Relink the repo"}, "fix"))
	dir, _ := knowledgeDir()
	again := time.Date(2026, 11, 3, 14, 10, 0, 0, time.UTC).UnixMilli()

	id, ok := relearnKnowHow(dir, "Juniper Bakery deploys broke after the repo rename", "new-entry", KnowHowMoment{At: again, Assistant: "claude"})
	if !ok || id != "first" {
		t.Fatalf("relearn = %q %v", id, ok)
	}
	// The same moment twice is still one moment.
	relearnKnowHow(dir, "Juniper Bakery deploys after repo rename", "new-entry", KnowHowMoment{At: again, Assistant: "claude"})
	if _, err := os.Stat(filepath.Join(dir, "new-entry.md")); !os.IsNotExist(err) {
		t.Fatal("learning it again must not add a new entry")
	}
	if _, ok := relearnKnowHow(dir, "Halcyon invoices use the customer's time zone", "new-entry", KnowHowMoment{At: again}); ok {
		t.Fatal("a different lesson is not folded in")
	}

	cards, err := app.ListKnowHow()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || len(cards[0].Moments) != 2 {
		t.Fatalf("cards %+v", cards)
	}
	if got := cards[0].Moments[1]; got.At != again || got.Assistant != "claude" {
		t.Fatalf("new moment %+v", got)
	}
	// An entry rewriting itself is not a copy of itself.
	if _, ok := relearnKnowHow(dir, "Juniper Bakery deploys broke after repo rename", "first", KnowHowMoment{At: again + 1}); ok {
		t.Fatal("except must skip the entry being written")
	}
}
