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
	if err != nil || result.Kind != "you_replied" || result.Person != "Dana" || result.At != at {
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
