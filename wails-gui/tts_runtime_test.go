package main

import (
	"context"
	"errors"
	"testing"
)

func TestReplaceAutoTtsPrewarmContextCancelsPreviousContext(t *testing.T) {
	app := &App{ctx: context.Background()}

	first := app.replaceAutoTtsPrewarmContext(true)
	second := app.replaceAutoTtsPrewarmContext(true)

	select {
	case <-first.Done():
	default:
		t.Fatal("replacing auto TTS prewarm context did not cancel the previous context")
	}
	select {
	case <-second.Done():
		t.Fatal("new auto TTS prewarm context was unexpectedly cancelled")
	default:
	}
}

func TestReplaceAutoTtsPrewarmContextCancelsWhenDisabled(t *testing.T) {
	app := &App{ctx: context.Background()}

	ctx := app.replaceAutoTtsPrewarmContext(true)
	if got := app.replaceAutoTtsPrewarmContext(false); got != nil {
		t.Fatal("disabling auto TTS returned a prewarm context")
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("disabling auto TTS did not cancel the prewarm context")
	}
}

func TestEnsureTtsReadyForLangStopsBeforeDownloadWhenCancelled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := app.ensureTtsReadyForLang(ctx, "zh")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ensureTtsReadyForLang() error = %v, want context.Canceled", err)
	}
}

func TestCancelledBriefCannotObserveOrStartModelWork(t *testing.T) {
	mobileBriefAudioTestHome(t)
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if app.startTtsVoiceDownload(ctx, "en") || app.setupModels != nil || app.tts != nil {
		t.Fatal("a cancelled brief must not initialize model work")
	}
}
