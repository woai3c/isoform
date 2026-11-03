package sop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleReadProvenanceIsCodeOwnedAndSurvivesEdits(t *testing.T) {
	store := newTestStore(t)
	draft := t.TempDir()
	body := "---\ntitle: Draft title\nsource: manual\nsourceIds: []\nreadProvenance: {\"version\":1}\n---\nDraft steps.\n"
	if err := os.WriteFile(filepath.Join(draft, "SOP.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	proof := `{"version":1,"apps":["Diary"],"readBlocked":true}`
	meta := Entry{Source: SourceTriage, SourceIDs: []string{"actual-session"}, ReadProvenance: proof}
	if _, err := store.InstallBundle("derived", draft, meta); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get("derived")
	if err != nil || stored.ReadProvenance != proof || stored.Source != SourceTriage || len(stored.SourceIDs) != 1 || stored.SourceIDs[0] != "actual-session" {
		t.Fatalf("draft overwrote source proof or store lost it: %+v err=%v", stored, err)
	}
	if _, err := store.Mutate("derived", func(entry *Entry) { entry.Title = "User edit"; entry.Status = StatusConfirmed }); err != nil {
		t.Fatal(err)
	}
	stored, err = store.Get("derived")
	if err != nil || stored.ReadProvenance != proof || stored.Title != "User edit" {
		t.Fatalf("edit lost source proof: %+v err=%v", stored, err)
	}
}
