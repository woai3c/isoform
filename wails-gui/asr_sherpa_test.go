//go:build !bindings && ((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSenseVoiceTag(t *testing.T) {
	cases := map[string]string{
		"<|zh|>":     "zh",
		"<|Speech|>": "speech",
		" en ":       "en",
		"":           "",
	}
	for in, want := range cases {
		if got := normalizeSenseVoiceTag(in); got != want {
			t.Fatalf("normalizeSenseVoiceTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSherpaSpeechEventFilter(t *testing.T) {
	if !isSherpaSpeechEvent("<|Speech|>") {
		t.Fatal("expected Speech event to be accepted")
	}
	if isSherpaSpeechEvent("<|BGM|>") {
		t.Fatal("expected BGM event to be rejected")
	}
	if isSherpaSpeechEvent("<|Applause|>") {
		t.Fatal("expected non-speech event to be rejected")
	}
}

func TestSherpaLanguageAllowed(t *testing.T) {
	allowed := sherpaLanguageSet([]string{"en", "zh", "yue"})
	if !sherpaLanguageAllowed(allowed, "<|zh|>") {
		t.Fatal("expected zh to be allowed")
	}
	if !sherpaLanguageAllowed(allowed, "<|en|>") {
		t.Fatal("expected en to be allowed")
	}
	if sherpaLanguageAllowed(allowed, "<|ko|>") {
		t.Fatal("expected ko to be rejected")
	}
	if !sherpaLanguageAllowed(sherpaLanguageSet([]string{"all"}), "<|ko|>") {
		t.Fatal("expected all language set to allow ko")
	}
}

func TestSherpaAllowedLanguagesForUILanguage(t *testing.T) {
	t.Setenv(envSherpaAllowedLanguages, "")
	t.Setenv(envSherpaLanguageHint, "")
	if got, want := sherpaAllowedLanguagesForUILanguage("zh-CN"), []string{"zh", "yue", "en"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("zh UI languages = %#v, want %#v", got, want)
	}
	if got, want := sherpaAllowedLanguagesForUILanguage("en"), []string{"en", "zh", "yue"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("en UI languages = %#v, want %#v", got, want)
	}

	t.Setenv(envSherpaAllowedLanguages, "en, ja;ko")
	if got, want := sherpaAllowedLanguagesForUILanguage("zh-CN"), []string{"en", "ja", "ko"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("env override languages = %#v, want %#v", got, want)
	}
}

func TestSherpaLanguageHintForUILanguage(t *testing.T) {
	t.Setenv(envSherpaLanguageHint, "")
	if got := sherpaLanguageHintForUILanguage("zh-CN"); got != "auto" {
		t.Fatalf("zh UI language hint = %q, want auto", got)
	}
	if got := sherpaLanguageHintForUILanguage("en"); got != "auto" {
		t.Fatalf("en UI language hint = %q, want auto", got)
	}

	t.Setenv(envSherpaLanguageHint, "auto")
	if got := sherpaLanguageHintForUILanguage("zh-CN"); got != "auto" {
		t.Fatalf("env language hint = %q, want auto", got)
	}
	t.Setenv(envSherpaLanguageHint, "bogus")
	if got := sherpaLanguageHintForUILanguage("zh-CN"); got != "auto" {
		t.Fatalf("invalid env language hint = %q, want auto", got)
	}
}

func TestPaddedSherpaSegmentSamples(t *testing.T) {
	samples := make([]float32, 100)
	for i := range samples {
		samples[i] = float32(i)
	}

	got := paddedSherpaSegmentSamples(samples, 20, 10, 10*time.Second/sherpaSampleRate)
	if len(got) != 30 {
		t.Fatalf("padded length = %d, want 30", len(got))
	}
	if got[0] != 10 || got[len(got)-1] != 39 {
		t.Fatalf("padded range = %.0f..%.0f, want 10..39", got[0], got[len(got)-1])
	}

	got = paddedSherpaSegmentSamples(samples, 5, 10, 10*time.Second/sherpaSampleRate)
	if got[0] != 0 {
		t.Fatalf("padded segment should clamp to start, got %.0f", got[0])
	}
}

func TestShouldReDecodeSherpaSegmentWithPadding(t *testing.T) {
	opts := sherpaTranscriptionOptions{speechPad: 500 * time.Millisecond}
	if !shouldReDecodeSherpaSegmentWithPadding("<|zh|>", opts) {
		t.Fatal("expected zh segment to be eligible for padded re-decode")
	}
	if !shouldReDecodeSherpaSegmentWithPadding("<|yue|>", opts) {
		t.Fatal("expected yue segment to be eligible for padded re-decode")
	}
	if shouldReDecodeSherpaSegmentWithPadding("<|en|>", opts) {
		t.Fatal("expected en segment to keep unpadded decode")
	}
	if shouldReDecodeSherpaSegmentWithPadding("<|zh|>", sherpaTranscriptionOptions{}) {
		t.Fatal("expected zero padding to disable re-decode")
	}
}

func TestSherpaTranscriberReturnsClosedWhenRecognizerIsNil(t *testing.T) {
	transcriber := &sherpaTranscriber{}
	_, err := transcriber.Transcribe(makeTestWAV(sherpaSampleRate, sherpaSampleRate/10))
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Transcribe error = %v, want closed transcriber error", err)
	}
}

func TestSherpaTranscriberModelSmoke(t *testing.T) {
	if os.Getenv("YANSU_RUN_SHERPA_MODEL_TESTS") != "1" {
		t.Skip("set YANSU_RUN_SHERPA_MODEL_TESTS=1 to run with local model files")
	}
	wavDir := os.Getenv("YANSU_SHERPA_TEST_WAV_DIR")
	if wavDir == "" {
		t.Skip("set YANSU_SHERPA_TEST_WAV_DIR to a directory with zh.wav and silence10.wav")
	}

	transcriber, err := newSherpaTranscriber(defaultSherpaPaths(), []string{"zh", "yue", "en"}, "en")
	if err != nil {
		t.Fatalf("newSherpaTranscriber: %v", err)
	}
	defer transcriber.Stop()

	results := make(map[string]string)
	for _, name := range []string{"zh.wav", "en.wav", "yue.wav", "ja.wav", "ko.wav", "silence10.wav"} {
		wav, err := os.ReadFile(filepath.Join(wavDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		got, err := transcriber.TranscribeBackground(wav, string(backgroundListeningSourceMicrophone))
		if err != nil {
			t.Fatalf("TranscribeBackground %s: %v", name, err)
		}
		results[name] = got
		t.Logf("%s => %q", name, got)
	}

	if !strings.Contains(results["zh.wav"], "开放时间") {
		t.Fatalf("zh transcript = %q, want it to preserve the opening phrase", results["zh.wav"])
	}
	enLower := strings.ToLower(results["en.wav"])
	if !strings.Contains(enLower, "gold") || strings.Contains(enLower, "code") {
		t.Fatalf("en transcript = %q, want gold and not code", results["en.wav"])
	}
	if !strings.Contains(results["yue.wav"], "意思") {
		t.Fatalf("yue transcript = %q, want a non-empty Cantonese transcript", results["yue.wav"])
	}
	if strings.TrimSpace(results["ja.wav"]) != "" {
		t.Fatalf("ja transcript = %q, want empty when ja is not allowed", results["ja.wav"])
	}
	if strings.TrimSpace(results["ko.wav"]) != "" {
		t.Fatalf("ko transcript = %q, want empty when ko is not allowed", results["ko.wav"])
	}
	if strings.TrimSpace(results["silence10.wav"]) != "" {
		t.Fatalf("silence transcript = %q, want empty", results["silence10.wav"])
	}
}
