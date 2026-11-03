package main

import (
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func TestBriefWalkthroughV116RunTimingUsesRecordedCause(t *testing.T) {
	brief := MobileBriefV1{BriefID: "one", Generation: &MobileBriefGeneration{Reason: "mac_asleep", ScheduledAt: "2026-11-02T06:50:00Z"}}
	runs := []mobileBriefRun{{BriefID: "other", Status: mobileBriefRunOK, ScheduledAt: "2026-11-02T07:00:00Z", At: "2026-11-02T09:23:00Z", Reason: "asleep"}}
	if got := mobileBriefWithRunTiming(brief, runs); got.Generation != nil {
		t.Fatalf("unmatched run: %+v", got.Generation)
	}
	runs = append(runs, mobileBriefRun{BriefID: "one", Status: mobileBriefRunOK, ScheduledAt: "2026-11-02T07:00:00Z", At: "2026-11-02T09:23:00Z", Reason: "closed"})
	got := mobileBriefWithRunTiming(brief, runs)
	if got.Generation == nil || got.Generation.Reason != "closed" || got.Generation.ScheduledAt != runs[1].ScheduledAt || got.Generation.GeneratedAt != runs[1].At {
		t.Fatalf("run timing: %+v", got.Generation)
	}
	runs[1].Reason = "unproved"
	if got := mobileBriefWithRunTiming(brief, runs); got.Generation == nil || got.Generation.Reason != "" {
		t.Fatalf("unknown cause: %+v", got.Generation)
	}
	runs[1].At = "2026-11-02T07:10:00Z"
	if got := mobileBriefWithRunTiming(brief, runs); got.Generation != nil {
		t.Fatalf("on time: %+v", got.Generation)
	}
}

func TestBriefGenerationKeepsTimingUntilCompletedRunIsRecorded(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 40)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.mobileBriefSvc = svc
	brief := MobileBriefV1{BriefID: "2026-10-09-morning", Slot: "morning", AsOf: formatMobileBriefTime(clock.Now()),
		Generation: &MobileBriefGeneration{Reason: "mac_asleep", ScheduledAt: formatMobileBriefTime(mobileBriefTestDay(7, 0))}}
	if got := svc.app.mobileBriefForGeneration(brief); got.Generation == nil || got.Generation.Reason != "mac_asleep" {
		t.Fatalf("generation timing lost before run: %+v", got.Generation)
	}
	if got := svc.app.mobileBriefForDelivery(brief); got.Generation != nil {
		t.Fatalf("read exposed timing without a completed run: %+v", got.Generation)
	}
	svc.recordRun(mobileBriefRun{BriefID: brief.BriefID, Slot: brief.Slot, ScheduledAt: brief.Generation.ScheduledAt,
		At: brief.AsOf, Status: mobileBriefRunOK, Reason: mobileBriefMissedAsleep})
	if got := svc.app.mobileBriefForDelivery(brief); got.Generation == nil || got.Generation.Reason != "asleep" {
		t.Fatalf("read did not use completed run: %+v", got.Generation)
	}
}

func TestBriefWalkthroughV116CalendarNoticeAndTitles(t *testing.T) {
	now := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	event := func(id string, start time.Time) calendar.Event {
		return calendar.Event{ID: id, Title: id, Start: start, End: start.Add(time.Hour)}
	}
	holiday := event("Day of the Dead", now.AddDate(0, 0, 1))
	holiday.AllDay, holiday.Provider, holiday.CalendarTitle = true, "subscribed", "US Holidays"
	dinner := event("Dinner", now.Add(-time.Minute))
	plan := event("Office closed for holiday", now.Add(-12*time.Hour))
	plan.AllDay, plan.End = true, now.Add(6*time.Hour)
	meeting := event("Priya / Alex: weekly tuesdays 1:1", now.Add(time.Hour))
	meeting.Attendees = []calendar.Attendee{{Name: "Priya"}, {Name: "Alex", IsMe: true}}
	candidates := mobileBriefCalendarCandidates(nil, []calendar.Event{holiday, dinner, plan, meeting}, "evening", now, time.UTC, "3:04 PM")
	if len(candidates) != 2 || candidates[0].Title != plan.Title || candidates[1].Title != "1:1 with Priya" {
		t.Fatalf("calendar candidates = %+v", candidates)
	}
}

