package main

import (
	"testing"
	"time"
)

func TestMCPReadMalformedMemoryCannotBorrowActivityProvenance(t *testing.T) {
	for _, kind := range []string{"activity", "learning"} {
		for _, refs := range []struct {
			name string
			ids  []string
		}{{"missing", nil}, {"blank", []string{"", " \t "}}} {
			t.Run(kind+"/"+refs.name, func(t *testing.T) {
				a := newMCPReadTestApp(t)
				at := seedMCPReadActivity(t, a, "valid-evidence", "Editor", "com.example.editor", "ordinary", "", "")
				session := ActivitySessionRow{ID: "valid-evidence", StartTime: at, AppNames: `["Editor"]`}
				writeMCPReadMemory(t, "valid", session.ID, "valid comparison marker")
				content := buildOKFMemoryMarkdown("malformed", "Unresolved source", time.Now(), "activity", "user_insight", []string{"malformed lineage marker"})
				content = withMemoryReadProvenance(content, memoryReadProvenance{Version: 2, Kinds: []string{kind}, Sessions: refs.ids, PromptSafe: true})
				writeMCPReadMarkdown(t, "malformed", content)
				if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "malformed lineage marker"}); len(got.Items) != 0 {
					t.Errorf("source without usable evidence was directly readable: %+v", got)
				}
				// Exercise the real deterministic Dream writer. The malformed
				// source must not borrow the other source's valid session IDs.
				dream := &MemoryDreamService{app: a}
				mergedID, err := dream.writeSynthesizedMemory("Merged source", "## Memory Points\n\n- malformed lineage marker", "user_insight", []string{"malformed", "valid"})
				if err != nil {
					t.Fatal(err)
				}
				merged, err := a.GetMemoryEntry(mergedID)
				if err != nil {
					t.Fatal(err)
				}
				if !memoryReadProvenanceFromContent(merged.Content).Withheld {
					t.Errorf("Dream supplied another source's sessions to unresolved %s evidence", kind)
				}
				// No model is initialized: the observed writer's existing
				// deterministic merge path retains the malformed original point.
				response := &mcMemoryGenerationResponse{Title: "Observed update", Dimension: "user_insight", MemoryPoints: []string{"new valid point"}}
				if _, err := (&ActivityTriager{app: a}).updateActivityMemory(a.ctx, "malformed", session, sessionGroup{Sessions: []ActivitySessionRow{session}}, response); err != nil {
					t.Fatal(err)
				}
				updated, err := a.GetMemoryEntry("malformed")
				if err != nil {
					t.Fatal(err)
				}
				if !memoryReadProvenanceFromContent(updated.Content).Withheld {
					t.Errorf("observed update supplied current sessions to unresolved %s evidence", kind)
				}
				if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "malformed lineage marker"}); len(got.Items) != 0 {
					t.Errorf("writer merge exposed unresolved source text: %+v", got)
				}
				if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "valid comparison marker"}); len(got.Items) != 1 || got.Items[0].ID.Text != "valid" {
					t.Fatalf("valid source compatibility changed: %+v", got)
				}
			})
		}
	}
}
