package main

import (
	"encoding/json"
	"testing"

	"wails-gui/internal/sop"
)

func TestMCPActivitySearchChecksSnapshotIdentity(t *testing.T) {
	for _, capture := range []string{"ax", "ocr"} {
		for _, identity := range []string{"safe", "app", "url", "parsed-app", "parsed-url", "malformed"} {
			t.Run(capture+"/"+identity, func(t *testing.T) {
				a := newMCPReadTestApp(t)
				at := seedMCPReadActivity(t, a, "browser", "Google Chrome", "com.google.Chrome", "ordinary browser work", "", "done")
				snapshot := ActivitySnapshotRow{SessionID: "browser", Timestamp: at, App: "chrome", BrowserURL: "https://example.org/project", AXText: "project private snapshot marker"}
				switch identity {
				case "app":
					snapshot.App = "gmail"
				case "url":
					snapshot.BrowserURL = "https://mail.google.com/mail/u/0/#inbox"
				case "parsed-app":
					snapshot.ParsedJSON = `{"metadata":{"app":"gmail","bundleId":"com.google.Chrome"}}`
				case "parsed-url":
					snapshot.ParsedJSON = `{"url":"https://mail.google.com/mail/u/0/#inbox","metadata":{"app":"chrome"}}`
				case "malformed":
					snapshot.ParsedJSON = "invalid JSON"
				}
				if capture == "ocr" {
					snapshot.AXText = ""
				}
				tx, err := a.activity.store.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := a.activity.store.SaveSnapshots(tx, []ActivitySnapshotRow{snapshot}); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if capture == "ocr" {
					var id int64
					if err := a.activity.store.db.QueryRow(`SELECT id FROM activity_snapshots WHERE session_id='browser'`).Scan(&id); err != nil {
						t.Fatal(err)
					}
					if err := a.activity.store.SaveOCRFrame(OCRFrameRow{SessionID: "browser", SegmentID: id, Text: "project private snapshot marker", FrameTime: 0, Confidence: 1}); err != nil {
						t.Fatal(err)
					}
				}
				got := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "project"})
				want := 0
				if identity == "safe" {
					want = 1
				}
				if len(got.Items) != want {
					t.Fatalf("snapshot identity gate returned %d items, want %d: %+v", len(got.Items), want, got)
				}
			})
		}
	}
}

func TestMCPPlaybookStatusUsesRetainedSourcePrivacy(t *testing.T) {
	for _, after := range []string{"retained", "sensitive", "paused", "deleted"} {
		t.Run(after, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			at := seedMCPReadActivity(t, a, "source", "Editor", "com.example.editor", "ordinary work", "", "done")
			if _, err := a.sopStore.Save(sop.Entry{ID: "retained", Title: "Review work", Source: sop.SourceTriage, SourceIDs: []string{"source"}, Status: sop.StatusConfirmed, Body: "approved steps"}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.playbookPlans.Update("retained", func(p *sop.Plan) { p.BodyHash = "approved"; p.Schedule = &sop.Schedule{Enabled: true} }); err != nil {
				t.Fatal(err)
			}
			if err := a.activity.store.deleteRetainedSession("source"); err != nil {
				t.Fatal(err)
			}
			switch after {
			case "sensitive":
				if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "paused":
				raw, _ := json.Marshal([]observationPause{{Start: at - 1, End: at + 70000}})
				if err := a.store.SetUIState(a.ctx, observationPausesKey, string(raw)); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := a.activity.store.DeleteSession("source"); err != nil {
					t.Fatal(err)
				}
			}
			got := callMCPReadTest(t, a, "playbook_status", map[string]any{})
			want := 0
			if after == "retained" {
				want = 1
			}
			if len(got.Items) != want {
				t.Fatalf("playbook status returned %d items, want %d: %+v", len(got.Items), want, got)
			}
		})
	}
}
