package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMainChatBriefDisplayUsesTasksReadyTime(t *testing.T) {
	settings := defaultMobileBriefSettings()
	settings.Evening = "18:00"
	brief := MobileBriefV1{BriefID: "2026-10-30-evening", Slot: "evening", AsOf: "2026-10-30T17:50:00Z", Timezone: "UTC", YourWeek: &MobileBriefWeek{}}
	state := upcomingBriefState{Settings: settings, Runs: []mobileBriefRun{{BriefID: brief.BriefID, Slot: "evening", Status: mobileBriefRunOK, ScheduledAt: "2026-10-30T17:50:00Z", At: "2026-10-30T17:55:00Z"}}}
	got := mainChatBriefDisplayFor(brief, state, time.UTC)
	if got.Slot != "evening" || got.AsOf != "2026-10-30T18:00:00Z" {
		t.Fatalf("on-time display: %+v", got)
	}
	state.Runs[0].At = "2026-10-30T18:45:00Z"
	got = mainChatBriefDisplayFor(brief, state, time.UTC)
	if got.AsOf != "2026-10-30T18:45:00Z" {
		t.Fatalf("late display: %+v", got)
	}
	state.Runs[0].BriefID = "other"
	got = mainChatBriefDisplayFor(brief, state, time.UTC)
	if got.AsOf != brief.AsOf {
		t.Fatalf("unrelated run changed display: %+v", got)
	}
}

func TestMainChatBriefDisplayProjectionKeepsUnknownFieldsAndWithholdsWithdrawn(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(18, 0)}
	service, _ := newTestMobileBriefService(t, clock)
	service.app.mobileBriefSvc = service
	brief := MobileBriefV1{BriefID: "2026-10-09-evening", Slot: "evening", LocalDate: "2026-10-09", AsOf: "2026-10-09T18:00:00Z", Timezone: "UTC", NarrativeStatus: "ok"}
	if err := service.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	project := service.app.mainChatBriefDisplayProjector()
	var shown map[string]any
	input := `{"kind":"brief_ready","briefId":"2026-10-09-evening","evidence":[{"kind":"brief"}],"futureField":"kept","futureNumber":9223372036854775807}`
	projected := project(mainChatProactiveTool, input)
	if err := json.Unmarshal([]byte(projected), &shown); err != nil {
		t.Fatal(err)
	}
	if shown["futureField"] != "kept" || shown["briefDisplay"] == nil || !strings.Contains(projected, `"futureNumber":9223372036854775807`) {
		t.Fatalf("title metadata or unknown field missing: %+v", shown)
	}
	legacy := `{"kind":"first_week","evidence":[{"kind":"engagement","id":"2026-10-09-evening"}]}`
	var old map[string]any
	if err := json.Unmarshal([]byte(project(mainChatProactiveTool, legacy)), &old); err != nil {
		t.Fatal(err)
	}
	if old["briefId"] != brief.BriefID || old["briefDisplay"] == nil {
		t.Fatalf("legacy card: %+v", old)
	}
	input = `{"kind":"brief_ready","briefId":"2026-10-09-evening","evidence":[]}`
	shown = nil
	if err := json.Unmarshal([]byte(project(mainChatProactiveTool, input)), &shown); err != nil {
		t.Fatal(err)
	}
	if _, ok := shown["briefDisplay"]; ok {
		t.Fatalf("withdrawn source gained title metadata: %+v", shown)
	}
}

// The card's first words are the brief's first non-empty paragraph, trimmed.
func TestMainChatBriefLeadIsTheFirstSavedParagraph(t *testing.T) {
	cases := map[string]struct {
		paragraphs []MobileBriefPara
		want       string
	}{
		"first paragraph":  {[]MobileBriefPara{{Text: "  Two replies are waiting on you.  "}, {Text: "Later."}}, "Two replies are waiting on you."},
		"skips empty ones": {[]MobileBriefPara{{Text: " "}, {Text: "The deck is due."}}, "The deck is due."},
		"no words":         {nil, ""},
	}
	for name, tc := range cases {
		if got := mainChatBriefLead(MobileBriefV1{Narrative: tc.paragraphs}); got != tc.want {
			t.Fatalf("%s: got %q want %q", name, got, tc.want)
		}
	}
}

// The lead is read through the same finished read Today uses, so it is exactly
// what that read returns, and a withdrawn card (no evidence) carries none.
func TestMainChatBriefDisplayLeadFollowsTheFinishedRead(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(18, 0)}
	service, _ := newTestMobileBriefService(t, clock)
	service.app.mobileBriefSvc = service
	brief := MobileBriefV1{BriefID: "2026-10-09-evening", Slot: "evening", LocalDate: "2026-10-09", AsOf: "2026-10-09T18:00:00Z", Timezone: "UTC", NarrativeStatus: "ok",
		Narrative: []MobileBriefPara{{Text: "Two replies are waiting on you."}}}
	if err := service.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	finished, _, err := service.finishedBrief(brief.BriefID)
	if err != nil {
		t.Fatal(err)
	}
	project := service.app.mainChatBriefDisplayProjector()
	var shown map[string]any
	input := `{"kind":"brief_ready","briefId":"2026-10-09-evening","evidence":[{"kind":"brief"}]}`
	if err := json.Unmarshal([]byte(project(mainChatProactiveTool, input)), &shown); err != nil {
		t.Fatal(err)
	}
	if want := mainChatBriefLead(finished); want != "" && shown["briefLead"] != want {
		t.Fatalf("the card's words differ from the finished read: got %v want %q", shown["briefLead"], want)
	}
	shown = nil
	withdrawn := `{"kind":"brief_ready","briefId":"2026-10-09-evening","evidence":[]}`
	if err := json.Unmarshal([]byte(project(mainChatProactiveTool, withdrawn)), &shown); err != nil {
		t.Fatal(err)
	}
	if _, leaked := shown["briefLead"]; leaked {
		t.Fatalf("a withdrawn card carried words: %+v", shown)
	}
}
