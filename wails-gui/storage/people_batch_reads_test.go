package storage

import (
	"reflect"
	"testing"
)

func TestListAppHandlesByPrincipalsMatchesPerPrincipalReads(t *testing.T) {
	s := newTestStore(t)
	first := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "First"})
	second := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Second"})
	for _, handle := range []struct {
		principalID, app, handleID, display string
	}{
		{first.ID, "telegram", "tg-z", "@z"},
		{first.ID, "telegram", "tg-a", "@a"},
		{first.ID, "slack", "slack-1", "A"},
		{second.ID, "email", "b@example.com", "b@example.com"},
	} {
		if _, err := s.BindAppHandle(ctx, handle.principalID, handle.app, handle.handleID, handle.display); err != nil {
			t.Fatalf("BindAppHandle(%q): %v", handle.handleID, err)
		}
	}
	batched, err := s.ListAppHandlesByPrincipals(ctx)
	if err != nil {
		t.Fatalf("ListAppHandlesByPrincipals: %v", err)
	}
	for _, principal := range []PrincipalRow{first, second} {
		want, err := s.ListAppHandlesByPrincipal(ctx, principal.ID)
		if err != nil {
			t.Fatalf("ListAppHandlesByPrincipal(%q): %v", principal.ID, err)
		}
		if !reflect.DeepEqual(batched[principal.ID], want) {
			t.Errorf("handles for %q = %#v, want %#v", principal.ID, batched[principal.ID], want)
		}
	}
}

func TestLinkedContactRefsMatchesPerPrincipalReads(t *testing.T) {
	s := newTestStore(t)
	first := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "First"})
	second := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Second"})
	unlinked := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Unlinked"})
	if _, err := s.db.Exec(`INSERT INTO imported_contact_cards(external_ref, payload) VALUES ('card-first', '{}'), ('card-second', '{}')`); err != nil {
		t.Fatalf("seed imported cards: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO principal_contact_links(principal_id, external_ref, receipt) VALUES (?, 'card-first', '{}'), (?, 'card-second', '{}')`, first.ID, second.ID); err != nil {
		t.Fatalf("seed contact links: %v", err)
	}
	batched, err := s.LinkedContactRefs(ctx)
	if err != nil {
		t.Fatalf("LinkedContactRefs: %v", err)
	}
	for _, principal := range []PrincipalRow{first, second} {
		want, err := s.LinkedContactRef(ctx, principal.ID)
		if err != nil {
			t.Fatalf("LinkedContactRef(%q): %v", principal.ID, err)
		}
		if got := batched[principal.ID]; got != want {
			t.Errorf("contact ref for %q = %q, want %q", principal.ID, got, want)
		}
	}
	if _, exists := batched[unlinked.ID]; exists {
		t.Errorf("batch returned a contact ref for unlinked principal %q", unlinked.ID)
	}
}
