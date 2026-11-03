package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestNormalizeEmailAddress(t *testing.T) {
	cases := map[string]string{
		" Owner@Example.com ":         "owner@example.com",
		"owner@example":               "",
		"Owner <owner@example.com>":   "",
		"a@b.test, c@d.test":          "",
		"owner@example.com\r\nBcc: x": "",
		"":                            "",
	}
	for in, want := range cases {
		if got := normalizeEmailAddress(in); got != want {
			t.Fatalf("normalizeEmailAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailChannelVerifyAndTrustOnlyAConfirmedAddress(t *testing.T) {
	relay := &fakeEmailRelay{}
	ch := newEmailChannel(t.TempDir())
	ch.relay = func() emailRelay { return relay }
	ctx := context.Background()

	if _, err := ch.Start(ctx, "not an address"); smsErrorCode(err) != emailErrInvalidAddress {
		t.Fatalf("bad address: %v", err)
	}
	status, err := ch.Start(ctx, "Owner@Example.com")
	if err != nil || status.State != emailStatePending || status.PendingAddress != "owner@example.com" || status.CodeExpiresAt == "" {
		t.Fatalf("start: %+v %v", status, err)
	}
	if ch.Active() {
		t.Fatal("active before the code")
	}
	// A relay that claims the subscription is active is not believed until
	// this Mac confirmed a code.
	relay.state = emailStateActive
	if status, _ := ch.Refresh(ctx); status.State == emailStateActive {
		t.Fatalf("believed an unconfirmed active state: %+v", status)
	}
	relay.state = emailStatePending
	if _, err := ch.Confirm(ctx, "12345"); smsErrorCode(err) != smsErrInvalidCode {
		t.Fatalf("short code: %v", err)
	}
	if _, err := ch.Start(ctx, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	status, err = ch.Confirm(ctx, "123456")
	if err != nil || status.State != emailStateActive || status.Address != "owner@example.com" || !ch.Active() {
		t.Fatalf("confirm: %+v %v", status, err)
	}

	m := engagement.Moment{Kind: engagement.KindPlaybookResult}
	if err := sendEngagementEmail(ctx, ch, engagement.Moment{Kind: engagement.KindWorkFinished}, engagementText{Title: "x"}); err == nil {
		t.Fatal("a kind without email copy must not be mailed")
	}
	if err := sendEngagementEmail(ctx, ch, m, engagementText{EmailSubject: "Isoform: check", EmailBody: "Downloads 1,240"}); err != nil {
		t.Fatal(err)
	}
	if mails := relay.mails(); len(mails) != 1 || mails[0]["subject"] != "Isoform: check" {
		t.Fatalf("mails = %+v", mails)
	}

	status, err = ch.Stop(ctx)
	if err != nil || status.State != emailStateNone || status.Address != "" || ch.Active() {
		t.Fatalf("stop: %+v %v", status, err)
	}
	if err := sendEngagementEmail(ctx, ch, m, engagementText{EmailSubject: "Isoform: check", EmailBody: "x"}); smsErrorCode(err) != "not_active" {
		t.Fatalf("send after stop: %v", err)
	}
	ch.relay = func() emailRelay { return nil }
	if _, err := ch.Start(ctx, "owner@example.com"); smsErrorCode(err) != smsErrOffline {
		t.Fatalf("offline: %v", err)
	}
}

func TestEngagementRequestedChannelsDeliverWhatTheyCan(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	var texted []string
	rig.svc.deliver.Text = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		return errors.New("the relay is offline")
	}
	rig.svc.deliver.Email = func(_ context.Context, _ storage.EngagementMomentRow, _ engagement.Moment, _ engagement.Verdict, text engagementText) error {
		texted = append(texted, text.EmailSubject)
		return nil
	}
	rig.presence = engagement.Presence{SMSActive: true, EmailActive: true}
	if _, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindPlaybookResult, SubjectID: "posthog", DedupeKey: "playbook_result:run-9",
		Params:   map[string]string{"title": "PostHog morning check", "result": "Downloads 1,240", "detail": "Downloads 1,240", "time": "08:56"},
		Channels: []engagement.Channel{engagement.ChannelText, engagement.ChannelEmail, engagement.ChannelPush},
	}); err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(ctx)
	// The phone is not registered and the text failed: the email goes, and
	// the moment stays open for the text and the phone alone.
	rows, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStateScheduled, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Reason != "partly sent" || rows[0].Channel != "email" || len(texted) != 1 || texted[0] != "Isoform: PostHog morning check" {
		t.Fatalf("partial = %+v, mailed = %v", rows, texted)
	}
	if left := momentFromRow(rows[0]); left.Params[engagementChannelsParam] != "" || len(left.Channels) != 2 ||
		left.Channels[0] != engagement.ChannelPush || left.Channels[1] != engagement.ChannelText {
		t.Fatalf("what is left to send: %+v", left)
	}
	// The text works again and the phone is paired: both go, the email is
	// not sent twice.
	var textedAgain int
	rig.svc.deliver.Text = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		textedAgain++
		return nil
	}
	rig.presence = engagement.Presence{SMSActive: true, EmailActive: true, PhoneRegistered: true, PhoneLastSeen: rig.clock}
	rig.clock = rig.clock.Add(engagementRetryDelay + time.Minute)
	rig.svc.Pass(ctx)
	rows, err = rig.store.ListEngagementMoments(ctx, storage.EngagementStateSent, 0)
	if err != nil || len(rows) != 1 || rows[0].Channel != "email+push+text" || textedAgain != 1 || len(texted) != 1 || len(rig.pushed) != 1 {
		t.Fatalf("sent = %+v err=%v texted=%d mailed=%v pushed=%d", rows, err, textedAgain, texted, len(rig.pushed))
	}
	rig.pushed = nil

	// With none of the chosen channels reachable it waits for one, never
	// going to a channel the user did not pick.
	rig.presence = engagement.Presence{PhoneRegistered: true, PhoneLastSeen: rig.clock}
	if _, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindPlaybookResult, SubjectID: "posthog", DedupeKey: "playbook_result:run-10",
		Params:   map[string]string{"title": "PostHog morning check", "result": "Downloads 1,250", "detail": "Downloads 1,250", "time": "08:56"},
		Channels: []engagement.Channel{engagement.ChannelEmail},
	}); err != nil {
		t.Fatal(err)
	}
	rig.clock = rig.clock.Add(time.Minute)
	rig.svc.Pass(ctx)
	if len(rig.pushed) != 0 || len(rig.desktop) != 0 || len(texted) != 1 {
		t.Fatalf("an unreachable choice went elsewhere: pushed=%+v desktop=%+v mailed=%v", rig.pushed, rig.desktop, texted)
	}
	held, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStateScheduled, 0)
	if err != nil || len(held) != 1 || held[0].Reason != "no chosen channel" {
		t.Fatalf("held = %+v %v", held, err)
	}
	// Once the address works again, the held result goes out by email.
	rig.presence.EmailActive = true
	rig.clock = rig.clock.Add(engagementRetryDelay + time.Minute)
	rig.svc.Pass(ctx)
	if len(texted) != 2 || len(rig.pushed) != 0 || len(rig.desktop) != 0 {
		t.Fatalf("after the address came back: mailed=%v pushed=%+v desktop=%+v", texted, rig.pushed, rig.desktop)
	}
}

