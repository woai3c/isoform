package main

import (
	"context"
	"testing"
	"wails-gui/storage"
)

func TestContactSuggestionsAndSearch(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "other", FullName: "A Other"},
		{ID: "name", FullName: "Maya Chen"},
		{ID: "email", FullName: "C Email", Emails: []string{"person@example.com"}},
		{ID: "phone", FullName: "Z Phone", Phones: []string{"+1 (415) 555-0123"}},
	}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	seen, err := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "陈玛雅"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []struct{ app, handle string }{{"wechat", "+14155550123"}, {"slack", "person@example.com"}} {
		if _, err := im.store.BindAppHandle(ctx, seen.ID, h.app, h.handle, h.handle); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := im.searchImportedContacts(ctx, seen.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"phone", "email", "name", "other"}
	if len(rows) != len(want) {
		t.Fatalf("suggestions=%+v", rows)
	}
	for i, id := range want {
		if rows[i].ContactID != id {
			t.Fatalf("suggestion %d=%+v, want %s", i, rows[i], id)
		}
	}
	for query, id := range map[string]string{"4155550123": "phone", "PERSON@EXAMPLE.COM": "email", "maya": "name"} {
		rows, err := im.searchImportedContacts(ctx, seen.ID, query)
		if err != nil || len(rows) != 1 || rows[0].ContactID != id {
			t.Fatalf("query %q=%+v %v", query, rows, err)
		}
	}
}

func TestContactLinkUnlinkPreservesPersonAndProvenance(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "synthetic-native-7F3A9C", FullName: "Maya Chen", Phones: []string{"+14155550123"}, Photo: "data:image/jpeg;base64,aGVsbG8="}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	imported := principalForHandle(t, im.store, "contacts", "synthetic-native-7F3A9C")
	seen, err := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "陈玛雅", CreatedFrom: "auto_from_chat", Aliases: []string{"original alias"}, Notes: "kept"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := im.store.BindAppHandle(ctx, seen.ID, "wechat", "maya-wechat", "MayaC"); err != nil {
		t.Fatal(err)
	}
	if err := im.store.BumpPrincipalSighting(ctx, seen.ID, "wechat", "", 123, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := im.linkContact(ctx, seen.ID, "synthetic-native-7F3A9C"); err != nil {
		t.Fatal(err)
	}
	owner := principalForHandle(t, im.store, "imessage", "+14155550123")
	if owner == nil || owner.ID != seen.ID || owner.Source != "" || owner.CreatedFrom != "auto_from_chat" || owner.Kind != storage.PrincipalKindContact {
		t.Fatalf("owner=%+v", owner)
	}
	if _, err := im.store.GetPrincipal(ctx, imported.ID); err == nil {
		t.Fatal("source was not merged")
	}
	dto := principalToDTO(*owner)
	handles, _ := im.store.ListAppHandlesByPrincipal(ctx, owner.ID)
	dto.attachHandles(handles)
	if err := decorateContactDTOs(ctx, im.store, []*PrincipalDTO{&dto}); err != nil {
		t.Fatal(err)
	}
	if !dto.ContactLinked || dto.ContactName != "Maya Chen" || dto.ContactPhoto == "" {
		t.Fatalf("contact presentation=%+v", dto)
	}
	if err := im.linkContact(ctx, seen.ID, "synthetic-native-7F3A9C"); err == nil {
		t.Fatal("double link allowed")
	}
	// Changed phone and photo are synced onto the linked person and undo too.
	src.list[0].Phones = []string{"+14155550124"}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	updated, _ := im.store.GetPrincipal(ctx, seen.ID)
	updated.Notes = "edited after linking"
	if _, err := im.store.UpdatePrincipal(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if err := im.store.UnlinkContactPrincipal(ctx, seen.ID); err != nil {
		t.Fatal(err)
	}
	restored, _ := im.store.GetPrincipal(ctx, seen.ID)
	if restored.DisplayName != "陈玛雅" || restored.Kind != storage.PrincipalKindAnonymous || restored.Notes != "edited after linking" || len(restored.Aliases) != 1 {
		t.Fatalf("restored=%+v", restored)
	}
	if owner := principalForHandle(t, im.store, "imessage", "+14155550124"); owner == nil || owner.ID != imported.ID {
		t.Fatal("updated contact handles not restored")
	}
	if owner := principalForHandle(t, im.store, "wechat", "maya-wechat"); owner == nil || owner.ID != seen.ID {
		t.Fatal("seen handle moved")
	}
	stats, _ := im.store.GetPrincipalStats(ctx, seen.ID)
	if stats.TotalMessages != 1 {
		t.Fatal("person history lost")
	}
	dto = principalToDTO(restored)
	handles, _ = im.store.ListAppHandlesByPrincipal(ctx, seen.ID)
	dto.attachHandles(handles)
	if err := decorateContactDTOs(ctx, im.store, []*PrincipalDTO{&dto}); err != nil {
		t.Fatal(err)
	}
	if dto.ContactLinked || dto.ContactName != "" || dto.ContactPhoto != "" {
		t.Fatal("unlink retained presentation")
	}
}

func TestRemoveImportedContactsUnlinksAndRemovesOnlySourceHandles(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana", Phones: []string{"+14155550123"}}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	person, _ := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Existing"})
	if _, err := im.store.BindAppHandle(ctx, person.ID, "wechat", "wx", "Existing"); err != nil {
		t.Fatal(err)
	}
	if err := im.linkContact(ctx, person.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := removeImportedContacts(ctx, im.store); err != nil {
		t.Fatal(err)
	}
	if principalForHandle(t, im.store, "imessage", "+14155550123") != nil {
		t.Fatal("imported phone retained")
	}
	if principalForHandle(t, im.store, "wechat", "wx") == nil {
		t.Fatal("curated handle removed")
	}
	if _, err := im.store.GetPrincipal(ctx, person.ID); err != nil {
		t.Fatal("existing person removed")
	}
	cards, _ := im.store.ListImportedContactCards(ctx)
	if len(cards) != 0 {
		t.Fatal("cache retained")
	}
}

