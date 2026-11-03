package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"wails-gui/models"
	"wails-gui/storage"
)

func setupVoiceFixture(t *testing.T) (*App, *models.Manager) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	var entries []models.Entry
	for _, id := range []string{"kokoro-en", "melotts-zh-en"} {
		data := []byte(id)
		n := int64(len(data))
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		languages := []string{"en"}
		if id == "melotts-zh-en" {
			languages = append(languages, "zh")
		}
		path := id + "/weights"
		entries = append(entries, models.Entry{ID: id, Job: "voice", DisplayName: id, Licence: models.Licence{Name: "MIT"}, Recommended: id == "kokoro-en", Languages: languages, SizeBytes: &n, Files: []models.File{{Path: path, Bytes: &n, SHA256: &hash, URLs: []string{"https://model-assets.yansu.app/" + path}}}, Sources: []models.Source{{Kind: "mirror", URL: "https://model-assets.yansu.app/"}}})
		dest := filepath.Join(dir, "models", path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := models.New(filepath.Join(dir, "models"), dir, entries, func(string) (int64, error) { return 10000, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	app := &App{}
	app.setupModelsOnce.Do(func() { app.setupModels = manager })
	return app, manager
}
func TestVoiceChoiceControlsEnglishAndChineseEngineLoading(t *testing.T) {
	app, manager := setupVoiceFixture(t)
	var loaded []string
	app.ensureTtsState().newEngine = func(id string) (speechSynthesizer, error) {
		loaded = append(loaded, id)
		return mobileBriefTestVoice{}, nil
	}
	if err := manager.SetChoice("voice", "melotts-zh-en"); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "zh"} {
		if _, err := app.ensureTtsReadyForLang(t.Context(), language); err != nil {
			t.Fatal(language, err)
		}
	}
	if len(loaded) != 1 || loaded[0] != "melotts-zh-en" {
		t.Fatalf("Melo selection loaded: %v", loaded)
	}
	if err := manager.SetChoice("voice", "kokoro-en"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureTtsReadyForLang(t.Context(), "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureTtsReadyForLang(t.Context(), "zh"); err != nil {
		t.Fatal("installed Chinese fallback", err)
	}
	if len(loaded) != 2 || loaded[1] != "kokoro-en" {
		t.Fatalf("choice change reused wrong engine: %v", loaded)
	}
	if err := manager.Remove("melotts-zh-en"); err != nil {
		t.Fatal(err)
	}
	if app.ttsModelPresentForLang("zh") {
		t.Fatal("missing fallback reported ready")
	}
	if _, err := app.ensureTtsReadyForLang(t.Context(), "zh"); !errors.Is(err, models.ErrNotReady) {
		t.Fatalf("missing fallback: %v", err)
	}
	if err := manager.Remove("kokoro-en"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureTtsReadyForLang(t.Context(), "en"); !errors.Is(err, models.ErrNotReady) {
		t.Fatalf("cached engine hid missing weights: %v", err)
	}
}
func TestConcurrentVoiceLoadingSharesTheChosenEngine(t *testing.T) {
	app, _ := setupVoiceFixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var loads atomic.Int32
	app.ensureTtsState().newEngine = func(id string) (speechSynthesizer, error) {
		loads.Add(1)
		entered <- struct{}{}
		<-release
		return mobileBriefTestVoice{}, nil
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := app.ensureTtsReadyForLang(context.Background(), "en")
			failures <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("chosen engine did not load")
	}
	close(release)
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loaded %d engines", loads.Load())
	}
}
func TestAfterCallSwitchRestoresChoiceAfterNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	manager, err := app.setupModelManager()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := app.SetModelChoice("afterCall", "qwen3-asr-1.7b"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetModelChoice("afterCall", "none"); err != nil {
		t.Fatal(err)
	}
	if app.afterCallSpeechModelEnabled() {
		t.Fatal("None was enabled")
	}
	if err := app.SetAfterCallSpeechModelEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !app.afterCallSpeechModelEnabled() || manager.Selected("afterCall") != "qwen3-asr-1.7b" {
		t.Fatal("switch could not restore the saved choice")
	}
	if err := app.SetAfterCallSpeechModelEnabled(false); err != nil {
		t.Fatal(err)
	}
	if manager.Selected("afterCall") != "none" {
		t.Fatal("legacy switch did not edit setup choice")
	}
	restarted := &App{}
	next, err := restarted.setupModelManager()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	if err := restarted.SetAfterCallSpeechModelEnabled(true); err != nil {
		t.Fatal(err)
	}
	if next.Selected("afterCall") != "qwen3-asr-1.7b" {
		t.Fatal("reopen lost the previous choice")
	}
	if err := restarted.SetModelChoice("afterCall", "none"); err != nil {
		t.Fatal(err)
	}
	if len(next.Downloads()) != 0 {
		t.Fatal("a switch authorized a download")
	}
}
func TestRevokedScreenRecordingClearsBothGrantedStates(t *testing.T) {
	for _, saved := range []string{"on", "onAfterReopen"} {
		if got := setupPermissionCurrentState(saved, false, false); got != "off" {
			t.Fatalf("%s => %s", saved, got)
		}
	}
}

func TestSetupModelJoinSharesTheOptionalBudgetAfterCriticalFlush(t *testing.T) {
	app := newPageFirstReadTestApp(t)
	previousDelay := agentSegmentWriteDelay
	agentSegmentWriteDelay = time.Hour
	defer func() { agentSegmentWriteDelay = previousDelay }()
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var firstID string
	app.onAgentEvent(func(event AgentEvent) {
		if event.Type == "assistant" && firstID == "" {
			firstID = event.ItemID
			if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: event.ItemID, Kind: "assistant", Content: event.Content}); err != nil {
				t.Error(err)
			}
		}
	})
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: main.LocalID, Content: "Reply "})
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: main.LocalID, Content: "saved"})
	nativeRelease, modelRelease := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, _ = app.pageNativePermissionsContext(ctx, func() PlatformPermissions { <-nativeRelease; return permissionSnapshotFixture(true) })
	t.Cleanup(func() { close(nativeRelease); close(modelRelease); awaitPagePermissionRefresh(t, app) })
	done := make(chan struct{})
	started := time.Now()
	go func() { app.flushAndStopPageFirstReads(modelRelease); close(done) }()
	select {
	case <-done:
		t.Fatal("held optional work was not joined")
	case <-time.After(30 * time.Millisecond):
	}
	rows, err := app.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil || len(rows) != 1 || rows[0].Content != "Reply saved" {
		t.Fatalf("model join delayed critical persistence: %+v %v", rows, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("optional joins exceeded shutdown budget")
	}
	if elapsed := time.Since(started); elapsed > pageFirstReadWaitBudget+125*time.Millisecond {
		t.Fatalf("model and native joins did not share a budget: %s", elapsed)
	}
}
