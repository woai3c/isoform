package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/storage"
)

func legacyContactsImporter(t *testing.T) contactsImporter {
	t.Helper()
	in, err := os.Open("testdata/contacts-v61/state.db.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	path := filepath.Join(t.TempDir(), "state.db")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, gz)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	// Check the genuine old schema before current storage can migrate it.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var version, tables int
	err = db.QueryRow("PRAGMA user_version").Scan(&version)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN
		('imported_contact_cards','contact_handle_provenance','principal_contact_links','principal_contact_link_updates')`).Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if version != 61 || tables != 0 {
		t.Fatalf("fixture is not v61: version=%d v62 tables=%d", version, tables)
	}
	st, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return contactsImporter{store: st, incremental: true, consentPath: filepath.Join(t.TempDir(), contactsConsentFileName)}
}

func refreshContactsForTest(t *testing.T, im contactsImporter, cards []systemContact) {
	t.Helper()
	if err := recordContactsConsent(im.consentPath, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := im.importContacts(context.Background(), &fakeContactSource{kind: contactsSourceSystem, list: cards}, nil, contactsConsentVersion)
	if err != nil || len(result.Errors) != 0 {
		t.Fatalf("refresh=%+v, %v", result, err)
	}
}

func legacyNativeCards() []systemContact {
	return []systemContact{
		{ID: "legacy-card", FullName: "Ana", Phones: []string{"+14155550101"}, Emails: []string{"ana@example.com"}},
		{ID: "curated-card", FullName: "Native name", Phones: []string{"+14155550103"}, Emails: []string{"curated@example.com"}},
		// The non-canonical native card deliberately comes first.
		{ID: "shared-b", FullName: "Shared", Phones: []string{"+14155550104"}, Emails: []string{"shared@example.com"}},
		{ID: "shared-a", FullName: "Shared", Phones: []string{"+14155550104"}, Emails: []string{"shared@example.com"}},
		{ID: "mixed-card", FullName: "Mixed", Phones: []string{"+14155550105"}, Emails: []string{"mixed@example.com"}},
		{ID: "edited-card", FullName: "Edited", Phones: []string{"+14155550106"}, Emails: []string{"edited@example.com"}},
	}
}

func TestContactsV61UpgradeWithdrawals(t *testing.T) {
	for _, change := range []string{"phone_changed", "email_removed", "card_deleted"} {
		t.Run(change, func(t *testing.T) {
			im := legacyContactsImporter(t)
			cards := legacyNativeCards()
			refreshContactsForTest(t, im, cards)
			switch change {
			case "phone_changed":
				cards[0].Phones = []string{"+14155550102"}
			case "email_removed":
				cards[0].Emails = nil
			case "card_deleted":
				cards = cards[1:]
			}
			refreshContactsForTest(t, im, cards)
			if change != "email_removed" && principalForHandle(t, im.store, "imessage", "+14155550101") != nil {
				t.Fatal("legacy phone survived withdrawal")
			}
			if change != "phone_changed" && principalForHandle(t, im.store, "email", "ana@example.com") != nil {
				t.Fatal("legacy email survived withdrawal")
			}
			if change == "phone_changed" {
				if p := principalForHandle(t, im.store, "imessage", "+14155550102"); p == nil || p.ID != "legacy" {
					t.Fatal("new phone lost original identity")
				}
			}
			if change == "card_deleted" {
				if principalForHandle(t, im.store, "contacts", "legacy-card") != nil {
					t.Fatal("legacy identifier survived deletion")
				}
				assertNoCachedContact(t, im, "legacy-card")
			}
		})
	}
}

func TestContactsV61UpgradePreservesCuratedAndSharedBindings(t *testing.T) {
	ctx := context.Background()
	im := legacyContactsImporter(t)
	cards := legacyNativeCards()
	refreshContactsForTest(t, im, cards)
	// Withdraw the curated, mixed-source and edited cards, plus one of the two
	// shared native cards. Keep shared-b unchanged to exercise checkpoint skips.
	refreshContactsForTest(t, im, cards[:3])
	for _, h := range []struct{ app, handle, owner string }{
		{"imessage", "+14155550103", "curated"}, {"email", "curated@example.com", "curated"},
		{"imessage", "+14155550105", "mixed"}, {"email", "mixed@example.com", "mixed"}, {"vcard", "file-card", "mixed"},
		{"imessage", "+14155550107", "edited"}, {"email", "personal@example.com", "edited"}, {"wechat", "curated-wechat", "edited"},
		{"imessage", "+14155550104", "shared"}, {"email", "shared@example.com", "shared"},
	} {
		if p := principalForHandle(t, im.store, h.app, h.handle); p == nil || p.ID != h.owner {
			t.Fatalf("curated/shared binding lost: %+v owner=%+v", h, p)
		}
	}
	if principalForHandle(t, im.store, "imessage", "+14155550106") != nil || principalForHandle(t, im.store, "email", "edited@example.com") != nil {
		t.Fatal("native fields survived on edited imported person")
	}
	curated, err := im.store.GetPrincipal(ctx, "curated")
	if err != nil || curated.DisplayName != "My chosen name" || curated.Notes != "Keep this note" || len(curated.Aliases) != 1 {
		t.Fatalf("curated profile changed: %+v, %v", curated, err)
	}
	// Now remove the final native contribution to shared fields.
	refreshContactsForTest(t, im, cards[:2])
	if principalForHandle(t, im.store, "imessage", "+14155550104") != nil || principalForHandle(t, im.store, "email", "shared@example.com") != nil {
		t.Fatal("final shared native contribution survived")
	}
	// Curated fields still survive their own native card's eventual deletion.
	refreshContactsForTest(t, im, cards[:1])
	if principalForHandle(t, im.store, "imessage", "+14155550103") == nil || principalForHandle(t, im.store, "email", "curated@example.com") == nil {
		t.Fatal("curated fields deleted with native card")
	}
}

func TestContactsV61UpgradePreservesManualBindingShapes(t *testing.T) {
	for _, tc := range []struct{ name, app, id, display string }{
		{"display_only_phone", "imessage", "", "+14155550108"},
		{"identifier_only_phone", "imessage", "+14155550109", ""},
		{"different_phone_fields", "imessage", "+14155550110", "+14155550111"},
		{"display_only_email", "email", "", "display-only@example.com"},
		{"identifier_only_email", "email", "id-only@example.com", ""},
		{"different_email_fields", "email", "manual-id@example.com", "manual-display@example.com"},
		{"display_only_native_id", "contacts", "", "manual-display-card"},
		{"identifier_only_native_id", "contacts", "manual-id-card", ""},
		{"different_native_id_fields", "contacts", "manual-other-card", "manual-card-label"},
	} {
		scenarios := []struct {
			name  string
			match bool
		}{{"unmatched", false}, {"matching", true}}
		if tc.app == "contacts" {
			// Native ID migration must leave non-importer rows alone even
			// when no corresponding native card survives the upgrade.
			scenarios = scenarios[:1]
		}
		for _, scenario := range scenarios {
			t.Run(tc.name+"/"+scenario.name, func(t *testing.T) {
				im := legacyContactsImporter(t)
				ctx := context.Background()
				find := func() storage.AppHandleRow {
					t.Helper()
					rows, err := im.store.ListAppHandlesByPrincipal(ctx, "edited")
					if err != nil {
						t.Fatal(err)
					}
					for _, h := range rows {
						if h.App == tc.app && h.HandleID == tc.id && h.DisplayHandle == tc.display {
							return h
						}
					}
					t.Fatalf("manual binding lost: %+v", tc)
					return storage.AppHandleRow{}
				}
				original := find()
				cards := legacyNativeCards()
				if scenario.match {
					for _, value := range []string{tc.id, tc.display} {
						if value == "" {
							continue
						}
						switch tc.app {
						case "imessage":
							cards[5].Phones = append(cards[5].Phones, value)
						case "email":
							cards[5].Emails = append(cards[5].Emails, value)
						}
					}
				}
				refreshContactsForTest(t, im, cards)
				if got := find(); got != original {
					t.Fatalf("first sync rewrote manual row: %+v -> %+v", original, got)
				}
				// Remove the matching native fields, then the entire native card.
				refreshContactsForTest(t, im, legacyNativeCards())
				if got := find(); got != original {
					t.Fatalf("field withdrawal rewrote manual row: %+v -> %+v", original, got)
				}
				refreshContactsForTest(t, im, legacyNativeCards()[:5])
				if got := find(); got != original {
					t.Fatalf("card deletion rewrote manual row: %+v -> %+v", original, got)
				}
				assertNoCachedContact(t, im, "edited-card")
				for _, native := range []struct{ app, value string }{
					{"contacts", "edited-card"}, {"imessage", "+14155550106"}, {"email", "edited@example.com"},
				} {
					if principalForHandle(t, im.store, native.app, native.value) != nil {
						t.Fatalf("native importer row survived deletion: %+v", native)
					}
				}
			})
		}
	}
}

func assertNoCachedContact(t *testing.T, im contactsImporter, id string) {
	t.Helper()
	cards, err := im.store.ListImportedContactCards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		if c.ExternalRef == id {
			t.Fatalf("withdrawn cached payload: %+v", c)
		}
	}
}

func TestContactsRefreshClearedCard(t *testing.T) {
	for _, mode := range []string{"cleared", "deleted", "initially_empty", "curated", "shared", "linked", "voice"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			im, _ := contactsTestImporter(t)
			im.incremental = true
			card := systemContact{ID: "card", FullName: "Ana", Phones: []string{"+14155550112"}, Emails: []string{"ana6@example.com"}, Photo: "data:image/png;base64,cGhvdG8="}
			var curated storage.PrincipalRow
			if mode == "curated" || mode == "linked" {
				curated = mustCreateManualContact(t, im.store, "My chosen name")
				curated.Notes = "Keep my note"
				if _, err := im.store.UpdatePrincipal(ctx, curated); err != nil {
					t.Fatal(err)
				}
				mustBind(t, im.store, curated.ID, "wechat", "keep-me")
				if mode == "curated" {
					mustBind(t, im.store, curated.ID, "imessage", card.Phones[0])
				}
			}
			initial := []systemContact{card}
			if mode == "initially_empty" {
				initial[0] = systemContact{ID: card.ID}
			}
			if mode == "shared" {
				other := card
				other.ID = "other"
				initial = append(initial, other)
			}
			refreshContactsForTest(t, im, initial)
			var voice storage.VoiceEmbeddingRow
			if mode == "voice" {
				owner := principalForHandle(t, im.store, "contacts", card.ID)
				var err error
				voice, err = im.store.CreateVoiceEmbedding(ctx, storage.VoiceEmbeddingRow{PrincipalID: owner.ID, Embedding: []float32{1, 0}, Label: "My sample"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "linked" {
				if err := im.linkContact(ctx, curated.ID, card.ID); err != nil {
					t.Fatal(err)
				}
			}
			remaining := []systemContact{{ID: card.ID}}
			if mode == "deleted" {
				remaining = nil
			}
			if mode == "shared" {
				remaining = append(remaining, initial[1])
			}
			refreshContactsForTest(t, im, remaining)
			assertNoCachedContact(t, im, card.ID)
			if mode == "voice" {
				if _, err := im.store.GetPrincipal(ctx, voice.PrincipalID); err != nil {
					t.Fatalf("profile with curated voice deleted: %v", err)
				}
				samples, err := im.store.ListVoiceEmbeddingsByPrincipal(ctx, voice.PrincipalID)
				if err != nil || len(samples) != 1 || samples[0].ID != voice.ID {
					t.Fatalf("curated voice sample lost: %+v, %v", samples, err)
				}
			}
			if principalForHandle(t, im.store, "contacts", card.ID) != nil {
				t.Fatal("cleared native identifier retained")
			}
			for _, h := range []struct{ app, handle string }{{"imessage", card.Phones[0]}, {"email", card.Emails[0]}} {
				keep := mode == "shared" || (mode == "curated" && h.app == "imessage")
				if p := principalForHandle(t, im.store, h.app, h.handle); (p != nil) != keep {
					t.Fatalf("%s binding=%+v, should remain=%v", h.app, p, keep)
				}
			}
			if curated.ID != "" {
				p, err := im.store.GetPrincipal(ctx, curated.ID)
				if err != nil || p.DisplayName != curated.DisplayName || p.Notes != curated.Notes {
					t.Fatalf("curated profile lost: %+v, %v", p, err)
				}
				if principalForHandle(t, im.store, "wechat", "keep-me") == nil {
					t.Fatal("curated handle lost")
				}
				dto := principalToDTO(p)
				handles, _ := im.store.ListAppHandlesByPrincipal(ctx, p.ID)
				dto.attachHandles(handles)
				if err := decorateContactDTOs(ctx, im.store, []*PrincipalDTO{&dto}); err != nil {
					t.Fatal(err)
				}
				if dto.ContactName != "" || dto.ContactPhoto != "" || dto.ContactLinked {
					t.Fatalf("stale contact display retained: %+v", dto)
				}
			}
		})
	}
}
