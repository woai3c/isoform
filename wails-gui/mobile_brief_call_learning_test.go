package main

import (
	"slices"
	"testing"
	"time"
)

// What Isoform learned from a call's notes (#67) shows in the brief's
// "Learned" list, as the phone reads it, under the same read gates as
// anything else learned: protecting a session of the call hides it again.
func TestBriefLearnedListShowsWhatACallTaught(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	start := time.Now().Add(-time.Hour)
	session := saveMeetingSlice(t, app, "call-learn-1", start, 10, "")
	group := sessionGroup{Sessions: []ActivitySessionRow{session}, entrySeed: meetingNotesEntrySeed("meeting-F1")}
	triager := NewActivityTriager(app, nil)
	if _, err := triager.saveKnowledge(session, group, &knowledgeGenerationResponse{Title: "Pricing", Description: "Pricing decisions", KnowledgePoints: []string{"The free tier stays."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := triager.saveActivityMemory(session, group, &mcMemoryGenerationResponse{Title: "Pricing call", Dimension: "topic_highlight", MemoryPoints: []string{"I keep the free tier."}}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	in := mobileBriefInput{Slot: mobileBriefSlotEvening, AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
	collectMobileBriefRecall(app, &in)
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	svc := newMobileBriefService(app)
	svc.location, svc.now = time.UTC, func() time.Time { return now }
	app.mobileBriefSvc = svc
	brief := buildMobileBriefV1(in)
	if err := svc.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	learned := func() []string {
		latest, ok := svc.LatestMobileBrief()
		if !ok {
			t.Fatal("no brief")
		}
		var titles []string
		for _, item := range latest.Sections.Learned {
			titles = append(titles, item.Title)
		}
		return titles
	}
	if got := learned(); !slices.Contains(got, "Pricing") || !slices.Contains(got, "Pricing call") {
		t.Fatalf("what the call taught is missing from Learned: %v", got)
	}
	// The real protection path: it publishes the privacy change that seals the brief again.
	if err := app.activity.store.UpdateTriageStatus(session.ID, "protected"); err != nil {
		t.Fatal(err)
	}
	if got := learned(); slices.Contains(got, "Pricing") || slices.Contains(got, "Pricing call") {
		t.Fatalf("a protected call still shows what it taught: %v", got)
	}
}
