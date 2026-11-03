package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTodayReplyDraftUsesCurrentCardAndNeverSends(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newTodayContextTestApp(t, true)
	app.eventBroadcaster = NewEventBroadcaster()
	if err := app.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	app, transport, moment := outboundSendTestCardWithApp(t, app)
	now := time.Now()
	in := mobileBriefInput{AsOf: now, Slot: "evening", Location: time.Local}
	collectMobileBriefWaitingReplies(t.Context(), app, &in)
	if len(in.OpenLoops) != 1 {
		t.Fatalf("waiting source: %+v", in.OpenLoops)
	}
	brief := buildMobileBriefV1(in)
	brief.Narrative, brief.NarrativeStatus = mobileBriefNarrativeTemplate(brief, "en"), "unavailable"
	brief = app.mobileBriefWithCards(app.mobileBriefForDelivery(brief))
	var card MobileBriefCard
	for _, candidate := range brief.Cards {
		if candidate.Kind == mobileBriefCardItem {
			card = candidate
			break
		}
	}
	if card.ID == "" {
		t.Fatalf("no item card: %+v", brief.Cards)
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	openedID := "request-" + uuid.NewString()
	id, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, card.Label, openedID)
	if err != nil || id != mobileDraftTaskID(moment.ID) || transport.count() != 0 {
		t.Fatalf("draft=%q sends=%d err=%v", id, transport.count(), err)
	}
	if err := app.CancelTodayReplyDraft(openedID); err == nil {
		t.Fatal("reported stopping an already opened draft")
	}
	if _, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, "another card's words", card.Label, "request-"+uuid.NewString()); err == nil {
		t.Fatal("accepted stale card words")
	}
	if _, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, "A different action", "request-"+uuid.NewString()); err == nil {
		t.Fatal("accepted a changed card label")
	}
	if err := app.store.SetUIState(context.Background(), permissionUIStateKey(mailPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, card.Label, "request-"+uuid.NewString()); err == nil {
		t.Fatal("opened a withdrawn mail source")
	}
	if transport.count() != 0 {
		t.Fatal("opening a draft sent mail")
	}
}

func TestTodayReplyDraftGenerationCanRetryWithoutSending(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newTodayContextTestApp(t, true)
	app.eventBroadcaster = NewEventBroadcaster()
	if err := app.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	moment := proactiveVoiceTestMail(t, app, now)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	app.engagement.now = func() time.Time { return now }
	row, err := app.engagement.Submit(t.Context(), moment)
	if err != nil {
		t.Fatal(err)
	}
	moment = momentFromRow(row)
	target, sourceKind := engagementChatTarget(moment)
	old := mainChatProactive{MomentID: moment.ID, Kind: string(moment.Kind), Headline: "A reply is waiting", Body: "A reply is waiting", State: "pending", Target: target,
		Evidence: []mainChatEvidenceRef{{Kind: sourceKind, ID: moment.SubjectID, Target: target}}, ActionIDs: []string{"open"}, ActionLabels: map[string]string{"open": "Open"}}
	populateMainChatProactiveCopy(&old, nil)
	originalID := app.postProactiveItem(old, "earlier-generic-notification")
	if originalID == "" {
		t.Fatal("could not store the earlier notification")
	}
	runner := &proactiveVoiceTestRunner{err: errors.New("temporary failure")}
	app.runner = proactiveVoiceTestRouter(runner)
	transport := &fakeProactiveSendTransport{}
	app.proactiveSendTransport = transport
	in := mobileBriefInput{AsOf: now, Slot: "evening", Location: time.Local}
	collectMobileBriefWaitingReplies(t.Context(), app, &in)
	if len(in.OpenLoops) != 1 {
		t.Fatalf("waiting source: %+v", in.OpenLoops)
	}
	brief := buildMobileBriefV1(in)
	brief.Narrative, brief.NarrativeStatus = mobileBriefNarrativeTemplate(brief, "en"), "unavailable"
	brief = app.mobileBriefWithCards(app.mobileBriefForDelivery(brief))
	var card MobileBriefCard
	for _, candidate := range brief.Cards {
		if candidate.Kind == mobileBriefCardItem {
			card = candidate
			break
		}
	}
	if card.ID == "" {
		t.Fatalf("no item card: %+v", brief.Cards)
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, card.Label, "request-"+uuid.NewString()); err == nil {
		t.Fatal("failed generation succeeded")
	}
	runner.err = nil
	runner.raw = `{"headline":"Dana is waiting for your reply.","body":"Here is a draft. Nothing goes out without your word.","draft":{"to":"addr_1","subject":"Re: Project review","text":"Thank you for your patience."},"actions":["copy_draft","open","dismiss"],"evidence":["m1","p1"],"abstain":false,"reason":null}`
	stoppedID := "request-" + uuid.NewString()
	runner.onCall = func() { _ = app.CancelTodayReplyDraft(stoppedID) }
	if _, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, card.Label, stoppedID); err == nil {
		t.Fatal("stopped run produced a draft")
	}
	runner.onCall = nil
	id, err := app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, card.Label, "request-"+uuid.NewString())
	if err != nil || id != mobileDraftTaskID(moment.ID) || transport.count() != 0 {
		t.Fatalf("draft=%q sends=%d err=%v calls=%d", id, transport.count(), err, runner.calls)
	}
	if runner.calls != 3 {
		t.Fatalf("generation calls=%d", runner.calls)
	}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := app.store.FindProactiveStreamItem(t.Context(), main.LocalID, moment.ID)
	if err != nil || updated == nil || updated.ID != originalID {
		t.Fatalf("did not update original card: %+v %v", updated, err)
	}
	cardAfter, ok := mobileDraftCardIn(updated, moment.ID)
	if !ok || cardAfter.DraftContent.Text != "Thank you for your patience." {
		t.Fatalf("updated draft: %+v", cardAfter)
	}
}

func TestTodayReplyDraftStopBeforeOpen(t *testing.T) {
	id := "request-" + uuid.NewString()
	if err := (&App{}).CancelTodayReplyDraft(id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := startTodayReplyRun(id); err == nil {
		t.Fatal("stopped request was admitted")
	}
}

func TestTodayReplyDraftDistinguishesAnUnroutedBriefItem(t *testing.T) {
	rig, _, brief := cardRig(t)
	view, err := rig.app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("brief: %v", err)
	}
	card := cardFor(t, *view.Brief, "Release memo")
	_, err = rig.app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, card.Label, "request-"+uuid.NewString())
	if !errors.Is(err, errTodayReplyDraftNoRoute) {
		t.Fatalf("missing route: %v", err)
	}
	_, err = rig.app.OpenTodayReplyDraft(brief.BriefID, card.ID, card.Message, "changed", "request-"+uuid.NewString())
	if !errors.Is(err, errTodayReplyDraftUnavailable) {
		t.Fatalf("stale card must not become a draft: %v", err)
	}
}
