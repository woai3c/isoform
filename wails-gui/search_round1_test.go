package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestSearchRound1OldChatTitleSurvivesNewerMessages(t *testing.T) {
	a := newMCPReadTestApp(t)
	searchTestSession(t, a, "old-side", "Budget review", false)
	searchTestSession(t, a, "main", "Main conversation", false)
	for i := 0; i < 60; i++ {
		searchTestItem(t, a, "main", fmt.Sprintf("recent-%d", i), "user", "New budget discussion", int64(2000+i))
	}
	got := searchTestResults(t, a, "budget", "chat", 5)
	if len(got) == 0 || got[0].Target.Kind != "chat" || got[0].Target.SessionID != "old-side" {
		t.Fatalf("old title displaced by recent message candidates: %+v", got)
	}
}

func TestSearchRound1MergedObservedHandleUsesPrivacyGates(t *testing.T) {
	for _, createdFrom := range []string{"manual", "system_contacts"} {
		t.Run(createdFrom, func(t *testing.T) {
			for _, gate := range []string{"allowed", "closed", "closed_wechat", "pause", "past_pause", "protected", "private", "sensitive", "sensitive_handle", "messaging", "missing_source"} {
				t.Run(gate, func(t *testing.T) {
					a := newMCPReadTestApp(t)
					appID, appName, title, triage := "com.example.editor", "Editor", "Release", "dismissed"
					switch gate {
					case "private":
						appID, appName, title = "com.google.Chrome", "Chrome", "Private Browsing"
					case "protected":
						triage = "protected"
					}
					at := seedMCPReadActivity(t, a, "observed-session", appName, appID, title, "", triage)
					contact, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{ID: "contact", Kind: "contact", DisplayName: "Alice", Relationship: "colleague", Aliases: []string{"Ally"}, CreatedFrom: createdFrom})
					if err != nil {
						t.Fatal(err)
					}
					if err := a.store.BindImportedContactHandle(t.Context(), contact.ID, "native-card", "email", "alice.imported@example.com"); err != nil {
						t.Fatal(err)
					}
					observed, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{ID: "observed", Kind: "contact", DisplayName: "Captured", CreatedFrom: "auto_from_chat"})
					if err != nil {
						t.Fatal(err)
					}
					handleApp := "editor"
					if gate == "messaging" || gate == "closed_wechat" {
						handleApp = "wechat"
					}
					if gate == "sensitive_handle" {
						handleApp = "1Password"
					}
					if _, err := a.store.BindAppHandle(t.Context(), observed.ID, handleApp, "observed-id", "observed-handle"); err != nil {
						t.Fatal(err)
					}
					sessionID := "observed-session"
					if gate == "missing_source" {
						sessionID = "missing-source"
					}
					if err := a.store.BumpPrincipalSighting(t.Context(), observed.ID, handleApp, sessionID, at, "messageonly"); err != nil {
						t.Fatal(err)
					}
					if _, err := a.store.MergePrincipals(t.Context(), observed.ID, contact.ID); err != nil {
						t.Fatal(err)
					}
					switch gate {
					case "closed", "closed_wechat":
						a.closedEyes.enabled.Store(true)
					case "pause", "past_pause":
						pause := observationPause{Start: at - 1, End: at + 60001}
						if gate == "pause" {
							pause = observationPause{Start: time.Now().Add(-time.Minute).UnixMilli(), End: time.Now().Add(time.Minute).UnixMilli()}
						}
						raw, _ := json.Marshal([]observationPause{pause})
						if err := a.store.SetUIState(t.Context(), observationPausesKey, string(raw)); err != nil {
							t.Fatal(err)
						}
					case "sensitive":
						if err := a.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
							t.Fatal(err)
						}
					}
					got := searchTestResults(t, a, "observed-handle", "person", 5)
					if gate == "allowed" {
						if len(got) != 1 || got[0].Target.ID != contact.ID {
							t.Fatalf("readable merged handle missing: %+v", got)
						}
					} else if len(got) != 0 {
						t.Fatalf("%s disclosed observed handle after merge: %+v", gate, got)
					}
					for _, query := range []string{"Alice", "Ally", "colleague", "alice.imported@example.com"} {
						got := searchTestResults(t, a, query, "person", 5)
						if len(got) != 1 || got[0].Target.ID != contact.ID || strings.Contains(got[0].Snippet, "observed-handle") {
							t.Fatalf("direct contact field lost or observed snippet leaked for %q: %+v", query, got)
						}
					}
				})
			}
		})
	}
}