func TestContactSharedHandleSurvivesCardChangesAndUnlinks(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "a", FullName: "Ana", Phones: []string{"+14155550123"}},
		{ID: "b", FullName: "Bo", Phones: []string{"+14155550123"}},
	}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	imported := principalForHandle(t, im.store, "contacts", "a")
	seen, _ := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Seen"})
	if err := im.linkContact(ctx, seen.ID, "a"); err != nil {
		t.Fatal(err)
	}
	src.list[0].Phones = nil
	src.list[1].Phones = append(src.list[1].Phones, "+14155550124")
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	if principalForHandle(t, im.store, "imessage", "+14155550123") == nil {
		t.Fatal("shared phone withdrawn while another card still uses it")
	}
	if err := im.store.UnlinkContactPrincipal(ctx, seen.ID); err != nil {
		t.Fatal(err)
	}
	for _, phone := range src.list[1].Phones {
		if owner := principalForHandle(t, im.store, "imessage", phone); owner == nil || owner.ID != imported.ID {
			t.Fatalf("shared card phone %s not restored: %+v", phone, owner)
		}
	}
}

func TestContactCanExplicitlyLinkSelfAndRefresh(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "me-card", FullName: "My name", Phones: []string{"+14155550101"}}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	self, err := im.store.EnsureSelf(ctx, "Me", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := im.linkContact(ctx, self.ID, "me-card"); err != nil {
		t.Fatal(err)
	}
	src.list[0].Phones = []string{"+14155550102"}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	if owner := principalForHandle(t, im.store, "imessage", "+14155550102"); owner == nil || owner.ID != self.ID {
		t.Fatal("explicit self link did not refresh")
	}
	if err := im.store.UnlinkContactPrincipal(ctx, self.ID); err != nil {
		t.Fatal(err)
	}
	restored, _ := im.store.GetPrincipal(ctx, self.ID)
	if restored.Kind != storage.PrincipalKindSelf || restored.DisplayName != "Me" {
		t.Fatal("self identity changed")
	}
}