func TestBriefWalkthroughV116UncertainAttendanceDoesNotLead(t *testing.T) {
	for _, text := range []string{"Isoform couldn't tell whether you attended your weekly 1:1.", "Isoform 没法确定你是否参会：周会。"} {
		brief := MobileBriefV1{NarrativeStatus: "ok", Narrative: []MobileBriefPara{{Text: text}, {Text: "Your project moved."}}, Audio: &MobileBriefAudio{Bytes: 1}}
		got := mobileBriefHygiene(brief)
		if len(got.Narrative) != 1 || strings.Contains(got.Narrative[0].Text, "tell") || got.Audio != nil {
			t.Fatalf("lead not removed: %+v", got)
		}
	}
	sections := mobileBriefNarrativeSections(MobileBriefV1{MeetingAttendance: []MobileBriefAttendance{{State: "unknown", Title: "1:1", Refs: []string{"e1"}}}})
	for _, section := range sections {
		for _, item := range section.items {
			if item.Kind == mobileBriefAttendanceSource && item.Status == "unknown" {
				t.Fatal("uncertain attendance entered narrative")
			}
		}
	}
}

func TestBriefWalkthroughV116UnknownAttendanceEvidenceDoesNotLead(t *testing.T) {
	brief := MobileBriefV1{
		NarrativeStatus:   "ok",
		MeetingAttendance: []MobileBriefAttendance{{State: "unknown", Refs: []string{"attendance"}}},
		Narrative:         []MobileBriefPara{{Text: "There is no attendance record for the weekly meeting.", Refs: []string{"attendance"}}, {Text: "Your project moved."}},
		Audio:             &MobileBriefAudio{Bytes: 1},
	}
	got := mobileBriefHygiene(brief)
	if len(got.Narrative) != 1 || got.Narrative[0].Text != "Your project moved." || got.Audio != nil {
		t.Fatalf("uncertain attendance led the brief: %+v", got)
	}
}

func TestBriefWalkthroughV116SavedCalendarNotice(t *testing.T) {
	app, store := newLinkTestApp(t)
	now := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	holiday := calendar.Event{ID: "holiday", Title: "Day of the Dead", Provider: "subscribed", CalendarTitle: "US Holidays", AllDay: true, Start: now.AddDate(0, 0, 1), End: now.AddDate(0, 0, 2)}
	storeEvent(t, store, holiday)
	brief := MobileBriefV1{AsOf: formatMobileBriefTime(now), NarrativeStatus: "ok", Narrative: []MobileBriefPara{{Text: "Tomorrow is Day of the Dead.", Refs: []string{"holiday-ref"}}}, Evidence: []MobileBriefRef{{Ref: "holiday-ref", SourceKind: mobileBriefCalendarSource, SourceID: holiday.ID, Label: holiday.Title}}, Sections: MobileBriefSections{Meetings: []MobileBriefItem{{Title: holiday.Title, Refs: []string{"holiday-ref"}}}}}
	got, _ := app.mobileBriefDelivery(brief)
	if len(got.Narrative) != 0 || len(got.Sections.Meetings) != 0 {
		t.Fatalf("stored notice survived: %+v", got)
	}
}

func TestBriefWalkthroughV116WeekQuestionDoesNotOfferLongDictation(t *testing.T) {
	dictated := MobileBriefItem{Kind: "commitment", Title: "Make sure I do some sort of calf race or exercise for AFO like every day"}
	if morningBriefRecommendationUsable(dictated) {
		t.Fatal("raw dictation offered as a short name")
	}
	dictated.Title = "AFO calf raises"
	if !morningBriefRecommendationUsable(dictated) {
		t.Fatal("short name withheld")
	}
}

func TestBriefWalkthroughV116UnreopenableCalendarIsNotEvidence(t *testing.T) {
	app, _ := newLinkTestApp(t)
	brief := MobileBriefV1{AsOf: "2026-11-02T18:00:00Z", NarrativeStatus: "ok", Narrative: []MobileBriefPara{{Text: "Tomorrow is a holiday.", Refs: []string{"calendar-ref"}}}, Evidence: []MobileBriefRef{{Ref: "calendar-ref", SourceKind: mobileBriefCalendarSource, SourceID: "missing", Label: "Holiday"}}, Sections: MobileBriefSections{Meetings: []MobileBriefItem{{Title: "Holiday", Refs: []string{"calendar-ref"}}}}}
	got, _ := app.mobileBriefDelivery(brief)
	if len(got.Narrative) != 0 || len(got.Sections.Meetings) != 0 {
		t.Fatalf("unreadable calendar was presented as evidence: %+v", got)
	}
}

