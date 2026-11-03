package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemorySemanticReviewParaphraseIsSuppressed(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	insertTestSession(t, app.activity.store, "original-session", "done", 100_000)
	insertTestSession(t, app.activity.store, "entirely-new-session", "done", 200_000)
	// This is a model-adapter unit test, not a claim that a live model was run.
	// Both prompts reach the existing no-tools runner and its semantic result
	// represents the complete record, independent of the two surface phrasings.
	runner := &memoryTranslationFakeRunner{ready: true, response: `{"version":"isoform-memory-semantics-v1","facts":[{"subject":"self","relation":"prefer","value":"dark mode","polarity":"positive","qualifiers":[]}]}`}
	app.runner = runner
	original := "# Display preference\n\n- **Source**: activity\n- **Activity Session**: original-session\n\n## Memory Points\n\n- Prefers dark mode.\n"
	if _, err := app.saveMemoryEntry("original-theme", original, false); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview("original-theme")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("original-theme", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	paraphrase := "# Interface choices\n\n- **Source**: activity\n- **Activity Session**: entirely-new-session\n\n## Memory Points\n\n- Likes dark UI themes.\n"
	if _, err := app.saveMemoryEntry("different-source", paraphrase, false); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("same meaning from a new session was accepted: %v", err)
	}
	if runner.calls < 2 {
		t.Fatalf("canonicalization did not inspect both complete records: calls=%d", runner.calls)
	}
}

func semanticReviewResponse(t *testing.T, facts ...memorySemanticFact) string {
	t.Helper()
	data, err := json.Marshal(memorySemanticResponse{Version: memorySemanticVersion, Status: "complete", Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMemorySemanticReviewWholeRecordBoundaries(t *testing.T) {
	base := memorySemanticFact{Subject: "self", Relation: "prefer", Value: "dark mode", Polarity: "positive", Qualifiers: []string{}}
	original, err := decodeMemorySemanticHash(semanticReviewResponse(t, base), "pinned-model")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*memorySemanticFact)
	}{
		{"other person", func(f *memorySemanticFact) { f.Subject = "person:alice" }},
		{"opposite polarity", func(f *memorySemanticFact) { f.Polarity = "negative" }},
		{"opposite value", func(f *memorySemanticFact) { f.Value = "light mode" }},
		{"conditional preference", func(f *memorySemanticFact) { f.Qualifiers = []string{"at night"} }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fact := base
			tt.change(&fact)
			hash, err := decodeMemorySemanticHash(semanticReviewResponse(t, fact), "pinned-model")
			if err != nil || hash == original {
				t.Fatalf("distinct meaning collapsed: hash=%s err=%v", hash, err)
			}
		})
	}
	second := memorySemanticFact{Subject: "self", Relation: "use", Value: "go", Polarity: "positive", Qualifiers: []string{"work", "weekdays"}}
	multi, err := decodeMemorySemanticHash(semanticReviewResponse(t, base, second), "pinned-model")
	if err != nil || multi == original {
		t.Fatalf("shared fact collapsed a different complete record: %v", err)
	}
	second.Qualifiers = []string{" weekdays ", "WORK"}
	reordered, err := decodeMemorySemanticHash(semanticReviewResponse(t, second, base, base), "pinned-model")
	if err != nil || multi != reordered {
		t.Fatalf("fact/qualifier ordering changed same meaning: %v", err)
	}
	otherModel, err := decodeMemorySemanticHash(semanticReviewResponse(t, base), "replacement-model")
	if err != nil || otherModel == original {
		t.Fatalf("model identity not pinned: %v", err)
	}
}

func TestMemorySemanticReviewRejectsInvalidCanonicalResponse(t *testing.T) {
	for _, response := range []string{
		`{"version":"isoform-memory-semantics-v0","facts":[]}`,
		`{"version":"isoform-memory-semantics-v1","status":"uncertain","facts":[]}`,
		`{"version":"isoform-memory-semantics-v1","facts":[{"subject":"anybody","relation":"prefer","value":"dark mode","polarity":"positive","qualifiers":[]}]}`,
		`{"version":"isoform-memory-semantics-v1","facts":[{"subject":"self","relation":"prefer","value":"dark mode","polarity":"maybe","qualifiers":[]}]}`,
		`{"version":"isoform-memory-semantics-v1","facts":[],"unreviewed":true}`,
	} {
		if _, err := decodeMemorySemanticHash(response, "model"); err == nil {
			t.Fatalf("accepted invalid response: %s", response)
		}
	}
}

func TestMemorySemanticReviewObservedOverwriteMustReverifyPreservedRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dark := semanticReviewResponse(t, memorySemanticFact{Subject: "self", Relation: "prefer", Value: "dark mode", Polarity: "positive", Qualifiers: []string{}})
	calendar := semanticReviewResponse(t, memorySemanticFact{Subject: "self", Relation: "check", Value: "calendar", Polarity: "positive", Qualifiers: []string{"each morning"}})
	app := &App{memoryCanonicalizer: func(prompt, _ string) (string, error) {
		if strings.Contains(prompt, "Checks the calendar each morning.") {
			return calendar, nil
		}
		return dark, nil
	}}
	original := "# Display preference\n\n- **Source**: activity\n\nPrefers dark mode.\n"
	retained := "# Calendar\n\n- **Source**: activity\n\nChecks the calendar each morning.\n"
	for id, content := range map[string]string{"forgotten": original, "retained": retained} {
		if _, err := app.saveMemoryEntry(id, content, false); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := app.GetMemoryForgetPreview("forgotten")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("forgotten", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	if forgotten, err := isMemoryForgotten(dir, "retained", retained, time.Now()); err != nil || forgotten {
		t.Fatalf("unchanged retained record hidden: %v, %v", forgotten, err)
	}
	paraphrase := "# Interface choice\n\n- **Source**: activity\n\nI favor night-mode interfaces.\n"
	if _, err := app.saveMemoryEntry("retained", paraphrase, false); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("preserved ID allowed overwritten forgotten meaning: %v", err)
	}
	if _, err := app.SaveMemoryEntry("retained", paraphrase); err != nil {
		t.Fatalf("explicit user correction rejected: %v", err)
	}
}

