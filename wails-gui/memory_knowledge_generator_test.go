package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/storage"

	"yansu-agent/agent"
)

type memoryTranslationFakeRunner struct {
	response   string
	responses  []string
	errors     []error
	lastPrompt string
	prompts    []string
	calls      int
	ready      bool
	permission string
}

func (r *memoryTranslationFakeRunner) Init(ctx context.Context) error { return nil }
func (r *memoryTranslationFakeRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.calls++
	r.lastPrompt = input.Prompt
	r.prompts = append(r.prompts, input.Prompt)
	if len(r.errors) > 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		if err != nil {
			return "", err
		}
	}
	response := r.response
	if len(r.responses) > 0 {
		response = r.responses[0]
		r.responses = r.responses[1:]
	}
	if err := callback(&agent.StreamEvent{Type: "assistant", Content: response}); err != nil {
		return "", err
	}
	return "run-test", nil
}
func (r *memoryTranslationFakeRunner) CancelRun(runID string) error { return nil }
func (r *memoryTranslationFakeRunner) RespondToPermission(sessionID, optionID string) error {
	r.permission = optionID
	return nil
}
func (r *memoryTranslationFakeRunner) CancelPermission(sessionID string) {}
func (r *memoryTranslationFakeRunner) Shutdown() error                   { return nil }
func (r *memoryTranslationFakeRunner) IsReady() bool                     { return r.ready }

func newTranslationTestApp(t *testing.T, runner *memoryTranslationFakeRunner) *App {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{
		store: store, runner: runner,
		// These fixtures reserve runner responses for translation and generation.
		// Semantic matching has its own receipt and adapter unit tests.
		memoryCanonicalizer: func(string, string) (string, error) {
			return "", errMemorySemanticUnverified
		},
	}
}

func TestCurrentUILanguageReadsUIState(t *testing.T) {
	runner := &memoryTranslationFakeRunner{response: "unused", ready: true}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	got := app.currentUILanguage()
	if got != "zh-CN" {
		t.Fatalf("currentUILanguage() = %q, want zh-CN", got)
	}
	if name := app.currentUILanguageName(); name != "Simplified Chinese" {
		t.Fatalf("currentUILanguageName() = %q, want Simplified Chinese", name)
	}
}

func TestResponseLanguageInstructionMentionsUILanguage(t *testing.T) {
	got := responseLanguageInstruction("Simplified Chinese")
	if !strings.Contains(got, "Simplified Chinese") {
		t.Fatalf("language instruction = %q, want target language name", got)
	}
}

