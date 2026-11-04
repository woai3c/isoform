package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/internal/engagement"
)

func TestWalk102PersonCardsRequireAnOpenQuestion(t *testing.T) {
	app := &App{}
	for _, moment := range []engagement.Moment{
		{Kind: engagement.KindFirstWeek, SubjectID: "person-1", Params: map[string]string{"stage": "person"}},
		{Kind: engagement.KindPersonReappeared, SubjectID: "person-1", Params: map[string]string{"name": "Mira", "where": "WeChat"}},
	} {
		if _, ok := app.proactiveItem(moment, engagementText{}); ok {
			t.Fatalf("person moment entered a digest: %s", moment.Kind)
		}
		if !app.postEngagementReport(moment, engagementText{}) {
			t.Fatalf("suppressed person moment should be acknowledged: %s", moment.Kind)
		}
	}
	for _, card := range []mainChatProactive{
		{MomentID: "old-first-week", Kind: "first_week", Target: mainChatTarget{Kind: "person", ID: "person-1"}, Headline: "Your first week with Isoform.", Body: engagement.FirstWeekCopy("person", "", "en")},
		{MomentID: "old-sighting", Kind: "person_reappeared", Target: mainChatTarget{Kind: "person", ID: "person-1"}, Headline: "Mira came up in WeChat."},
	} {
		raw, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		content, input := app.projectProactiveVoiceDelivery(context.Background(), mainChatProactiveTool, card.Body, string(raw))
		if strings.Contains(content+input, "Mira") || strings.Contains(content+input, "Someone you know") || !mainChatWithdrawnThreadUpdate(input) {
			t.Fatalf("person card escaped: %s %s", content, input)
		}
	}
}