func TestContactUnlinkRestoresOriginalActivityAndPreservesLaterObservations(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana"}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	original := principalForHandle(t, im.store, "contacts", "a")
	seen, _ := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "Seen"})
	// Include a preview collision and an overlapping activity session.
	for _, row := range []struct {
		id, app, session, preview string
		at                        int64
	}{
		{original.ID, "imessage", "shared", "contact original", 100},
		{original.ID, "imessage", "contact-session", "contact second", 200},
		{seen.ID, "wechat", "shared", "seen original", 200},
	} {
		if err := im.store.BumpPrincipalSighting(ctx, row.id, row.app, row.session, row.at, row.preview); err != nil {
			t.Fatal(err)
		}
	}
	if err := im.linkContact(ctx, seen.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if err := im.store.BumpPrincipalSighting(ctx, seen.ID, "wechat", "new-session", 300, "after linking"); err != nil {
		t.Fatal(err)
	}
	additional, _ := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "Extra", Aliases: []string{"Ana"}})
	if err := im.store.BumpPrincipalSighting(ctx, additional.ID, "slack", "extra-session", 400, "later merge"); err != nil {
		t.Fatal(err)
	}
	if _, err := im.store.MergePrincipals(ctx, additional.ID, seen.ID); err != nil {
		t.Fatal(err)
	}
	if err := im.store.UnlinkContactPrincipal(ctx, seen.ID); err != nil {
		t.Fatal(err)
	}
	profile, err := im.store.GetPrincipal(ctx, seen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Aliases) != 1 || profile.Aliases[0] != "Ana" {
		t.Fatal("later merge's shared alias was removed")
	}
	oldStats, err := im.store.GetPrincipalStats(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	newStats, err := im.store.GetPrincipalStats(ctx, seen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldStats.TotalMessages != 2 || newStats.TotalMessages != 3 {
		t.Fatalf("activity counts contact=%d person=%d", oldStats.TotalMessages, newStats.TotalMessages)
	}
	if len(oldStats.RecentMessages) != 2 || len(newStats.RecentMessages) != 3 {
		t.Fatalf("previews contact=%+v person=%+v", oldStats.RecentMessages, newStats.RecentMessages)
	}
	oldSessions, _ := im.store.ListPrincipalSessionSightings(ctx, original.ID, 20)
	newSessions, _ := im.store.ListPrincipalSessionSightings(ctx, seen.ID, 20)
	if len(oldSessions) != 2 || len(newSessions) != 3 {
		t.Fatalf("sessions contact=%+v person=%+v", oldSessions, newSessions)
	}
	for _, s := range newSessions {
		if s.SessionID == "shared" && s.FirstSeenAt != 200 {
			t.Fatal("contact's earlier session contribution remained on seen person")
		}
	}
}

func TestContactLinkCanMergeCardAlreadyDeduplicatedIntoCuratedPerson(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	curated, err := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Curated name", Notes: "Original curated notes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := im.store.BindAppHandle(ctx, curated.ID, "email", "ana@example.com", "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana", Emails: []string{"ana@example.com"}}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	seen, _ := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "Seen in WeChat"})
	options, err := im.searchImportedContacts(ctx, seen.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 1 || !options[0].Available {
		t.Fatal("curated contact is not linkable")
	}
	if err := im.linkContact(ctx, seen.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if owner := principalForHandle(t, im.store, "email", "ana@example.com"); owner == nil || owner.ID != seen.ID {
		t.Fatal("handles were not merged into existing seen person")
	}
	if err := im.store.UnlinkContactPrincipal(ctx, seen.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := im.store.GetPrincipal(ctx, curated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DisplayName != "Curated name" || restored.Notes != "Original curated notes" || restored.Source != "" {
		t.Fatal("curated source profile was not restored")
	}
}

func TestContactUnlinkPreservesDossiersAndVoiceSamplesWithoutStartingResearch(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana"}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	original := principalForHandle(t, im.store, "contacts", "a")
	seen, _ := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Seen"})
	if err := im.store.SavePrincipalResearch(ctx, storage.PrincipalResearchRow{PrincipalID: original.ID, Summary: "Existing dossier", Markdown: "Existing source notes"}); err != nil {
		t.Fatal(err)
	}
	if err := im.store.UpsertPrincipalResearchStatus(ctx, original.ID, storage.PrincipalResearchStatusResearching, ""); err != nil {
		t.Fatal(err)
	}
	sample, err := im.store.CreateVoiceEmbedding(ctx, storage.VoiceEmbeddingRow{PrincipalID: original.ID, Embedding: []float32{1, 0}, SampleRate: 16000, AudioSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := im.store.CreateVoiceEmbedding(ctx, storage.VoiceEmbeddingRow{PrincipalID: original.ID, Embedding: []float32{0, 1}, SampleRate: 16000, AudioSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := im.linkContact(ctx, seen.ID, "a"); err != nil {
		t.Fatal(err)
	}
	dossier, err := im.store.GetPrincipalResearch(ctx, seen.ID)
	if err != nil || dossier.Status != storage.PrincipalResearchStatusIdle {
		t.Fatal("link started research")
	}
	voices, err := im.store.ListVoiceEmbeddingsByPrincipal(ctx, seen.ID)
	if err != nil || len(voices) != 2 {
		t.Fatal("link discarded existing voice samples")
	}
	if err := im.store.DeleteVoiceEmbedding(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	if err := im.store.SavePrincipalResearch(ctx, storage.PrincipalResearchRow{PrincipalID: seen.ID, Markdown: "Later target notes"}); err != nil {
		t.Fatal(err)
	}
	if err := im.store.UnlinkContactPrincipal(ctx, seen.ID); err != nil {
		t.Fatal(err)
	}
	dossier, err = im.store.GetPrincipalResearch(ctx, original.ID)
	if err != nil || dossier.Status != storage.PrincipalResearchStatusReady || dossier.Markdown != "Existing source notes" {
		t.Fatalf("original dossier not restored: %+v %v", dossier, err)
	}
	later, _ := im.store.GetPrincipalResearch(ctx, seen.ID)
	if later.Markdown != "Later target notes" {
		t.Fatal("later dossier was overwritten")
	}
	voices, err = im.store.ListVoiceEmbeddingsByPrincipal(ctx, original.ID)
	if err != nil || len(voices) != 1 || voices[0].ID != sample.ID {
		t.Fatal("voice ownership or later deletion was not preserved")
	}
}

func TestContactLinkPromotesContentSubjectAndUnlinkRestoresKind(t *testing.T) {
	im, _ := contactsTestImporter(t)
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana"}}}
	if _, err := im.importContacts(ctx, src, nil, contactsConsentVersion); err != nil {
		t.Fatal(err)
	}
	subject, err := im.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContentSubject, DisplayName: "Ana in a document"})
	if err != nil {
		t.Fatal(err)
	}
	if err := im.linkContact(ctx, subject.ID, "a"); err != nil {
		t.Fatal(err)
	}
	linked, _ := im.store.GetPrincipal(ctx, subject.ID)
	if linked.Kind != storage.PrincipalKindContact {
		t.Fatal("explicit link did not make the subject a known contact")
	}
	if err := im.store.UnlinkContactPrincipal(ctx, subject.ID); err != nil {
		t.Fatal(err)
	}
	restored, _ := im.store.GetPrincipal(ctx, subject.ID)
	if restored.Kind != storage.PrincipalKindContentSubject {
		t.Fatal("unlink lost original kind")
	}
}
