package main

import (
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestMainChatFollowupDelayUsesMedianSameThreadReplies(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	send := storage.OutboundSendRow{Channel: "mail", PrincipalID: "person-1", DraftTo: "dana@example.test", SourceKey: engagement.DedupeKey(engagement.KindWaitingOnReply, "person-1", "thread-1")}
	rows := []storage.MailMessageRow{}
	for i, delay := range []time.Duration{time.Hour, 3 * time.Hour, 2 * time.Hour} {
		at := base.Add(time.Duration(i) * 5 * time.Hour)
		rows = append(rows,
			storage.MailMessageRow{ID: "out-" + string(rune('a'+i)), Mailbox: storage.MailboxSent, To: "dana@example.test", ThreadKey: "thread-1", At: at.UnixMilli()},
			storage.MailMessageRow{ID: "in-" + string(rune('a'+i)), Mailbox: storage.MailboxInbox, PrincipalID: "person-1", ThreadKey: "thread-1", At: at.Add(delay).UnixMilli()},
		)
	}
	rows = append(rows, storage.MailMessageRow{ID: "other", Mailbox: storage.MailboxSent, To: "dana@example.test", ThreadKey: "other", At: base.Add(time.Minute).UnixMilli()})
	if got := mainChatFollowupDelay(rows, send, base.Add(30*time.Hour)); got != 2*time.Hour {
		t.Fatalf("median=%v", got)
	}
	if got := mainChatFollowupDelay(rows[:4], send, base.Add(30*time.Hour)); got != 0 {
		t.Fatalf("insufficient samples=%v", got)
	}
}

func TestMainChatReplyRetiresFollowup(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	send := storage.OutboundSendRow{MomentID: "moment", Revision: "revision", Channel: "mail", PrincipalID: "person-1", Recipient: "Dana", DraftTo: "dana@example.test", Text: "Yes", SourceKey: engagement.DedupeKey(engagement.KindWaitingOnReply, "person-1", "thread-1")}
	if _, ok, err := app.store.ClaimOutboundSend(t.Context(), send, 100); err != nil || !ok {
		t.Fatalf("claim=%v err=%v", ok, err)
	}
	sentAt := time.Now().UnixMilli()
	if err := app.store.MarkOutboundSendAccepted(t.Context(), send.MomentID, send.Revision, sentAt, sentAt+time.Hour.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	send.SentAt, send.FollowupAt = sentAt, sentAt+time.Hour.Milliseconds()
	if err := app.scheduleMainChatFollowup(t.Context(), send); err != nil {
		t.Fatal(err)
	}
	rows := []storage.MailMessageRow{{ID: "reply", Mailbox: storage.MailboxInbox, PrincipalID: "person-1", ThreadKey: "thread-1", At: sentAt + 1}}
	app.reconcileMainChatFollowups(t.Context(), rows)
	waiting, err := app.store.ListOutboundSendsWaiting(t.Context())
	if err != nil || len(waiting) != 0 {
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
	open, err := app.store.GetOpenEngagementMomentByKey(t.Context(), mainChatFollowupDedupeKey(send))
	if err != nil || open != nil {
		t.Fatalf("followup still open=%+v err=%v", open, err)
	}
}

func TestOutboundWaitingPeopleAndTopicProjection(t *testing.T) {
	app := mainChatTestApp(t)
	person, err := app.store.CreatePrincipal(t.Context(), storage.PrincipalRow{ID: "person-1", Kind: storage.PrincipalKindContact, DisplayName: "Dana Ruiz", CreatedFrom: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	send := storage.OutboundSendRow{MomentID: "moment", Revision: "revision", Channel: "mail", PrincipalID: person.ID, Recipient: person.DisplayName, DraftTo: "dana@example.test", Text: "Yes"}
	if _, ok, err := app.store.ClaimOutboundSend(t.Context(), send, 100); err != nil || !ok {
		t.Fatalf("claim=%v err=%v", ok, err)
	}
	if err := app.store.MarkOutboundSendAccepted(t.Context(), send.MomentID, send.Revision, 200, 300); err != nil {
		t.Fatal(err)
	}
	rows := []PrincipalWithStatsDTO{{Principal: principalToDTO(person)}}
	loops, err := app.peopleLoops(t.Context(), rows, time.UnixMilli(250), newPeopleReadGateCache())
	if err != nil || len(loops[person.ID]) != 1 || loops[person.ID][0].Kind != "waiting_on_them" || loops[person.ID][0].DueAt != 300 {
		t.Fatalf("loops=%+v err=%v", loops, err)
	}
	build := topicProjectBuild{project: topicProject{ID: "project", Name: "Plan", PeopleKeys: []string{"person:dana ruiz"}}}
	view := topicProjectView(build, nil, topicRegistryData{waitingOnThem: map[string]int64{"person:dana ruiz": 200}})
	if view.Status != TopicStatusWaitingOnThem {
		t.Fatalf("topic status=%q", view.Status)
	}
	build.goals = []topicGoalObservation{{ID: "goal", State: PurposeStateWaiting}}
	view = topicProjectView(build, nil, topicRegistryData{waitingOnThem: map[string]int64{"person:dana ruiz": 200}})
	if view.Status != TopicStatusWaitingOnYou {
		t.Fatalf("waiting-on-you priority=%q", view.Status)
	}
}
