package main

import (
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/recentfiles"
)

func purposeDetailForFiles(id, kind, state, title string, at int64) PurposeOpenLoopDetail {
	var detail PurposeOpenLoopDetail
	detail.OpenLoop.ID = id
	detail.OpenLoop.Kind = kind
	detail.Current.LifecycleState = state
	detail.Current.Title = title
	detail.Current.OccurredAt = at
	return detail
}

func TestFileLinkWords(t *testing.T) {
	cases := map[string][]string{
		"Q3 deck":                     {"q3", "deck"},
		"Send Sam the Q3 deck":        {"sam", "q3", "deck"},
		"Final draft v2 2026":         {"v2"},
		"Budget_review-notes":         {"budget"},
		"季度报告 初稿":                     {"季度报告", "初稿"},
		"Untitled document (copy) 12": {},
	}
	for text, want := range cases {
		got := fileLinkWords(text)
		if len(got) != len(want) {
			t.Errorf("fileLinkWords(%q) = %v, want %v", text, got, want)
			continue
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("fileLinkWords(%q) misses %q: %v", text, w, got)
			}
		}
	}
}

func TestLinkFilesToCommitments(t *testing.T) {
	details := []PurposeOpenLoopDetail{
		purposeDetailForFiles("c1", PurposeKindCommitment, PurposeStateActive, "Send Sam the Q3 deck", 100),
		purposeDetailForFiles("c2", PurposeKindCommitment, PurposeStateActive, "Finish the budget review for Q3", 200),
		purposeDetailForFiles("c3", PurposeKindCommitment, PurposeStateResolved, "Write the onboarding guide", 300),
		purposeDetailForFiles("g1", PurposeKindGoal, PurposeStateActive, "Ship the onboarding guide", 400),
		purposeDetailForFiles("c4", PurposeKindCommitment, PurposeStateWaiting, "Kickoff presentation", 50),
	}
	rows := []FileActivityRow{
		{ID: "deck", Name: "Q3 deck.key"},
		{ID: "budget", Name: "Q3 budget.numbers"},
		{ID: "guide", Name: "Onboarding guide.pages"},
		{ID: "notes", Name: "notes.txt"},
		{ID: "lone", Name: "Presentation.key"},
		{ID: "short", Name: "Q3.pdf"},
	}
	links := linkFilesToCommitments(rows, details)
	if links["deck"].ID != "c1" {
		t.Fatalf("deck linked to %+v", links["deck"])
	}
	if links["budget"].ID != "c2" {
		t.Fatalf("budget linked to %+v", links["budget"])
	}
	if _, ok := links["guide"]; ok {
		t.Fatal("only open commitments take part, never closed ones or goals")
	}
	if _, ok := links["notes"]; ok {
		t.Fatal("a name with no significant words links to nothing")
	}
	if links["lone"].ID != "c4" {
		t.Fatalf("one long word that is all the name says links: %+v", links["lone"])
	}
	if _, ok := links["short"]; ok {
		t.Fatal("one short shared word is not enough")
	}
	if got := linkFilesToCommitments(rows, nil); len(got) != 0 {
		t.Fatalf("no commitments, no links: %v", got)
	}
}

func TestMobileBriefDoingCandidates(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	private := "/Users/sam/Documents/Taxes"
	rows := []FileActivityRow{
		{ID: "a", Name: "Q3 deck.key", Kind: recentfiles.KindPresentation, Action: recentfiles.ActionEdited,
			ActiveAt: at.UnixMilli(), AppBundleID: "com.apple.iWork.Keynote", AppName: "Keynote"},
		{ID: "b", Name: "Plan.txt", Kind: recentfiles.KindText, Action: recentfiles.ActionOpened, ActiveAt: at.Add(time.Hour).UnixMilli()},
		{ID: "c", Name: "Vault.txt", Action: recentfiles.ActionOpened, ActiveAt: at.UnixMilli(),
			AppBundleID: "com.agilebits.onepassword7", AppName: "1Password 7"},
		{ID: "d", Name: "return.pdf", Action: recentfiles.ActionOpened, ActiveAt: at.UnixMilli(),
			DirHashes: recentfiles.AncestorHashes(private + "/2026/return.pdf")},
	}
	links := map[string]fileCommitment{"a": {ID: "c1", Title: "Send Sam the Q3 deck"}}
	got := mobileBriefDoingCandidates(rows, links, []string{private})
	if len(got) != 2 {
		t.Fatalf("candidates = %+v", got)
	}
	deck := got[0]
	if deck.SourceKind != mobileBriefFileSource || deck.SourceID != "file:a" || deck.Title != "Q3 deck.key" ||
		deck.Detail != "Edited; for Send Sam the Q3 deck" || !deck.AppsShown || deck.Apps[0] != "Keynote" {
		t.Fatalf("deck = %+v", deck)
	}
	if got[1].Detail != "Opened" || len(got[1].Apps) != 0 {
		t.Fatalf("plan = %+v", got[1])
	}
}

func TestMobileBriefKeepsFileNamesFromTheNarrative(t *testing.T) {
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot:        mobileBriefSlotEvening,
		WindowStart: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
		AsOf:        time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC),
		Doing: []mobileBriefCandidate{{
			SourceKind: mobileBriefFileSource, SourceID: "file:a", Title: "Severance agreement.pdf",
			Detail: "Opened", At: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC),
		}},
		OpenLoops: []mobileBriefCandidate{
			{SourceKind: "purpose", SourceID: "p1", Title: "Plan the offsite", Kind: "commitment", At: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), DueAt: time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC)},
			{SourceKind: "purpose", SourceID: "p2", Title: "Book the venue", Kind: "commitment", At: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), DueAt: time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)},
		},
	})
	if len(brief.Sections.Doing) != 1 {
		t.Fatalf("the phone gets the document: %+v", brief.Sections.Doing)
	}
	prompt := mobileBriefTestPrompt(brief, "English")
	if strings.Contains(prompt, "Severance") {
		t.Fatal("a file name must never reach the narrative model")
	}
	if !strings.Contains(prompt, "Plan the offsite") {
		t.Fatal("other evidence still reaches the narrative")
	}
}