func TestMemorySemanticReviewDurableReceiptsAreBodyBoundAndRefreshed(t *testing.T) {
	app, dir := newForgetTestApp(t)
	model := effectiveLightTaskModel(app)
	content := "# Allowed observation\n\n- **Source**: activity\n\nChecks the calendar each morning."
	hash, err := app.canonicalMemoryHash(content, model)
	if err != nil {
		t.Fatal(err)
	}
	key := memorySemanticCacheKey(dir, model, content)
	path := filepath.Join(dir, ".semantic", key+".json")
	oldReceipt := memorySemanticReceipt{Version: memorySemanticVersion, Model: model, Hash: hash, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
	data, _ := json.Marshal(oldReceipt)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	memorySemanticCache.Lock()
	memorySemanticCache.values[key] = oldReceipt
	memorySemanticCache.Unlock()
	app.memoryCanonicalizer = func(string, string) (string, error) { return "", errors.New("must reuse prepared canonical result") }
	if _, err := app.canonicalMemoryHash(content, model); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var renewed memorySemanticReceipt
	if err := json.Unmarshal(data, &renewed); err != nil {
		t.Fatal(err)
	}
	if renewed.ExpiresAt < time.Now().Add(29*24*time.Hour).UnixMilli() {
		t.Fatalf("receipt was not renewed for the new observation: %+v", renewed)
	}
	memorySemanticCache.Lock()
	delete(memorySemanticCache.values, key)
	memorySemanticCache.Unlock()
	if got, ok := cachedMemorySemanticHash(dir, model, content); !ok || got != hash {
		t.Fatalf("restart lost allowed observation proof: %q, %v", got, ok)
	}
	if _, ok := cachedMemorySemanticHash(dir, model, content+" A different fact."); ok {
		t.Fatal("receipt accepted changed content")
	}
	if strings.Contains(string(data), "calendar") || strings.Contains(string(data), "Checks") {
		t.Fatal("semantic receipt retained plaintext")
	}
	renewed.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	data, _ = json.Marshal(renewed)
	_ = os.WriteFile(path, data, 0600)
	memorySemanticCache.Lock()
	memorySemanticCache.values[key] = renewed
	memorySemanticCache.Unlock()
	if _, ok := cachedMemorySemanticHash(dir, model, content); ok {
		t.Fatal("expired memory cache receipt remained valid")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired disk receipt remained: %v", err)
	}
}

func TestMemorySemanticReviewUnavailableModelAllowsUnrelatedObservations(t *testing.T) {
	app, dir := newForgetTestApp(t)
	content := "# Removed\n\n- **Source**: activity\n\nA private preference."
	if _, err := app.canonicalMemoryHash(content, effectiveLightTaskModel(app)); err != nil {
		t.Fatal(err)
	}
	prepared, err := app.prepareMemoryForgetSemantics([]string{content})
	if err != nil {
		t.Fatal(err)
	}
	retained := "# Existing\n\n- **Source**: activity\n\nA different observation."
	if err := os.WriteFile(filepath.Join(dir, "retained.md"), []byte(retained), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, "removed", []string{content}, time.Now(), prepared); err != nil {
		t.Fatal(err)
	}
	app.memoryCanonicalizer = func(string, string) (string, error) { return "", errors.New("provider unavailable") }
	unknown := "# New\n\n- **Source**: activity\n\nA new observation."
	if err := app.prepareMemoryObservedWrite(dir, "new", unknown); err != nil {
		t.Fatalf("optional model blocked unrelated observation: %v", err)
	}
	if forgotten, err := isMemoryForgotten(dir, "external", unknown, time.Now()); err != nil || forgotten {
		t.Fatalf("optional model blocked unrelated record: forgotten=%v err=%v", forgotten, err)
	}
	if err := app.prepareMemoryObservedWrite(dir, "retained", retained); err != nil {
		t.Fatalf("existing unchanged record blocked by unavailable model: %v", err)
	}
	if err := app.prepareMemoryObservedWrite(dir, "user", strings.ReplaceAll(unknown, "activity", "user_edit")); err != nil {
		t.Fatalf("user correction blocked: %v", err)
	}
	prepared.Model = "different-model"
	if err := recordMemoryTombstone(dir, "removed-two", []string{content}, time.Now(), prepared); err != nil {
		t.Fatal(err)
	}
	if err := app.prepareMemoryObservedWrite(dir, "new", unknown); err != nil {
		t.Fatalf("model change blocked unrelated observation: %v", err)
	}
	if err := app.prepareMemoryObservedWrite(dir, "new-source", content); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("model change lost local suppression: %v", err)
	}
}

func TestMemorySemanticReviewNoFactualContentCanBeForgotten(t *testing.T) {
	app, _ := newForgetTestApp(t)
	app.memoryCanonicalizer = func(string, string) (string, error) {
		return `{"version":"isoform-memory-semantics-v1","status":"no_facts","facts":[]}`, nil
	}
	if _, err := app.saveMemoryEntry("blank-note", "# Untitled\n\n- **Source**: activity\n", false); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview("blank-note")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("blank-note", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
}
