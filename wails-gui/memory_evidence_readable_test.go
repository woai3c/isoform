package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
)

func readableEvidenceMoment(t *testing.T, app *App, id, appName, bundle string, at int64) {
	t.Helper()
	store := app.activity.store
	insertTestSession(t, store, id, "done", at)
	raw, _ := json.Marshal([]string{appName})
	if _, err := store.db.Exec(`UPDATE activity_sessions SET app_names=? WHERE id=?`, string(raw), id); err != nil {
		t.Fatal(err)
	}
	event, _ := json.Marshal(map[string]string{"appName": appName, "appBundleId": bundle})
	if _, err := store.db.Exec(`UPDATE activity_events SET data=? WHERE session_id=?`, string(event), id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_snapshots SET app=?,ax_text='AX_ONLY_SECRET',parsed_json=? WHERE session_id=?`, appName, fmt.Sprintf(`{"body":"PARSED_ONLY_SECRET","metadata":{"bundleId":%q}}`, bundle), id); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveOCRFrame(OCRFrameRow{SessionID: id, Text: "OCR_ONLY_SECRET", FrameTime: 1, Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	run := "run-" + id
	insertTestRun(t, store, run, at, at, "done", 1)
	sessions, _ := json.Marshal([]string{id})
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=?,app_name=?,app_key=? WHERE id=?`, string(sessions), appName, bundle, run); err != nil {
		t.Fatal(err)
	}
	runs, _ := json.Marshal([]string{run})
	if err := store.InsertActivity(ActivityRow{ID: "moment-" + id, Status: activityStatusSettled, Title: "Reviewed the plan", RunIDs: string(runs), StartMs: at, EndMs: at + 60000, ActiveMsByApp: fmt.Sprintf(`{%q:60000}`, appName)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertActivityRunLink(run, "moment-"+id, 1, at); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryEvidenceReadableMomentTitlesAndPrivacy(t *testing.T) {
	for _, gate := range []string{"readable", "user_title", "loginwindow", "Dock", "protected", "sensitive", "private_app_setting", "private", "private_snapshot", "paused", "closed_eyes", "mixed_moment"} {
		t.Run(gate, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			prior := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
			pii.SetUserSensitiveApps(nil)
			appName, bundle := "Editor", "com.example.editor"
			if gate == "loginwindow" {
				appName, bundle = "loginwindow", "com.apple.loginwindow"
			}
			if gate == "Dock" {
				appName, bundle = "Dock", "com.apple.dock"
			}
			readableEvidenceMoment(t, app, "source", appName, bundle, 100000)
			store := app.activity.store
			switch gate {
			case "user_title":
				if _, err := store.db.Exec(`UPDATE activities SET user_title='Corrected moment title' WHERE id='moment-source'`); err != nil {
					t.Fatal(err)
				}
			case "protected":
				if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{bundle: appName})
			case "private_app_setting":
				if err := app.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "private":
				if _, err := store.db.Exec(`UPDATE activity_events SET data=json_set(data,'$.privateWindow',json('true')) WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "private_snapshot":
				if _, err := store.db.Exec(`UPDATE activity_snapshots SET parsed_json='{"title":"Private - Incognito"}' WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "paused":
				app.saveObservationPauses([]observationPause{{Start: 100000, End: 160000}})
			case "closed_eyes":
				app.closedEyes.enabled.Store(true)
			case "mixed_moment":
				insertTestSession(t, store, "private-sibling", "protected", 200000)
				insertTestRun(t, store, "private-run", 2, 200000, "done", 1)
				if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids='["private-sibling"]' WHERE id='private-run'`); err != nil {
					t.Fatal(err)
				}
				if _, err := store.db.Exec(`UPDATE activities SET run_ids='["run-source","private-run"]' WHERE id='moment-source'`); err != nil {
					t.Fatal(err)
				}
			}
			dir, _ := memoryDir()
			content := "# A preference\n- **Activity Session**: source\n\nKeep the plan clear.\n"
			if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := app.GetMemoryEvidenceReadable("belief")
			// The existing detail fallback must not bypass denied evidence.
			entry, readErr := app.GetMemoryEntry("belief")
			if readErr != nil {
				t.Fatal(readErr)
			}
			wireJSON, marshalErr := json.Marshal(entry)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			var wire struct {
				Links []string `json:"activitySessionIds"`
			}
			if err := json.Unmarshal(wireJSON, &wire); err != nil {
				t.Fatal(err)
			}
			if gate != "readable" && gate != "user_title" && len(wire.Links) != 0 {
				t.Fatalf("%s leaked fallback links: %v", gate, wire.Links)
			}
			if (gate == "readable" || gate == "user_title") && (len(wire.Links) != 1 || wire.Links[0] != "source") {
				t.Fatalf("readable fallback links lost: %v", wire.Links)
			}
			if err != nil {
				t.Fatal(err)
			}
			if gate != "readable" && gate != "user_title" {
				if len(rows) != 0 {
					t.Fatalf("%s leaked: %+v", gate, rows)
				}
				return
			}
			title := "Reviewed the plan"
			if gate == "user_title" {
				title = "Corrected moment title"
			}
			if len(rows) != 1 || rows[0].Text != title || rows[0].AppName != "Editor" || rows[0].ActivitySessionID != "source" || rows[0].At != 100000 || rows[0].DurationMs != 60000 {
				t.Fatalf("rows=%+v", rows)
			}
			raw, _ := json.Marshal(rows)
			for _, secret := range []string{"OCR_ONLY_SECRET", "AX_ONLY_SECRET", "PARSED_ONLY_SECRET"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("captured text leaked: %s", raw)
				}
			}
		})
	}
}

func TestMemoryEvidenceReadableNewestEightSessions(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	var ids []string
	for i := range 10 {
		id := fmt.Sprintf("s%d", i)
		ids = append(ids, id)
		readableEvidenceMoment(t, app, id, "Editor", "com.example.editor", int64(i+1)*100000)
		if _, err := app.activity.store.db.Exec(`UPDATE activities SET title=? WHERE id=?`, fmt.Sprintf("Reviewed plan %d", i), "moment-"+id); err != nil {
			t.Fatal(err)
		}
	}
	ids = append(ids, "s9")
	dir, _ := memoryDir()
	content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Work style", "activity_sessions": ids}, "Clear plans.")
	if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := app.GetMemoryEvidenceReadable("belief")
	if err != nil || len(rows) != 8 {
		t.Fatalf("rows=%+v %v", rows, err)
	}
	if rows[0].ActivitySessionID != "s9" || rows[7].ActivitySessionID != "s2" {
		t.Fatalf("not newest: %+v", rows)
	}
}

func TestWorkPatternSharedSystemNoiseCompatibility(t *testing.T) {
	for _, tc := range []struct {
		key, name      string
		ambient, noise bool
	}{
		{"com.apple.loginwindow", "loginwindow", true, true}, {"com.apple.dock", "Dock", true, true}, {"name:system ui server", "", true, true},
		{"com.apple.windowmanager", "Window Manager", true, true}, {"com.apple.controlcenter", "Control Center", true, true}, {"com.apple.notificationcenterui", "Notification Center", true, true},
		{"com.apple.screensaver.engine", "Screen Saver Engine", true, true}, {"", "WindowServer", false, true}, {"com.apple.spotlight", "Spotlight", false, true}, {"", "UserNotificationCenter", false, true},
		{"com.example.editor", "Editor", false, false}, {"com.apple.spotlight", "Dock", true, true},
	} {
		if got := workPatternIsAmbientSystemApp(tc.key, tc.name); got != tc.ambient {
			t.Errorf("ambient(%q,%q)=%v", tc.key, tc.name, got)
		}
		if got := isSystemNoiseApp(tc.name, tc.key); got != tc.noise {
			t.Errorf("noise(%q,%q)=%v", tc.name, tc.key, got)
		}
	}
}

func TestMemoryEvidenceReadableOnlySupportingLinksAndDedup(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	for i, id := range []string{"one", "two", "three", "comparison", "neighbour"} {
		readableEvidenceMoment(t, app, id, "Editor", "com.example.editor", 100000+int64(i))
	}
	for _, id := range []string{"comparison", "neighbour"} {
		if _, err := app.activity.store.db.Exec(`UPDATE activities SET title='Unrelated family discussion' WHERE id=?`, "moment-"+id); err != nil {
			t.Fatal(err)
		}
	}
	content := renderOKFDocument(map[string]any{
		"title": "Design preferences", "source": "activity",
		"activity_sessions": []string{"one", "two", "three", "comparison"},
		"evidence_sessions": []string{"one", "one", "two", "three"},
		"read_provenance":   memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"one", "two", "three", "comparison"}, PromptSafe: true}),
	}, "Use simple language.")
	dir, _ := memoryDir()
	if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := app.GetMemoryEvidenceReadable("belief")
	if err != nil || len(rows) != 1 || rows[0].Text != "Reviewed the plan" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	old, err := app.GetMemoryEvidence("belief")
	if err != nil || len(old) != 3 {
		t.Fatalf("legacy evidence sources=%+v err=%v", old, err)
	}
	entry, err := app.GetMemoryEntry("belief")
	if err != nil || len(entry.ActivitySessionIDs) != 3 {
		t.Fatalf("links=%+v err=%v", entry.ActivitySessionIDs, err)
	}
	view, err := app.GetAboutYou()
	if err != nil || len(view.Beliefs) != 1 || view.Beliefs[0].SessionCount != 3 {
		t.Fatalf("comparison inflated belief evidence: %+v %v", view.Beliefs, err)
	}
	// Privacy dependencies still include comparison inputs.
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='comparison'`); err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetMemoryEvidenceReadable("belief")
	if err != nil || len(rows) != 0 {
		t.Fatalf("withdrawn comparison leaked=%+v %v", rows, err)
	}
}

func TestMemoryEvidenceReadableRejectsBroadenedMomentAndHiddenTitle(t *testing.T) {
	for _, gate := range []string{"neighbour_moment", "redacted_title", "stored_placeholder"} {
		t.Run(gate, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			readableEvidenceMoment(t, app, "source", "Editor", "com.example.editor", 100000)
			switch gate {
			case "neighbour_moment":
				readableEvidenceMoment(t, app, "other", "Editor", "com.example.editor", 100001)
				if _, err := app.activity.store.db.Exec(`UPDATE activities SET run_ids='["run-source","run-other"]' WHERE id='moment-source'`); err != nil {
					t.Fatal(err)
				}
			case "redacted_title":
				if _, err := app.activity.store.db.Exec(`UPDATE activities SET title='Account number: 1234567890' WHERE id='moment-source'`); err != nil {
					t.Fatal(err)
				}
			case "stored_placeholder":
				if _, err := app.activity.store.db.Exec(`UPDATE activities SET title='Review [REDACTED:account_number]' WHERE id='moment-source'`); err != nil {
					t.Fatal(err)
				}
			}
			dir, _ := memoryDir()
			if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte("# Preference\n- **Activity Session**: source\n\nClear design."), 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := app.GetMemoryEvidenceReadable("belief")
			if err != nil || len(rows) != 0 {
				t.Fatalf("%s leaked %+v %v", gate, rows, err)
			}
		})
	}
}

func TestMemoryEvidenceLegacyFlattenedLineageFailsClosed(t *testing.T) {
	content := renderOKFDocument(map[string]any{"source": "activity", "activity_sessions": []string{"source", "comparison"}, "read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"source", "comparison"}, PromptSafe: true})}, "Point")
	if got := memoryEvidenceSessionIDs(content); len(got) != 0 {
		t.Fatalf("ambiguous links=%v", got)
	}
	current := activityMemoryWithProvenance("id", content, memoryReadProvenanceFromContent(content), nil, []string{"source", "source"})
	if got := memoryEvidenceSessionIDs(current); len(got) != 1 || got[0] != "source" {
		t.Fatalf("support=%v", got)
	}
	if len(memoryReadProvenanceFromContent(current).Sessions) != 2 {
		t.Fatal("privacy lineage lost")
	}
	edited := userAuthoredMemoryMarkdown("id", current, current)
	if got := memoryEvidenceSessionIDs(edited); len(got) != 1 || got[0] != "source" {
		t.Fatalf("edit changed support=%v", got)
	}
}

func TestMemoryEvidenceLinksRecheckAtDelivery(t *testing.T) {
	for _, gate := range []string{"closed_eyes", "private_app", "source_edited"} {
		t.Run(gate, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			readableEvidenceMoment(t, app, "source", "Editor", "com.example.editor", 100000)
			dir, _ := memoryDir()
			path := filepath.Join(dir, "belief.md")
			content := "# Preference\n- **Activity Session**: source\n\nClear design."
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			entry, err := app.GetMemoryEntry("belief")
			if err != nil {
				t.Fatal(err)
			}
			switch gate {
			case "closed_eyes":
				app.closedEyes.enabled.Store(true)
			case "private_app":
				if err := app.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "source_edited":
				if err := os.WriteFile(path, []byte(content+" Changed."), 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Links []string `json:"activitySessionIds"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Links) != 0 {
				t.Fatalf("%s stale links: %s", gate, raw)
			}
		})
	}
}
