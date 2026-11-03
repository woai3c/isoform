package main

import (
	"strings"
	"testing"
	"time"
)

func TestBriefConversationLatestContinuationWins(t *testing.T) {
	fixtures := threadOutcomeFixtures(t)
	for _, tc := range []struct {
		name    string
		next    int
		extract bool
	}{
		{"order placed", 2, true},
		{"not extracted", 1, false},
		{"still at checkout", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newPrivacyReadTestApp(t)
			now := time.Now()
			root := seedThreadFixture(t, app, "cart-root", fixtures[0], now.AddDate(0, 0, -20))
			root = saveFixtureOutcome(t, app, root, fixtures[0])
			before := mobileBriefInput{AsOf: now.AddDate(0, 0, -10), Location: time.UTC}
			collectMobileBriefOlderThreads(t.Context(), app, &before)
			if len(before.OpenLoops) != 1 || before.OpenLoops[0].LoopKey == "" {
				t.Fatal("missing canonical root loop")
			}
			prior := buildMobileBriefV1(before)
			child := seedThreadFixture(t, app, "cart-continuation", fixtures[tc.next], now.AddDate(0, 0, -2))
			child.ContinuesActivityID = root.ID
			if _, err := app.activity.store.db.Exec(`UPDATE activities SET continues_activity_id=? WHERE id=?`, root.ID, child.ID); err != nil {
				t.Fatal(err)
			}
			if tc.extract {
				child = saveFixtureOutcome(t, app, child, fixtures[tc.next])
			}
			after := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefOlderThreads(t.Context(), app, &after)
			if !tc.extract || tc.next == 2 {
				if len(after.OpenLoops) != 0 {
					t.Fatalf("revived older cart: %+v", after.OpenLoops)
				}
				return
			}
			if len(after.OpenLoops) != 1 || !strings.Contains(after.OpenLoops[0].SourceID, child.ID) || after.OpenLoops[0].LoopKey != before.OpenLoops[0].LoopKey {
				t.Fatalf("latest continuation lost its identity: %+v", after.OpenLoops)
			}
			input := after
			input.PreviousBriefs = []MobileBriefV1{prior}
			input.Moved = append([]mobileBriefCandidate(nil), after.OpenLoops...)
			selected := selectMobileBriefOpenLoops(input)
			if len(selected.OpenLoops) != 0 || len(selected.Moved) != 0 {
				t.Fatal("continuation bypassed suppression of the earlier source")
			}
		})
	}
}

func TestBriefConversationContinuationFamilies(t *testing.T) {
	rows := []mobileBriefThreadState{
		{id: "root", start: 1, end: 2},
		{id: "draft", parent: "root", start: 3, end: 4},
		{id: "done", parent: "root", start: 5, end: 6},
		{id: "future", parent: "root", start: 9, end: 10},
		{id: "cycle-a", parent: "cycle-b", start: 1},
		{id: "cycle-b", parent: "cycle-a", start: 1},
	}
	families := mobileBriefThreadFamilies(rows, 8)
	if len(families) != 1 || len(families["root"]) != 3 || families["root"][0].id != "done" {
		t.Fatalf("latest sibling, future or cycle handling: %+v", families)
	}
}

func TestBriefConversationRecapAliasesFollowLatestContinuation(t *testing.T) {
	fixtures := threadOutcomeFixtures(t)
	for _, next := range []string{"completed", "unextracted", "protected"} {
		t.Run(next, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newPrivacyReadTestApp(t)
			now := time.Now()
			root := seedThreadFixture(t, app, "recap-cart", fixtures[0], now.Add(-6*time.Hour))
			root = saveFixtureOutcome(t, app, root, fixtures[0])
			child := seedThreadFixture(t, app, "recap-newest", fixtures[2], now.Add(-2*time.Hour))
			child.ContinuesActivityID = root.ID
			if _, err := app.activity.store.db.Exec(`UPDATE activities SET continues_activity_id=? WHERE id=?`, root.ID, child.ID); err != nil {
				t.Fatal(err)
			}
			if next != "unextracted" {
				child = saveFixtureOutcome(t, app, child, fixtures[2])
			}
			if next == "protected" {
				app.saveObservationPauses([]observationPause{{Start: child.StartMs, End: child.EndMs + 1}})
			}
			history := seedThreadFixture(t, app, "historical-saved", fixtures[6], now.Add(-8*time.Hour))
			saveFixtureOutcome(t, app, history, fixtures[6])
			in := mobileBriefInput{Slot: "morning", WindowStart: now.Add(-12 * time.Hour), AsOf: now, Location: time.Local}
			collectMobileBriefRecap(app, &in)
			if len(in.Moved) < 2 {
				t.Fatal("fixture did not reach the real recap collector")
			}
			collectMobileBriefOlderThreads(t.Context(), app, &in)
			brief := buildMobileBriefV1(in)
			brief.Narrative = mobileBriefNarrativeTemplate(brief, "en")
			_, brief.Offers = mobileBriefClosing(brief, "en")
			brief = app.mobileBriefForDelivery(brief)
			for _, section := range mobileBriefSectionLines(brief.Sections) {
				for _, item := range section.items {
					if strings.Contains(item.Title, "still in the cart") {
						t.Fatalf("superseded cart survived %s: %+v", section.name, item)
					}
				}
			}
			for _, offer := range brief.Offers {
				if offer.Action == "order" {
					t.Fatalf("superseded action: %+v", offer)
				}
			}
			var text string
			for _, p := range brief.Narrative {
				text += p.Text + "\n"
			}
			if strings.Contains(text, "still in the cart") || strings.Contains(text, "help place the order") {
				t.Fatal(text)
			}
			if !strings.Contains(text, "release memo") {
				t.Fatalf("legitimate saved highlight lost: %s", text)
			}
			if next == "completed" && !strings.Contains(text, "order was placed") {
				t.Fatalf("settled outcome lost: %s", text)
			}
		})
	}
}

func TestBriefConversationLatestSettledDecisionKeepsNextStep(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	fixture := threadOutcomeFixtures(t)[7]
	fixture.NextStep, fixture.Action = "Note the decision in the plan.", "review"
	now := time.Now()
	row := seedThreadFixture(t, app, "settled-with-next-step", fixture, now.Add(-3*time.Hour))
	saveFixtureOutcome(t, app, row, fixture)
	in := mobileBriefInput{Slot: "morning", WindowStart: now.Add(-8 * time.Hour), AsOf: now, Location: time.Local}
	collectMobileBriefRecap(app, &in)
	collectMobileBriefOlderThreads(t.Context(), app, &in)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.Review) != 1 || !strings.Contains(brief.Sections.Review[0].Title, "settled RTK") {
		t.Fatalf("latest settled decision lost: %+v", brief.Sections)
	}
	offers := mobileBriefConversationOffers(brief, "en")
	if len(offers) != 1 || offers[0].Label != "Note the decision in the plan" || offers[0].Kind != "note_decision" {
		t.Fatalf("settled next step lost: %+v", offers)
	}
}
