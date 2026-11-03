package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRejectMemoryAttributionUsesForgetCascade(t *testing.T) {
	app, dir := newForgetTestApp(t)
	store := newTestStore(t)
	app.activity = &activityMonitorState{store: store}
	const id = "rejected-employer"
	content := "# Works at Acme\n\n- **Source**: activity\n\n## Memory Points\n\n- I work at Acme.\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	archive := writeForgetArchiveFixture(t, dir, "old-employer", id, "Works at Acme", content)
	derived := seedMemoryForgetSelf(t, store, id, "Work at Acme")
	if err := app.memoryStore.Sync("reject-review"); err != nil {
		t.Fatal(err)
	}
	if err := app.RejectMemoryAttribution(id, "The Acme statement was somebody else's"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Errorf("rejection retained archived private statement: %v", err)
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM self_model_entries WHERE id = ?", derived.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("rejection retained derivative: count=%d err=%v", count, err)
	}
	if _, err := app.saveMemoryEntry("reobserved-employer", content, false); !errors.Is(err, errMemoryForgotten) {
		t.Errorf("rejection allowed re-observation: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, memoryRejectionsLogName)); err == nil && len(data) != 0 {
		t.Errorf("rejection retained identifying log: %s", data)
	}
}

func TestForgetMemoryScrubsMatchingRejectionHistory(t *testing.T) {
	app, dir := newForgetTestApp(t)
	entry, err := app.SaveMemoryEntry("legacy-rejected", "# Private statement\n\nAn old rejected statement returned.")
	if err != nil {
		t.Fatal(err)
	}
	if err := appendMemoryRejection(dir, MemoryRejectionEntry{MemoryID: entry.ID, Title: entry.Title, ActivitySessionIDs: []string{"private-session"}, Reason: "private reason"}); err != nil {
		t.Fatal(err)
	}
	if err := appendMemoryRejection(dir, MemoryRejectionEntry{MemoryID: "independent", Title: "Keep this correction", Reason: "independent reason"}); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	var showedCorrection bool
	for _, item := range preview.Items {
		showedCorrection = showedCorrection || item.Kind == "memory_correction"
	}
	if !showedCorrection {
		t.Error("forget preview omitted identifying rejection history")
	}
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	rows := readRejectionLog(t)
	if len(rows) != 1 || rows[0].MemoryID != "independent" {
		t.Fatalf("rejection scrub scope incorrect: %+v", rows)
	}
}

func TestForgetMemoryCascadesForwardThroughDreamSynthesis(t *testing.T) {
	app, dir := newForgetTestApp(t)
	store := newTestStore(t)
	app.activity = &activityMonitorState{store: store}
	files := map[string]string{
		"source-a":        "# Source A\n\nA private personal observation.\n",
		"source-b":        "# Source B\n\nAn independent observation.\n",
		"synthesis":       "# Combined\n\n- **MergedFrom**: source-a, source-b\n\nA private personal observation combined with B.\n",
		"later-synthesis": "---\nid: later-synthesis\nsource: dream\nmerged_from:\n  - synthesis\n---\n# Later synthesis\n\nPrivate observation appears again.\n",
		"unrelated":       "# Unrelated\n\nDoes not cite a removed source.\n",
	}
	for id, content := range files {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := writeForgetArchiveFixture(t, dir, "synthesis-archive", "synthesis", "Combined", files["synthesis"])
	independentArchive := writeForgetArchiveFixture(t, dir, "source-b-archive", "source-b", "Source B", files["source-b"])
	derived := seedMemoryForgetSelf(t, store, "later-synthesis", "Derived private observation")
	if err := app.memoryStore.Sync("dream-cascade-review"); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview("source-a")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range preview.Items {
		seen[item.ID] = true
	}
	for _, id := range []string{"source-a", "synthesis", "later-synthesis", "synthesis-archive", derived.ID} {
		if !seen[id] {
			t.Errorf("preview omitted derived record %s", id)
		}
	}
	if seen["source-b"] || seen["source-b-archive"] || seen["unrelated"] {
		t.Errorf("preview included independent parents or records: %+v", preview.Items)
	}
	if err := app.ForgetMemoryEntry("source-a", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"source-a", "synthesis", "later-synthesis"} {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); !os.IsNotExist(err) {
			t.Errorf("derived memory %s remains after forget: %v", id, err)
		}
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Errorf("synthesis archive remains: %v", err)
	}
	if _, err := os.Stat(independentArchive); err != nil {
		t.Errorf("independent source archive removed: %v", err)
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM self_model_entries WHERE id = ?", derived.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("synthesis derivative remains: count=%d err=%v", count, err)
	}
}

func TestForgettingDreamSynthesisKeepsIndependentSourcesRestorable(t *testing.T) {
	app, dir := newForgetTestApp(t)
	source := "# Independent source\n\nAn independent original preference.\n"
	writeForgetArchiveFixture(t, dir, "source-archive", "source-a", "Independent source", source)
	entry, err := app.saveMemoryEntry("synthesis", "# Combined\n\n- **MergedFrom**: source-a, source-b\n\nA different combined statement.\n", false)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 {
		t.Fatalf("synthesis preview includes ancestors: %+v", preview.Items)
	}
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	forgotten, err := isMemoryForgotten(dir, "source-a", source, time.Now())
	if err != nil || forgotten {
		t.Fatalf("forgetting child poisoned its independent parent: forgotten=%v err=%v", forgotten, err)
	}
	if _, err := app.RestoreArchivedMemory("source-archive"); err != nil {
		t.Fatalf("independent source cannot be restored: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "source-a.md"))
	if err != nil || !strings.Contains(string(data), "independent original preference") {
		t.Fatalf("restored source differs: %s err=%v", data, err)
	}
}

func TestForgetMemoryConfirmationIncludesDescendantChanges(t *testing.T) {
	app, dir := newForgetTestApp(t)
	for id, content := range map[string]string{
		"source": "# Source\n\nPrivate source fact.\n",
		"child":  "# Child\n\n- **MergedFrom**: source\n\nThe original derived fact.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := app.GetMemoryForgetPreview("source")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.md"), []byte("# Child\n\n- **MergedFrom**: source\n\nA newly edited derivative.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("source", preview.ConfirmationToken); err == nil {
		t.Fatal("confirmation accepted a changed descendant")
	}
	if _, err := app.GetMemoryEntry("source"); err != nil {
		t.Fatalf("stale confirmation removed the source: %v", err)
	}
}

func TestForgetMemoryConfirmationIncludesCorrectionChanges(t *testing.T) {
	app, dir := newForgetTestApp(t)
	entry, err := app.SaveMemoryEntry("source", "# Source\n\nPrivate statement.")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendMemoryRejection(dir, MemoryRejectionEntry{MemoryID: entry.ID, Title: "Later correction"}); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err == nil {
		t.Fatal("confirmation omitted a newly saved identifying correction")
	}
}

func TestForgetSynthesisPreservesIdenticalArchivedParent(t *testing.T) {
	app, dir := newForgetTestApp(t)
	const body = "\n\nAn independently saved original preference.\n"
	writeForgetArchiveFixture(t, dir, "independent-parent", "source-a", "Original", "# Original"+body)
	if _, err := app.saveMemoryEntry("synthesis", "# Combined\n\n- **MergedFrom**: source-a"+body, false); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview("synthesis")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("synthesis", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreArchivedMemory("independent-parent"); err != nil {
		t.Fatalf("matching content hid an unpreviewed independent archive: %v", err)
	}
}

func TestRejectMemoryAttributionRequiresFreshPreview(t *testing.T) {
	app, _ := newForgetTestApp(t)
	entry, err := app.SaveMemoryEntry("reject-confirmation", "# Rejected record\n\nOriginal fact.")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveMemoryEntry(entry.ID, "# Rejected record\n\nNewer fact."); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", preview.ConfirmationToken} {
		if err := app.RejectMemoryAttributionWithPreview(entry.ID, "wrong attribution", token); err == nil {
			t.Fatal("rejection accepted a missing or stale confirmation")
		}
	}
	if _, err := app.GetMemoryEntry(entry.ID); err != nil {
		t.Fatalf("unconfirmed rejection deleted the record: %v", err)
	}
}

func TestForgetMemoryPreservesIndependentUserCorrection(t *testing.T) {
	app, dir := newForgetTestApp(t)
	store := newTestStore(t)
	app.activity = &activityMonitorState{store: store}
	if _, err := app.saveMemoryEntry("source", "# Source\n\nOriginal private fact.\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.saveMemoryEntry("corrected", "# Old synthesis\n\n- **MergedFrom**: source\n\nOld derived private fact.\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.saveMemoryEntry("old-correction-synthesis", "# Old derivative\n\n- **MergedFrom**: corrected\n\nThe old private fact still appears here.\n", false); err != nil {
		t.Fatal(err)
	}
	oldSelf := seedMemoryForgetSelf(t, store, "corrected", "The former private statement")
	edited, err := app.SaveMemoryEntry("corrected", "# My correction\n\nThis is a new, independent note.")
	if err != nil {
		t.Fatal(err)
	}
	currentRef := taskContextSourceRef(taskContextSourceMemoryKind, edited.ID, edited.ModTime, edited.Content)
	laterID, err := NewMemoryDreamService(app).writeSynthesizedMemory("Later goal", "A new goal based on the independent correction.", "user_insight", []string{edited.ID}, currentRef)
	if err != nil {
		t.Fatal(err)
	}
	self, _, err := store.AddSelfModelEntry(SelfModelEntry{
		Kind: selfModelKindPreference, Scope: "project:yansu", Statement: "An independent current preference",
		Status: selfModelStatusActive, Confidence: 0.9, ReasoningStatus: "explicit_user",
		Evidence: []SelfModelEvidence{{SourceKind: taskContextSourceMemoryKind, SourceID: edited.ID,
			SourceRefs: []string{currentRef}, Confidence: 1, OccurredAt: time.Now().UnixMilli()}},
		CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.memoryStore.Sync("independent-correction"); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview("source")
	if err != nil {
		t.Fatal(err)
	}
	var historical, oldDerivative, oldSelfShown bool
	for _, item := range preview.Items {
		if item.ID == edited.ID || item.ID == laterID || item.ID == self.ID {
			t.Fatalf("preview deletes independent correction or its current derivatives: %+v", item)
		}
		historical = historical || item.Kind == "memory_history"
		oldDerivative = oldDerivative || item.ID == "old-correction-synthesis"
		oldSelfShown = oldSelfShown || item.ID == oldSelf.ID
	}
	if !historical || !oldDerivative || !oldSelfShown || preview.HistoryCount != 1 {
		t.Fatalf("old derived version omitted from scope: %+v", preview)
	}
	if err := app.ForgetMemoryEntry("source", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	retained, err := app.GetMemoryEntry(edited.ID)
	if err != nil || retained.Content != edited.Content {
		t.Fatalf("independent correction lost: %+v err=%v", retained, err)
	}
	if _, err := app.GetMemoryEntry(laterID); err != nil {
		t.Fatalf("independent correction's new synthesis removed: %v", err)
	}
	if _, err := app.GetMemoryEntry("old-correction-synthesis"); !os.IsNotExist(err) {
		t.Fatalf("old derivative retained forgotten facts: %v", err)
	}
	var selfCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM self_model_entries WHERE id = ?", self.ID).Scan(&selfCount); err != nil || selfCount != 1 {
		t.Fatalf("independent correction's current Self entry removed: count=%d err=%v", selfCount, err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM self_model_entries WHERE id = ?", oldSelf.ID).Scan(&selfCount); err != nil || selfCount != 0 {
		t.Fatalf("old Self retained forgotten facts: count=%d err=%v", selfCount, err)
	}
	versions, err := os.ReadDir(filepath.Join(dir, memoryHistoryDirName, edited.ID))
	if err != nil || len(versions) != 0 {
		t.Fatalf("old derived text retained: entries=%v err=%v", versions, err)
	}
	var indexed int
	if err := app.memoryStore.db.QueryRow("SELECT COUNT(*) FROM memory_chunks WHERE path = ?", edited.ID+".md").Scan(&indexed); err != nil || indexed == 0 {
		t.Fatalf("independent correction removed from search index: count=%d err=%v", indexed, err)
	}
}

func TestForgetMemoryRejectsInvalidArchiveIdentity(t *testing.T) {
	for _, id := range []string{"", ".", "../outside", "/absolute"} {
		t.Run(id, func(t *testing.T) {
			app, dir := newForgetTestApp(t)
			if _, err := app.SaveMemoryEntry("source", "# Source\n\nPrivate fact."); err != nil {
				t.Fatal(err)
			}
			writeMemoryFixture(t, "independent", "Keep independent history")
			writeForgetArchiveFixture(t, dir, "invalid-archive", id, "Bad provenance", "# Derived\n\n- **MergedFrom**: source\n\nOld fact.")
			if _, err := app.GetMemoryForgetPreview("source"); err == nil {
				t.Fatal("preview accepted invalid archive identity")
			}
			if _, err := os.Stat(filepath.Join(dir, memoryHistoryDirName, "independent", "1.md")); err != nil {
				t.Fatalf("invalid archive affected independent history: %v", err)
			}
		})
	}
}

func TestForgetMemoryIgnoresBodyProvenanceLookalikes(t *testing.T) {
	app, dir := newForgetTestApp(t)
	if _, err := app.saveMemoryEntry("source", "# Source\n\nPrivate fact.", false); err != nil {
		t.Fatal(err)
	}
	for id, content := range map[string]string{
		"example":     "# Documentation\n\nA metadata example:\n\n```markdown\n- **MergedFrom**: source\n```\n",
		"okf-example": "---\nid: okf-example\nsource: activity\n---\n# Documentation\n\n- **MergedFrom**: source\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := app.GetMemoryForgetPreview("source")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].ID != "source" {
		t.Fatalf("body example interpreted as actual provenance: %+v", preview.Items)
	}
	if err := app.ForgetMemoryEntry("source", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example", "okf-example"} {
		entry, err := app.GetMemoryEntry(id)
		if err != nil {
			t.Fatal(err)
		}
		if forgotten, err := isMemoryForgotten(dir, id, entry.Content, time.Now()); err != nil || forgotten {
			t.Fatalf("body example silently suppressed: id=%s forgotten=%v err=%v", id, forgotten, err)
		}
	}
}
