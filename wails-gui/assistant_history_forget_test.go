package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAssistantHistoryOffForgetsArchivedSourcesAndDerivatives(t *testing.T) {
	app, dir := newForgetTestApp(t)
	activity := newTestStore(t)
	app.activity = &activityMonitorState{store: activity}
	history := renderOKFDocument(map[string]any{"title": "History", "source": assistantHistorySource}, "A history-derived preference.")
	observed := renderOKFDocument(map[string]any{"title": "Observed", "source": "activity"}, "An independent observed preference.")
	archive := writeForgetArchiveFixture(t, dir, "history-archive", "history", "History", history)
	for id, content := range map[string]string{
		"observed":           observed,
		"history-synthesis":  renderOKFDocument(map[string]any{"title": "History synthesis", "source": "dream-synthesis", "merged_from": []string{"history"}}, "A history synthesis."),
		"mixed-synthesis":    renderOKFDocument(map[string]any{"title": "Mixed", "source": "dream-synthesis", "merged_from": []string{"history", "observed"}}, "A mixed synthesis."),
		"observed-synthesis": renderOKFDocument(map[string]any{"title": "Observed synthesis", "source": "dream-synthesis", "merged_from": []string{"observed"}}, "An observed synthesis."),
	} {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	removedSelf := seedMemoryForgetSelf(t, activity, "history-synthesis", "History-derived self learning")
	keptSelf := seedMemoryForgetSelf(t, activity, "observed-synthesis", "Independent self learning")
	portraitPath, err := portraitPath()
	if err != nil {
		t.Fatal(err)
	}
	portrait := &portraitDocument{
		Poles:  []int{0, 0, 0, 0},
		Inputs: []portraitInput{{MemoryID: "history-synthesis", Confirmed: true}, {MemoryID: "observed-synthesis", Confirmed: true}},
		Sentences: []portraitSentence{
			{Role: "insight", Parts: []portraitPart{{Text: "History sentence", IDs: []string{"history-synthesis"}}}},
			{Role: "insight", Parts: []portraitPart{{Text: "Observed sentence", IDs: []string{"observed-synthesis"}}}},
		},
	}
	if err := writePortrait(portraitPath, portrait); err != nil {
		t.Fatal(err)
	}
	knowledgeDir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	for id, source := range map[string]string{"history-knowhow": "history-synthesis", "history-knowhow-child": "history-knowhow", "observed-knowhow": "observed-synthesis", "observed-knowhow-child": "observed-knowhow"} {
		content := renderOKFDocument(map[string]any{"title": id, "source": "observed", "source_ids": []string{source}}, "A reusable item.")
		if err := os.WriteFile(filepath.Join(knowledgeDir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.SetReadAssistantHistory(false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{archive, filepath.Join(dir, "history-synthesis.md"), filepath.Join(dir, "mixed-synthesis.md"), filepath.Join(knowledgeDir, "history-knowhow.md"), filepath.Join(knowledgeDir, "history-knowhow-child.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("history derivative remains at %s: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "observed.md"), filepath.Join(dir, "observed-synthesis.md"), filepath.Join(knowledgeDir, "observed-knowhow.md"), filepath.Join(knowledgeDir, "observed-knowhow-child.md")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("independent record missing at %s: %v", path, err)
		}
	}
	for id, want := range map[string]int{removedSelf.ID: 0, keptSelf.ID: 1} {
		var count int
		if err := activity.db.QueryRow("SELECT COUNT(*) FROM self_model_entries WHERE id = ?", id).Scan(&count); err != nil || count != want {
			t.Errorf("self learning %s: count=%d want=%d err=%v", id, count, want, err)
		}
	}
	portrait, err = readPortrait(portraitPath)
	if err != nil || portrait == nil || len(portrait.Sentences) != 1 || portrait.Sentences[0].text() != "Observed sentence" {
		t.Fatalf("portrait after withdrawal: %+v, %v", portrait, err)
	}
}

func TestAssistantHistoryOffForgetsSelfDreamCompilation(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for id, source := range map[string]string{"history": assistantHistorySource, "observed": "activity"} {
		content := renderOKFDocument(map[string]any{"title": id, "source": source}, "Independent fixture "+id)
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	seedMemoryForgetSelf(t, rig.store, "history", "Draft concise project updates.")
	kept := seedMemoryForgetSelf(t, rig.store, "observed", "Keep independent notes.")
	addSelfDreamTestEntry(t, rig.store, "Draft concise project updates", time.Now().Add(-time.Minute).UnixMilli())
	dream, err := rig.app.runSelfDreamShadow(context.Background(), "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	var compilationID string
	for _, operation := range dream.Operations {
		if operation.Operation != SelfDreamOperationMerge {
			continue
		}
		reviewed, err := rig.app.ReviewSelfDreamOperation(ReviewSelfDreamOperationInput{
			OperationID: operation.ID, IdempotencyKey: "history-forget-merge", Decision: SelfDreamDecisionAccept,
		})
		if err != nil {
			t.Fatal(err)
		}
		compilationID = reviewed.Compilations[0].ID
		break
	}
	if compilationID == "" {
		t.Fatal("fixture did not compile a Self Dream update")
	}
	if err := rig.app.SetReadAssistantHistory(false); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		id    string
		want  int
	}{
		{"SELECT COUNT(*) FROM self_dream_compilations_v1 WHERE id = ?", compilationID, 0},
		{"SELECT COUNT(*) FROM self_model_entries WHERE id = ?", kept.ID, 1},
	} {
		var count int
		if err := rig.store.db.QueryRow(check.query, check.id).Scan(&count); err != nil || count != check.want {
			t.Errorf("count for %s: got %d want %d: %v", check.id, count, check.want, err)
		}
	}
}

func TestAssistantHistoryOffPreservesIndependentEdit(t *testing.T) {
	app, dir := newForgetTestApp(t)
	activity := newTestStore(t)
	app.activity = &activityMonitorState{store: activity}
	original := renderOKFDocument(map[string]any{"title": "History", "source": assistantHistorySource}, "History-derived original.")
	old, err := app.saveMemoryEntry("history", original, false)
	if err != nil {
		t.Fatal(err)
	}
	current, err := app.SaveMemoryEntry("history", "# Correction\n\nAn independent correction.")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		id, content string
	}{
		{"old-derived", old.Content}, {"current-derived", current.Content},
	} {
		ref := taskContextSourceRef(taskContextSourceMemoryKind, "history", 1, fixture.content)
		content := renderOKFDocument(map[string]any{"title": fixture.id, "source": "dream-synthesis", "merged_from": []string{"history"}, "merged_from_refs": []string{ref}}, fixture.id)
		if err := os.WriteFile(filepath.Join(dir, fixture.id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldRef := taskContextSourceRef(taskContextSourceMemoryKind, "history", 1, old.Content)
	oldChild := renderOKFDocument(map[string]any{"title": "Old child", "source": "dream-synthesis", "merged_from": []string{"history"}, "merged_from_refs": []string{oldRef}}, "Old history-derived child.")
	if _, err := app.saveMemoryEntry("corrected-child", oldChild, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveMemoryEntry("corrected-child", "# Child correction\n\nAnother independent correction."); err != nil {
		t.Fatal(err)
	}
	knowledgeDir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ id, content string }{{"old-knowhow", old.Content}, {"current-knowhow", current.Content}} {
		ref := taskContextSourceRef(taskContextSourceMemoryKind, "history", 1, fixture.content)
		content := renderOKFDocument(map[string]any{"title": fixture.id, "source": "observed", "source_ids": []string{"history", ref}}, fixture.id)
		if err := os.WriteFile(filepath.Join(knowledgeDir, fixture.id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var entries []SelfModelEntry
	for _, fixture := range []struct {
		statement, content string
	}{
		{"History-derived self", old.Content}, {"Correction-derived self", current.Content},
	} {
		ref := taskContextSourceRef(taskContextSourceMemoryKind, "history", 1, fixture.content)
		entry, _, err := activity.AddSelfModelEntry(SelfModelEntry{
			Kind: selfModelKindPreference, Scope: "project:yansu", Statement: fixture.statement,
			Status: selfModelStatusActive, Confidence: 0.9, ReasoningStatus: "explicit_user",
			Evidence: []SelfModelEvidence{{SourceKind: taskContextSourceMemoryKind, SourceID: "history", SourceRefs: []string{ref}, Confidence: 1, OccurredAt: time.Now().UnixMilli()}},
		})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	path, err := portraitPath()
	if err != nil {
		t.Fatal(err)
	}
	portrait := &portraitDocument{Poles: []int{0, 0, 0, 0}, Inputs: []portraitInput{{MemoryID: "history", Digest: portraitDigest(old), Confirmed: true}}, Sentences: []portraitSentence{{Role: "insight", Parts: []portraitPart{{Text: "History sentence", IDs: []string{"history"}}}}}}
	if err := writePortrait(path, portrait); err != nil {
		t.Fatal(err)
	}
	if err := app.SetReadAssistantHistory(false); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		id     string
		exists bool
	}{{"old-knowhow", false}, {"current-knowhow", true}} {
		_, err := os.Stat(filepath.Join(knowledgeDir, fixture.id+".md"))
		if (err == nil) != fixture.exists {
			t.Errorf("%s existence: %v", fixture.id, err)
		}
	}
	for _, fixture := range []struct {
		id     string
		exists bool
	}{{"history", true}, {"old-derived", false}, {"current-derived", true}, {"corrected-child", true}} {
		_, err := os.Stat(filepath.Join(dir, fixture.id+".md"))
		if (err == nil) != fixture.exists {
			t.Errorf("%s existence: %v", fixture.id, err)
		}
	}
	versions, err := os.ReadDir(filepath.Join(dir, memoryHistoryDirName, "history"))
	if err != nil && !os.IsNotExist(err) || len(versions) != 0 {
		t.Fatalf("history version remains: %d, %v", len(versions), err)
	}
	childVersions, err := os.ReadDir(filepath.Join(dir, memoryHistoryDirName, "corrected-child"))
	if err != nil && !os.IsNotExist(err) || len(childVersions) != 0 {
		t.Fatalf("history-derived child version remains: %d, %v", len(childVersions), err)
	}
	for i, entry := range entries {
		var count int
		want := i
		if err := activity.db.QueryRow("SELECT COUNT(*) FROM self_model_entries WHERE id = ?", entry.ID).Scan(&count); err != nil || count != want {
			t.Errorf("self entry %d: count=%d want=%d err=%v", i, count, want, err)
		}
	}
	portrait, err = readPortrait(path)
	if err != nil || portrait == nil || len(portrait.Sentences) != 0 {
		t.Fatalf("history portrait remains: %+v, %v", portrait, err)
	}
	forgotten, err := isMemoryForgotten(dir, "history", current.Content, time.Now())
	if err != nil || forgotten {
		t.Fatalf("independent correction was suppressed: %v, %v", forgotten, err)
	}
	for _, fixture := range []struct {
		content string
		want    bool
	}{{old.Content, true}, {current.Content, false}} {
		ref := taskContextSourceRef(taskContextSourceMemoryKind, "history", 1, fixture.content)
		blocked, err := isMemoryHistoricalReferenceForgotten(dir, "history", []string{ref}, time.Now())
		if err != nil || blocked != fixture.want {
			t.Errorf("historical source guard: got %v want %v: %v", blocked, fixture.want, err)
		}
	}
}

func TestAssistantHistoryOffWithdrawsMachineMergedMemory(t *testing.T) {
	app, dir := newForgetTestApp(t)
	old := renderOKFDocument(map[string]any{"title": "History", "source": assistantHistorySource}, "History-derived original.")
	mixed := renderOKFDocument(map[string]any{"title": "Mixed", "source": "activity"}, "History and observed details were merged.")
	if _, err := app.saveMemoryEntry("history", old, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.saveMemoryEntry("history", mixed, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.saveMemoryEntry("observed", renderOKFDocument(map[string]any{"title": "Observed", "source": "activity"}, "Independent observation."), false); err != nil {
		t.Fatal(err)
	}
	if err := app.SetReadAssistantHistory(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history.md")); !os.IsNotExist(err) {
		t.Fatalf("inseparable machine merge remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "observed.md")); err != nil {
		t.Fatalf("independent observation removed: %v", err)
	}
}
