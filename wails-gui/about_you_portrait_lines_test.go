package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// portraitTestWithLines is the test draft with its How to work with me lines
// replaced, so a rewrite brings different lines.
func portraitTestWithLines(t *testing.T, lines ...portraitLine) string {
	t.Helper()
	var parsed portraitDraft
	if err := json.Unmarshal([]byte(portraitTestDraft), &parsed); err != nil {
		t.Fatal(err)
	}
	parsed.WorkWithMe = lines
	raw, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func portraitLineByText(lines []AboutYouPortraitLine, text string) (AboutYouPortraitLine, bool) {
	for _, line := range lines {
		if line.Text == text {
			return line, true
		}
	}
	return AboutYouPortraitLine{}, false
}

// Y41: a How to work with me line the person confirmed survives a rewrite
// that brings other lines, verbatim, with its "You confirmed this" mark.
func TestPortraitConfirmedLineSurvivesRewrite(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	runner.response = portraitTestDraft
	now := time.Now()
	service.check(context.Background(), now)
	before := mustPortraitRead(t, app, "ready")
	pinned, ok := portraitLineByText(before.WorkWithMe, "Send the number, not the opinion.")
	if !ok || pinned.Confirmed {
		t.Fatalf("setup: want the unconfirmed line first, got %+v", before.WorkWithMe)
	}
	if _, err := app.ConfirmMemoryEntry("belief-12"); err != nil {
		t.Fatal(err)
	}

	// The writer now brings different lines and never repeats the confirmed one.
	runner.response = portraitTestWithLines(t,
		portraitLine{Text: "Ask in writing first.", IDs: []string{"belief-0", "belief-1"}},
		portraitLine{Text: "Mornings are for building.", IDs: []string{"belief-10", "belief-11"}},
	)
	service.stateMu.Lock()
	service.force = true
	service.stateMu.Unlock()
	service.check(context.Background(), now.Add(2*time.Hour))

	doc, err := readPortrait(path)
	if err != nil || doc == nil {
		t.Fatalf("read portrait: %v", err)
	}
	if len(runner.inputs) < 2 {
		t.Fatal("the rewrite did not run")
	}
	got := mustPortraitRead(t, app, "ready")
	line, ok := portraitLineByText(got.WorkWithMe, "Send the number, not the opinion.")
	if !ok {
		t.Fatalf("confirmed line lost in the rewrite: %+v", got.WorkWithMe)
	}
	if !line.Confirmed {
		t.Fatalf("confirmed line lost its mark: %+v", line)
	}
	if _, ok := portraitLineByText(got.WorkWithMe, "Ask in writing first."); !ok {
		t.Fatalf("a new line did not join: %+v", got.WorkWithMe)
	}
	if got.WorkWithMe[0].Text != "Send the number, not the opinion." {
		t.Fatalf("confirmed line is not first: %+v", got.WorkWithMe)
	}
}

// A confirmed belief whose content changed (new digest) is still confirmed;
// its line stays, and the person still sees it as confirmed.
func TestPortraitConfirmedLineSurvivesChangedBelief(t *testing.T) {
	app, service, _, path := newPortraitTestRig(t, 20)
	now := time.Now()
	service.check(context.Background(), now)
	if _, err := app.ConfirmMemoryEntry("belief-12"); err != nil {
		t.Fatal(err)
	}
	service.check(context.Background(), now.Add(time.Minute))
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "belief-12.md")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Adding a point changes the digest and keeps the confirmation.
	if err := os.WriteFile(file, []byte(strings.TrimRight(string(raw), "\n")+"\n- A later detail.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service.check(context.Background(), now.Add(2*time.Minute))

	doc, err := readPortrait(path)
	if err != nil || doc == nil {
		t.Fatalf("read portrait: %v", err)
	}
	found := false
	for _, line := range doc.WorkWithMe {
		if line.Text == "Send the number, not the opinion." {
			found = true
			if !line.Pinned {
				t.Fatalf("stored line is not pinned: %+v", line)
			}
		}
	}
	if !found {
		t.Fatalf("line resting on a changed confirmed belief was dropped: %+v", doc.WorkWithMe)
	}
	got := mustPortraitRead(t, app, "ready")
	if line, ok := portraitLineByText(got.WorkWithMe, "Send the number, not the opinion."); !ok || !line.Confirmed {
		t.Fatalf("shown line = %+v, found %t", line, ok)
	}
}

// Not quite is the person's own answer about the belief, so its line leaves.
func TestPortraitPinnedLineLeavesWhenBeliefIsNotQuite(t *testing.T) {
	app, service, _, _ := newPortraitTestRig(t, 20)
	now := time.Now()
	service.check(context.Background(), now)
	if _, err := app.ConfirmMemoryEntry("belief-12"); err != nil {
		t.Fatal(err)
	}
	if err := app.AnswerAboutYouPortraitBelief("belief-12", false); err != nil {
		t.Fatal(err)
	}
	service.check(context.Background(), now.Add(time.Minute))
	if line, ok := portraitLineByText(mustPortraitRead(t, app, "ready").WorkWithMe, "Send the number, not the opinion."); ok && line.Confirmed {
		t.Fatalf("a Not quite belief still pins its line: %+v", line)
	}
}

func TestPinLinesKeepsConfirmedFirstAndCapsNewOnes(t *testing.T) {
	confirmed := map[string]bool{"a": true}
	shown := []portraitLine{
		{Text: "pinned", IDs: []string{"a", "x"}},
		{Text: "plain", IDs: []string{"y"}},
	}
	written := []portraitLine{
		{Text: "repeats the pinned beliefs", IDs: []string{"a"}},
		{Text: "one", IDs: []string{"b"}},
		{Text: "two", IDs: []string{"c"}},
		{Text: "three", IDs: []string{"d"}},
		{Text: "four", IDs: []string{"e"}},
		{Text: "five", IDs: []string{"f"}},
	}
	got := pinLines(shown, written, confirmed)
	var texts []string
	for _, line := range got {
		texts = append(texts, line.Text)
	}
	want := []string{"pinned", "one", "two", "three", "four"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("pinLines = %v, want %v", texts, want)
	}
	if !got[0].Pinned || got[1].Pinned {
		t.Fatalf("pin flags = %+v", got)
	}
}

// Show it replaces the shown version with the waiting one; a line confirmed
// on the shown version stays first.
func TestPortraitShowPendingKeepsConfirmedLine(t *testing.T) {
	doc := &portraitDocument{
		Language:   "en",
		Sentences:  []portraitSentence{},
		WorkWithMe: []portraitLine{{Text: "Send the number, not the opinion.", IDs: []string{"belief-12", "belief-13"}, Pinned: true}},
		Pending: &portraitDocument{
			Language:   "en",
			Sentences:  []portraitSentence{},
			WorkWithMe: []portraitLine{{Text: "Ask in writing first.", IDs: []string{"belief-0", "belief-1"}}},
		},
	}
	doc.showPending(map[string]bool{"belief-12": true})
	if len(doc.WorkWithMe) != 2 || doc.WorkWithMe[0].Text != "Send the number, not the opinion." || !doc.WorkWithMe[0].Pinned || doc.WorkWithMe[1].Text != "Ask in writing first." {
		t.Fatalf("after Show it = %+v", doc.WorkWithMe)
	}
	if doc.Pending != nil {
		t.Fatal("Show it left a waiting version")
	}
}
