package main

import (
	"encoding/json"
	"testing"
)

func TestMCPMemoryRetainedSourceRemainsReadableWithoutRawAccess(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMCPReadTestApp(t)
	seedMCPReadActivity(t, app, "source", "Editor", "com.example.editor", "project work", "project evidence", "done")
	writeMCPReadMemory(t, "derived", "source", "project remembered fact")
	if err := app.activity.store.deleteRetainedSession("source"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"memory_search", "memory_list"} {
		got := callMCPReadTest(t, app, tool, map[string]any{"query": "project"})
		if len(got.Items) != 1 || got.Items[0].ID.Text != "derived" {
			t.Fatalf("%s lost retained memory: %+v", tool, got)
		}
	}
	policy, err := app.mcpReadPrivacy(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !policy.memorySessionAllowed("source") || policy.sessionAllowed("source") {
		t.Fatal("retained derivative proof authorized raw activity or person-source access")
	}
	limited, err := app.mcpReadPrivacy(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	limited.sessionChecks = mcpReadSessionCheckLimit
	if limited.memorySessionAllowed("source") || !limited.sessionLimited("source") {
		t.Fatal("retention bypassed request provenance budget")
	}
}

func TestMCPMemoryRetentionPreservesStrictPrivacy(t *testing.T) {
	for _, scenario := range []string{"sensitive-after-retention", "paused-source", "private", "protected", "missing-bundle", "scrubbed-browser", "no-events", "malformed-event", "legacy-proof", "missing-receipt", "explicit-delete"} {
		t.Run(scenario, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newMCPReadTestApp(t)
			name, bundle, title, status := "Editor", "com.example.editor", "project work", "done"
			switch scenario {
			case "private":
				name, bundle, title = "Safari", "com.apple.Safari", "project work - Private Browsing"
			case "protected":
				status = "protected"
			case "missing-bundle":
				bundle = ""
			case "scrubbed-browser":
				name, bundle, title = "Safari", "com.apple.Safari", ""
			}
			at := seedMCPReadActivity(t, app, "source", name, bundle, title, "project evidence", status)
			writeMCPReadMemory(t, "derived", "source", "project remembered fact")
			switch scenario {
			case "no-events":
				if _, err := app.activity.store.db.Exec(`DELETE FROM activity_events WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "malformed-event":
				if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='invalid json' WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			}
			if err := app.activity.store.deleteRetainedSession("source"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "sensitive-after-retention":
				if err := app.store.SetUIState(app.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "paused-source":
				raw, _ := json.Marshal([]observationPause{{Start: at - 1, End: at + 70000}})
				if err := app.store.SetUIState(app.ctx, observationPausesKey, string(raw)); err != nil {
					t.Fatal(err)
				}
			case "legacy-proof":
				if _, err := app.activity.store.db.Exec(`UPDATE activity_retained_source_privacy SET provenance=json_remove(provenance,'$.mcpVerified') WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				if _, err := app.activity.store.db.Exec(`DELETE FROM activity_retained_source_privacy WHERE session_id='source'`); err != nil {
					t.Fatal(err)
				}
			case "explicit-delete":
				if err := app.activity.store.DeleteSession("source"); err != nil {
					t.Fatal(err)
				}
			}
			for _, tool := range []string{"memory_search", "memory_list"} {
				got := callMCPReadTest(t, app, tool, map[string]any{"query": "project"})
				if len(got.Items) != 0 {
					t.Fatalf("%s disclosed %s retained source: %+v", tool, scenario, got)
				}
			}
		})
	}
}
