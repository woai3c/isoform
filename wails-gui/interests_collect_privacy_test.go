package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestCollectInterestInputAppliesMemorySourcePrivacy(t *testing.T) {
	for _, source := range []string{"envelope-app", "live-session", "retained-session", "paused-session", "closed-eyes"} {
		t.Run(source, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			pii.SetUserSensitiveApps(nil)
			insertTestSession(t, app.activity.store, "interest-source", "done", 100_000)
			if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET app_names='["Diary"]' WHERE id='interest-source'`); err != nil {
				t.Fatal(err)
			}
			proof := memoryReadProvenance{Version: 2, Kinds: []string{"activity"}}
			switch source {
			case "envelope-app":
				proof.Apps = []string{"Diary"}
			default:
				proof.Sessions = []string{"interest-source"}
			}
			content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Private preference", "source": "dream-synthesis", "dimension": "user_insight", "read_provenance": memoryReadProvenanceJSON(proof)}, "## Memory Points\n\n- Private-interest-needle is a longstanding preference.")
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "private-interest.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			readEvidence := func() string {
				input := collectInterestInput(app, time.UnixMilli(0), time.UnixMilli(200_000))
				items, _ := interestItems(input)
				var out strings.Builder
				for _, item := range items {
					out.WriteString(item.Text)
				}
				return out.String()
			}
			if got := readEvidence(); !strings.Contains(got, "Private-interest-needle") {
				t.Fatalf("readable memory did not reach interest evidence: %q", got)
			}
			switch source {
			case "retained-session":
				if err := app.activity.store.deleteRetainedSession("interest-source"); err != nil {
					t.Fatal(err)
				}
				if got := readEvidence(); !strings.Contains(got, "Private-interest-needle") {
					t.Fatal("raw retention removed readable interest evidence")
				}
				pii.AddUserSensitiveApp("com.example.diary", "Diary")
			case "paused-session":
				app.saveObservationPauses([]observationPause{{Start: 100_000, End: 160_000}})
			case "closed-eyes":
				app.closedEyes.enabled.Store(true)
			default:
				pii.AddUserSensitiveApp("com.example.diary", "Diary")
			}
			if got := readEvidence(); strings.Contains(got, "Private-interest-needle") {
				t.Fatalf("protected memory entered interest evidence: %q", got)
			}
		})
	}
}
