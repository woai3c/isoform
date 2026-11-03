package main

import (
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/storage"
)

func TestMCPReadSearchSnapshotBudgetCompleteness(t *testing.T) {
	for _, tool := range []struct{ name, key, wantID string }{
		{"activity_search", "query", "readable"},
		{"people_lookup", "name", "readable-person"},
	} {
		t.Run(tool.name, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			a := newMCPReadTestApp(t)
			at := seedMCPReadActivity(t, a, "readable", "Editor", "com.example.editor", "publicsearch", "", "done")
			seedMCPReadActivity(t, a, "private", "Safari", "com.apple.Safari", "privatesearch - Private Browsing", "", "done")
			for _, person := range []struct{ id, session, name string }{
				{"readable-person", "readable", "publicsearch"},
				{"private-person", "private", "privatesearch"},
			} {
				if _, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: person.id, Kind: storage.PrincipalKindContact, DisplayName: person.name, CreatedFrom: "activity"}); err != nil {
					t.Fatal(err)
				}
				if err := a.store.BumpPrincipalSighting(a.ctx, person.id, "Editor", person.session, at, ""); err != nil {
					t.Fatal(err)
				}
			}
			checkReadable := func() {
				t.Helper()
				got := callMCPReadTest(t, a, tool.name, map[string]any{tool.key: "publicsearch"})
				if len(got.Items) != 1 || got.Items[0].ID.Text != tool.wantID || got.Truncated || !got.Bounded || got.Omitted != 0 || got.Withheld != 0 {
					t.Fatalf("readable boundary fixture was not complete: %+v", got)
				}
			}
			checkReadable()
			// One ordinary event and exactly 10,000 valid snapshots still fit
			// this session's separate event/snapshot caps and the shared budget.
			if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS
				(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10000)
				INSERT INTO activity_snapshots(session_id,timestamp,file_path,app,parsed_json)
				SELECT 'readable',?+i,'','Editor','{}' FROM n`, at); err != nil {
				t.Fatal(err)
			}
			checkReadable()
			if _, err := a.activity.store.db.Exec(`INSERT INTO activity_snapshots(session_id,timestamp,file_path,app,parsed_json)
				VALUES('readable',?,'','Editor','{}')`, at+10001); err != nil {
				t.Fatal(err)
			}
			var previous []byte
			for _, query := range []string{"publicsearch", "privatesearch", "absentsearch"} {
				got := callMCPReadTest(t, a, tool.name, map[string]any{tool.key: query})
				if len(got.Items) != 0 || !got.Truncated || got.Bounded || got.Omitted != 0 || got.Withheld != 0 || !strings.Contains(got.Reason, "partial answer") {
					t.Errorf("snapshot-limited query %q claimed complete or leaked a source count: %+v", query, got)
				}
				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if previous != nil && string(previous) != string(encoded) {
					t.Errorf("query-dependent snapshot completeness: %s vs %s", previous, encoded)
				}
				previous = encoded
			}
		})
	}
}
