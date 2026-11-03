package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"wails-gui/storage"
)

func TestPeopleModelReadRequiresCurrentImportConsent(t *testing.T) {
	for _, state := range []string{"absent", "legacy", "renewed"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			st := newContactsTestStore(t)
			a := &App{store: st}
			ctx := context.Background()
			p, err := st.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Legacy Person", CreatedFrom: contactsSourceSystem, Source: contactsSourceSystem})
			if err != nil {
				t.Fatal(err)
			}
			mustBind(t, st, p.ID, "contacts", "system-id")
			mustBind(t, st, p.ID, "email", "legacy@example.com")
			mustBind(t, st, p.ID, "imessage", "+14155550123")
			manual := mustCreateManualContact(t, st, "Manual Person")
			mustBind(t, st, manual.ID, "email", "manual@example.com")
			if state != "absent" {
				path, err := contactsConsentPath()
				if err != nil {
					t.Fatal(err)
				}
				version := 1
				if state == "renewed" {
					version = contactsConsentVersion
				}
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(contactsConsent{Version: version, AcceptedAt: time.Now().UTC().Format(time.RFC3339)})
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			handles, err := a.peopleHandlesForModel(ctx, st, p)
			if err != nil || (len(handles) == 3) != (state == "renewed") {
				t.Fatalf("imported handles consent=%s: %+v, %v", state, handles, err)
			}
			got := a.injectPeopleContext("Legacy Person and Manual Person")
			if strings.Contains(got, "legacy@example.com") || strings.Contains(got, "manual@example.com") {
				t.Fatal("People model context must redact contact PII after consent filtering")
			}
			if !strings.Contains(got, "Manual Person") || !strings.Contains(got, "[REDACTED:private_email]") {
				t.Fatal("manual contact context lost")
			}
		})
	}
}
