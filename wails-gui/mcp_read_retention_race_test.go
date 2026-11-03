package main

import "testing"

func TestMCPMemoryRechecksRetentionAfterLiveSessionCheck(t *testing.T) {
	for _, scenario := range []string{"live-empty", "retained-empty", "retained-safe", "retained-mail", "retained-private", "retained-malformed", "missing-receipt", "legacy-snapshot-proof", "explicit-delete"} {
		t.Run(scenario, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newMCPReadTestApp(t)
			at := seedMCPReadActivity(t, app, "source", "Google Chrome", "com.google.Chrome", "ordinary browser work", "", "done")
			policy, err := app.mcpReadPrivacy(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			// Cache a readable live source before snapshots change. The shared
			// session gate now checks snapshots too; memory admission must still
			// recheck a later snapshot or its retained privacy receipt.
			if !policy.sessionAllowed("source") {
				t.Fatal("ordinary live source should pass before snapshots change")
			}
			snapshot := ActivitySnapshotRow{SessionID: "source", Timestamp: at, App: "chrome", BrowserURL: "https://example.org/project"}
			switch scenario {
			case "retained-mail":
				snapshot.BrowserURL = "https://mail.google.com/mail/u/0/#inbox"
			case "retained-private":
				snapshot.ParsedJSON = `{"title":"Private Browsing","metadata":{"app":"chrome"}}`
			case "retained-malformed":
				snapshot.ParsedJSON = "invalid JSON"
			}
			if scenario != "live-empty" && scenario != "retained-empty" {
				tx, err := app.activity.store.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := app.activity.store.SaveSnapshots(tx, []ActivitySnapshotRow{snapshot}); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "live-empty" {
				if err := app.activity.store.deleteRetainedSession("source"); err != nil {
					t.Fatal(err)
				}
				var snapshots int
				if err := app.activity.store.db.QueryRow(`SELECT COUNT(*) FROM activity_snapshots WHERE session_id='source'`).Scan(&snapshots); err != nil {
					t.Fatal(err)
				}
				if snapshots != 0 {
					t.Fatal("retention did not remove raw snapshots")
				}
			}
			switch scenario {
			case "missing-receipt":
				if _, err := app.activity.store.db.Exec(`DELETE FROM activity_retained_source_privacy WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "legacy-snapshot-proof":
				if _, err := app.activity.store.db.Exec(`UPDATE activity_retained_source_privacy SET provenance=json_remove(provenance,'$.mcpSnapshots') WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "explicit-delete":
				if err := app.activity.store.DeleteSession("source"); err != nil {
					t.Fatal(err)
				}
			}
			want := scenario == "live-empty" || scenario == "retained-empty" || scenario == "retained-safe"
			if allowed := policy.memorySessionAllowed("source"); allowed != want {
				t.Fatalf("memory allowed=%v, want %v after retention interleaved the privacy checks", allowed, want)
			}
			if scenario != "live-empty" && policy.sessionAllowed("source") {
				t.Fatal("removed session kept its cached raw activity authorization")
			}
		})
	}
}
