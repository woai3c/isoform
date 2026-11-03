package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func searchRound2DirectState(t *testing.T, a *App, state string) {
	t.Helper()
	switch state {
	case "closed":
		a.closedEyes.enabled.Store(true)
	case "paused":
		raw, err := json.Marshal([]observationPause{{Start: time.Now().Add(-time.Minute).UnixMilli(), End: time.Now().Add(time.Minute).UnixMilli()}})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.store.SetUIState(t.Context(), observationPausesKey, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchRound2TypedHandlesStayDirect(t *testing.T) {
	for _, state := range []string{"open", "closed", "paused"} {
		t.Run(state, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			// Exercise the same contact creation and handle binding APIs as People.
			contact, err := a.CreateContact("Typed Contact", "colleague", nil, []AppHandleDTO{{App: "email", DisplayHandle: "typed-owner"}})
			if err != nil {
				t.Fatal(err)
			}
			const address = "typed.person@example.com"
			if _, err := a.BindAppHandle(contact.ID, "email", "", address); err != nil {
				t.Fatal(err)
			}
			// Sessionless debug/stat counters do not prove a handle was captured.
			if err := a.store.BumpPrincipalSighting(t.Context(), contact.ID, "email", "", 1000, "debug-only"); err != nil {
				t.Fatal(err)
			}
			searchRound2DirectState(t, a, state)
			for _, query := range []string{"typed-owner", address, "PERSON@EXAMPLE.COM"} {
				got := searchTestResults(t, a, query, "person", 5)
				if len(got) != 1 || got[0].Target.Kind != "person" || got[0].Target.ID != contact.ID {
					t.Fatalf("typed handle %q lost without any captured session (%s): %+v", query, state, got)
				}
				if strings.Contains(got[0].Snippet, address) {
					t.Fatalf("typed email displayed without redaction: %+v", got)
				}
			}
			// A merged captured app must not turn the owner's typed email
			// into a capture, or attach that observed handle to its snippet.
			at := seedMCPReadActivity(t, a, "wechat-session", "Editor", "com.example.editor", "Release", "", "dismissed")
			observed, err := a.store.EnsureAnonymousContact(t.Context(), "wechat", "observed-id", "observed-wechat")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.store.BumpPrincipalSighting(t.Context(), observed.ID, "wechat", "wechat-session", at, "not searchable"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.MergePrincipals(t.Context(), observed.ID, contact.ID); err != nil {
				t.Fatal(err)
			}
			got := searchTestResults(t, a, address, "person", 5)
			if len(got) != 1 || got[0].Target.ID != contact.ID || strings.Contains(got[0].Snippet, "observed-wechat") {
				t.Fatalf("captured app changed the typed-handle lookup: %+v", got)
			}
			if got := searchTestResults(t, a, "observed-wechat", "person", 5); len(got) != 0 {
				t.Fatalf("typed-handle exception disclosed merged capture: %+v", got)
			}
		})
	}
}

func TestSearchRound2RawIdentityLookupStillGatesCapturedEvidence(t *testing.T) {
	for _, state := range []string{"open", "closed", "paused", "protected"} {
		t.Run(state, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			triage := "dismissed"
			if state == "protected" {
				triage = "protected"
			}
			at := seedMCPReadActivity(t, a, "captured-session", "Editor", "com.example.editor", "Release", "", triage)
			contact, err := a.CreateContact("Mixed Contact", "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			const address = "captured.person@example.com"
			observed, err := a.store.EnsureAnonymousContact(t.Context(), "editor", "captured-id", address)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.store.BumpPrincipalSighting(t.Context(), observed.ID, "editor", "captured-session", at, "not searchable"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.MergePrincipals(t.Context(), observed.ID, contact.ID); err != nil {
				t.Fatal(err)
			}
			searchRound2DirectState(t, a, state)
			got := searchTestResults(t, a, address, "person", 5)
			if state == "open" {
				if len(got) != 1 || got[0].Target.ID != contact.ID || strings.Contains(got[0].Snippet, address) {
					t.Fatalf("readable raw identity missing or unredacted: %+v", got)
				}
			} else if len(got) != 0 {
				t.Fatalf("raw lookup bypassed %s capture gate: %+v", state, got)
			}
		})
	}
}

func TestSearchRound2ImportedEmailMatchesRawAndRedactsDisplay(t *testing.T) {
	for _, createdFrom := range []string{"system_contacts", "auto_from_chat"} {
		t.Run(createdFrom, func(t *testing.T) {
			for _, state := range []string{"open", "closed", "paused"} {
				t.Run(state, func(t *testing.T) {
					a := newMCPReadTestApp(t)
					contact, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{ID: "ana", Kind: "contact", DisplayName: "Ana", CreatedFrom: createdFrom})
					if err != nil {
						t.Fatal(err)
					}
					const address = "ana@example.com"
					if err := a.store.BindImportedContactHandle(t.Context(), contact.ID, "native-ana", "email", address); err != nil {
						t.Fatal(err)
					}
					redacted, count := pii.RedactText(address)
					if count == 0 || redacted == address {
						t.Fatal("fixture does not exercise the email redactor")
					}
					// Import ownership stays direct even if this app also has captured
					// evidence whose session is protected.
					at := seedMCPReadActivity(t, a, "protected-email", "Editor", "com.example.editor", "Release", "", "protected")
					if err := a.store.BumpPrincipalSighting(t.Context(), contact.ID, "email", "protected-email", at, "not searchable"); err != nil {
						t.Fatal(err)
					}
					searchRound2DirectState(t, a, state)
					for _, query := range []string{address, "ANA@EXAMPLE.COM", "example.com"} {
						got := searchTestResults(t, a, query, "person", 5)
						if len(got) != 1 || got[0].Target.ID != contact.ID {
							t.Fatalf("imported email %q lost after redaction (%s): %+v", query, state, got)
						}
						if strings.Contains(got[0].Snippet, address) || !strings.Contains(got[0].Snippet, pii.DisplayText(redacted)) || strings.Contains(got[0].Snippet, "[REDACTED:") {
							t.Fatalf("raw lookup did not retain redacted display: %+v", got)
						}
					}
				})
			}
		})
	}
}
