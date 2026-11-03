package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/proactivevoice"
	"wails-gui/storage"
)

type fakeProactiveSendTransport struct {
	mu    sync.Mutex
	calls []storage.OutboundSendRow
	err   error
}

func (f *fakeProactiveSendTransport) Send(ctx context.Context, send storage.OutboundSendRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, send)
	return f.err
}

func (f *fakeProactiveSendTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func outboundSendTestCard(t *testing.T) (*App, *fakeProactiveSendTransport, engagement.Moment) {
	t.Helper()
	return outboundSendTestCardWithApp(t, mainChatTestApp(t))
}

func outboundSendTestCardWithApp(t *testing.T, app *App) (*App, *fakeProactiveSendTransport, engagement.Moment) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	moment := proactiveVoiceTestMail(t, app, now)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	app.engagement.now = func() time.Time { return now }
	stored, err := app.engagement.Submit(t.Context(), moment)
	if err != nil {
		t.Fatal(err)
	}
	moment = momentFromRow(stored)
	source, reason := app.loadProactiveVoiceSource(t.Context(), moment, now, "en")
	job, addresses, assembly := assembleProactiveVoiceJob(string(moment.Kind), "en", true, now, source)
	if reason != "" || assembly != "" {
		t.Fatalf("source=%s assembly=%s", reason, assembly)
	}
	card := mainChatProactive{
		MomentID: moment.ID, Kind: string(moment.Kind), Headline: "Draft ready", Body: "Draft ready",
		State: "pending", Target: mainChatTarget{Kind: "mail"}, ActionLabels: map[string]string{},
	}
	result := proactivevoice.Output{
		Headline: "Draft ready", Body: "Draft ready",
		Draft:   &proactivevoice.Draft{To: job.DraftTo, Subject: job.DraftSubject, Text: "Yes, that works."},
		Actions: proactivevoice.Actions(string(moment.Kind), true),
	}
	if !applyProactiveVoiceResult(&card, job, result, addresses) {
		t.Fatal("draft rejected")
	}
	populateMainChatSendDraft(&card, moment, source)
	populateMainChatProactiveCopy(&card, nil)
	if !mainChatDraftSendable(card.DraftContent) || !app.saveProactiveVoiceReceipt(t.Context(), moment, now, job, addresses, source.Provenance, &card) {
		t.Fatal("sendable receipt was not created")
	}
	if app.postProactiveItem(card, "outbound-send-test") == "" {
		t.Fatal("card was not stored")
	}
	fake := &fakeProactiveSendTransport{}
	app.proactiveSendTransport = fake
	return app, fake, moment
}

func outboundStoredCard(t *testing.T, app *App, momentID string) mainChatProactive {
	t.Helper()
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, err := app.store.FindProactiveStreamItem(t.Context(), main.LocalID, momentID)
	if err != nil || row == nil {
		t.Fatalf("stored card: %+v %v", row, err)
	}
	var card mainChatProactive
	if err := json.Unmarshal([]byte(row.ToolInput), &card); err != nil {
		t.Fatal(err)
	}
	return card
}