func TestEngagementNeverResendsToAChannelThatGotTheResult(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	mailed, texted := 0, 0
	rig.svc.deliver.Email = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		mailed++
		return nil
	}
	rig.svc.deliver.Text = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		texted++
		return nil
	}
	rig.presence = engagement.Presence{SMSActive: true, EmailActive: true}
	if _, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindPlaybookResult, SubjectID: "posthog", DedupeKey: "playbook_result:run-11",
		Params:   map[string]string{"title": "PostHog morning check", "result": "Downloads 1,240", "detail": "Downloads 1,240", "time": "08:56"},
		Channels: []engagement.Channel{engagement.ChannelText, engagement.ChannelEmail},
	}); err != nil {
		t.Fatal(err)
	}
	// An earlier pass mailed the result and could not record what was left:
	// the delivered list names email, the stored choice still has both.
	rows, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStatePending, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending = %+v %v", rows, err)
	}
	if err := rig.store.SetEngagementMomentState(ctx, rows[0].ID, storage.EngagementStateScheduled, "partly sent",
		storage.EngagementMomentRow{Channel: "email", HoldUntil: rig.clock.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	rig.clock = rig.clock.Add(time.Minute)
	rig.svc.Pass(ctx)
	sent, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStateSent, 0)
	if err != nil || len(sent) != 1 || sent[0].Channel != "email+text" || mailed != 0 || texted != 1 {
		t.Fatalf("sent = %+v err=%v mailed=%d texted=%d", sent, err, mailed, texted)
	}

	// When every chosen channel already has it, the moment is closed
	// without sending anything.
	if _, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindPlaybookResult, SubjectID: "posthog", DedupeKey: "playbook_result:run-12",
		Params:   map[string]string{"title": "PostHog morning check", "result": "Downloads 1,250", "detail": "Downloads 1,250", "time": "08:56"},
		Channels: []engagement.Channel{engagement.ChannelEmail},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = rig.store.ListEngagementMoments(ctx, storage.EngagementStatePending, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending = %+v %v", rows, err)
	}
	_ = rig.store.SetEngagementMomentState(ctx, rows[0].ID, storage.EngagementStateScheduled, "partly sent",
		storage.EngagementMomentRow{Channel: "email", HoldUntil: rig.clock.UnixMilli()})
	rig.clock = rig.clock.Add(time.Minute)
	rig.svc.Pass(ctx)
	if sent, _ := rig.store.ListEngagementMoments(ctx, storage.EngagementStateSent, 0); len(sent) != 2 || mailed != 0 {
		t.Fatalf("sent = %+v mailed=%d", sent, mailed)
	}
}

func TestEngagementRemembersADeliveryTheStoreDidNotTake(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	mailed, texted := 0, 0
	rig.svc.deliver.Email = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		mailed++
		return nil
	}
	rig.svc.deliver.Text = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		texted++
		return nil
	}
	rig.presence = engagement.Presence{SMSActive: true, EmailActive: true}
	if _, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindPlaybookResult, SubjectID: "posthog", DedupeKey: "playbook_result:run-13",
		Params:   map[string]string{"title": "PostHog morning check", "result": "Downloads 1,240", "detail": "Downloads 1,240", "time": "08:56"},
		Channels: []engagement.Channel{engagement.ChannelText, engagement.ChannelEmail},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStatePending, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending = %+v %v", rows, err)
	}
	// An earlier pass mailed it and the store took neither the delivery nor
	// what was left.
	rig.svc.noteUnrecorded(rows[0].ID, engagementUnrecorded{channels: "email"})
	rig.svc.Pass(ctx)
	sent, err := rig.store.ListEngagementMoments(ctx, storage.EngagementStateSent, 0)
	if err != nil || len(sent) != 1 || sent[0].Channel != "email+text" || mailed != 0 || texted != 1 {
		t.Fatalf("sent = %+v err=%v mailed=%d texted=%d", sent, err, mailed, texted)
	}
	if _, left := rig.svc.unrecorded[rows[0].ID]; left {
		t.Fatal("a recorded delivery is forgotten")
	}
}
