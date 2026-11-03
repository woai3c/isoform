package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

// A template brief that cites a recorded call keeps its other paragraphs on
// Today: only the cards, paragraphs and offers that announce the call leave
// (#62). The model's text may mention the call without citing it, so it still
// leaves whole, and Today shows the template of the cards instead. A source
// withdrawn for privacy still withdraws everything that names it.
func TestTodayBriefTextSurvivesSilencedRecordingAnnouncements(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	if _, err := app.SaveMemoryEntry("launch", "# Launch plan\nPrepare the release."); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	storeEvent(t, app.store, calendar.Event{ID: "calendar-call", Title: "Design review", Start: now, End: now.Add(time.Hour), ConferenceURL: "https://example.com/meeting"})
	brief := buildMobileBriefV1(mobileBriefInput{AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.Local, Slot: mobileBriefSlotMorning,
		OpenLoops: []mobileBriefCandidate{{SourceKind: "memory", SourceID: "launch", Title: "Launch plan", Kind: "goal", Status: "active"}},
		Meetings: []mobileBriefCandidate{
			{SourceKind: mobileBriefMeetingAppSource, SourceID: "recorded-session", Title: "Recorded call", At: now},
			{SourceKind: mobileBriefCalendarSource, SourceID: "calendar-call", Title: "Design review", Detail: "Zoom; notes ready", At: now},
		},
	})
	refOf := func(kind string) string {
		for _, ref := range brief.Evidence {
			if ref.SourceKind == kind {
				return ref.Ref
			}
		}
		t.Fatalf("fixture has no %s evidence", kind)
		return ""
	}
	brief.Narrative = []MobileBriefPara{
		{Text: "Launch plan is open; decide the release steps first.", Refs: []string{refOf("memory")}},
		{Text: "You recorded the call earlier.", Refs: []string{refOf(mobileBriefMeetingAppSource)}},
		{Text: "Design review notes are ready.", Refs: []string{refOf(mobileBriefCalendarSource)}},
	}
	brief.NarrativeStatus = "unavailable"
	brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
	before := brief.Limits.ExcludedSensitiveItems

	got := app.mobileBriefForDelivery(brief)
	if len(got.Narrative) != 1 || got.Narrative[0].Text != brief.Narrative[0].Text {
		t.Fatalf("template withdrawn for a silenced announcement: %+v (%s)", got.Narrative, got.NarrativeStatus)
	}
	if got.Audio != nil {
		t.Fatal("the spoken brief still reads the dropped announcement")
	}
	for _, item := range got.Sections.Meetings {
		if item.Title == "Recorded call" || strings.Contains(item.Detail, "notes ready") {
			t.Fatalf("recording announced itself: %+v", item)
		}
	}
	if got.Limits.ExcludedSensitiveItems != before {
		t.Fatal("a silenced announcement was counted as excluded sensitive activity")
	}
	view := todayBriefView(now, time.Local, got, true, MobileBriefHealth{Enabled: true}, false, nil)
	if view.Brief == nil || view.Empty || len(view.Brief.Narrative) != 1 {
		t.Fatalf("Today has no brief text to render: %+v", view)
	}

	// The model's text leaves whole; Today shows the template of the cards.
	model := brief
	model.NarrativeStatus = "ok"
	withdrawn := app.mobileBriefForDelivery(model)
	if len(withdrawn.Narrative) != 0 || withdrawn.Audio != nil {
		t.Fatalf("model text that cites a recorded call survived: %+v", withdrawn.Narrative)
	}
	shown := app.mobileBriefWithTemplate(withdrawn)
	if len(shown.Narrative) == 0 {
		t.Fatal("Today shows no text for a brief whose model text left")
	}
	for _, para := range shown.Narrative {
		if strings.Contains(para.Text, "Recorded call") || strings.Contains(para.Text, "notes ready") {
			t.Fatalf("the template announces the recorded call: %q", para.Text)
		}
	}

	// A source withdrawn for privacy still withdraws the whole written brief.
	memories, _ := memoryDir()
	if err := os.Remove(filepath.Join(memories, "launch.md")); err != nil {
		t.Fatal(err)
	}
	if withdrawn := app.mobileBriefForDelivery(brief); len(withdrawn.Narrative) != 0 {
		t.Fatalf("withdrawn source kept the written brief: %+v", withdrawn.Narrative)
	}
}

// Opening Today refreshes the stored brief and saves it. Before, a brief that
// cited a recorded call lost its written text on the first open, on disk too,
// so it never came back. Now it keeps that text through the refresh and every
// later read; only what announces the call is left out.
func TestTodayOpenKeepsTheStoredBriefTextWhenItCitesARecordedCall(t *testing.T) {
	rig := newHandoffTestRig(t)
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app, rig.app.mobileBriefSvc = rig.app, svc
	seedMobileBriefReadableHistory(t, rig.app, "h-morning")
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Evidence) == 0 {
		t.Fatal("readable morning fixture lost its evidence")
	}
	// One call has its own card; the other is cited only by the text.
	call := MobileBriefRef{Ref: "e" + strconv.Itoa(len(brief.Evidence)+1), SourceKind: mobileBriefMeetingAppSource, SourceID: "recorded-session", Label: "Recorded call", ObservedAt: brief.AsOf}
	brief.Evidence = append(brief.Evidence, call)
	quiet := MobileBriefRef{Ref: "e" + strconv.Itoa(len(brief.Evidence)+1), SourceKind: mobileBriefMeetingAppSource, SourceID: "second-recorded-session", Label: "Second call", ObservedAt: brief.AsOf}
	brief.Evidence = append(brief.Evidence, quiet)
	brief.Sections.Meetings = append(brief.Sections.Meetings, MobileBriefItem{ID: "recorded-call", Title: "Recorded call", Status: "done", At: brief.AsOf, Kind: "meeting", Refs: []string{call.Ref}})
	recap := MobileBriefPara{Text: "The morning recap.", Refs: []string{brief.Evidence[0].Ref}}
	brief.Narrative = []MobileBriefPara{recap, {Text: "You recorded the call earlier.", Refs: []string{call.Ref, quiet.Ref}}}
	brief.NarrativeStatus = "unavailable" // Isoform's template, as on Oct 19
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	clock.Set(mobileBriefTestDay(11, 0))
	for read := 1; read <= 2; read++ {
		view, err := rig.app.GetLatestBrief()
		if err != nil || view.Brief == nil {
			t.Fatalf("read %d: view = %+v, %v", read, view, err)
		}
		if len(view.Brief.Narrative) != 1 || view.Brief.Narrative[0].Text != recap.Text {
			t.Fatalf("read %d: Today lost the written brief: %+v (%s)", read, view.Brief.Narrative, view.Brief.NarrativeStatus)
		}
		for _, item := range view.Brief.Sections.Meetings {
			if item.Title == "Recorded call" {
				t.Fatalf("read %d: the recorded call announced itself", read)
			}
		}
		for _, ref := range view.Brief.Evidence {
			if ref.SourceKind == mobileBriefMeetingAppSource {
				t.Fatalf("read %d: the recorded call is still cited", read)
			}
		}
		stored, ok := svc.store.load(brief.BriefID)
		if !ok || len(stored.Narrative) != 1 || stored.Narrative[0].Text != recap.Text {
			t.Fatalf("read %d: the stored brief lost its text: %+v", read, stored.Narrative)
		}
	}
}
