package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
)

func TestGetMemoryEvidencePrivacyAndProjection(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	prior := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
	pii.SetUserSensitiveApps(nil)
	insertTestSession(t, app.activity.store, "evidence-session", "dismissed", 1000)
	text := "Working on a report. Contact customer@example.com. " + strings.Repeat("界", 210)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_snapshots SET ax_text=? WHERE session_id=?`, text, "evidence-session"); err != nil {
		t.Fatal(err)
	}
	content := "# Report style\n- **Source**: activity\n- **Apps**: TestApp\n- **Activity Session**: evidence-session\n\n- I prefer concise reports.\n"
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].Day != 1000 || len(rows[0].Apps) != 1 || len([]rune(rows[0].Snippet)) > 200 || strings.Contains(rows[0].Snippet, "customer@example.com") {
		t.Fatalf("unsafe projection: %+v", rows)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "evidence-session") {
		t.Fatalf("exposed source identity: %s", raw)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_snapshots SET ax_text='', parsed_json=? WHERE session_id=?`, `{"body":"Reviewed a concise project plan.","title":"Planning"}`, "evidence-session"); err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 1 || rows[0].Snippet != "Reviewed a concise project plan." {
		t.Fatalf("parsed text missing: %+v %v", rows, err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_snapshots SET parsed_json=? WHERE session_id=?`, `{"body":"Private typed field","metadata":{"focusRole":"AXSecureTextField"}}`, "evidence-session"); err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 1 || rows[0].Snippet != "" {
		t.Fatalf("secure field escaped: %+v %v", rows, err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_snapshots SET parsed_json='' WHERE session_id=?`, "evidence-session"); err != nil {
		t.Fatal(err)
	}

	app.closedEyes.enabled.Store(true)
	rows, err = app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 0 {
		t.Fatalf("pause leaked: %+v %v", rows, err)
	}
	app.closedEyes.enabled.Store(false)
	pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
	rows, err = app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 0 {
		t.Fatalf("sensitive app leaked: %+v %v", rows, err)
	}
	pii.SetUserSensitiveApps(nil)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"windowTitle":"Private - Incognito"}' WHERE session_id=?`, "evidence-session"); err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 0 {
		t.Fatalf("private window leaked: %+v %v", rows, err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"appName":"TestApp"}' WHERE session_id=?`, "evidence-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, "evidence-session"); err != nil {
		t.Fatal(err)
	}
	rows, err = app.GetMemoryEvidence("report")
	if err != nil || len(rows) != 0 {
		t.Fatalf("protected session leaked: %+v %v", rows, err)
	}
}

func TestGetMemoryEvidenceUsesPrivacyCheckedExactOCR(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstSecure bool
		firstOCR    string
		sibling     bool
		secure      bool
		siblingOCR  string
		want        string
	}{
		{name: "exact readable OCR", firstOCR: "Readable exact frame.", want: "Readable exact frame."},
		{name: "empty ordinary before readable sibling", sibling: true, siblingOCR: "Readable sibling frame.", want: "Readable sibling frame."},
		{name: "empty ordinary before secure sibling", sibling: true, secure: true, siblingOCR: "Private sibling password"},
		{name: "direct secure OCR", firstSecure: true, firstOCR: "Private exact password"},
		{name: "secure before readable exact sibling", firstSecure: true, firstOCR: "Private first password", sibling: true, siblingOCR: "Readable later frame.", want: "Readable later frame."},
		{name: "no OCR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			prior := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
			pii.SetUserSensitiveApps(nil)
			store := app.activity.store
			const session = "exact-evidence"
			insertTestSession(t, store, session, "dismissed", 1000)
			snapshots, err := store.GetSessionSnapshots(session)
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("snapshots=%v err=%v", snapshots, err)
			}
			secureMetadata := `{"metadata":{"focusRole":"AXSecureTextField"}}`
			if tc.firstSecure {
				if _, err := store.db.Exec(`UPDATE activity_snapshots SET parsed_json=? WHERE id=?`, secureMetadata, snapshots[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.firstOCR != "" {
				if err := store.SaveOCRFrame(OCRFrameRow{SessionID: session, SegmentID: snapshots[0].ID, Text: tc.firstOCR}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.sibling {
				parsed := ""
				if tc.secure {
					parsed = secureMetadata
				}
				result, err := store.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path, width, height, size_bytes, storage_tier, parsed_json) VALUES (?, 2000, 'sibling.jpg', 100, 100, 1, 'hot', ?)`, session, parsed)
				if err != nil {
					t.Fatal(err)
				}
				id, err := result.LastInsertId()
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SaveOCRFrame(OCRFrameRow{SessionID: session, SegmentID: id, Text: tc.siblingOCR}); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			content := "# Writing style\n- **Source**: activity\n- **Apps**: TestApp\n- **Activity Session**: " + session + "\n\n- Keep replies short.\n"
			if err := os.WriteFile(filepath.Join(dir, "exact-ocr.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := app.GetMemoryEvidence("exact-ocr")
			if err != nil || len(rows) != 1 || rows[0].Snippet != tc.want {
				t.Fatalf("rows=%+v err=%v; want snippet %q", rows, err, tc.want)
			}
		})
	}
}
