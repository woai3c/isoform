package main

import "testing"

func TestEffectiveTaskModelsUseConfiguredPrimaryAndLightModels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveAIProviderConfig(AIProviderConfig{
		Provider:   "openai",
		APIKey:     "test-key",
		Model:      "gpt-5.6-sol",
		LightModel: "gpt-5.6-terra",
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if got := effectivePrimaryTaskModel(app); got != "gpt-5.6-sol" {
		t.Fatalf("primary model=%q", got)
	}
	if got := effectiveLightTaskModel(app); got != "gpt-5.6-terra" {
		t.Fatalf("light model=%q", got)
	}
}

func TestEffectiveTaskModelsPinDefaultNativeAliases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_HAIKU_MODEL", "")
	app := &App{}
	if got := effectivePrimaryTaskModel(app); got != apiClaudeSonnetModel {
		t.Fatalf("primary model=%q, want %q", got, apiClaudeSonnetModel)
	}
	if got := effectiveLightTaskModel(app); got != apiClaudeHaikuModel {
		t.Fatalf("light model=%q, want %q", got, apiClaudeHaikuModel)
	}
}