func TestBriefWalkthroughV116ReopensCalendarForFriendlyTitle(t *testing.T) {
	app, store := newLinkTestApp(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	event := calendar.Event{ID: "meeting", Title: "Priya / Alex: weekly tuesdays 1:1", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), Attendees: []calendar.Attendee{{Name: "Priya"}, {Name: "Alex", IsMe: true}}}
	storeEvent(t, store, event)
	original := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "meeting-ref", SourceKind: mobileBriefCalendarSource, SourceID: event.ID, Label: event.Title}}, Sections: MobileBriefSections{Meetings: []MobileBriefItem{{Title: event.Title, Refs: []string{"meeting-ref"}}}}, Narrative: []MobileBriefPara{{Text: "Prepare for your " + event.Title + ".", Refs: []string{"meeting-ref"}}, {Text: event.Title, Refs: []string{"other-ref"}}}, Audio: &MobileBriefAudio{Bytes: 1}, Cards: []MobileBriefCard{{}}, Offers: []MobileBriefOffer{{Refs: []string{"meeting-ref"}, Prompt: "Help me prepare for " + event.Title + "."}}}
	got := app.mobileBriefCalendarDisplay(t.Context(), original)
	if got.Sections.Meetings[0].Title != "1:1 with Priya" || got.Narrative[0].Text != "Prepare for your 1:1 with Priya." {
		t.Fatalf("title not projected: %+v", got)
	}
	if got.Narrative[1].Text != event.Title || original.Sections.Meetings[0].Title != event.Title || original.Narrative[0].Text != "Prepare for your "+event.Title+"." {
		t.Fatal("rewrote unbound text or the stored input")
	}
	// v1.1.10 C: the offer keeps its subject, renamed, so its chip stays.
	if got.Audio != nil || got.Cards != nil || len(got.Offers) != 1 || got.Offers[0].Prompt != "Help me prepare for the 1:1 with Priya." || got.Evidence[0].Label != event.Title {
		t.Fatalf("stale actions or audio survived, or source label changed: %+v", got.Offers)
	}
}

func TestBriefWalkthroughV116CalendarTitleNeedsPersonEvidence(t *testing.T) {
	app, store := newLinkTestApp(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	event := calendar.Event{ID: "ambiguous", Title: "Priya / Alex: weekly tuesdays 1:1", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}
	storeEvent(t, store, event)
	brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e", SourceKind: mobileBriefCalendarSource, SourceID: event.ID}}, Narrative: []MobileBriefPara{{Text: event.Title, Refs: []string{"e"}}}, Audio: &MobileBriefAudio{Bytes: 1}}
	got := app.mobileBriefCalendarDisplay(t.Context(), brief)
	if got.Narrative[0].Text != event.Title || got.Audio == nil {
		t.Fatalf("invented a person: %+v", got)
	}
}

func TestBriefV118AllDayStoredParagraphHasNoMidnight(t *testing.T) {
	app, store := newLinkTestApp(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	event := calendar.Event{ID: "all-day", Title: "Team offsite", AllDay: true, Start: now, End: now.Add(24 * time.Hour)}
	storeEvent(t, store, event)
	brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e", SourceKind: mobileBriefCalendarSource, SourceID: event.ID}}, Narrative: []MobileBriefPara{{Text: "Team offsite is tomorrow at 12:00 AM.", Refs: []string{"e"}}}, Audio: &MobileBriefAudio{Bytes: 1}}
	got := app.mobileBriefCalendarDisplay(t.Context(), brief)
	if got.Narrative[0].Text != "Team offsite is tomorrow." || got.Audio != nil || brief.Narrative[0].Text != "Team offsite is tomorrow at 12:00 AM." {
		t.Fatalf("all-day projection = %+v", got)
	}
}

func TestBriefV110AllDayTimeVariants(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"Offsite is tomorrow at 9:30 AM.", "Offsite is tomorrow."},
		{"Offsite is tomorrow at 14:00.", "Offsite is tomorrow."},
		{"明天上午9点是团建。", "明天是团建。"},
	} {
		if got := briefAllDayTime.ReplaceAllString(tc.input, ""); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBriefWalkthroughV116HolidayCalendarAcrossProviders(t *testing.T) {
	for _, provider := range []string{"subscribed", "google", "icloud"} {
		event := calendar.Event{AllDay: true, Provider: provider, CalendarTitle: "US Holidays"}
		if !mobileBriefUnprovedCalendarNotice(event) {
			t.Errorf("holiday calendar from %s survived", provider)
		}
	}
	ordinary := calendar.Event{AllDay: true, Provider: "subscribed", CalendarTitle: "Team schedule"}
	if mobileBriefUnprovedCalendarNotice(ordinary) {
		t.Fatal("ordinary subscribed calendar was discarded")
	}
	ordinary.CalendarTitle = "Holiday planning"
	if mobileBriefUnprovedCalendarNotice(ordinary) {
		t.Fatal("holiday planning calendar was discarded")
	}
	ordinary.CalendarTitle = "中国节日"
	if !mobileBriefUnprovedCalendarNotice(ordinary) {
		t.Fatal("Chinese holiday calendar survived")
	}
}
