package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"yansu-agent/agent"
)

func TestTodayChatSendNeutralDraftRechecksPurposePrivacyAfterPrepare(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	app.activeTags = map[string]bool{}
	app.eventBroadcaster = NewEventBroadcaster()
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	base := now.Add(-time.Minute).UnixMilli()
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "original", AppNames: `["Notes"]`, StartTime: base, EndTime: base + 1}, nil, nil); err != nil {
		t.Fatal(err)
	}
	evidence := purposeTestEvidence("original", PurposeEvidenceObservedUser, PurposeStanceSupport, base)
	evidence.SourceKind, evidence.SourceID = "activity_session", "original"
	const confidential = "Atlas confidential acquisition title"
	detail := createTodayContextPurpose(t, app, "original", confidential, evidence)
	store, _ := app.purposeProductStore()
	update := purposeTestAppendInput(detail, "public-outcome", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "public-outcome", base+10)
	update.Evidence[0].SourceKind = "explicit_user_statement"
	const public = "The public launch checklist is complete."
	update.DesiredOutcome = public
	if _, _, err := store.AppendEvent(update); err != nil {
		t.Fatal(err)
	}
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot: "morning", WindowStart: now.Add(-time.Hour), AsOf: now,
		OpenLoops: []mobileBriefCandidate{{SourceKind: "purpose", SourceID: detail.OpenLoop.ID, Title: confidential, At: now.Add(-time.Minute)}},
	})
	if len(brief.Sections.OpenLoops) != 1 {
		t.Fatalf("missing Purpose card: %+v", brief.Sections.OpenLoops)
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "brief_item", SourceID: brief.Sections.OpenLoops[0].ID, BriefID: brief.BriefID}
	const neutral = "Help me think through this item."
	const authored = " Please focus on Friday and give me two options."
	prepared, err := app.PrepareTodayChatTurn(main.LocalID, neutral+authored, ref)
	if err != nil || prepared != neutral+authored {
		t.Fatalf("preparation changed the user's added wording: %q, %v", prepared, err)
	}
	// The source is still allowed during Prepare, then becomes sensitive before
	// Send. Only the independently supported outcome may reach the model.
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	app.runner = runner
	app.tagCancels = map[string]context.CancelFunc{}
	app.sessionTempDirs = map[string]string{}
	app.streams = map[string]context.CancelFunc{}
	app.runIDByTag = map[string]string{}
	app.pendingTraceIDs = map[string]string{}
	if _, err := app.SendPromptWithTodayContext(prepared, "", "m", "", main.LocalID, nil, nil, false, false, "en", "", StreamItemData{ID: "today-user", Kind: "user"}, ref); err != nil {
		t.Fatal(err)
	}
	var input *agent.TaskInput
	select {
	case input = <-runner.inputs:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not receive the Today turn")
	}
	prompt, _ := input.PromptFunc()
	if strings.Contains(prompt, confidential) || !strings.Contains(prompt, public) || !strings.HasSuffix(prompt, neutral+authored) {
		t.Fatalf("send reused private source text or changed user wording: %s", prompt)
	}
	waitForTagIdle(t, app, main.LocalID)
	page, err := app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range page.Items {
		if strings.Contains(row.Content, confidential) {
			t.Fatal("confidential card title entered durable chat history")
		}
		if row.ID == "today-user" {
			found = true
			if row.Content != neutral+authored {
				t.Fatalf("persisted user wording changed: %q", row.Content)
			}
		}
	}
	if !found {
		t.Fatal("the prepared user message was lost")
	}
}
