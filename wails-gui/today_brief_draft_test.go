package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"wails-gui/storage"
)

func briefDraftTestAnswer(t *testing.T, prepare func(*App, *mobileBriefService, TodayChatContextRef)) (*App, TodayChatContextRef, *fakeProactiveSendTransport, *mobileBriefService) {
	t.Helper()
	rig, svc, brief := cardRig(t)
	a := rig.app
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	view, err := latestBriefAfterRefresh(t, a)
	if err != nil || view.Brief == nil {
		t.Fatalf("brief: %v", err)
	}
	card := cardFor(t, *view.Brief, "Release memo")
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID, GeneratedMessage: card.Message, BriefDraft: true}
	if prepare != nil {
		prepare(a, svc, ref)
	}
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.admitTodayChatTurn(t.Context(), main.LocalID, storage.StreamItemRow{ID: "draft-user", Kind: "user", Content: card.Message}, ref, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BindAnswerChipUserRun(t.Context(), main.LocalID, "draft-user", "draft-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "draft-answer", Kind: "assistant", RunID: "draft-run", Content: "The memo."}); err != nil {
		t.Fatal(err)
	}
	a.savePanelAskAnswer(main.LocalID, "draft-user", "draft-run", "draft-answer", "The memo.")
	transport := &fakeProactiveSendTransport{}
	a.proactiveSendTransport = transport
	return a, ref, transport, svc
}

