package main

import (
	"reflect"
	"testing"
)

func TestPredefinedAIProviders_Shape(t *testing.T) {
	presets := predefinedAIProviders()
	if len(presets) == 0 {
		t.Fatal("expected a non-empty provider catalog")
	}
	seenIDs := map[string]bool{}
	for _, p := range presets {
		if p.ID == "" {
			t.Errorf("preset %q has empty id", p.Name)
		}
		if seenIDs[p.ID] {
			t.Errorf("duplicate preset id %q", p.ID)
		}
		seenIDs[p.ID] = true
		// APIFormat must be one of the two formats the runner understands.
		if p.APIFormat != "openai" && p.APIFormat != "anthropic" {
			t.Errorf("preset %q has unsupported apiFormat %q", p.ID, p.APIFormat)
		}
	}
	// Anchor a few well-known entries the desktop UI relies on.
	for _, id := range []string{"openai", "anthropic", "deepseek", "qwen", "ollama", "custom"} {
		if !seenIDs[id] {
			t.Errorf("expected catalog to include preset %q", id)
		}
	}
}

func TestAIProviderPresetByID(t *testing.T) {
	tests := []struct {
		in     string
		wantID string
		wantOK bool
	}{
		{"deepseek", "deepseek", true},
		{"  DeepSeek  ", "deepseek", true}, // trimmed + lowercased
		{"OPENAI", "openai", true},
		{"", "", false},
		{"does-not-exist", "", false},
	}
	for _, tt := range tests {
		got, ok := aiProviderPresetByID(tt.in)
		if ok != tt.wantOK || (ok && got.ID != tt.wantID) {
			t.Errorf("aiProviderPresetByID(%q) = (%q,%v), want (%q,%v)", tt.in, got.ID, ok, tt.wantID, tt.wantOK)
		}
	}
}

func TestDeepSeekPresetIsOpenAIFormat(t *testing.T) {
	// Regression: DeepSeek's working /models listing is the OpenAI-style
	// endpoint, not the Anthropic /v1/models path. A misclassification here
	// is exactly the bug that left the model dropdown empty.
	p, ok := aiProviderPresetByID("deepseek")
	if !ok {
		t.Fatal("deepseek preset missing")
	}
	if p.APIFormat != "openai" {
		t.Errorf("deepseek apiFormat = %q, want openai", p.APIFormat)
	}
	if p.BaseURL != "https://api.deepseek.com" {
		t.Errorf("deepseek baseURL = %q", p.BaseURL)
	}
	if len(p.SeedModels) == 0 {
		t.Error("deepseek should carry seed models for the fallback")
	}
}

func TestOllamaPresetNeedsNoKey(t *testing.T) {
	p, ok := aiProviderPresetByID("ollama")
	if !ok {
		t.Fatal("ollama preset missing")
	}
	if p.RequiresKey {
		t.Error("ollama should not require an API key")
	}
}

func TestMergeModelList(t *testing.T) {
	tests := []struct {
		name    string
		primary []string
		extra   string
		want    []string
	}{
		{"sorts and dedups", []string{"gpt-4o", "gpt-4o-mini", "gpt-4o"}, "", []string{"gpt-4o", "gpt-4o-mini"}},
		{"appends unlisted extra last", []string{"b", "a"}, "z-custom", []string{"a", "b", "z-custom"}},
		{"extra already present is not duplicated", []string{"a", "b"}, "a", []string{"a", "b"}},
		{"trims blanks", []string{" a ", "", "b"}, "  ", []string{"a", "b"}},
		{"empty primary keeps extra selectable", nil, "my-model", []string{"my-model"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeModelList(tt.primary, tt.extra)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeModelList(%v,%q) = %v, want %v", tt.primary, tt.extra, got, tt.want)
			}
		})
	}
}

func TestListAIProviderModels_SeedFallbackWithoutKey(t *testing.T) {
	// With a preset selected but no API key, listing must return the seed
	// models (never an empty dropdown) without attempting a network call.
	app := &App{}
	cfg := AIProviderConfig{Provider: "openai", PresetID: "deepseek"}
	got, err := app.ListAIProviderModels(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected seed fallback, got empty list")
	}
	want := mergeModelList([]string{"deepseek-chat", "deepseek-reasoner"}, "")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want seed %v", got, want)
	}
}

func TestListAIProviderModels_CustomNoSeedRequiresKey(t *testing.T) {
	// The free-form "custom" preset carries no seed; without a key there is
	// nothing to fall back to, so the error surfaces (user must configure).
	app := &App{}
	cfg := AIProviderConfig{Provider: "openai", PresetID: "custom"}
	if _, err := app.ListAIProviderModels(cfg); err == nil {
		t.Fatal("expected an error for custom preset without a key or seed")
	}
}
