package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newForgetTestApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore(filepath.Dir(dir), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	app := &App{memoryStore: store}
	configureTestMemoryCanonicalizer(t, app)
	return app, dir
}

func TestForgetMemoryRemovesHistoryIndexAndEmbeddingCache(t *testing.T) {
	app, dir := newForgetTestApp(t)
	const id = "private-preference"
	original := "# Private preference\n\n- **Source**: activity\n- **Dimension**: user_insight\n\n## Memory Points\n\n- I prefer the private example.\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := app.SaveMemoryEntry(id, strings.ReplaceAll(original, "private example", "corrected private example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := app.memoryStore.Sync("test"); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{original, updated.Content} {
		for _, chunk := range chunkMarkdown(content, memoryChunkTokens, memoryChunkOverlap) {
			if _, err := app.memoryStore.db.Exec(`INSERT INTO memory_embedding_cache(provider,model,provider_key,hash,embedding) VALUES('test','test','test',?,'[1,0]')`, chunk.Hash); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := app.memoryStore.db.Exec("UPDATE memory_chunks SET embedding = '[1,0]' WHERE path = ?", id+".md"); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(id)
	if err != nil {
		t.Fatal(err)
	}
	if preview.HistoryCount != 1 || preview.EmbeddingCount != 3 || preview.RetentionDays != 30 || len(preview.Items) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if err := app.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, id+".md"), filepath.Join(dir, memoryHistoryDirName, id)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("forgotten path remains: %s, err=%v", path, err)
		}
	}
	for _, table := range []string{"memory_chunks", "memory_chunks_fts", "memory_files", "memory_embedding_cache"} {
		var count int
		if err := app.memoryStore.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s count=%d err=%v", table, count, err)
		}
	}
	// A re-observation under a different ID is rejected at the write boundary.
	_, err = app.saveMemoryEntry("new-source", original, false)
	if !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("reobservation error = %v", err)
	}
	// Even externally restored files cannot repopulate the index.
	if err := os.WriteFile(filepath.Join(dir, "restored.md"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.memoryStore.Sync("restored-test"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := app.memoryStore.db.QueryRow("SELECT COUNT(*) FROM memory_chunks").Scan(&count); err != nil || count != 0 {
		t.Fatalf("forgotten content reindexed: count=%d err=%v", count, err)
	}
}

func TestForgetMemoryRequiresFreshConfirmation(t *testing.T) {
	app, _ := newForgetTestApp(t)
	entry, err := app.SaveMemoryEntry("memory-confirmation", "# Before\n\nBefore body")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveMemoryEntry(entry.ID, "# After\n\nAfter body"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", preview.ConfirmationToken} {
		if err := app.ForgetMemoryEntry(entry.ID, token); err == nil {
			t.Fatal("stale or missing confirmation accepted")
		}
	}
	if _, err := app.GetMemoryEntry(entry.ID); err != nil {
		t.Fatalf("stale confirmation deleted memory: %v", err)
	}
}

func TestMemoryTombstoneExpiresAfterThirtyDaysAndRetainsNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	content := "# Unique private topic\n\n- **Activity Session**: source-private-7\n\n## Memory Points\n\n- A private durable preference.\n"
	if err := recordMemoryTombstone(dir, "original", []string{content}, now); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, id, content string
		at                time.Time
		want              bool
	}{
		{"same id", "original", "entirely changed", now, true},
		{"same body new id", "new", strings.ToUpper(content), now, true},
		{"same source", "new", "# Changed\n\n- **Activity Session**: source-private-7\n\nOther", now, false},
		{"derived", "dream", "# Dream\n\n- **MergedFrom**: other, original\n\nOther", now, true},
		{"unrelated", "new", "# Unrelated\n\nA different fact.", now, false},
		{"last instant", "original", content, now.Add(30*24*time.Hour - time.Millisecond), true},
		{"expired", "original", content, now.Add(30 * 24 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isMemoryForgotten(dir, tc.id, tc.content, tc.at)
			if err != nil || got != tc.want {
				t.Fatalf("forgotten=%t err=%v want=%t", got, err, tc.want)
			}
		})
	}
	data, err := os.ReadFile(filepath.Join(dir, memoryTombstonesFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range []string{"original", "private", "preference", "source-private-7"} {
		if strings.Contains(string(data), plaintext) {
			t.Errorf("tombstone retained %q", plaintext)
		}
	}
}

func TestUserMemoryEditKeepsProvenanceAndRejectsObservedOverwrite(t *testing.T) {
	app, dir := newForgetTestApp(t)
	const id = "user-edited"
	content := "# Preference\n\n- **Source**: activity\n- **Dimension**: deep_user_model\n- **Activity Session**: session-7\n\n## Memory Points\n\n- Corrected preference.\n"
	entry, err := app.SaveMemoryEntry(id, content)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Source != "user_edit" || entry.Dimension != "deep_user_model" || len(entry.ActivitySessionIDs) != 1 || entry.UpdatedAt == 0 {
		t.Fatalf("user edit attribution/provenance = %+v", entry)
	}
	if _, err := app.saveMemoryEntry(id, "# Observed\n\nContradicting observation", false); !errors.Is(err, errMemoryUserEdited) {
		t.Fatalf("observed overwrite error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".md"))
	if err != nil || string(data) != entry.Content {
		t.Fatalf("user edit overwritten: %s err=%v", data, err)
	}
	machine, err := app.saveMemoryEntry("machine", content, false)
	if err != nil || machine.Source != "activity" {
		t.Fatalf("machine incorrectly attributed: %+v err=%v", machine, err)
	}
}

func TestForgetFailsClosedWhenDerivativeStoreUnavailable(t *testing.T) {
	app, dir := newForgetTestApp(t)
	entry, err := app.SaveMemoryEntry("unavailable-store", "# A record\n\nPersonal fact")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "activity.db"), []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetMemoryForgetPreview(entry.ID); err == nil {
		t.Fatal("unavailable derivative storage was reported as an empty scope")
	}
}

func TestObservedMemoryWritersRespectForgetAndUserEdits(t *testing.T) {
	app, dir := newForgetTestApp(t)
	memory := &mcMemoryGenerationResponse{Title: "Observed preference", Dimension: "user_insight", MemoryPoints: []string{"I prefer this observed option."}}
	generator := &MemoryGenerator{app: app}
	source := memorySource("conversation")
	sourceID := "conversation-to-forget"
	id := "conversation-" + memoryForgetHash(string(source) + ":" + sourceID)[:16]
	original := buildOKFMemoryMarkdown(id, memory.Title, time.Now(), source, memory.Dimension, memory.MemoryPoints)
	if _, err := app.saveMemoryEntry(id, original, false); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	memory.MemoryPoints = []string{"A paraphrase from the same conversation."}
	if _, err := generator.saveMemoryFile(source, sourceID, memory); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("generator recreated forgotten source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".md")); !os.IsNotExist(err) {
		t.Fatalf("generator recreated file: %v", err)
	}

	session := ActivitySessionRow{ID: "edited-activity-session"}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}
	activityID := activityMemoryEntryID(session.ID)
	if _, err := app.SaveMemoryEntry(activityID, "# A user's correction\n\nKeep the correction intact."); err != nil {
		t.Fatal(err)
	}
	triager := &ActivityTriager{app: app}
	if _, err := triager.saveActivityMemory(session, group, memory); !errors.Is(err, errMemoryUserEdited) {
		t.Fatalf("activity overwrote user correction: %v", err)
	}
}

func TestMemoryWriteGatesRejectCorruptSuppressionFile(t *testing.T) {
	app, dir := newForgetTestApp(t)
	if err := os.WriteFile(filepath.Join(dir, memoryTombstonesFile), []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.saveMemoryEntry("new-memory", "# A memory\n\nContent", false); err == nil {
		t.Fatal("corrupt suppression was ignored")
	}
}

func TestUserMemoryAttributionAliases(t *testing.T) {
	for _, source := range []string{"user_edit", "manual", "user", "edit"} {
		if !isUserEditedMemory("# Note\n\n- **Source**: " + source + "\n\nContent") {
			t.Errorf("explicit source %q not recognized", source)
		}
	}
	if !isUserEditedMemory("# Note\n\n- **Dimension**: custom\n\nContent") {
		t.Error("legacy user note not recognized")
	}
	if isUserEditedMemory("# Note\n\n- **Source**: activity\n- **Dimension**: user_insight\n\nContent") {
		t.Error("observation classified as a user edit")
	}
}

func writeForgetArchiveFixture(t *testing.T, dir, archiveID, originalID, title, content string) string {
	t.Helper()
	archiveDir := filepath.Join(dir, memoryArchiveDirName)
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(archiveDir, archiveID+".md")
	header := archiveHeaderLines(MemoryEntry{ID: originalID, Title: title}, "dream-fixture", ArchiveReasonExactDup, "")
	if err := os.WriteFile(path, []byte(header+content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestForgetMemoryRemovesArchivedCopiesAndTheirCachedEmbeddings(t *testing.T) {
	app, dir := newForgetTestApp(t)
	const id = "reobserved-after-dream"
	entry, err := app.SaveMemoryEntry(id, "# Current version\n\nThe current observation.")
	if err != nil {
		t.Fatal(err)
	}
	archivedContent := "# Earlier private version\n\nAn older private preference.\n"
	unrelatedContent := "# Unrelated version\n\nAn unrelated saved preference.\n"
	archivePath := writeForgetArchiveFixture(t, dir, "archive-private", id, "Earlier private version", archivedContent)
	unrelatedPath := writeForgetArchiveFixture(t, dir, "archive-unrelated", "another-memory", "Unrelated version", unrelatedContent)
	unrelatedBytes, err := os.ReadFile(unrelatedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{archivedContent, unrelatedContent} {
		for _, chunk := range chunkMarkdown(content, memoryChunkTokens, memoryChunkOverlap) {
			if _, err := app.memoryStore.db.Exec(`INSERT INTO memory_embedding_cache(provider,model,provider_key,hash,embedding) VALUES('test','test','test',?,'[1,0]')`, chunk.Hash); err != nil {
				t.Fatal(err)
			}
		}
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.HistoryCount != 1 || preview.EmbeddingCount != 1 || len(preview.Items) != 2 {
		t.Fatalf("archive missing from preview: %+v", preview)
	}
	var archiveShown bool
	for _, item := range preview.Items {
		if item.Kind == "memory_archive" && item.ID == "archive-private" && item.Title == "Earlier private version" {
			archiveShown = true
		}
	}
	if !archiveShown {
		t.Fatalf("archived record missing from removal list: %+v", preview.Items)
	}
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("forgotten archive remains: %v", err)
	}
	retained, err := os.ReadFile(unrelatedPath)
	if err != nil || string(retained) != string(unrelatedBytes) {
		t.Fatalf("unrelated archive changed: err=%v", err)
	}
	for _, chunk := range chunkMarkdown(archivedContent, memoryChunkTokens, memoryChunkOverlap) {
		var count int
		if err := app.memoryStore.db.QueryRow("SELECT COUNT(*) FROM memory_embedding_cache WHERE hash = ?", chunk.Hash).Scan(&count); err != nil || count != 0 {
			t.Errorf("archived embedding remains: count=%d err=%v", count, err)
		}
	}
	var retainedEmbeddings int
	if err := app.memoryStore.db.QueryRow("SELECT COUNT(*) FROM memory_embedding_cache").Scan(&retainedEmbeddings); err != nil || retainedEmbeddings != 1 {
		t.Fatalf("unrelated cached embedding changed: count=%d err=%v", retainedEmbeddings, err)
	}
	forgotten, err := isMemoryForgotten(dir, "another-source", archivedContent, time.Now())
	if err != nil || !forgotten {
		t.Fatalf("archived content omitted from tombstone: forgotten=%v err=%v", forgotten, err)
	}
}

func TestForgetMemoryRejectsPreviewWhenArchiveIsAdded(t *testing.T) {
	app, dir := newForgetTestApp(t)
	entry, err := app.SaveMemoryEntry("archive-confirmation", "# Current\n\nCurrent observation.")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, memoryArchiveDirName)); !os.IsNotExist(err) {
		t.Fatalf("preview created an archive directory: %v", err)
	}
	archivePath := writeForgetArchiveFixture(t, dir, "archive-added-later", entry.ID, "Old version", "# Old version\n\nPrivate old content.\n")
	if err := app.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err == nil {
		t.Fatal("confirmation omitted an archive added after preview")
	}
	if _, err := app.GetMemoryEntry(entry.ID); err != nil {
		t.Fatalf("stale confirmation removed live record: %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("stale confirmation removed archive: %v", err)
	}
	fresh, err := app.GetMemoryForgetPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ConfirmationToken == preview.ConfirmationToken || fresh.HistoryCount != 1 {
		t.Fatalf("new archive did not change confirmation: %+v", fresh)
	}
}
