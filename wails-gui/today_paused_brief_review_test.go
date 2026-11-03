package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

// A paused brief question may use the delivered brief, but never a source
// withdrawn after that brief was made or unrelated observations.
func TestPausedBriefCardUsesOnlyItsStillReadableBrief(t *testing.T) {
	app, tag, _ := todaySendTestApp(t)
	brief := boundMemoryConversationFixture(t, app)
	bindConversationBriefFixture(t, app, &brief)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: brief.Offers[0].ID, BriefID: brief.BriefID}
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatalf("unpaused control: %v", err)
	}
	if _, err := app.SaveMemoryEntry("unrelated", "# Unrelated private note\nIt must not accompany this brief."); err != nil {
		t.Fatal(err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	preparedContext, err := app.buildTodayChatContext(t.Context(), ref)
	if err != nil {
		t.Fatalf("the delivered brief should remain usable while paused: %v", err)
	}
	if !strings.Contains(preparedContext.Text, "Do not look up their activity, memory or screen") {
		t.Fatal("paused brief guidance allows further observation")
	}
	if strings.Contains(preparedContext.Text, "Unrelated private note") {
		t.Fatalf("unrelated memory entered paused brief context: %s", preparedContext.Text)
	}
	if !strings.Contains(preparedContext.Text, "Launch plan") {
		t.Fatalf("paused answer is missing its brief: %s", preparedContext.Text)
	}
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	app.runner = runner
	app.tagCancels = map[string]context.CancelFunc{}
	app.sessionTempDirs = map[string]string{}
	app.streams = map[string]context.CancelFunc{}
	app.runIDByTag = map[string]string{}
	app.pendingTraceIDs = map[string]string{}
	if _, err := app.SendPromptWithTodayContext("Tell me more about this.", "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "paused-brief-user", Kind: "user"}, ref); err != nil {
		t.Fatalf("send paused brief: %v", err)
	}
	select {
	case input := <-runner.inputs:
		prompt, _ := input.PromptFunc()
		if !strings.Contains(prompt, "Launch plan") || strings.Contains(prompt, "Unrelated private note") || !strings.Contains(prompt, "Do not look up their activity, memory or screen") {
			t.Fatal("paused brief prompt did not preserve the brief boundary")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("paused brief did not reach the model")
	}
	waitForTagIdle(t, app, tag)
	if _, err := app.SaveMemoryEntry("launch", "# A different plan\nPrevious wording has been withdrawn."); err != nil {
		t.Fatal(err)
	}
	preparedContext, err = app.buildTodayChatContext(t.Context(), ref)
	if err == nil && strings.Contains(preparedContext.Text, "Launch plan") {
		t.Fatalf("withdrawn brief text survived the change: %s", preparedContext.Text)
	}
}

func TestTodayShowsOnlyStillProvenBriefCardsWhilePaused(t *testing.T) {
	app, _, _ := todaySendTestApp(t)
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	brief := boundMemoryConversationFixture(t, app)
	bindConversationBriefFixture(t, app, &brief)
	service := newMobileBriefService(app)
	app.mobileBriefSvc = service
	if err := service.store.save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false); app.closedEyes.policyPending.Store(false) })
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil || len(view.Brief.Cards) == 0 || len(view.Brief.Narrative) == 0 {
		t.Fatalf("paused desktop brief lacks its proven cards: %+v %v", view.Brief, err)
	}
	selected, err := app.GetTodayBriefByID(brief.BriefID)
	if err != nil || selected.Brief == nil || len(selected.Brief.Cards) == 0 {
		t.Fatal("selected desktop brief lost its cards while paused")
	}

	if phone, ok := service.GetMobileBrief(brief.BriefID); !ok || len(phone.Cards) != 0 || len(phone.Narrative) != 0 {
		t.Fatal("desktop exception changed normal brief delivery")
	}
	app.closedEyes.policyPending.Store(true)
	if _, err := app.PrepareTodayChatDraft(TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: brief.Offers[0].ID, BriefID: brief.BriefID}); !errors.Is(err, errTodayChatObservationPaused) {
		t.Fatalf("unknown policy prepared a brief card: %v", err)
	}
	pending, err := app.GetLatestBrief()
	if err != nil || pending.Brief != nil && (len(pending.Brief.Cards) > 0 || len(pending.Brief.Narrative) > 0) {
		t.Fatal("unknown startup policy exposed the brief")
	}
	app.closedEyes.policyPending.Store(false)
	if _, err := app.SaveMemoryEntry("launch", "# Updated plan\nOld wording withdrawn."); err != nil {
		t.Fatal(err)
	}
	withdrawn, err := app.GetLatestBrief()
	if err != nil || withdrawn.Brief != nil && (len(withdrawn.Brief.Cards) > 0 || len(withdrawn.Brief.Narrative) > 0) {
		t.Fatal("withdrawn brief survived its source change")
	}
}
