package main

import (
	"context"
	"testing"
	"time"
	"wails-gui/storage"
)

func TestTodayNeedResolutionReportsOnlyProvenReply(t *testing.T) {
	app, _, moment := outboundSendTestCard(t)
	rows, err := app.store.ListMailMessages(context.Background(), 0)
	if err != nil || len(rows) == 0 {
		t.Fatal("missing incoming mail")
	}
	before, err := app.GetTodayNeedResolution(moment.ID)
	if err != nil || before.Kind != "" {
		t.Fatalf("unanswered: %+v %v", before, err)
	}
	at := rows[0].At + int64(time.Hour/time.Millisecond)
	sent := storage.MailMessageRow{ID: "sent-by-you", Mailbox: storage.MailboxSent, To: "dana@example.com", Subject: "Re: Project review", ThreadKey: mailThreadKey("Re: Project review"), At: at}
	if _, err := app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{sent}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetTodayNeedResolution(moment.ID)
	// The name is the recipient's full display name as the mail shows it (ea3237c6e).
	if err != nil || result.Kind != "you_replied" || result.Person != "Dana Ruiz" || result.At != at {
		t.Fatalf("replied: %+v %v", result, err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	result, _ = app.GetTodayNeedResolution(moment.ID)
	if result.Kind != "" {
		t.Fatal("paused source was published")
	}
}

func TestTodayNeedResolutionDistinguishesDismissedAndUnreadable(t *testing.T) {
	app, _, moment := outboundSendTestCard(t)
	if _, err := app.ResolveMainChatProactiveAction(moment.ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetTodayNeedResolution(moment.ID)
	if err != nil || result.Kind != "resolved" {
		t.Fatalf("dismissed: %+v %v", result, err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	result, err = app.GetTodayNeedResolution(moment.ID)
	if err == nil || result.Kind == "resolved" {
		t.Fatalf("unreadable: %+v %v", result, err)
	}
}

// A proven reply with no usable name (no contact name, no sender name, only a
// bare address) still resolves the need; the row then says "your reply".
func TestTodayNeedResolutionNamelessReplyStillResolves(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	person, err := app.store.CreatePrincipal(ctx, storage.PrincipalRow{ID: "person-nameless", Kind: storage.PrincipalKindContact, DisplayName: ""})
	if err != nil {
		t.Fatal(err)
	}
	// The sender's name is only their address: the mail source still reads it, but a name is never an address.
	row := storage.MailMessageRow{ID: "mail-nameless", Mailbox: storage.MailboxInbox, SenderEmail: "dana@example.com", SenderName: "dana@example.com", ToMe: true, Subject: "Project review", ThreadKey: "thread-nameless", At: now.Add(-48 * time.Hour).UnixMilli(), PrincipalID: person.ID}
	if _, err := app.store.UpsertMailMessages(ctx, []storage.MailMessageRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	app, _, moment := outboundSendTestCardForMoment(t, app, mailWaitingMoment(mailWaitingOnReply([]storage.MailMessageRow{row}, now)[0], "", now), now)
	before, err := app.GetTodayNeedResolution(moment.ID)
	if err != nil || before.Kind != "" {
		t.Fatalf("unanswered: %+v %v", before, err)
	}
	at := row.At + int64(time.Hour/time.Millisecond)
	sent := storage.MailMessageRow{ID: "sent-by-you", Mailbox: storage.MailboxSent, To: "dana@example.com", Subject: "Re: Project review", ThreadKey: mailThreadKey("Re: Project review"), At: at}
	if _, err := app.store.UpsertMailMessages(ctx, []storage.MailMessageRow{sent}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetTodayNeedResolution(moment.ID)
	if err != nil || result.Kind != "you_replied" || result.Person != "" || result.At != at {
		t.Fatalf("nameless replied: %+v %v", result, err)
	}
}
