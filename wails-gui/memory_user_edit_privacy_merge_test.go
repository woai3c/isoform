package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestUserMemoryEditRetainsPrivacyAndForgetDependencies(t *testing.T) {
	for _, kind := range []string{"source_ids", "merged_from", "legacy-merged-from"} {
		for _, retained := range []bool{false, true} {
			name := kind + "/independent replacement"
			if retained {
				name = kind + "/retained explicit dependency"
			}
			t.Run(name, func(t *testing.T) {
				app, dir := newForgetTestApp(t)
				previous := renderOKFDocument(map[string]any{
					"title": "Derived preference", "source": "activity", "dimension": "user_insight",
					"apps": []string{"Diary"}, "activity_sessions": []string{"original-session"},
					"read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Withheld: true, ReadBlocked: true}),
					kind:              []string{"original-observation"},
				}, "## Memory Points\n\n- Original observation.")
				if kind == "legacy-merged-from" {
					previous = "# Derived preference\n\n- **Source**: activity\n- **Apps**: Diary\n- **Activity Session**: original-session\n- **MergedFrom**: original-observation\n\n## Memory Points\n\n- Original observation.\n"
				}
				if err := os.WriteFile(filepath.Join(dir, "edited.md"), []byte(previous), 0600); err != nil {
					t.Fatal(err)
				}
				metadata := map[string]any{
					"title": "Corrected preference", "source": "manual", "dimension": "user_insight",
					"apps": []string{"Calendar"}, "activity_sessions": []string{"additional-session"},
					"read_provenance": memoryReadProvenanceJSON(memoryReadSource("manual")),
				}
				if retained {
					// A human may retain a real dependency, including its source
					// observation metadata, in the replacement document.
					metadata["merged_from"] = []string{"original-observation"}
					metadata["merged_from_refs"] = []string{"memory:original-observation"}
					if kind == "source_ids" {
						metadata["source_ids"] = []string{"original-observation"}
					}
				}
				supplied := renderOKFDocument(metadata, "## Memory Points\n\n- User correction.")
				entry, err := app.SaveMemoryEntry("edited", supplied)
				if err != nil {
					t.Fatal(err)
				}
				p := memoryReadProvenanceFromContent(entry.Content)
				if entry.Source != "user_edit" || !p.Withheld || (kind != "legacy-merged-from" && !p.ReadBlocked) {
					t.Fatalf("edit changed attribution or source restrictions: source=%s provenance=%+v", entry.Source, p)
				}
				if !containsString(p.Apps, "Diary") || !containsString(p.Apps, "Calendar") || !containsString(p.Sessions, "original-session") || !containsString(p.Sessions, "additional-session") || app.memorySourceAllowsRead(entry.Content) {
					t.Fatalf("edit discarded source restrictions: %+v", p)
				}
				actual, _ := parseOKFDocument(entry.Content)
				for _, key := range []string{"source_ids", "merged_from", "merged_from_refs"} {
					if !reflect.DeepEqual(okfStringSlice(actual.Frontmatter, key), okfStringSlice(metadata, key)) {
						t.Errorf("replacement changed explicit %s: got=%v want=%v", key, actual.Frontmatter[key], metadata[key])
					}
				}
				if _, err := app.saveMemoryEntry("edited", "# Observed\n\nReplacement.", false); !errors.Is(err, errMemoryUserEdited) {
					t.Fatalf("observed writer overwrote correction: %v", err)
				}
				if err := recordMemoryTombstone(dir, "original-observation", []string{"Original source."}, time.Now()); err != nil {
					t.Fatal(err)
				}
				forgotten, err := isMemoryForgotten(dir, entry.ID, entry.Content, time.Now())
				if err != nil || forgotten != retained {
					t.Fatalf("replacement forgetting boundary changed: forgotten=%v want=%v err=%v", forgotten, retained, err)
				}
			})
		}
	}
}
