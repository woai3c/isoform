package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestMemoryListSkipsEvidenceAndKeepsSingleEntryLinks(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	ids := []string{"list-one", "list-two", "list-three"}
	for i, id := range ids {
		readableEvidenceMoment(t, app, id, "Editor", "com.example.editor", 100000+int64(i))
		if _, err := app.activity.store.db.Exec(`UPDATE activities SET title=? WHERE id=?`, fmt.Sprintf("Reviewed plan %d", i), "moment-"+id); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		id := fmt.Sprintf("list-%03d", i)
		content := renderOKFDocument(map[string]any{
			"title": "Design preference", "source": "activity", "activity_sessions": ids, "evidence_sessions": ids,
			"read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: ids, PromptSafe: true}),
		}, "## Memory Points\n\n- Use clear language.")
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	evidenceReads := 0
	app.memoryEvidenceReadObserver = func() { evidenceReads++ }
	entries, err := app.GetMemoryEntries()
	if err != nil || len(entries) != 200 {
		t.Fatalf("list=%d err=%v", len(entries), err)
	}
	for _, entry := range entries {
		if !slices.Equal(entry.ActivitySessionIDs, ids) {
			t.Fatalf("internal support metadata changed: %v", entry.ActivitySessionIDs)
		}
	}
	var raw []byte
	var elapsed []time.Duration
	for range 3 {
		start := time.Now()
		raw, err = json.Marshal(entries)
		elapsed = append(elapsed, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(elapsed)
	t.Logf("200 entries, 3 readable sessions each: marshal median=%s samples=%v evidenceReads=%d", elapsed[1], elapsed, evidenceReads)
	if evidenceReads != 0 {
		t.Fatalf("list delivery built per-entry evidence: %d reads", evidenceReads)
	}
	var wire []struct {
		ID    string   `json:"id"`
		Links []string `json:"activitySessionIds"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, entry := range wire {
		if len(entry.Links) != 0 {
			t.Fatalf("list %s exposed links: %v", entry.ID, entry.Links)
		}
	}
	single, err := app.GetMemoryEntry("list-000")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(single)
	if err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Links []string `json:"activitySessionIds"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	slices.Sort(detail.Links)
	want := slices.Clone(ids)
	slices.Sort(want)
	if evidenceReads != 1 || !slices.Equal(detail.Links, want) {
		t.Fatalf("single delivery: reads=%d links=%v want=%v", evidenceReads, detail.Links, want)
	}
	app.closedEyes.enabled.Store(true)
	raw, err = json.Marshal(single)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Links) != 0 {
		t.Fatalf("single delivery bypassed Closed Eyes: %v", detail.Links)
	}
}
