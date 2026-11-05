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
		{"Retry the public sync when GitHub rejects SSH", "Retry the public sync when SSH pushes fail"},
		{"Retry the public sync when GitHub rejects SSH", "Retry public sync through flaky SSH"},
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
		{"Retry the private sync when SSH pushes fail", "Retry the public sync when SSH pushes fail"},
		{"Never offer a localhost relay for phone pairing", "Offer a localhost relay for phone pairing"},
	}
	for _, pair := range different {
		if sameKnowHowLesson(pair[0], pair[1]) {
			t.Errorf("%q and %q are different lessons", pair[0], pair[1])
		}
	}
}

func TestKnowHowRelearnKeepsAllActivitySourcesAndHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	original := buildOKFKnowledgeMarkdown("old", "'database is locked' during merge checks", "Retry safely", time.Unix(1000, 0), "activity", []string{"Terminal"}, []string{"first"}, []string{"Retry the check"}, "fix")
	writeKnowHowFixture(t, "old", original)
	moment := KnowHowMoment{At: 2000000, App: "Terminal", SessionID: "second"}
	for i := 0; i < 2; i++ {
		id, ok := relearnKnowHow(dir, "'database is locked' during merge checks", "new", moment, "second", "third")
		if !ok || id != "old" {
			t.Fatalf("relearn: %q %v", id, ok)
		}
	}
	app := &App{}
	entry, err := app.GetKnowledgeEntry("old")
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.SessionIDs) != 3 || entry.SessionIDs[0] != "first" || entry.SessionIDs[1] != "second" || entry.SessionIDs[2] != "third" {
		t.Fatalf("sources lost or duplicated: %v", entry.SessionIDs)
	}
	doc, ok := parseOKFDocument(entry.Content)
	if !ok || len(okfStringSlice(doc.Frontmatter, knowHowRelearnedKey)) != 1 || !entry.HasHistory {
		t.Fatalf("moment or history missing: %+v", entry)
	}
	rewritten := buildOKFKnowledgeMarkdown("old", "Updated", "", time.Unix(3000, 0), "activity", nil, entry.SessionIDs, []string{"Retry"}, "fix")
	kept, ok := parseOKFDocument(preserveKnowHowMoments(rewritten, entry.Content))
	if !ok || len(okfStringSlice(kept.Frontmatter, knowHowRelearnedKey)) != 1 {
		t.Fatal("rewrite lost relearned moment")
	}
}

func TestKnowHowOppositeTitlesDoNotFoldIdenticalTakeaways(t *testing.T) {
	takeaway := "The pairing relay is documented with details for the computer and phone."
	cards := []KnowHowCard{
		{ID: "allow", Title: "Offer a localhost relay for phone pairing", Takeaway: takeaway},
		{ID: "deny", Title: "Never offer a localhost relay for phone pairing", Takeaway: takeaway},
	}
	if got := mergeKnowHowLessons(cards); len(got) != 2 {
		t.Fatalf("opposite lessons folded: %+v", got)
	}
}

func TestKnowHowMergesCopiesAlreadySaved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	app.knowHow.counter = &knowHowFakeCounter{}
	day := time.Date(2026, 10, 30, 14, 10, 0, 0, time.UTC)
	writeKnowHowFixture(t, "first", buildOKFKnowledgeMarkdown("first", "Retry the public sync when GitHub rejects SSH", "Retry the sync", day, "activity", []string{"Terminal"}, []string{"s1"}, []string{"Relink the repo"}, "fix"))
	writeKnowHowFixture(t, "second", buildOKFKnowledgeMarkdown("second", "Retry the public sync when SSH pushes fail", "", day.Add(24*time.Hour), "activity", []string{"Safari"}, []string{"s2"}, []string{"Relink"}, "fix"))
	writeKnowHowFixture(t, "third", buildOKFKnowledgeMarkdown("third", "Retry public sync through flaky SSH", "", day.Add(48*time.Hour), "activity", nil, []string{"s3"}, []string{"Relink"}, "fix"))
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
	if lesson.Title != "Retry the public sync when GitHub rejects SSH" {
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

func TestKnowHowCopiesWithDifferentTitlesKeepMoments(t *testing.T) {
	cards := []KnowHowCard{
		{ID: "new", Title: "Panel response caching", Takeaway: "Cache the failed response until the next refresh to keep the panel responsive.", Moments: []KnowHowMoment{{At: 2}}},
		{ID: "old", Title: "Reducing panel CPU", Takeaway: "Cache the failed response until the next refresh to keep the panel responsive.", Moments: []KnowHowMoment{{At: 1}}},
		{ID: "other", Title: "A separate lesson", Takeaway: "Use a different timeout when refreshing the panel after a failed request.", Moments: []KnowHowMoment{{At: 3}}},
	}
	got := mergeKnowHowLessons(cards)
	if len(got) != 2 {
		t.Fatalf("cards = %+v", got)
	}
	for _, card := range got {
		if card.ID == "old" && (len(card.MergedIDs) != 1 || len(card.Moments) != 2 || card.Moments[1].At != 2) {
			t.Fatalf("lost provenance: %+v", card)
		}
	}
}
