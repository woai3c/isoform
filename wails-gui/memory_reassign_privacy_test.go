package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func TestReassignDreamMemoryPreservesPrivacyProvenance(t *testing.T) {
	app := newMemoryInjectionPrivacyFixture(t)
	target, err := app.store.CreatePrincipal(context.Background(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	insertTestSession(t, app.activity.store, "dream-diary-source", "done", 100_000)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET app_names='["Diary"]' WHERE id='dream-diary-source'`); err != nil {
		t.Fatal(err)
	}
	service := &MemoryDreamService{app: app}
	id, err := service.writeSynthesizedMemoryWithProvenance("Dream preference", "## Memory Points\n\n- I prefer reassignmentprivacyneedle private-diary-preference.", "user_insight", []string{"dream-input"}, []memoryReadProvenance{{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"dream-diary-source"}, Apps: []string{"Diary"}}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := parseOKFDocument(entry.Content)
	if !ok {
		t.Fatal("Dream did not write OKF")
	}
	// Reassignment must also preserve provenance fields introduced by other
	// writers, without a whitelist that silently drops future dependencies.
	doc.Frontmatter["source_ids"] = []string{"observation-source"}
	doc.Frontmatter["evaluation_provenance"] = map[string]any{"revision": "evaluation-revision", "evidence": []string{"evidence-source"}}
	path := filepath.Join(app.memoryStore.baseDir, id+".md")
	if err := os.WriteFile(path, []byte(renderOKFDocument(doc.Frontmatter, doc.Body)), 0600); err != nil {
		t.Fatal(err)
	}
	original, _ := parseOKFDocument(renderOKFDocument(doc.Frontmatter, doc.Body))
	if err := app.memoryStore.Sync("dream-reassignment-fixture"); err != nil {
		t.Fatal(err)
	}
	const prompt = "Please help."
	if got := app.injectMemoryContext(context.Background(), prompt, "reassignmentprivacyneedle"); !strings.Contains(got, "private-diary-preference") {
		t.Fatalf("readable Dream fixture was not injected: %q", got)
	}
	pii.AddUserSensitiveApp("com.example.diary", "Diary")
	if err := app.ReassignMemoryAttribution(id, target.ID, "Alex said this"); err != nil {
		t.Fatal(err)
	}
	reassigned, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	after, ok := parseOKFDocument(reassigned.Content)
	if !ok {
		t.Fatalf("reassignment invalidated the OKF header: %s", reassigned.Content)
	}
	for key, value := range original.Frontmatter {
		switch key {
		case "title", "source", "dimension", "timestamp":
			continue
		}
		if !reflect.DeepEqual(after.Frontmatter[key], value) {
			t.Errorf("reassignment changed provenance key %s: got %#v, want %#v", key, after.Frontmatter[key], value)
		}
	}
	if reassigned.Source != "user_edit" || reassigned.Dimension != "topic_highlight" {
		t.Fatalf("wrong correction attribution: %+v", reassigned)
	}
	if app.memorySourceAllowsRead(reassigned.Content) {
		t.Fatal("reassigned Dream lost its sensitive source restriction")
	}
	if err := app.memoryStore.Sync("dream-reassigned"); err != nil {
		t.Fatal(err)
	}
	if got := app.injectMemoryContext(context.Background(), prompt, "reassignmentprivacyneedle"); got != prompt {
		t.Fatalf("reassigned protected Dream entered prompt: %q", got)
	}
	if err := recordMemoryTombstone(app.memoryStore.baseDir, "dream-input", []string{"original source"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if forgotten, err := isMemoryForgotten(app.memoryStore.baseDir, id, reassigned.Content, time.Now()); err != nil || !forgotten {
		t.Fatalf("reassignment lost scoped forgetting dependencies: forgotten=%v err=%v", forgotten, err)
	}
}

func TestReassignUserEditedMemorySucceedsAndKeepsUserAuthority(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	target, err := app.store.CreatePrincipal(context.Background(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.SaveMemoryEntry("corrected", "# Preference\n\n## Memory Points\n\n- I prefer this explicit correction.")
	if err != nil || entry.Source != "user_edit" {
		t.Fatalf("user edit fixture: source=%s err=%v", entry.Source, err)
	}
	if err := app.ReassignMemoryAttribution(entry.ID, target.ID, "Correct speaker"); err != nil {
		t.Fatalf("human reassignment rejected a user edit: %v", err)
	}
	after, err := app.GetMemoryEntry(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Source != "user_edit" || after.Dimension != "topic_highlight" || !strings.Contains(after.Content, "Alex said: I prefer this explicit correction.") {
		t.Fatalf("reassignment lost user correction authority or body: %+v", after)
	}
	if _, err := app.saveMemoryEntry(entry.ID, "# Observed replacement\n\n- Replace the correction.", false); !errors.Is(err, errMemoryUserEdited) {
		t.Fatalf("machine writer replaced reassigned user edit: %v", err)
	}
}
