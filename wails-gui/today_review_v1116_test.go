package main

import (
	"testing"
	"time"

	"wails-gui/internal/engagement"
)

// today-review v1.1.16 D14: a brief asked for is named by its time, before
// noon morning and from noon evening; scheduled slots keep their name.
func TestBriefSlotFollowsTheTime(t *testing.T) {
	settings := defaultMobileBriefSettings()
	settings.Morning, settings.Evening = "07:00", "17:44"
	for _, tc := range []struct {
		hour, minute int
		want         string
	}{{2, 4, "morning"}, {11, 59, "morning"}, {12, 0, "evening"}, {17, 24, "evening"}, {23, 0, "evening"}} {
		brief := MobileBriefV1{Slot: mobileBriefSlotOnRequest, DisplaySlot: mobileBriefSlotMorning, AsOf: formatMobileBriefTime(mobileBriefTestDay(tc.hour, tc.minute)), Timezone: "UTC"}
		if got := mobileBriefDisplaySlot(brief, settings); got != tc.want {
			t.Errorf("%d:%02d: %s, want %s", tc.hour, tc.minute, got, tc.want)
		}
	}
	scheduled := MobileBriefV1{Slot: mobileBriefSlotMorning, AsOf: formatMobileBriefTime(mobileBriefTestDay(17, 24)), Timezone: "UTC"}
	if got := mobileBriefDisplaySlot(scheduled, settings); got != mobileBriefSlotMorning {
		t.Errorf("scheduled morning made late = %s", got)
	}
}

// D14: the greeting matches the time, or there is none.
func TestBriefGreetingMatchesTheTime(t *testing.T) {
	at := func(hour, minute int) string { return formatMobileBriefTime(mobileBriefTestDay(hour, minute)) }
	for _, tc := range []struct{ asOf, text, want string }{
		{at(17, 24), "Good morning. Your 1:1 with Priya is at 7:30 PM.", "Your 1:1 with Priya is at 7:30 PM."},
		{at(17, 24), "Good morning, Alex. Your 1:1 with Priya is at 7:30 PM.", "Your 1:1 with Priya is at 7:30 PM."},
		{at(14, 0), "Good morning, here is the rest of your day.", "Here is the rest of your day."},
		{at(20, 0), "早上好，今天你还有三个会。", "今天你还有三个会。"},
		{at(17, 24), "Good evening. Your 1:1 with Priya is at 7:30 PM.", "Good evening. Your 1:1 with Priya is at 7:30 PM."},
		{at(8, 0), "Good morning. Standup is at 9:00 AM.", "Good morning. Standup is at 9:00 AM."},
		{at(17, 24), "Morning standup moved to tomorrow.", "Morning standup moved to tomorrow."},
	} {
		brief := MobileBriefV1{Slot: mobileBriefSlotOnRequest, AsOf: tc.asOf, Timezone: "UTC", Narrative: []MobileBriefPara{{Text: tc.text}, {Text: "That's all."}}}
		got := mobileBriefWithMatchingGreeting(brief)
		if got.Narrative[0].Text != tc.want {
			t.Errorf("%s %q => %q want %q", tc.asOf, tc.text, got.Narrative[0].Text, tc.want)
		}
		if brief.Narrative[0].Text != tc.text {
			t.Fatal("greeting check mutated the stored brief")
		}
		if shown := mobileBriefHygiene(brief); len(shown.Narrative) == 0 || shown.Narrative[0].Text != tc.want {
			t.Errorf("read guard shows %+v, want %q", shown.Narrative, tc.want)
		}
	}
}

// D17: when a brief was made in the last two hours, the next scheduled one
// is skipped.
func TestBriefNextScheduledIsSkippedAfterOneWasMade(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "17:44"}
	now := mobileBriefTestDay(17, 28)
	factsMadeAt := func(made time.Time) mobileBriefScheduleFacts {
		return mobileBriefScheduleFacts{MadeJustBefore: func(at time.Time) bool { return mobileBriefMadeJustBefore(made, at) }}
	}
	facts := factsMadeAt(mobileBriefTestDay(17, 24))
	if at, _ := mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotEvening, facts); !at.Equal(mobileBriefTestDay(17, 44).AddDate(0, 0, 1)) {
		t.Fatalf("evening next = %v, want tomorrow 17:44", at)
	}
	next, found := mobileBriefScheduledNextRun(now, time.UTC, settings, facts)
	if !found || next.Slot != mobileBriefSlotMorning || !next.ScheduledAt.Equal(mobileBriefTestDay(7, 0).AddDate(0, 0, 1)) {
		t.Fatalf("next brief = %+v, want tomorrow's morning at 07:00", next)
	}
	// Made more than two hours before the slot: the slot still runs.
	if at, _ := mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotEvening, factsMadeAt(mobileBriefTestDay(15, 43))); !at.Equal(mobileBriefTestDay(17, 44)) {
		t.Fatalf("evening next = %v, want today 17:44", at)
	}
	// Today's pending slot follows the same rule.
	latest := MobileBriefV1{BriefID: "2026-10-09-request", AsOf: formatMobileBriefTime(mobileBriefTestDay(17, 24)), Timezone: "UTC"}
	if _, pending := mobileBriefPendingOccurrence(mobileBriefTestDay(17, 50), time.UTC, settings, latest, true, nil); pending {
		t.Fatal("the evening slot is still pending twenty minutes after a brief was made")
	}
}

// D16: the first brief's Chat card names its brief, so it is titled with the
// brief's slot and time, not the first-week title.
func TestFirstWeekBriefCardNamesItsBrief(t *testing.T) {
	app := mainChatTestApp(t)
	copy, _ := engagement.Copy(engagement.KindFirstWeek, "en")
	for _, tc := range []struct{ stage, want string }{{"brief", "2026-11-03-request"}, {"welcome", ""}} {
		item, _ := app.proactiveItem(engagement.Moment{
			ID: "first-week-" + tc.stage, Kind: engagement.KindFirstWeek, SubjectID: "2026-11-03-request",
			Params: map[string]string{"stage": tc.stage},
		}, engagementText{PushBody: copy.PushBody, Title: copy.PushBody})
		if item.BriefID != tc.want {
			t.Errorf("stage %s: briefId = %q, want %q", tc.stage, item.BriefID, tc.want)
		}
	}
}