func TestTranslateMemoryEntryToCurrentLanguageSnapshotsBeforeWrite(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	original := "# Original\n\n- **Updated**: 2026-05-11 10:00\n\n## Memory Points\n\n- Original point\n"
	if err := os.WriteFile(filepath.Join(dir, "memory-1.md"), []byte(original), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		response: "# 已翻译\n\n- **Updated**: 2026-05-11 10:00\n\n## Memory Points\n\n- 已翻译的要点\n",
		ready:    true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	entry, err := app.TranslateMemoryEntryToCurrentLanguage("memory-1")
	if err != nil {
		t.Fatalf("TranslateMemoryEntryToCurrentLanguage: %v", err)
	}
	if !strings.Contains(entry.Content, "已翻译") {
		t.Fatalf("translated content = %q, want translated text", entry.Content)
	}
	if !strings.Contains(runner.lastPrompt, "Simplified Chinese") {
		t.Fatalf("prompt = %q, want current UI language", runner.lastPrompt)
	}

	versions, err := app.ListMemoryVersions("memory-1")
	if err != nil {
		t.Fatalf("ListMemoryVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions))
	}
	snapshot, err := app.GetMemoryVersion("memory-1", versions[0].TimestampMs)
	if err != nil {
		t.Fatalf("GetMemoryVersion: %v", err)
	}
	if snapshot != original {
		t.Fatalf("snapshot = %q, want original", snapshot)
	}
}

func TestTranslateMemoryEntryPreservesMetadataOutsideModelInput(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	original := "# Original UI Preference\n\n- **Updated**: 2026-05-11 16:43\n- **Source**: slack\n- **Dimension**: user_insight\n\n## Memory Points\n\n- User dislikes sloppy purple palettes\n"
	if err := os.WriteFile(filepath.Join(dir, "memory-1.md"), []byte(original), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		response: "---\n# 原始 UI 偏好\n\n- **Updated**: translated date should be ignored\n- **Source**: translated source should be ignored\n- **Dimension**: translated_dimension\n\n## Memory Points\n\n- 用户不喜欢随意的紫色配色\n---\n",
		ready:    true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	entry, err := app.TranslateMemoryEntryToCurrentLanguage("memory-1")
	if err != nil {
		t.Fatalf("TranslateMemoryEntryToCurrentLanguage: %v", err)
	}

	if strings.Contains(runner.lastPrompt, "2026-05-11 16:43") || strings.Contains(runner.lastPrompt, "user_insight") {
		t.Fatalf("prompt should not include metadata values, got:\n%s", runner.lastPrompt)
	}
	for _, want := range []string{
		"- **Updated**: 2026-05-11 16:43",
		"- **Source**: slack",
		"- **Dimension**: user_insight",
		"- 用户不喜欢随意的紫色配色",
	} {
		if !strings.Contains(entry.Content, want) {
			t.Fatalf("translated content missing %q:\n%s", want, entry.Content)
		}
	}
	if strings.Contains(entry.Content, "translated date should be ignored") || strings.Contains(entry.Content, "translated_dimension") {
		t.Fatalf("translated metadata leaked into content:\n%s", entry.Content)
	}
	if strings.Contains(entry.Content, "---") {
		t.Fatalf("translation envelope leaked into content:\n%s", entry.Content)
	}
	if count := strings.Count(entry.Content, "- **Updated**:"); count != 1 {
		t.Fatalf("Updated metadata count = %d, want 1:\n%s", count, entry.Content)
	}
}

func TestTranslateAllMemoryVersionsTranslatesSnapshotsInPlace(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	current := "# Current\n\n## Memory Points\n\n- Current point\n"
	if err := os.WriteFile(filepath.Join(dir, "memory-1.md"), []byte(current), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	historyDir, err := memoryHistoryDir(dir, "memory-1")
	if err != nil {
		t.Fatalf("memoryHistoryDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "1000.md"), []byte("# Old A\n"), 0600); err != nil {
		t.Fatalf("write history a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "2000.md"), []byte("# Old B\n"), 0600); err != nil {
		t.Fatalf("write history b: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		responses: []string{"# 已翻译 B\n", "# 已翻译 A\n"},
		ready:     true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	versions, err := app.translateAllMemoryVersionsToLanguage("memory-1", app.currentUILanguageName())
	if err != nil {
		t.Fatalf("translateAllMemoryVersionsToLanguage: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want 2", runner.calls)
	}
	live, err := os.ReadFile(filepath.Join(dir, "memory-1.md"))
	if err != nil {
		t.Fatalf("read live memory: %v", err)
	}
	if string(live) != current {
		t.Fatalf("live memory changed = %q, want %q", string(live), current)
	}
	newest, err := app.GetMemoryVersion("memory-1", 2000)
	if err != nil {
		t.Fatalf("GetMemoryVersion newest: %v", err)
	}
	if !strings.Contains(newest, "已翻译 B") {
		t.Fatalf("newest snapshot = %q, want translated B", newest)
	}
	oldest, err := app.GetMemoryVersion("memory-1", 1000)
	if err != nil {
		t.Fatalf("GetMemoryVersion oldest: %v", err)
	}
	if !strings.Contains(oldest, "已翻译 A") {
		t.Fatalf("oldest snapshot = %q, want translated A", oldest)
	}
}

func TestTranslateKnowledgeEntryToCurrentLanguageSnapshotsBeforeWrite(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	original := "# Original Knowledge\n\n- **Date**: 2026-05-11 10:00\n\n## Key Insights\n\n- Original insight\n"
	if err := os.WriteFile(filepath.Join(dir, "knowledge-1.md"), []byte(original), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		response: "# 已翻译知识\n\n- **Date**: 2026-05-11 10:00\n\n## Key Insights\n\n- 已翻译洞见\n",
		ready:    true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	entry, err := app.TranslateKnowledgeEntryToCurrentLanguage("knowledge-1")
	if err != nil {
		t.Fatalf("TranslateKnowledgeEntryToCurrentLanguage: %v", err)
	}
	if !strings.Contains(entry.Content, "已翻译") {
		t.Fatalf("translated content = %q, want translated text", entry.Content)
	}
	if !strings.Contains(runner.lastPrompt, "Simplified Chinese") {
		t.Fatalf("prompt = %q, want current UI language", runner.lastPrompt)
	}

	versions, err := app.ListKnowledgeVersions("knowledge-1")
	if err != nil {
		t.Fatalf("ListKnowledgeVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions))
	}
	snapshot, err := app.GetKnowledgeVersion("knowledge-1", versions[0].TimestampMs)
	if err != nil {
		t.Fatalf("GetKnowledgeVersion: %v", err)
	}
	if snapshot != original {
		t.Fatalf("snapshot = %q, want original", snapshot)
	}
}

func TestGetKnowledgeEntries_SkipsOKFReservedFiles(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	for name, body := range map[string]string{
		"index.md":     "# Index\n",
		"log.md":       "# Log\n",
		"knowledge.md": "# Real Knowledge\n\n- **Date**: 2026-06-22 10:00\n\n## Key Insights\n\n- real\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	app := &App{}
	entries, err := app.GetKnowledgeEntries()
	if err != nil {
		t.Fatalf("GetKnowledgeEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "knowledge" {
		t.Fatalf("entries = %+v, want only knowledge", entries)
	}
}

func TestTranslateKnowledgeEntryPreservesMetadataOutsideModelInput(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	original := "# Original Knowledge\n\n- **Date**: 2026-05-11 16:43\n- **Source**: slack\n- **Apps**: Code, Chrome\n\n## Key Insights\n\n- UI palettes should follow product tone\n"
	if err := os.WriteFile(filepath.Join(dir, "knowledge-1.md"), []byte(original), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		response: "# 原始知识\n\n- **Date**: translated date should be ignored\n- **Source**: translated source should be ignored\n- **Apps**: translated apps\n\n## Key Insights\n\n- UI 配色应遵循产品调性\n",
		ready:    true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	entry, err := app.TranslateKnowledgeEntryToCurrentLanguage("knowledge-1")
	if err != nil {
		t.Fatalf("TranslateKnowledgeEntryToCurrentLanguage: %v", err)
	}

	if strings.Contains(runner.lastPrompt, "2026-05-11 16:43") || strings.Contains(runner.lastPrompt, "Code, Chrome") {
		t.Fatalf("prompt should not include metadata values, got:\n%s", runner.lastPrompt)
	}
	for _, want := range []string{
		"- **Date**: 2026-05-11 16:43",
		"- **Source**: slack",
		"- **Apps**: Code, Chrome",
		"- UI 配色应遵循产品调性",
	} {
		if !strings.Contains(entry.Content, want) {
			t.Fatalf("translated content missing %q:\n%s", want, entry.Content)
		}
	}
	if strings.Contains(entry.Content, "translated date should be ignored") || strings.Contains(entry.Content, "translated apps") {
		t.Fatalf("translated metadata leaked into content:\n%s", entry.Content)
	}
	if count := strings.Count(entry.Content, "- **Date**:"); count != 1 {
		t.Fatalf("Date metadata count = %d, want 1:\n%s", count, entry.Content)
	}
}

func TestTranslateAllKnowledgeVersionsTranslatesSnapshotsInPlace(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	current := "# Current Knowledge\n\n## Key Insights\n\n- Current insight\n"
	if err := os.WriteFile(filepath.Join(dir, "knowledge-1.md"), []byte(current), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}
	historyDir, err := knowledgeHistoryDir(dir, "knowledge-1")
	if err != nil {
		t.Fatalf("knowledgeHistoryDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "1000.md"), []byte("# Old Knowledge A\n"), 0600); err != nil {
		t.Fatalf("write knowledge history a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "2000.md"), []byte("# Old Knowledge B\n"), 0600); err != nil {
		t.Fatalf("write knowledge history b: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		responses: []string{"# 已翻译知识 B\n", "# 已翻译知识 A\n"},
		ready:     true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	versions, err := app.translateAllKnowledgeVersionsToLanguage("knowledge-1", app.currentUILanguageName())
	if err != nil {
		t.Fatalf("translateAllKnowledgeVersionsToLanguage: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want 2", runner.calls)
	}
	live, err := os.ReadFile(filepath.Join(dir, "knowledge-1.md"))
	if err != nil {
		t.Fatalf("read live knowledge: %v", err)
	}
	if string(live) != current {
		t.Fatalf("live knowledge changed = %q, want %q", string(live), current)
	}
	newest, err := app.GetKnowledgeVersion("knowledge-1", 2000)
	if err != nil {
		t.Fatalf("GetKnowledgeVersion newest: %v", err)
	}
	if !strings.Contains(newest, "已翻译知识 B") {
		t.Fatalf("newest snapshot = %q, want translated B", newest)
	}
	oldest, err := app.GetKnowledgeVersion("knowledge-1", 1000)
	if err != nil {
		t.Fatalf("GetKnowledgeVersion oldest: %v", err)
	}
	if !strings.Contains(oldest, "已翻译知识 A") {
		t.Fatalf("oldest snapshot = %q, want translated A", oldest)
	}
}

// translateCurrentEntriesForTest runs the work the Start* translation bindings
// do in the background, synchronously, so tests can read the summary.
func translateCurrentEntriesForTest(
	app *App,
	memories, knowledge bool,
	req GlobalTranslationRequest,
) (GlobalTranslationBatchSummary, error) {
	languageName, err := app.resolveGlobalTranslationLanguage(req)
	if err != nil {
		return GlobalTranslationBatchSummary{}, err
	}
	summary := GlobalTranslationBatchSummary{
		TargetLanguage: languageName,
		IncludeHistory: req.IncludeHistory,
	}
	if memories {
		entries, err := app.GetMemoryEntries()
		if err != nil {
			return summary, err
		}
		app.translateMemoryEntriesToLanguage(entries, languageName, req.IncludeHistory, &summary)
	}
	if knowledge {
		entries, err := app.GetKnowledgeEntries()
		if err != nil {
			return summary, err
		}
		app.translateKnowledgeEntriesToLanguage(entries, languageName, req.IncludeHistory, &summary)
	}
	return summary, nil
}

func TestTranslateCurrentMemoryAndKnowledgeEntriesUsesExplicitTargetLanguage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	memDir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	knowledgeDir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	memoryOriginal := "# 记忆\n\n- **Updated**: 2026-05-11 10:00\n\n## Memory Points\n\n- 用户喜欢中文界面\n"
	knowledgeOriginal := "# 知识\n\n- **Date**: 2026-05-11 10:00\n\n## Key Insights\n\n- 批量翻译需要摘要\n"
	if err := os.WriteFile(filepath.Join(memDir, "memory-1.md"), []byte(memoryOriginal), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(knowledgeDir, "knowledge-1.md"), []byte(knowledgeOriginal), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		responses: []string{
			"# Memory\n\n- **Updated**: 2026-05-11 10:00\n\n## Memory Points\n\n- The user prefers a Chinese UI\n",
			"# Knowledge\n\n- **Date**: 2026-05-11 10:00\n\n## Key Insights\n\n- Batch translation needs a summary\n",
		},
		ready: true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	summary, err := translateCurrentEntriesForTest(app, true, true, GlobalTranslationRequest{
		Target: "English",
	})
	if err != nil {
		t.Fatalf("translateCurrentEntriesForTest: %v", err)
	}
	if summary.TargetLanguage != "English" {
		t.Fatalf("TargetLanguage = %q, want English", summary.TargetLanguage)
	}
	if summary.Total != 2 || summary.Translated != 2 || summary.Failed != 0 {
		t.Fatalf("summary counts = total %d translated %d failed %d, want 2/2/0", summary.Total, summary.Translated, summary.Failed)
	}
	if summary.Memories.Translated != 1 || summary.Knowledge.Translated != 1 {
		t.Fatalf("kind counts = memories %+v knowledge %+v, want one translated each", summary.Memories, summary.Knowledge)
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want 2", runner.calls)
	}
	for _, prompt := range runner.prompts {
		if !strings.Contains(prompt, "English") {
			t.Fatalf("prompt = %q, want explicit English target", prompt)
		}
		if strings.Contains(prompt, "Simplified Chinese") {
			t.Fatalf("prompt = %q, should not follow current UI language when explicit target is English", prompt)
		}
	}

	memEntry, err := app.GetMemoryEntry("memory-1")
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}
	if !strings.Contains(memEntry.Content, "Chinese UI") {
		t.Fatalf("memory content = %q, want English translation", memEntry.Content)
	}
	memVersions, err := app.ListMemoryVersions("memory-1")
	if err != nil {
		t.Fatalf("ListMemoryVersions: %v", err)
	}
	if len(memVersions) != 1 {
		t.Fatalf("memory versions = %d, want 1", len(memVersions))
	}
	memSnapshot, err := app.GetMemoryVersion("memory-1", memVersions[0].TimestampMs)
	if err != nil {
		t.Fatalf("GetMemoryVersion: %v", err)
	}
	if memSnapshot != memoryOriginal {
		t.Fatalf("memory snapshot = %q, want original", memSnapshot)
	}

	knowledgeEntry, err := app.GetKnowledgeEntry("knowledge-1")
	if err != nil {
		t.Fatalf("GetKnowledgeEntry: %v", err)
	}
	if !strings.Contains(knowledgeEntry.Content, "Batch translation") {
		t.Fatalf("knowledge content = %q, want English translation", knowledgeEntry.Content)
	}
	knowledgeVersions, err := app.ListKnowledgeVersions("knowledge-1")
	if err != nil {
		t.Fatalf("ListKnowledgeVersions: %v", err)
	}
	if len(knowledgeVersions) != 1 {
		t.Fatalf("knowledge versions = %d, want 1", len(knowledgeVersions))
	}
	knowledgeSnapshot, err := app.GetKnowledgeVersion("knowledge-1", knowledgeVersions[0].TimestampMs)
	if err != nil {
		t.Fatalf("GetKnowledgeVersion: %v", err)
	}
	if knowledgeSnapshot != knowledgeOriginal {
		t.Fatalf("knowledge snapshot = %q, want original", knowledgeSnapshot)
	}
}

func TestTranslateCurrentMemoryAndKnowledgeEntriesIncludesHistoryWithExplicitTargetLanguage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	memDir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	knowledgeDir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "memory-1.md"), []byte("# 当前记忆\n\n## Memory Points\n\n- 当前内容\n"), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	memHistoryDir, err := memoryHistoryDir(memDir, "memory-1")
	if err != nil {
		t.Fatalf("memoryHistoryDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memHistoryDir, "1000.md"), []byte("# 历史记忆\n"), 0600); err != nil {
		t.Fatalf("write memory history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(knowledgeDir, "knowledge-1.md"), []byte("# 当前知识\n\n## Key Insights\n\n- 当前洞见\n"), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}
	knowledgeHistoryDir, err := knowledgeHistoryDir(knowledgeDir, "knowledge-1")
	if err != nil {
		t.Fatalf("knowledgeHistoryDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(knowledgeHistoryDir, "1000.md"), []byte("# 历史知识\n"), 0600); err != nil {
		t.Fatalf("write knowledge history: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		responses: []string{
			"# Current Memory\n\n## Memory Points\n\n- Current content\n",
			"# Original Current Memory Snapshot\n",
			"# Historical Memory\n",
			"# Current Knowledge\n\n## Key Insights\n\n- Current insight\n",
			"# Original Current Knowledge Snapshot\n",
			"# Historical Knowledge\n",
		},
		ready: true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}

	summary, err := translateCurrentEntriesForTest(app, true, true, GlobalTranslationRequest{
		Target:         "English",
		IncludeHistory: true,
	})
	if err != nil {
		t.Fatalf("translateCurrentEntriesForTest: %v", err)
	}
	if summary.TargetLanguage != "English" || !summary.IncludeHistory {
		t.Fatalf("summary target/includeHistory = %q/%v, want English/true", summary.TargetLanguage, summary.IncludeHistory)
	}
	if summary.Total != 4 || summary.Translated != 4 || summary.Failed != 0 {
		t.Fatalf("summary counts = total %d translated %d failed %d, want 4/4/0", summary.Total, summary.Translated, summary.Failed)
	}
	if summary.Memories.Total != 2 || summary.Memories.Translated != 2 || summary.Knowledge.Total != 2 || summary.Knowledge.Translated != 2 {
		t.Fatalf("kind counts = memories %+v knowledge %+v, want 2 translated each", summary.Memories, summary.Knowledge)
	}
	if len(summary.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(summary.Items))
	}
	for _, prompt := range runner.prompts {
		if !strings.Contains(prompt, "English") {
			t.Fatalf("prompt = %q, want explicit English target", prompt)
		}
		if strings.Contains(prompt, "Simplified Chinese") {
			t.Fatalf("prompt = %q, should not follow current UI language when explicit target is English", prompt)
		}
	}

	memEntry, err := app.GetMemoryEntry("memory-1")
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}
	if !strings.Contains(memEntry.Content, "Current content") {
		t.Fatalf("memory content = %q, want English live translation", memEntry.Content)
	}
	memHistorical, err := app.GetMemoryVersion("memory-1", 1000)
	if err != nil {
		t.Fatalf("GetMemoryVersion existing history: %v", err)
	}
	if !strings.Contains(memHistorical, "Historical Memory") {
		t.Fatalf("memory history = %q, want English history translation", memHistorical)
	}
	memVersions, err := app.ListMemoryVersions("memory-1")
	if err != nil {
		t.Fatalf("ListMemoryVersions: %v", err)
	}
	if len(memVersions) != 2 {
		t.Fatalf("memory versions = %d, want existing plus live snapshot", len(memVersions))
	}

	knowledgeEntry, err := app.GetKnowledgeEntry("knowledge-1")
	if err != nil {
		t.Fatalf("GetKnowledgeEntry: %v", err)
	}
	if !strings.Contains(knowledgeEntry.Content, "Current insight") {
		t.Fatalf("knowledge content = %q, want English live translation", knowledgeEntry.Content)
	}
	knowledgeHistorical, err := app.GetKnowledgeVersion("knowledge-1", 1000)
	if err != nil {
		t.Fatalf("GetKnowledgeVersion existing history: %v", err)
	}
	if !strings.Contains(knowledgeHistorical, "Historical Knowledge") {
		t.Fatalf("knowledge history = %q, want English history translation", knowledgeHistorical)
	}
	knowledgeVersions, err := app.ListKnowledgeVersions("knowledge-1")
	if err != nil {
		t.Fatalf("ListKnowledgeVersions: %v", err)
	}
	if len(knowledgeVersions) != 2 {
		t.Fatalf("knowledge versions = %d, want existing plus live snapshot", len(knowledgeVersions))
	}
}

func TestTranslateCurrentMemoryAndKnowledgeEntriesReturnsFailureSummaryAndContinues(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	memDir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	knowledgeDir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	memoryOriginal := "# Original Memory\n\n## Memory Points\n\n- Original memory point\n"
	knowledgeOriginal := "# Original Knowledge\n\n## Key Insights\n\n- Original insight\n"
	if err := os.WriteFile(filepath.Join(memDir, "memory-1.md"), []byte(memoryOriginal), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(knowledgeDir, "knowledge-1.md"), []byte(knowledgeOriginal), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		errors:    []error{context.DeadlineExceeded, nil},
		responses: []string{"# Translated Knowledge\n\n## Key Insights\n\n- Translated insight\n"},
		ready:     true,
	}
	app := newTranslationTestApp(t, runner)

	summary, err := translateCurrentEntriesForTest(app, true, true, GlobalTranslationRequest{
		Target: "other",
		Other:  "Spanish",
	})
	if err != nil {
		t.Fatalf("translateCurrentEntriesForTest: %v", err)
	}
	if summary.TargetLanguage != "Spanish" {
		t.Fatalf("TargetLanguage = %q, want Spanish", summary.TargetLanguage)
	}
	if summary.Total != 2 || summary.Translated != 1 || summary.Failed != 1 {
		t.Fatalf("summary counts = total %d translated %d failed %d, want 2/1/1", summary.Total, summary.Translated, summary.Failed)
	}
	if len(summary.Items) != 2 {
		t.Fatalf("summary items = %d, want 2", len(summary.Items))
	}
	if summary.Items[0].Kind != "memory" || summary.Items[0].Status != "failed" || summary.Items[0].Error == "" {
		t.Fatalf("first item = %+v, want failed memory with error", summary.Items[0])
	}
	if summary.Items[1].Kind != "knowledge" || summary.Items[1].Status != "translated" {
		t.Fatalf("second item = %+v, want translated knowledge", summary.Items[1])
	}

	memEntry, err := app.GetMemoryEntry("memory-1")
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}
	if memEntry.Content != memoryOriginal {
		t.Fatalf("failed memory content = %q, want original", memEntry.Content)
	}
	memVersions, err := app.ListMemoryVersions("memory-1")
	if err != nil {
		t.Fatalf("ListMemoryVersions: %v", err)
	}
	if len(memVersions) != 0 {
		t.Fatalf("failed memory versions = %d, want 0", len(memVersions))
	}

	knowledgeEntry, err := app.GetKnowledgeEntry("knowledge-1")
	if err != nil {
		t.Fatalf("GetKnowledgeEntry: %v", err)
	}
	if !strings.Contains(knowledgeEntry.Content, "Translated insight") {
		t.Fatalf("knowledge content = %q, want translated insight", knowledgeEntry.Content)
	}
}

func TestTranslateCurrentMemoryEntriesIncludesHistoryFailureSummaryAndContinues(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	memDir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "memory-1.md"), []byte("# Memory One\n\n- **Updated**: 2026-05-11 10:00\n\n## Memory Points\n\n- One\n"), 0600); err != nil {
		t.Fatalf("write memory one: %v", err)
	}
	historyOne, err := memoryHistoryDir(memDir, "memory-1")
	if err != nil {
		t.Fatalf("memoryHistoryDir one: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyOne, "1000.md"), []byte("# Memory One History\n"), 0600); err != nil {
		t.Fatalf("write memory one history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "memory-2.md"), []byte("# Memory Two\n\n- **Updated**: 2026-05-11 09:00\n\n## Memory Points\n\n- Two\n"), 0600); err != nil {
		t.Fatalf("write memory two: %v", err)
	}

	runner := &memoryTranslationFakeRunner{
		responses: []string{
			"# Memory One Translated\n",
			"# Memory Two Translated\n",
			"# Memory Two Snapshot Translated\n",
		},
		errors: []error{nil, context.DeadlineExceeded, nil, nil},
		ready:  true,
	}
	app := newTranslationTestApp(t, runner)

	summary, err := translateCurrentEntriesForTest(app, true, false, GlobalTranslationRequest{
		Target:         "English",
		IncludeHistory: true,
	})
	if err != nil {
		t.Fatalf("translateCurrentEntriesForTest: %v", err)
	}
	if summary.Total != 4 || summary.Translated != 3 || summary.Failed != 1 {
		t.Fatalf("summary counts = total %d translated %d failed %d, want 4/3/1", summary.Total, summary.Translated, summary.Failed)
	}
	if summary.Memories.Total != 4 || summary.Memories.Translated != 3 || summary.Memories.Failed != 1 {
		t.Fatalf("memory counts = %+v, want 4/3/1", summary.Memories)
	}
	if len(summary.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(summary.Items))
	}
	if summary.Items[1].Kind != "memory-history" || summary.Items[1].Status != "failed" || summary.Items[1].Error == "" {
		t.Fatalf("second item = %+v, want failed memory-history with error", summary.Items[1])
	}
	if summary.Items[2].Kind != "memory" || summary.Items[2].ID != "memory-2" || summary.Items[2].Status != "translated" {
		t.Fatalf("third item = %+v, want translated second memory", summary.Items[2])
	}
	entry, err := app.GetMemoryEntry("memory-2")
	if err != nil {
		t.Fatalf("GetMemoryEntry memory-2: %v", err)
	}
	if !strings.Contains(entry.Content, "Memory Two Translated") {
		t.Fatalf("memory-2 content = %q, want translated despite prior history failure", entry.Content)
	}
}

// Translating rewrites every memory and knowledge entry, so it starts only from
// the app window.
func TestStartTranslateAllEntriesStaysOffTheBrowserBridge(t *testing.T) {
	for _, method := range []string{
		"StartTranslateAllCurrentMemoryEntries",
		"StartTranslateAllCurrentKnowledgeEntries",
	} {
		if browserRPCAllowlist[method] {
			t.Errorf("%s rewrites learned entries and must stay off the browser allowlist", method)
		}
	}
}

func TestMemoryGeneratorMemoryWrites_NormalizeMarkdownToCurrentUILanguage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	runner := &memoryTranslationFakeRunner{
		responses: []string{
			"# 对话记忆\n\n- **Source**: should be ignored\n- **Dimension**: should_be_ignored\n\n## Memory Points\n\n- 已翻译的创建要点\n",
			`{"memoryPoints":["old English point","new English point"]}`,
			"# 对话记忆更新\n\n- **Source**: should be ignored\n- **Dimension**: should_be_ignored\n\n## Memory Points\n\n- 已翻译的旧要点\n- 已翻译的新要点\n",
		},
		ready: true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}
	mc := &MemoryGenerator{app: app}

	entryID, err := mc.saveMemoryFile(memorySourceSlack, "src-create", &mcMemoryGenerationResponse{
		Title:        "Conversation Memory",
		MemoryPoints: []string{"create point"},
		Dimension:    "topic_highlight",
	})
	if err != nil {
		t.Fatalf("saveMemoryFile: %v", err)
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	created := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	for _, want := range []string{
		"title: 对话记忆",
		"source: slack",
		"dimension: topic_highlight",
		"- 已翻译的创建要点",
	} {
		if !strings.Contains(created, want) {
			t.Fatalf("created memory missing %q:\n%s", want, created)
		}
	}

	updateID := "conversation-update-target"
	existing := "# Existing Memory\n\n- **Updated**: 2026-05-11 10:00\n- **Source**: conversation\n- **Dimension**: user_insight\n\n## Memory Points\n\n- old English point\n"
	if err := os.WriteFile(filepath.Join(dir, updateID+".md"), []byte(existing), 0600); err != nil {
		t.Fatalf("write existing memory: %v", err)
	}
	if err := mc.updateMemoryFile(context.Background(), updateID, memorySourceDiscord, "src-update", &mcMemoryGenerationResponse{
		Title:        "Conversation Memory Updated",
		MemoryPoints: []string{"new English point"},
		Dimension:    "agentic_memory",
	}, ""); err != nil {
		t.Fatalf("updateMemoryFile: %v", err)
	}
	updated := mustReadFile(t, filepath.Join(dir, updateID+".md"))
	for _, want := range []string{
		"title: 对话记忆更新",
		"source: discord",
		"dimension: agentic_memory",
		"- 已翻译的旧要点",
		"- 已翻译的新要点",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated memory missing %q:\n%s", want, updated)
		}
	}
	if strings.Contains(updated, "old English point") || strings.Contains(updated, "new English point") {
		t.Fatalf("updated memory kept untranslated points:\n%s", updated)
	}
	if runner.calls != 3 {
		t.Fatalf("runner calls = %d, want 3", runner.calls)
	}
	if !strings.Contains(runner.lastPrompt, "Simplified Chinese") {
		t.Fatalf("last prompt should target current UI language, got:\n%s", runner.lastPrompt)
	}
	if strings.Contains(runner.lastPrompt, "agentic_memory") || strings.Contains(runner.lastPrompt, "discord") {
		t.Fatalf("last prompt should not include metadata, got:\n%s", runner.lastPrompt)
	}
}

func TestMemoryGeneratorKnowledgeWrites_NormalizeMarkdownToCurrentUILanguage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	runner := &memoryTranslationFakeRunner{
		responses: []string{
			"# 对话知识\n\n- **Source**: should be ignored\n\n## Description\n\n已翻译描述\n\n## Key Insights\n\n- 已翻译创建洞见\n",
			"# 对话知识更新\n\n- **Source**: should be ignored\n- **Apps**: should be ignored\n\n## Description\n\n已翻译更新描述\n\n## Key Insights\n\n- 已翻译旧洞见\n- 已翻译新洞见\n",
		},
		ready: true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}
	mc := &MemoryGenerator{app: app}

	entryID := mc.saveKnowledgeFile(memorySourceFeishu, "src-create", &knowledgeGenerationResponse{
		Title:           "Conversation Knowledge",
		Description:     "English description",
		KnowledgePoints: []string{"create insight"},
	})
	if entryID == "" {
		t.Fatal("saveKnowledgeFile returned empty entryID")
	}
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	created := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	for _, want := range []string{
		"title: 对话知识",
		"source: feishu",
		"## Description",
		"已翻译描述",
		"- 已翻译创建洞见",
	} {
		if !strings.Contains(created, want) {
			t.Fatalf("created knowledge missing %q:\n%s", want, created)
		}
	}

	updateID := "knowledge-update-target"
	existing := "# Existing Knowledge\n\n- **Date**: 2026-05-11 10:00\n- **Source**: conversation\n- **Apps**: Code, Chrome\n- **Activity Session**:\n  - session-a - 10:00, Code\n\n## Key Insights\n\n- old English insight\n"
	if err := os.WriteFile(filepath.Join(dir, updateID+".md"), []byte(existing), 0600); err != nil {
		t.Fatalf("write existing knowledge: %v", err)
	}
	mc.updateKnowledgeFile(memorySourceSlack, "src-update", &knowledgeGenerationResponse{
		UpdateID:        updateID,
		Title:           "Conversation Knowledge Updated",
		Description:     "English update description",
		KnowledgePoints: []string{"new English insight"},
	})
	updated := mustReadFile(t, filepath.Join(dir, updateID+".md"))
	for _, want := range []string{
		"title: 对话知识更新",
		"source: conversation",
		"apps:",
		"- Code",
		"- Chrome",
		"activity_sessions:",
		"- session-a",
		"已翻译更新描述",
		"- 已翻译旧洞见",
		"- 已翻译新洞见",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated knowledge missing %q:\n%s", want, updated)
		}
	}
	if strings.Contains(updated, "old English insight") || strings.Contains(updated, "new English insight") {
		t.Fatalf("updated knowledge kept untranslated insights:\n%s", updated)
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want 2", runner.calls)
	}
	if !strings.Contains(runner.lastPrompt, "Simplified Chinese") {
		t.Fatalf("last prompt should target current UI language, got:\n%s", runner.lastPrompt)
	}
	if strings.Contains(runner.lastPrompt, "Code, Chrome") || strings.Contains(runner.lastPrompt, "session-a") {
		t.Fatalf("last prompt should not include machine-readable metadata, got:\n%s", runner.lastPrompt)
	}
}

func TestMemoryGeneratorMemoryWrite_FailOpenWhenLanguageNormalizationFails(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	runner := &memoryTranslationFakeRunner{
		errors: []error{context.DeadlineExceeded},
		ready:  true,
	}
	app := newTranslationTestApp(t, runner)
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}
	mc := &MemoryGenerator{app: app}

	entryID, err := mc.saveMemoryFile(memorySourceSlack, "src-fail-open", &mcMemoryGenerationResponse{
		Title:        "Original Memory",
		MemoryPoints: []string{"original point"},
		Dimension:    "user_insight",
	})
	if err != nil {
		t.Fatalf("saveMemoryFile should fail open on translation error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	content := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	for _, want := range []string{
		"title: Original Memory",
		"source: slack",
		"dimension: user_insight",
		"- original point",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("fail-open content missing %q:\n%s", want, content)
		}
	}
}

func TestMemoryTriageResponse_ParsesWorthKnowledge(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantWorth     bool
		wantKnowledge bool
		wantReason    string
	}{
		{
			name:          "both true",
			input:         `{"worth": true, "worthKnowledge": true, "reason": "contains personal context and technical insight"}`,
			wantWorth:     true,
			wantKnowledge: true,
			wantReason:    "contains personal context and technical insight",
		},
		{
			name:          "worth only",
			input:         `{"worth": true, "worthKnowledge": false, "reason": "personal preferences only"}`,
			wantWorth:     true,
			wantKnowledge: false,
			wantReason:    "personal preferences only",
		},
		{
			name:          "knowledge only",
			input:         `{"worth": false, "worthKnowledge": true, "reason": "specific API gotcha"}`,
			wantWorth:     false,
			wantKnowledge: true,
			wantReason:    "specific API gotcha",
		},
		{
			name:          "neither",
			input:         `{"worth": false, "worthKnowledge": false, "reason": "casual chat"}`,
			wantWorth:     false,
			wantKnowledge: false,
			wantReason:    "casual chat",
		},
		{
			name:          "missing worthKnowledge defaults to false",
			input:         `{"worth": true, "reason": "old format"}`,
			wantWorth:     true,
			wantKnowledge: false,
			wantReason:    "old format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp memoryTriageResponse
			if err := json.Unmarshal([]byte(tt.input), &resp); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if resp.Worth != tt.wantWorth {
				t.Errorf("worth = %v, want %v", resp.Worth, tt.wantWorth)
			}
			if resp.WorthKnowledge != tt.wantKnowledge {
				t.Errorf("worthKnowledge = %v, want %v", resp.WorthKnowledge, tt.wantKnowledge)
			}
			if resp.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", resp.Reason, tt.wantReason)
			}
		})
	}
}

func TestKnowledgeGenerationResponse_ParsesAllActions(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAction string
		wantPoints int
		wantTitle  string
	}{
		{
			name:       "create with points",
			input:      `{"action":"create","title":"Slack API limits","description":"discovered during debugging","knowledgePoints":["Slack message limit is ~3000 chars","Slack mrkdwn uses *bold* not **bold**"]}`,
			wantAction: "create",
			wantPoints: 2,
			wantTitle:  "Slack API limits",
		},
		{
			name:       "update existing",
			input:      `{"action":"update","updateId":"activity-abc123","title":"Updated knowledge","knowledgePoints":["point 1"]}`,
			wantAction: "update",
			wantPoints: 1,
			wantTitle:  "Updated knowledge",
		},
		{
			name:       "skip duplicate",
			input:      `{"action":"skip","title":"Already known","knowledgePoints":[]}`,
			wantAction: "skip",
			wantPoints: 0,
			wantTitle:  "Already known",
		},
		{
			name:       "empty action defaults handled by caller",
			input:      `{"title":"No action field","knowledgePoints":["point"]}`,
			wantAction: "",
			wantPoints: 1,
			wantTitle:  "No action field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp knowledgeGenerationResponse
			if err := json.Unmarshal([]byte(tt.input), &resp); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if resp.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", resp.Action, tt.wantAction)
			}
			if len(resp.KnowledgePoints) != tt.wantPoints {
				t.Errorf("knowledgePoints count = %d, want %d", len(resp.KnowledgePoints), tt.wantPoints)
			}
			if resp.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", resp.Title, tt.wantTitle)
			}
		})
	}
}

func TestParseKnowledgeMetadata(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name: "memory-created file",
			content: `# Some Title

- **Date**: 2025-01-15 10:30
- **Source**: slack

## Key Insights

- insight one
`,
			want: map[string]string{
				"Date":   "2025-01-15 10:30",
				"Source": "slack",
			},
		},
		{
			name: "activity-created file (legacy single-line)",
			content: `# Activity Knowledge

- **Date**: 2025-01-15 10:30
- **Apps**: VSCode, Terminal
- **Activity Session**: sess-abc123

## Key Insights

- insight one
`,
			want: map[string]string{
				"Date":             "2025-01-15 10:30",
				"Apps":             "VSCode, Terminal",
				"Activity Session": "sess-abc123",
			},
		},
		{
			name: "activity-created file (nested bullets)",
			content: `# Activity Knowledge

- **Date**: 2025-01-15 10:30
- **Apps**: VSCode, Terminal
- **Activity Session**:
  - sess-abc123 — 10:30, VSCode
  - sess-def456 — 10:35, Terminal

## Key Insights

- insight one
`,
			want: map[string]string{
				"Date": "2025-01-15 10:30",
				"Apps": "VSCode, Terminal",
				"Activity Session": "- **Activity Session**:\n" +
					"  - sess-abc123 — 10:30, VSCode\n" +
					"  - sess-def456 — 10:35, Terminal",
			},
		},
		{
			name:    "empty file",
			content: "",
			want:    map[string]string{},
		},
		{
			name: "no metadata lines",
			content: `# Title

Just some text.
`,
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "test.md")
			if err := os.WriteFile(fp, []byte(tt.content), 0600); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			got := parseKnowledgeMetadata(fp)
			if len(got) != len(tt.want) {
				t.Errorf("got %d keys, want %d: %v", len(got), len(tt.want), got)
			}
			for k, wantV := range tt.want {
				if got[k] != wantV {
					t.Errorf("meta[%q] = %q, want %q", k, got[k], wantV)
				}
			}
		})
	}
}