func TestMainChatSendFinalizesOnceAndRecoversAccepted(t *testing.T) {
	app, fake, moment := outboundSendTestCard(t)
	state, err := app.ResolveMainChatProactiveAction(moment.ID, "send")
	if err != nil || state != "acted" || fake.count() != 1 {
		t.Fatalf("send state=%q calls=%d err=%v", state, fake.count(), err)
	}
	card := outboundStoredCard(t, app, moment.ID)
	if card.State != "acted" {
		t.Fatalf("card state=%q", card.State)
	}
	ledger, err := app.store.LoadOutboundSend(t.Context(), moment.ID, card.DraftContent.Revision)
	if err != nil || ledger == nil || ledger.State != storage.OutboundSendFinalized || ledger.SentAt <= 0 || ledger.FollowupAt <= ledger.SentAt {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
	reports := mainChatStructuredRows(t, app, mainChatReportTool)
	if len(reports) != 1 || reports[0].ID != mainChatSendReceiptID(moment.ID, card.DraftContent.Revision) {
		t.Fatalf("reports=%+v", reports)
	}
	var report mainChatReport
	if err := json.Unmarshal([]byte(reports[0].ToolInput), &report); err != nil {
		t.Fatal(err)
	}
	if report.Source != "message_send" || report.Channel != "mail" || report.SentAt != ledger.SentAt || report.PrincipalID != moment.SubjectID || report.Target.Kind != mainChatTargetNone {
		t.Fatalf("report=%+v", report)
	}
	if len(report.Evidence) != 1 || report.Evidence[0].Kind != "principal" || report.Evidence[0].ID != moment.SubjectID {
		t.Fatalf("receipt topic evidence=%+v", report.Evidence)
	}
	if state, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err != nil || state != "acted" || fake.count() != 1 || len(mainChatStructuredRows(t, app, mainChatReportTool)) != 1 {
		t.Fatalf("duplicate state=%q calls=%d err=%v", state, fake.count(), err)
	}
	original, err := app.store.GetEngagementMoment(t.Context(), moment.ID)
	if err != nil || original.State != storage.EngagementStateSuppressed {
		t.Fatalf("original=%+v err=%v", original, err)
	}

	otherApp, otherFake, otherMoment := outboundSendTestCard(t)
	otherCard := outboundStoredCard(t, otherApp, otherMoment.ID)
	sourceKey := otherMoment.DedupeKey
	attempt := outboundRowForMainChatDraft(&otherCard, sourceKey)
	if _, claimed, err := otherApp.store.ClaimOutboundSend(t.Context(), attempt, time.Now().UnixMilli()); err != nil || !claimed {
		t.Fatalf("recovery claim=%v err=%v", claimed, err)
	}
	sentAt := time.Now().UnixMilli()
	if err := otherApp.store.MarkOutboundSendAccepted(t.Context(), otherMoment.ID, otherCard.DraftContent.Revision, sentAt, sentAt+time.Hour.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	if state, err := otherApp.ResolveMainChatProactiveAction(otherMoment.ID, "send"); err != nil || state != "acted" || otherFake.count() != 0 {
		t.Fatalf("accepted recovery state=%q calls=%d err=%v", state, otherFake.count(), err)
	}
}

func TestMainChatSendFailureAndUnknownNeverBlindResend(t *testing.T) {
	app, fake, moment := outboundSendTestCard(t)
	fake.err = errors.New("definitive refusal")
	if _, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err == nil || fake.count() != 1 {
		t.Fatalf("failure calls=%d err=%v", fake.count(), err)
	}
	card := outboundStoredCard(t, app, moment.ID)
	if card.State != "pending" || len(mainChatStructuredRows(t, app, mainChatReportTool)) != 0 {
		t.Fatalf("failed card=%+v", card)
	}
	ledger, _ := app.store.LoadOutboundSend(t.Context(), moment.ID, card.DraftContent.Revision)
	if ledger.State != storage.OutboundSendFailed {
		t.Fatalf("failed ledger=%+v", ledger)
	}
	fake.err = nil
	if state, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err != nil || state != "acted" || fake.count() != 2 {
		t.Fatalf("retry state=%q calls=%d err=%v", state, fake.count(), err)
	}

	unknownApp, unknownFake, unknownMoment := outboundSendTestCard(t)
	unknownFake.err = proactiveSendUnknown(errors.New("connection dropped"))
	for range 2 {
		if _, err := unknownApp.ResolveMainChatProactiveAction(unknownMoment.ID, "send"); !errors.Is(err, errMainChatSendUnknown) {
			t.Fatalf("unknown err=%v", err)
		}
	}
	if unknownFake.count() != 1 {
		t.Fatalf("unknown transport calls=%d", unknownFake.count())
	}
	unknownCard := outboundStoredCard(t, unknownApp, unknownMoment.ID)
	unknownLedger, _ := unknownApp.store.LoadOutboundSend(t.Context(), unknownMoment.ID, unknownCard.DraftContent.Revision)
	if unknownCard.State != "pending" || unknownLedger.State != storage.OutboundSendUnknown {
		t.Fatalf("unknown card=%+v ledger=%+v", unknownCard, unknownLedger)
	}
}

func TestMainChatSendRejectsLegacyAndConcurrentDuplicate(t *testing.T) {
	app := mainChatTestApp(t)
	legacy := mainChatProactive{MomentID: "legacy-send", Kind: string(engagement.KindWaitingOnReply), Headline: "Reply", Body: "Reply", Draft: true,
		DraftContent: &mainChatProactiveDraft{To: "dana@example.test", Subject: "Re: Plan", Text: "Yes"}, State: "pending",
		ActionIDs: []string{"copy_draft", "open", "dismiss"}, ActionLabels: map[string]string{}, Target: mainChatTarget{Kind: "mail"}}
	if app.postProactiveItem(legacy, "legacy-send") == "" {
		t.Fatal("legacy card was not stored")
	}
	if _, err := app.ResolveMainChatProactiveAction(legacy.MomentID, "send"); err == nil {
		t.Fatal("legacy draft gained send authority")
	}

	concurrentApp, fake, moment := outboundSendTestCard(t)
	const attempts = 4
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := concurrentApp.ResolveMainChatProactiveAction(moment.ID, "send")
			if err == nil && state != "acted" {
				err = errors.New("unexpected state " + state)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if fake.count() != 1 || len(mainChatStructuredRows(t, concurrentApp, mainChatReportTool)) != 1 {
		t.Fatalf("concurrent calls=%d reports=%d", fake.count(), len(mainChatStructuredRows(t, concurrentApp, mainChatReportTool)))
	}
}

func quickMainChatSendReceiptRetries(t *testing.T) {
	t.Helper()
	previous := mainChatSendReceiptRetryDelays
	mainChatSendReceiptRetryDelays = []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 200 * time.Millisecond, time.Second}
	t.Cleanup(func() { mainChatSendReceiptRetryDelays = previous })
}

// fixMainChatSendEngagement gives the app a working engagement service again,
// under the lock the background receipt save holds.
func fixMainChatSendEngagement(app *App) {
	mainChatProactiveActionMu.Lock()
	defer mainChatProactiveActionMu.Unlock()
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
}

func waitMainChatSendFinalized(t *testing.T, app *App, momentID, revision string) *storage.OutboundSendRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ledger, err := app.store.LoadOutboundSend(t.Context(), momentID, revision)
		if err == nil && ledger != nil && ledger.State == storage.OutboundSendFinalized {
			return ledger
		}
		if time.Now().After(deadline) {
			t.Fatalf("receipt never saved: ledger=%+v err=%v", ledger, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// K2: Mail took it, the receipt isn't saved yet. The send reads as sent, with
// no error to retry, and the receipt saves quietly afterwards.
func TestMainChatAcceptedSendReadsSentWhileReceiptSavesInBackground(t *testing.T) {
	quickMainChatSendReceiptRetries(t)
	app, fake, moment := outboundSendTestCard(t)
	card := outboundStoredCard(t, app, moment.ID)
	attempt := outboundRowForMainChatDraft(&card, moment.DedupeKey)
	if _, claimed, err := app.store.ClaimOutboundSend(t.Context(), attempt, time.Now().UnixMilli()); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	sentAt := time.Now().UnixMilli()
	if err := app.store.MarkOutboundSendAccepted(t.Context(), moment.ID, card.DraftContent.Revision, sentAt, sentAt+time.Hour.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	app.engagement = &engagementService{}
	state, err := app.ResolveMainChatProactiveAction(moment.ID, "send")
	if err != nil || state != storage.EngagementReceiptActed {
		t.Fatalf("accepted send with a failed receipt: state=%q err=%v", state, err)
	}
	if fake.count() != 0 {
		t.Fatalf("accepted send went to Mail again: calls=%d", fake.count())
	}
	fixMainChatSendEngagement(app)
	waitMainChatSendFinalized(t, app, moment.ID, card.DraftContent.Revision)
	if stored := outboundStoredCard(t, app, moment.ID); stored.State != storage.EngagementReceiptActed || stored.SentAt != sentAt {
		t.Fatalf("card after the background receipt: state=%q sentAt=%d want %d", stored.State, stored.SentAt, sentAt)
	}
	if fake.count() != 0 || len(mainChatStructuredRows(t, app, mainChatReportTool)) != 1 {
		t.Fatalf("calls=%d reports=%d", fake.count(), len(mainChatStructuredRows(t, app, mainChatReportTool)))
	}
}

// K2: a fresh send whose receipt fails to save is still a send: Mail got it
// once, the caller hears "acted", and a second Send never mails again.
func TestMainChatSendReceiptFailureNeverReadsAsFailedSend(t *testing.T) {
	quickMainChatSendReceiptRetries(t)
	app, fake, moment := outboundSendTestCard(t)
	card := outboundStoredCard(t, app, moment.ID)
	app.engagement = &engagementService{}
	state, err := app.ResolveMainChatProactiveAction(moment.ID, "send")
	if err != nil || state != storage.EngagementReceiptActed || fake.count() != 1 {
		t.Fatalf("send with a failed receipt: state=%q calls=%d err=%v", state, fake.count(), err)
	}
	if errors.Is(err, errMainChatSendFinalize) {
		t.Fatal("a failed receipt read as a failed send")
	}
	fixMainChatSendEngagement(app)
	ledger := waitMainChatSendFinalized(t, app, moment.ID, card.DraftContent.Revision)
	if ledger.SentAt <= 0 || ledger.FollowupAt <= ledger.SentAt {
		t.Fatalf("ledger=%+v", ledger)
	}
	if stored := outboundStoredCard(t, app, moment.ID); stored.State != storage.EngagementReceiptActed || stored.SentAt != ledger.SentAt {
		t.Fatalf("card=%+v", stored)
	}
	if state, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err != nil || state != storage.EngagementReceiptActed || fake.count() != 1 {
		t.Fatalf("second send state=%q calls=%d err=%v", state, fake.count(), err)
	}
}

// K2: Edit on the draft card sends the person's words through the binding.
func TestSendMainChatProactiveDraftSendsTheEdit(t *testing.T) {
	app, fake, moment := outboundSendTestCard(t)
	state, err := app.SendMainChatProactiveDraft(moment.ID, "Edited: Thursday at 3 works.")
	if err != nil || state != storage.EngagementReceiptActed || fake.count() != 1 {
		t.Fatalf("edited send state=%q calls=%d err=%v", state, fake.count(), err)
	}
	if fake.calls[0].Text != "Edited: Thursday at 3 works." {
		t.Fatalf("sent text=%q", fake.calls[0].Text)
	}
	stored := outboundStoredCard(t, app, moment.ID)
	if stored.SentAt <= 0 || stored.DraftContent.Text != "Edited: Thursday at 3 works." {
		t.Fatalf("card=%+v", stored)
	}
}

// The sent time is not source copy: a sent card stays readable.
func TestMainChatSentAtLeavesThePresentationHash(t *testing.T) {
	app, _, moment := outboundSendTestCard(t)
	card := outboundStoredCard(t, app, moment.ID)
	before := proactiveVoicePresentationHash(card)
	card.SentAt = time.Now().UnixMilli()
	if proactiveVoicePresentationHash(card) != before {
		t.Fatal("sentAt changed the presentation hash")
	}
}

func TestMainChatSendDoesNotStartAfterDismiss(t *testing.T) {
	app, fake, moment := outboundSendTestCard(t)
	fake.err = errors.New("definitive refusal")
	if _, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err == nil {
		t.Fatal("initial send unexpectedly succeeded")
	}
	if state, err := app.ResolveMainChatProactiveAction(moment.ID, "dismiss"); err != nil || state != "dismissed" {
		t.Fatalf("dismiss state=%q err=%v", state, err)
	}
	fake.err = nil
	if _, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err == nil {
		t.Fatal("dismissed card started a new send")
	}
	if fake.count() != 1 || len(mainChatStructuredRows(t, app, mainChatReportTool)) != 0 {
		t.Fatalf("dismissed card transport calls=%d", fake.count())
	}
}
