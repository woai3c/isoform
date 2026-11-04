package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// N38: Together holds only moments that involve the person; other work from
// a session they appeared in stays in Activity.
func TestPersonTogetherLeavesOutUnrelatedSameDayWork(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Lina Okafor")
	day := time.Now().Add(-26 * time.Hour).Truncate(time.Minute).UnixMilli()
	session := ActivitySessionRow{ID: "day-session", AppNames: `["WeChat","Safari","Keynote"]`, StartTime: day, EndTime: day + 4*3_600_000, TriageStatus: "done"}
	if err := a.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	moment := func(id, app, title string, at int64) {
		run := testRun("run-"+id, app, at, at+60_000, 60_000, 1)
		run.FirstEventRowID, run.LastEventRowID = at, at
		run.SessionIDs = []string{session.ID}
		insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
		if err := a.activity.store.InsertActivity(ActivityRow{ID: id, Title: title, RunIDs: fmt.Sprintf(`["%s"]`, run.ID), ActiveMsByApp: fmt.Sprintf(`{%q:60000}`, app), StartMs: at, EndMs: at + 60_000}); err != nil {
			t.Fatal(err)
		}
	}
	moment("chat", "WeChat", "Messaged about the launch timing", day+10*60_000)
	moment("deck", "Keynote", "Wrote the launch notes for Lina Okafor", day+30*60_000)
	moment("search", "Safari", "Searched for pet adoption centers", day+60*60_000)
	moment("pitch", "Keynote", "Opened the pitch deck", day+90*60_000)
	moment("review", "Isoform", "Reviewed Today, Chat and Tasks views", day+120*60_000)
	if err := a.store.BumpPrincipalSighting(t.Context(), person.ID, "wechat", session.ID, day+10*60_000+5_000, "message text never returned"); err != nil {
		t.Fatal(err)
	}

	detail, err := a.GetPersonDetail(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, item := range detail.Together {
		if item.Kind == "activity" {
			titles = append(titles, item.Title)
		}
	}
	got := strings.Join(titles, " | ")
	for _, want := range []string{"Messaged about the launch timing", "Wrote the launch notes for Lina Okafor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Together lost %q: %s", want, got)
		}
	}
	for _, unrelated := range []string{"pet adoption", "pitch deck", "Reviewed Today"} {
		if strings.Contains(got, unrelated) {
			t.Fatalf("Together shows unrelated work %q on Lina's card: %s", unrelated, got)
		}
	}
}

func TestPersonTogetherEmptyWithoutInvolvement(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Theo Marsh")
	day := time.Now().Add(-26 * time.Hour).Truncate(time.Minute).UnixMilli()
	session := ActivitySessionRow{ID: "theo-day", AppNames: `["Safari"]`, StartTime: day, EndTime: day + 3_600_000, TriageStatus: "done"}
	if err := a.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-theo", "Safari", day+600_000, day+660_000, 60_000, 1)
	run.FirstEventRowID, run.LastEventRowID = day+600_000, day+600_000
	run.SessionIDs = []string{session.ID}
	insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
	if err := a.activity.store.InsertActivity(ActivityRow{ID: "theo-search", Title: "Searched for voice-agent platforms", RunIDs: `["run-theo"]`, ActiveMsByApp: `{"Safari":60000}`, StartMs: day + 600_000, EndMs: day + 660_000}); err != nil {
		t.Fatal(err)
	}
	// Seen in this session, but not while the search ran.
	if err := a.store.BumpPrincipalSighting(t.Context(), person.ID, "wechat", session.ID, day+50*60_000, "x"); err != nil {
		t.Fatal(err)
	}
	detail, err := a.GetPersonDetail(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range detail.Together {
		if item.Kind == "activity" {
			t.Fatalf("unrelated activity on the card: %+v", item)
		}
	}
}