func TestParseKnowledgeMetadata_MissingFile(t *testing.T) {
	got := parseKnowledgeMetadata("/nonexistent/path/file.md")
	if len(got) != 0 {
		t.Errorf("expected empty map for missing file, got %v", got)
	}
}

func TestUpdateKnowledgeFile_PreservesExistingSource(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	filePath := filepath.Join(dir, "conversation-123.md")
	existing := `# Existing Knowledge

- **Date**: 2025-01-15 10:30
- **Source**: conversation

## Key Insights

- old point
`
	if err := os.WriteFile(filePath, []byte(existing), 0600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	mc := &MemoryGenerator{}
	mc.updateKnowledgeFile(memorySourceSlack, "src-1", &knowledgeGenerationResponse{
		UpdateID:        "conversation-123",
		Title:           "Updated Knowledge",
		Description:     "updated description",
		KnowledgePoints: []string{"new point"},
	})

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "source: conversation\n") {
		t.Fatalf("expected existing source to be preserved, got:\n%s", content)
	}
	if strings.Contains(content, "source: slack\n") {
		t.Fatalf("unexpected source overwrite, got:\n%s", content)
	}
}

func TestUpdateKnowledgeFile_DoesNotAddSourceToActivityOriginFile(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	filePath := filepath.Join(dir, "activity-123.md")
	existing := `# Activity Knowledge

- **Date**: 2025-01-15 10:30
- **Apps**: VSCode, Terminal
- **Activity Session**: sess-abc123

## Key Insights

- old point
`
	if err := os.WriteFile(filePath, []byte(existing), 0600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	mc := &MemoryGenerator{}
	mc.updateKnowledgeFile(memorySourceSlack, "src-2", &knowledgeGenerationResponse{
		UpdateID:        "activity-123",
		Title:           "Updated Activity Knowledge",
		Description:     "updated description",
		KnowledgePoints: []string{"new point"},
	})

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "\nsource:") {
		t.Fatalf("did not expect Source metadata to be added, got:\n%s", content)
	}
	if !strings.Contains(content, "apps:\n- VSCode\n- Terminal\n") {
		t.Fatalf("expected Apps metadata to be preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "activity_sessions:\n- sess-abc123\n") {
		t.Fatalf("expected Activity Session metadata to be preserved, got:\n%s", content)
	}
}

func TestUpdateKnowledgeFile_PreservesNestedActivitySessionBlock(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	filePath := filepath.Join(dir, "activity-456.md")
	existing := `# Activity Knowledge

- **Date**: 2025-01-15 10:30
- **Apps**: VSCode, Terminal
- **Activity Session**:
  - sess-abc123 — 10:30, VSCode
  - sess-def456 — 10:35, Terminal

## Key Insights

- old point
`
	if err := os.WriteFile(filePath, []byte(existing), 0600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	mc := &MemoryGenerator{}
	mc.updateKnowledgeFile(memorySourceSlack, "src-3", &knowledgeGenerationResponse{
		UpdateID:        "activity-456",
		Title:           "Updated Activity Knowledge",
		Description:     "updated description",
		KnowledgePoints: []string{"new point"},
	})

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"activity_sessions:",
		"- sess-abc123",
		"- sess-def456",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected activity session %q to be preserved in OKF frontmatter, got:\n%s", want, content)
		}
	}
}