func TestTodayProvenAddressComesOnlyFromMailHeadersOrALinkedContactsCard(t *testing.T) {
	a := newHandoffTestRig(t).app
	ctx := context.Background()
	if err := a.store.SetUIState(ctx, permissionUIStateKey(mailPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	day := 24 * time.Hour
	priya := peopleTestContact(t, a, "Priya Raman")
	stale := peopleTestContact(t, a, "Old Friend")
	sam := peopleTestContact(t, a, "Sam Okafor")
	headers := []storage.MailMessageRow{
		{ID: "in-older", Mailbox: storage.MailboxInbox, SenderEmail: "priya@old.example", To: "me@example.test", Subject: "Old", ThreadKey: mailThreadKey("Old"), At: now.Add(-20 * day).UnixMilli(), PrincipalID: priya.ID},
		{ID: "sent-newer", Mailbox: storage.MailboxSent, To: "priya@example.com", Subject: "Plan", ThreadKey: mailThreadKey("Plan"), At: now.Add(-10 * day).UnixMilli(), PrincipalID: priya.ID},
		{ID: "in-stale", Mailbox: storage.MailboxInbox, SenderEmail: "friend@example.test", To: "me@example.test", Subject: "Hi", ThreadKey: mailThreadKey("Hi"), At: now.Add(-100 * day).UnixMilli(), PrincipalID: stale.ID},
	}
	if _, err := a.store.UpsertMailMessages(ctx, headers, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if address, source, at, ok := a.todayProvenAddress(ctx, priya.ID, now); !ok || address != "priya@example.com" || source != "mail" || at != headers[1].At {
		t.Fatalf("mail header address = %q %q %d %v", address, source, at, ok)
	}
	if address, _, _, ok := a.todayProvenAddress(ctx, stale.ID, now); ok {
		t.Fatalf("a header older than 90 days proved %q", address)
	}
	im := contactsImporter{store: a.store, consentPath: filepath.Join(t.TempDir(), contactsConsentFileName)}
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "card-lee", FullName: "Lee Park", Emails: []string{"lee@park.example"}}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	lee := principalForHandle(t, a.store, "email", "lee@park.example")
	if lee == nil {
		t.Fatal("contact card was not imported")
	}
	if address, source, _, ok := a.todayProvenAddress(ctx, lee.ID, now); !ok || address != "lee@park.example" || source != "contacts" {
		t.Fatalf("contacts address = %q %q %v", address, source, ok)
	}
	// A handle without a Contacts card or a header proves nothing, and neither
	// does a name the model wrote.
	if _, err := a.store.BindAppHandle(ctx, sam.ID, "email", "sam@example.test", "sam@example.test"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"unlinked handle": sam.ID, "model name": "Priya Raman", "empty": ""} {
		if address, _, _, ok := a.todayProvenAddress(ctx, id, now); ok {
			t.Fatalf("%s proved %q", name, address)
		}
	}
	if err := a.store.SetUIState(ctx, permissionUIStateKey(mailPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := a.todayProvenAddress(ctx, priya.ID, now); ok {
		t.Fatal("an address was proved with Mail turned off")
	}
}

func TestBriefDraftWithoutAProvenAddressIsNotOffered(t *testing.T) {
	// The fixture's card is about work, with no person id: there is no route,
	// so no draft, and Send cannot take an address from the caller.
	a, ref, transport, _ := briefDraftTestAnswer(t, nil)
	if route, err := a.GetTodayBriefDraftRoute(ref); err == nil {
		t.Fatalf("route without a person = %+v", route)
	}
	if draft, err := a.GetTodayBriefDraft(ref); err == nil || draft != nil {
		t.Fatalf("draft without a route = %+v, %v", draft, err)
	}
	if err := a.SendTodayBriefDraft(ref, "draft-answer", "Memo", "Text"); err == nil || transport.count() != 0 {
		t.Fatal("a draft without a proven address sent")
	}
}

func TestBriefDraftSendsOnlyToTheProvenAddress(t *testing.T) {
	var priya PrincipalDTO
	a, ref, transport, _ := briefDraftTestAnswer(t, func(a *App, svc *mobileBriefService, ref TodayChatContextRef) {
		priya = peopleTestContact(t, a, "Priya Raman")
		if _, err := a.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{ID: "sent-plan", Mailbox: storage.MailboxSent, To: "priya@example.com", Subject: "Plan", ThreadKey: mailThreadKey("Plan"), At: time.Now().Add(-time.Hour).UnixMilli(), PrincipalID: priya.ID}}, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		withPersonRef(t, svc, ref, priya.ID)
	})
	route, err := a.GetTodayBriefDraftRoute(ref)
	if err != nil || route.PersonID != priya.ID || route.Name != "Priya Raman" || route.Address != "priya@example.com" || route.Source != "mail" || route.Subject == "" {
		t.Fatalf("route = %+v, %v", route, err)
	}
	before, err := a.GetTodayBriefDraft(ref)
	if err != nil || before == nil || before.State != "ready" || before.Route != route || before.Subject != route.Subject || transport.count() != 0 {
		t.Fatalf("draft=%+v err=%v", before, err)
	}
	if err := a.SendTodayBriefDraft(ref, "draft-answer", "Edited subject", "My revision"); err != nil {
		t.Fatal(err)
	}
	if transport.count() != 1 || transport.calls[0].DraftTo != "priya@example.com" || transport.calls[0].Channel != "mail" || transport.calls[0].Subject != "Edited subject" || transport.calls[0].Text != "My revision" || transport.calls[0].PrincipalID != priya.ID {
		t.Fatalf("send=%+v", transport.calls)
	}
	if err := a.SendTodayBriefDraft(ref, "draft-answer", "Edited subject", "Another revision"); err == nil || transport.count() != 1 {
		t.Fatal("an edited replay sent again")
	}
	after, err := a.GetTodayBriefDraft(ref)
	if err != nil || after == nil || after.State != "sent" || after.Destination != "priya@example.com" || after.Answer.Answer != "My revision" {
		t.Fatalf("receipt=%+v err=%v", after, err)
	}
}

func TestBriefDraftRefusesUnknownOutcomesAndWithdrawnSources(t *testing.T) {
	a, ref, transport, _ := briefDraftTestAnswer(t, func(a *App, svc *mobileBriefService, ref TodayChatContextRef) {
		priya := peopleTestContact(t, a, "Priya Raman")
		if _, err := a.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{ID: "in-plan", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", To: "me@example.test", Subject: "Plan", ThreadKey: mailThreadKey("Plan"), At: time.Now().Add(-time.Hour).UnixMilli(), PrincipalID: priya.ID}}, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		withPersonRef(t, svc, ref, priya.ID)
	})
	transport.err = proactiveSendUnknown(errors.New("outcome unknown"))
	if err := a.SendTodayBriefDraft(ref, "draft-answer", "Memo", "Text"); !errors.Is(err, errMainChatSendUnknown) {
		t.Fatalf("unknown outcome: %v", err)
	}
	if err := a.SendTodayBriefDraft(ref, "draft-answer", "Memo", "Different"); err == nil || transport.count() != 1 {
		t.Fatal("unknown outcome allowed another send")
	}
	if err := a.handoffStore.Delete("memo"); err != nil {
		t.Fatal(err)
	}
	if draft, err := a.GetTodayBriefDraft(ref); err == nil || draft != nil {
		t.Fatalf("withdrawn draft=%+v err=%v", draft, err)
	}
}

// withPersonRef gives the stored card a person id, as a brief written from a
// Who you know record does.
func withPersonRef(t *testing.T, svc *mobileBriefService, ref TodayChatContextRef, personID string) {
	t.Helper()
	for _, brief := range svc.store.briefs() {
		if brief.BriefID != ref.BriefID {
			continue
		}
		brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: "person-1", SourceKind: "principal", SourceID: personID, Label: "Priya Raman"})
		first := ""
		for i := range brief.Cards {
			if brief.Cards[i].ID == ref.SourceID && len(brief.Cards[i].Refs) > 0 {
				first = brief.Cards[i].Refs[0]
				brief.Cards[i].Refs = append(brief.Cards[i].Refs, "person-1")
			}
		}
		// Delivery rebuilds cards from their items, so the item names the person too.
		sections := reflect.ValueOf(&brief.Sections).Elem()
		for i := 0; i < sections.NumField(); i++ {
			items, ok := sections.Field(i).Addr().Interface().(*[]MobileBriefItem)
			if !ok {
				continue
			}
			for j := range *items {
				if len((*items)[j].Refs) > 0 && (*items)[j].Refs[0] == first {
					(*items)[j].Refs = append((*items)[j].Refs, "person-1")
				}
			}
		}
		if err := svc.store.save(brief, svc.now()); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("missing brief")
}
