package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestTodayV1119EventQuestionGoesOnceTheEventStarts(t *testing.T) {
	answer := 1
	brief := MobileBriefV1{
		BriefID: "2026-11-03-evening", Slot: mobileBriefSlotEvening, AsOf: "2026-11-03T18:02:00Z",
		Evidence: []MobileBriefRef{{Ref: "cal-1", SourceKind: mobileBriefCalendarSource}},
		Sections: MobileBriefSections{Meetings: []MobileBriefItem{{Kind: "meeting", Title: "1:1 with Sam", At: "2026-11-03T19:30:00Z", Status: "upcoming", Refs: []string{"cal-1"}}}},
		Narrative: []MobileBriefPara{
			{Text: "A quiet evening. The 1:1 with Sam is at 7:30 PM.", Refs: []string{"cal-1"}},
			{Text: "Want me to help you prepare for the 1:1 with Sam at 7:30 PM?", Refs: []string{"cal-1"}},
		},
		Cards:  []MobileBriefCard{{AnswerTo: "closing"}, {Paragraph: &answer}, {}},
		Offers: []MobileBriefOffer{{Refs: []string{"cal-1"}}},
	}
	before := mobileBriefWithoutStartedEventQuestion(brief, time.Date(2026, 11, 3, 19, 0, 0, 0, time.UTC))
	if len(before.Narrative) != 2 || len(before.Cards) != 3 || len(before.Offers) != 1 {
		t.Fatalf("before 7:30 PM the question stays: %+v", before)
	}
	after := mobileBriefWithoutStartedEventQuestion(brief, time.Date(2026, 11, 3, 21, 52, 0, 0, time.UTC))
	if len(after.Narrative) != 1 || after.Narrative[0].Text != brief.Narrative[0].Text || len(after.Cards) != 1 || len(after.Offers) != 0 {
		t.Fatalf("after the 1:1 starts only the question and its buttons go: %+v", after)
	}
	if len(brief.Narrative) != 2 || len(brief.Cards) != 3 {
		t.Fatalf("the saved brief is not changed: %+v", brief)
	}
	zh := brief
	zh.Narrative = append([]MobileBriefPara{brief.Narrative[0]}, MobileBriefPara{Text: "要我帮你准备晚上7:30和 Sam 的一对一吗？", Refs: []string{"cal-1"}})
	if got := mobileBriefWithoutStartedEventQuestion(zh, time.Date(2026, 11, 3, 21, 52, 0, 0, time.UTC)); len(got.Narrative) != 1 {
		t.Fatalf("the Chinese question goes too: %+v", got.Narrative)
	}
	onNow := brief
	onNow.AsOf = "2026-11-03T19:40:00Z"
	if got := mobileBriefWithoutStartedEventQuestion(onNow, time.Date(2026, 11, 3, 21, 52, 0, 0, time.UTC)); len(got.Narrative) != 2 {
		t.Fatalf("a question about an event already on when the brief was made stays: %+v", got.Narrative)
	}
}

func TestTodayV1119TodayDropsAStartedEventQuestion(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 11, 3, 21, 52, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.mobileBriefSvc = svc
	brief := mobileBriefStoredBrief(t, "2026-11-03", mobileBriefSlotEvening, 18)
	brief.AsOf = "2026-11-03T18:02:00Z"
	brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: "cal-1", SourceKind: mobileBriefCalendarSource})
	brief.Sections.Meetings = append(brief.Sections.Meetings, MobileBriefItem{Kind: "meeting", Title: "1:1 with Sam", At: "2026-11-03T19:30:00Z", Status: "upcoming", Refs: []string{"cal-1"}})
	brief.Narrative = append(brief.Narrative, MobileBriefPara{Text: "Want me to help you prepare for the 1:1 with Sam at 7:30 PM?", Refs: []string{"cal-1"}})
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() (TodayBriefView, error){
		"latest": svc.app.GetLatestBrief,
		"by id":  func() (TodayBriefView, error) { return svc.app.GetTodayBriefByID(brief.BriefID) },
	} {
		view, err := read()
		if err != nil || view.Brief == nil {
			t.Fatalf("%s: %+v, %v", name, view, err)
		}
		for _, para := range view.Brief.Narrative {
			if para.Text == "Want me to help you prepare for the 1:1 with Sam at 7:30 PM?" {
				t.Fatalf("%s still asks about the started 1:1", name)
			}
		}
	}
}

func TestMainChatV1119DidntRunLineGoesOnceTheBriefRan(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 11, 3, 18, 30, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	app := svc.app
	app.mobileBriefSvc = svc
	ctx := context.Background()
	brief := mobileBriefStoredBrief(t, "2026-11-03", mobileBriefSlotEvening, 18)
	missed, err := app.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{Kind: "brief_missed", SubjectID: brief.BriefID, DedupeKey: "brief_missed:" + brief.BriefID})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := json.Marshal(mainChatStatus{Period: "evening", Entries: []mainChatStatusEntry{
		{ID: "missed", MomentID: missed.ID, Kind: "brief_missed", Text: "Your evening brief didn't run."},
		{ID: "pause", Kind: "capture_paused", Text: "Capture paused at 1:37 PM."},
	}})
	kinds := func(input string) []string {
		var got mainChatStatus
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, entry := range got.Entries {
			out = append(out, entry.Kind)
		}
		return out
	}
	if got := kinds(app.mainChatBriefMissedProjector(ctx)(mainChatStatusTool, string(status))); len(got) != 2 {
		t.Fatalf("before the brief ran its line stays: %v", got)
	}
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if got := kinds(app.mainChatBriefMissedProjector(ctx)(mainChatStatusTool, string(status))); len(got) != 1 || got[0] != "capture_paused" {
		t.Fatalf("after a later try made the brief only its didn't-run line goes: %v", got)
	}
}
