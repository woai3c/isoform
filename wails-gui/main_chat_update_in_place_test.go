package main

import (
	"strings"
	"testing"
	"time"
)

// A card whose outcome was refreshed after it was written keeps its place and
// time, shows the newer outcome, and records the refresh time (chat v1.0.12).
func TestRefreshedThreadOutcomeUpdatesCardInPlace(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, app, "thread", f, now)
	row = saveFixtureOutcome(t, app, row, f)
	// The card was written from an earlier outcome, so its source hash is stale.
	stale := threadOutcomeSourceID(row.ID, &ActivityThreadOutcome{Version: threadOutcomeVersion, Kind: "assistant", State: "incomplete", Summary: "An earlier outcome."})
	card := mainChatProactive{MomentID: "refreshed", Kind: "decision_due", State: "pending",
		ActivityOutcomeSource: stale, Headline: "Old headline", Body: "An earlier outcome.", ActionIDs: []string{"open"}}
	if !app.projectThreadOutcomeItem(t.Context(), &card, "en") {
		t.Fatal("refreshed outcome was not projected")
	}
	if card.Kind != "decision_due" || card.State != "pending" {
		t.Fatalf("refreshed card lost its place or state: kind %s state %s", card.Kind, card.State)
	}
	if !strings.Contains(card.Body, f.Summary) || strings.Contains(card.Body, "An earlier outcome.") {
		t.Fatalf("refreshed card does not show the newer outcome: %q", card.Body)
	}
	if card.UpdatedAt != row.EndMs || card.UpdatedAt == 0 {
		t.Fatalf("UpdatedAt = %d, want the refresh time %d", card.UpdatedAt, row.EndMs)
	}
	if card.ActivityOutcomeSource != threadOutcomeSourceID(row.ID, app.readableThreadOutcome(t.Context(), row)) {
		t.Fatal("refreshed card did not adopt the current source, so its action would be held")
	}
	// The same card read again is unchanged: no second refresh, no withdrawal.
	again := card
	if !app.projectThreadOutcomeItem(t.Context(), &again, "en") || again.Kind != "decision_due" || again.Body != card.Body {
		t.Fatalf("second read changed the card: %+v", again)
	}
}

// A card whose outcome was refreshed to "acted on" says it is done.
func TestRefreshedThreadOutcomeDoneSaysSo(t *testing.T) {
	card := mainChatProactive{MomentID: "done", Kind: "decision_due", State: "pending", Body: "Old."}
	outcome := &ActivityThreadOutcome{Version: threadOutcomeVersion, Kind: "assistant", State: "acted_on", Summary: "You replied to the thread.", UserActed: "observed"}
	applyRefreshedThreadOutcome(&card, "act_done", outcome, 1793864902018)
	if card.State != "done" || card.Kind != "decision_due" || card.UpdatedAt != 1793864902018 {
		t.Fatalf("done refresh = %+v", card)
	}
}

// A first-week card about a person is never shown as a first-week card, and is
// not counted as a removed source either.
func TestFirstWeekPersonCardIsWithheldNotShown(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	card := mainChatProactive{MomentID: "first-week-person", Kind: "first_week", State: "pending",
		Target: mainChatTarget{Kind: "person", ID: "person-1"}, Headline: "Your first week with Isoform.", Body: "Someone you know appeared."}
	if !app.projectProactiveVoiceItem(t.Context(), &card, "en") {
		t.Fatal("person first-week card was not projected")
	}
	if card.Kind != mainChatWithheldKind || card.State != "expired" {
		t.Fatalf("person first-week card = kind %s state %s, want withheld and expired", card.Kind, card.State)
	}
	if class := mainChatProactiveRowClass(card, now); class != MainChatRowRoutine {
		t.Fatalf("withheld card classified as %s, want routine", class)
	}
	if !mainChatWithdrawnThreadUpdate(`{"momentId":"x","kind":"withheld"}`) {
		t.Fatal("withheld card is counted as a routine update")
	}
}
