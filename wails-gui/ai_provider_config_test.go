package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalAssistantBudgetConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, budget string
		want         float64
	}{
		{name: "default", want: 10},
		{name: "configured", budget: `,"assistantMaxBudgetUsd":42.5`, want: 42.5},
		{name: "zero uses default", budget: `,"assistantMaxBudgetUsd":0`, want: 10},
		{name: "negative uses default", budget: `,"assistantMaxBudgetUsd":-1`, want: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			writeAIProviderFile(t, fmt.Sprintf(`{"provider":"claude-code"%s}`, tc.budget))
			cfg := loadAIProviderConfig()
			for range 2 {
				runner, ok := cfg.externalAgentRunnerConfig()
				if !ok || runner.MaxBudgetUSD != tc.want {
					t.Errorf("assistant budget = %v, want %v", runner.MaxBudgetUSD, tc.want)
				}
				if err := saveAIProviderConfig(cfg); err != nil {
					t.Fatal(err)
				}
				cfg = loadAIProviderConfig()
			}
		})
	}
}

func TestSaveProviderSettingsPreservesAssistantBudget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeAIProviderFile(t, `{"provider":"managed","assistantMaxBudgetUsd":42.5}`)
	// A restart is already in progress, so this exercises the real settings
	// save without launching a sidecar. Older clients omit the file-only field.
	app := &App{starting: true}
	if err := app.SaveAIProviderConfig(AIProviderConfig{Provider: "managed"}); err != nil {
		t.Fatal(err)
	}
	if got := loadAIProviderConfig().AssistantMaxBudgetUSD; got != 42.5 {
		t.Fatalf("unrelated settings save changed assistant budget to %v", got)
	}
}

// writeAIProviderFile writes ai-provider.json under the test's HOME.
func writeAIProviderFile(t *testing.T, contents string) {
	t.Helper()
	path, err := aiProviderConfigPath()
	if err != nil {
		t.Fatalf("aiProviderConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write ai-provider.json: %v", err)
	}
}

// Configs written before managed mode existed migrate on read: a missing file
// or the retired "yansu" gateway value means managed. Advanced providers are
// kept as they are.
func TestAIProviderConfigMigration(t *testing.T) {
	cases := []struct {
		name string
		file string // empty means no file
		want string
	}{
		{name: "missing file is managed"},
		{name: "empty object is managed", file: `{}`, want: aiProviderManaged},
		{name: "legacy yansu gateway value migrates", file: `{"provider":"yansu"}`, want: aiProviderManaged},
		{name: "legacy value with padding and case migrates", file: `{"provider":" Yansu "}`, want: aiProviderManaged},
		{name: "explicit managed is kept", file: `{"provider":"managed"}`, want: aiProviderManaged},
		{name: "anthropic advanced is kept", file: `{"provider":"anthropic","apiKey":"k","model":"m"}`, want: "anthropic"},
		{name: "openai advanced is kept", file: `{"provider":"OpenAI","apiKey":"k"}`, want: "openai"},
		{name: "external agent is kept", file: `{"provider":"claude-code"}`, want: "claude-code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if tc.file != "" {
				writeAIProviderFile(t, tc.file)
			}
			if tc.want == "" {
				tc.want = aiProviderManaged
			}
			cfg := loadAIProviderConfig()
			if cfg.Provider != tc.want {
				t.Errorf("loaded provider = %q, want %q", cfg.Provider, tc.want)
			}
			if got := cfg.normalizedProvider(); got != tc.want {
				t.Errorf("normalizedProvider() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAIProviderConfigManagedActive(t *testing.T) {
	if !(AIProviderConfig{}).managedActive() {
		t.Error("the zero config must be managed")
	}
	if !(AIProviderConfig{Provider: "yansu"}).managedActive() {
		t.Error("the legacy yansu value must be managed")
	}
	if (AIProviderConfig{Provider: "anthropic"}).managedActive() {
		t.Error("anthropic must not be managed")
	}
}

// Switching modes must not lose the saved key or the remembered provider.
func TestAIProviderConfigSaveKeepsAdvancedProviderAndKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := AIProviderConfig{
		Provider:         aiProviderManaged,
		AdvancedProvider: "openai",
		APIKey:           "sk-saved",
		BaseURL:          "https://proxy.example.com/v1",
		Model:            "gpt-4o",
	}
	if err := saveAIProviderConfig(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := loadAIProviderConfig()
	if got.Provider != aiProviderManaged || got.AdvancedProvider != "openai" {
		t.Fatalf("provider fields = %q / %q", got.Provider, got.AdvancedProvider)
	}
	if got.APIKey != "sk-saved" || got.BaseURL != want.BaseURL || got.Model != "gpt-4o" {
		t.Fatalf("key or models lost on round trip: %+v", got)
	}
}
