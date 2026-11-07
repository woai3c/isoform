package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/storage"
)

// setupSettledAtlasHistory writes one history row bound to the atlas note, with
// the note aged so it settles, and returns the turn context and the note path.
func setupSettledAtlasHistory(t *testing.T) (*App, string, context.Context, string, string) {
	t.Helper()
	a, tag := topicChatTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "atlas.md")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(note, old, old); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := a.topicChatAdmissionMetadataFromSources(t.Context(), []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}, a.collectTodayChatSources(t.Context(), time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "w-0", Kind: "user", Content: "recent topic", TopicID: "atlas", AutomationContext: metadata}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.finishChatPreparation(tag, cancel) })
	return a, tag, ctx, note, string(original)
}

// shownHistory reports whether the turn's history is kept: one row, bound.
func shownHistory(t *testing.T, a *App, tag string, ctx context.Context) bool {
	t.Helper()
	rows, bound, err := a.sourceBoundChatHistory(ctx, tag)
	if err != nil {
		t.Fatalf("history read failed: %v", err)
	}
	return len(rows) == 1 && bound
}

func writeNote(t *testing.T, note, body string) {
	t.Helper()
	if err := os.WriteFile(note, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// Requirements 1-4 and 6: the history follows the note's words.
func TestTopicHistoryFollowsTheNotesWords(t *testing.T) {
	a, tag, ctx, note, original := setupSettledAtlasHistory(t)
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("before any edit the history should be kept")
	}
	// A blank line: the same words, so the history stays.
	writeNote(t, note, "# Atlas\n\nCurrent release plan")
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("a blank line withdrew the history")
	}
	// A save with the same words (the file is rewritten, nothing else changes).
	writeNote(t, note, "# Atlas\n\nCurrent release plan\n")
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("a save with the same words withdrew the history")
	}
	// A word changed: the history rests on words that are gone, so it is withdrawn.
	writeNote(t, note, "# Atlas\nUnrelated errands")
	if shownHistory(t, a, tag, ctx) {
		t.Fatal("a changed word did not withdraw the history")
	}
	// The original words come back: the history comes back with them.
	writeNote(t, note, original)
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("restoring the words did not bring the history back")
	}
}

// Requirement 5: an old-form proof is accepted while the file still matches it,
// and it is upgraded to the current form in the stored row.
func TestTopicHistoryOldFormProofStaysBoundAndIsUpgraded(t *testing.T) {
	a, tag := topicChatTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "atlas.md")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(note, old, old); err != nil {
		t.Fatal(err)
	}
	refs := []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}
	sources := a.collectTodayChatSources(t.Context(), time.Now())
	legacy, err := a.chatTopicLegacyFingerprintFromSources(t.Context(), refs, sources, map[string]topicChatFingerprintInput{})
	if err != nil {
		t.Fatal(err)
	}
	oldProof, err := json.Marshal(struct {
		TopicContext topicChatProof `json:"topicContext"`
	}{topicChatProof{Version: 1, Refs: refs, Fingerprint: legacy}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "w-0", Kind: "user", Content: "recent topic", TopicID: "atlas", AutomationContext: string(oldProof)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	defer a.finishChatPreparation(tag, cancel)
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("an old-form proof on an unchanged note withdrew the history")
	}
	rows, err := a.store.LoadStreamItems(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		TopicContext topicChatProof `json:"topicContext"`
	}
	if err := json.Unmarshal([]byte(rows[len(rows)-1].AutomationContext), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.TopicContext.Version != topicChatProofVersion {
		t.Fatalf("the stored proof was not upgraded: version=%d", stored.TopicContext.Version)
	}
}

// Requirement 5, the limit: an old-form proof holds the file time, so a restore
// of the same words cannot bring it back. It stays withdrawn.
func TestTopicHistoryOldFormProofDoesNotReturnAfterAnEdit(t *testing.T) {
	a, tag := topicChatTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "atlas.md")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(note, old, old); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	refs := []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}
	legacy, err := a.chatTopicLegacyFingerprintFromSources(t.Context(), refs, a.collectTodayChatSources(t.Context(), time.Now()), map[string]topicChatFingerprintInput{})
	if err != nil {
		t.Fatal(err)
	}
	oldProof, _ := json.Marshal(struct {
		TopicContext topicChatProof `json:"topicContext"`
	}{topicChatProof{Version: 1, Refs: refs, Fingerprint: legacy}})
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "w-0", Kind: "user", Content: "recent topic", TopicID: "atlas", AutomationContext: string(oldProof)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	defer a.finishChatPreparation(tag, cancel)
	writeNote(t, note, "# Atlas\nUnrelated errands")
	if shownHistory(t, a, tag, ctx) {
		t.Fatal("a changed word did not withdraw the old-form history")
	}
	writeNote(t, note, string(original))
	if shownHistory(t, a, tag, ctx) {
		t.Fatal("an old-form proof came back after a restore; it holds the file time and must not")
	}
}

// Closed Eyes is a gate that runs first: pausing hides the history even when
// the words still match, and unpausing shows it again.
func TestTopicHistoryClosedEyesStillWithholdsWhenWordsMatch(t *testing.T) {
	a, tag, ctx, _, _ := setupSettledAtlasHistory(t)
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("before pausing the history should be kept")
	}
	a.closedEyes.enabled.Store(true)
	if shownHistory(t, a, tag, ctx) {
		t.Fatal("Closed Eyes did not withhold the history")
	}
	a.closedEyes.enabled.Store(false)
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("the history did not come back when Closed Eyes ended with the words unchanged")
	}
}

// A forgotten note recreated with the same words must not bring its history
// back: the forget tombstone suppresses the note, so the proof has no source.
func TestTopicHistoryForgottenNoteDoesNotReturnWithTheSameWords(t *testing.T) {
	a, tag, ctx, note, original := setupSettledAtlasHistory(t)
	if !shownHistory(t, a, tag, ctx) {
		t.Fatal("before the forget the history should be kept")
	}
	preview, err := a.GetMemoryForgetPreview("atlas")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ForgetMemoryEntry("atlas", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	if shownHistory(t, a, tag, ctx) {
		t.Fatal("the forget did not withhold the history")
	}
	writeNote(t, note, original)
	if shownHistory(t, a, tag, ctx) {
		t.Fatal("a forgotten note recreated with the same words brought its history back")
	}
}
