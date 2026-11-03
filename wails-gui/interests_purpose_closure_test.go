package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func TestFreshPurposeInputsCheckCompleteActivityAndKnowledgeProvenance(t *testing.T) {
	for _, restriction := range []string{"private_capture", "paused_capture", "flattened_knowledge", "activity_summary"} {
		t.Run(restriction, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			old := pii.GetUserSensitiveApps()
			pii.SetUserSensitiveApps(nil)
			t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
			now := time.Now()
			at := now.Add(-time.Hour)
			source := seedMobileBriefPurposeRun(t, app, "purpose-closure", 1, at, 60000, []string{"Diary"})
			kind, id, hash := "activity_session", source, ""
			if restriction == "flattened_knowledge" {
				kind, id = "knowledge", "interest-knowledge"
				dir, err := knowledgeDir()
				if err != nil {
					t.Fatal(err)
				}
				body := renderOKFDocument(map[string]any{"title": "Optical camera design", "read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Apps: []string{"Diary"}})}, "Optical camera design")
				if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				hash = agencyPayloadHash([]byte(strings.TrimSpace(body)))
			}
			if restriction == "activity_summary" {
				kind, id = "activity", "closure-summary"
				title := "Optical camera design"
				hash = agencyPayloadHash([]byte(strings.TrimSpace(title)))
				if err := app.activity.store.InsertActivity(ActivityRow{ID: id, Title: title, ActiveMsByApp: `{"Notes":60000}`, RunIDs: `["purpose-closure"]`, StartMs: at.UnixMilli(), EndMs: at.Add(time.Minute).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			}
			mobileTestPurpose(t, NewPurposeMapStore(app.activity.store.db), "closure-goal", PurposeKindGoal, "Optical camera project", false, func(input *CreatePurposeOpenLoopInput) {
				e := input.Evidence[0]
				e.SourceKind, e.SourceID = kind, id
				if hash != "" {
					e.SourceHash = hash
				}
				e.SourceObservedAt = at.UnixMilli()
				e.EvidenceClass = PurposeEvidenceObservedUser
				e.IndependenceKey = id
				input.Evidence = append(input.Evidence, e)
			})
			if len(collectInterestInput(app, now.Add(-24*time.Hour), now).Goals) != 1 {
				t.Fatal("readable Purpose control missing")
			}
			switch restriction {
			case "private_capture", "activity_summary":
				raw, _ := json.Marshal(activitymon.ActivityEvent{PrivateWindow: true, AppName: "Diary", AppBundleID: "com.example.diary", Timestamp: at.UnixMilli()})
				if _, err := app.activity.store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES(?,?,'test',?,0)`, source, at.UnixMilli(), string(raw)); err != nil {
					t.Fatal(err)
				}
			case "paused_capture":
				app.saveObservationPauses([]observationPause{{Start: at.Add(-time.Minute).UnixMilli(), End: at.Add(time.Minute).UnixMilli()}})
			case "flattened_knowledge":
				pii.AddUserSensitiveApp("com.example.diary", "Diary")
			}
			if len(collectInterestInput(app, now.Add(-24*time.Hour), now).Goals) != 0 {
				t.Fatalf("Purpose reintroduced %s evidence", restriction)
			}
		})
	}
}
