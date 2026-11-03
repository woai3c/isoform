package main

import (
	"strings"
	"testing"
)

func TestMCPMemoryReadsCheckSnapshotIdentityBeforeAndAfterRetention(t *testing.T) {
	for _, identity := range []string{"safe", "app", "url", "parsed-app", "parsed-url", "private", "malformed"} {
		t.Run(identity, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			at := seedMCPReadActivity(t, a, "source", "Google Chrome", "com.google.Chrome", "ordinary browser work", "", "done")
			writeMCPReadMemory(t, "derived", "source", "project remembered fact")
			snapshot := ActivitySnapshotRow{SessionID: "source", Timestamp: at, App: "chrome", BrowserURL: "https://example.org/private-path?private-query", AXText: "private snapshot text"}
			switch identity {
			case "app":
				snapshot.App = "gmail"
			case "url":
				snapshot.BrowserURL = "https://mail.google.com/private-path?private-query"
			case "parsed-app":
				snapshot.ParsedJSON = `{"metadata":{"app":"gmail","bundleId":"com.google.Chrome"}}`
			case "parsed-url":
				snapshot.ParsedJSON = `{"url":"https://mail.google.com/private-path?private-query","metadata":{"app":"chrome"}}`
			case "private":
				snapshot.ParsedJSON = `{"title":"private snapshot text - Incognito","metadata":{"app":"chrome"}}`
			case "malformed":
				snapshot.ParsedJSON = "invalid JSON"
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
			for _, retained := range []bool{false, true} {
				if retained {
					if err := a.activity.store.deleteRetainedSession("source"); err != nil {
						t.Fatal(err)
					}
					var receipt string
					if err := a.activity.store.db.QueryRow(`SELECT provenance FROM activity_retained_source_privacy WHERE session_id='source'`).Scan(&receipt); err != nil {
						t.Fatal(err)
					}
					for _, private := range []string{"private-path", "private-query", "private snapshot text"} {
						if strings.Contains(receipt, private) {
							t.Fatalf("retention kept snapshot content %q: %s", private, receipt)
						}
					}
				}
				for _, tool := range []string{"memory_list", "memory_search"} {
					got := callMCPReadTest(t, a, tool, map[string]any{"query": "project"})
					want := 0
					if identity == "safe" {
						want = 1
					}
					if len(got.Items) != want {
						t.Errorf("%s returned %d items, want %d (retained=%v): %+v", tool, len(got.Items), want, retained, got)
					}
				}
			}
		})
	}
}

func TestMCPMemoryRetentionRequiresSnapshotCompleteness(t *testing.T) {
	a := newMCPReadTestApp(t)
	seedMCPReadActivity(t, a, "source", "Editor", "com.example.editor", "ordinary work", "", "done")
	writeMCPReadMemory(t, "derived", "source", "project remembered fact")
	if err := a.activity.store.deleteRetainedSession("source"); err != nil {
		t.Fatal(err)
	}
	// An explicit empty snapshot list proves there were no snapshots. An old
	// receipt that omits the field cannot establish that stronger boundary.
	for _, omitted := range []bool{false, true} {
		if omitted {
			if _, err := a.activity.store.db.Exec(`UPDATE activity_retained_source_privacy SET provenance=json_remove(provenance,'$.mcpSnapshots') WHERE session_id='source'`); err != nil {
				t.Fatal(err)
			}
		}
		for _, tool := range []string{"memory_list", "memory_search"} {
			want := 1
			if omitted {
				want = 0
			}
			if got := callMCPReadTest(t, a, tool, map[string]any{"query": "project"}); len(got.Items) != want {
				t.Fatalf("%s returned %d items, want %d (snapshot proof omitted=%v): %+v", tool, len(got.Items), want, omitted, got)
			}
		}
	}
}
