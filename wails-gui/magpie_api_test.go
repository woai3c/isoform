package main

import (
	"context"
	"strings"
	"testing"

	"yansu-agent/agent"
)

func TestAIProviderConfigForModeRoundTripKeepsKey(t *testing.T) {
	advanced := AIProviderConfig{Provider: "openai", APIKey: "sk-keep", BaseURL: "https://x.example.com/v1", Model: "gpt-4o"}

	managed, err := aiProviderConfigForMode(advanced, "managed")
	if err != nil {
		t.Fatalf("to managed: %v", err)
	}
	if managed.Provider != aiProviderManaged || managed.AdvancedProvider != "openai" {
		t.Fatalf("managed = provider %q advanced %q", managed.Provider, managed.AdvancedProvider)
	}
	if managed.APIKey != "sk-keep" || managed.Model != "gpt-4o" || managed.BaseURL != advanced.BaseURL {
		t.Fatalf("switching to managed must keep the saved key and models: %+v", managed)
	}

	back, err := aiProviderConfigForMode(managed, "advanced")
	if err != nil {
		t.Fatalf("back to advanced: %v", err)
	}
	if back.Provider != "openai" || back.APIKey != "sk-keep" || back.Model != "gpt-4o" {
		t.Fatalf("back to advanced = %+v", back)
	}
}

func TestAIProviderConfigForModeDefaults(t *testing.T) {
	// No remembered provider: advanced falls back to anthropic.
	cfg, err := aiProviderConfigForMode(AIProviderConfig{Provider: aiProviderManaged}, "advanced")
	if err != nil || cfg.Provider != "anthropic" {
		t.Fatalf("advanced without a remembered provider = %q, %v", cfg.Provider, err)
	}

	// Already managed: managed is a no-op and keeps the remembered provider.
	managed := AIProviderConfig{Provider: aiProviderManaged, AdvancedProvider: "codex"}
	same, err := aiProviderConfigForMode(managed, "managed")
	if err != nil || same.Provider != aiProviderManaged || same.AdvancedProvider != "codex" {
		t.Fatalf("managed to managed = %+v, %v", same, err)
	}

	if _, err := aiProviderConfigForMode(managed, "bogus"); err == nil {
		t.Fatal("unknown mode must be an error")
	}
}

func TestApplyManagedRunnerConfig(t *testing.T) {
	cfg := agent.NativeRunnerConfig{
		AuthToken:          "stale-token",
		DisableEnvFallback: false,
	}
	applyManagedRunnerConfig(&cfg, "http://127.0.0.1:3999", magpieRouteTargets{
		Chat: magpieChatGroupModel, Background: magpieBackgroundGroupModel, Light: magpieLightGroupModel,
	})

	if cfg.BaseURL != "http://127.0.0.1:3999" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.ProviderFormat != "anthropic" {
		t.Errorf("ProviderFormat = %q", cfg.ProviderFormat)
	}
	if cfg.APIKey != "yansu-local" || cfg.AuthToken != "" {
		t.Errorf("credentials = key %q token %q", cfg.APIKey, cfg.AuthToken)
	}
	if !cfg.DisableEnvFallback {
		t.Error("DisableEnvFallback must be set in managed mode")
	}
	if cfg.DefaultModel != defaultClaudeChatModel {
		t.Errorf("DefaultModel = %q, want the chat alias", cfg.DefaultModel)
	}
	if got := cfg.ModelAliases["sonnet"]; got != "group/yansu-chat" {
		t.Errorf("sonnet alias = %q", got)
	}
	if got := cfg.ModelAliases["haiku"]; got != "group/yansu-light" {
		t.Errorf("haiku alias = %q", got)
	}
}

// Every pinned Claude id that Yansu's call sites can emit must map to a group.
// An unmapped id would be sent to magpie as-is and could reach a subscription
// directly, skipping the consent gate.
func TestManagedModelAliasesCoverPinnedIDs(t *testing.T) {
	managed := managedModelAliases()
	for short, pinned := range claudeAPIModelAliases() {
		if _, ok := managed[short]; !ok {
			t.Errorf("managed aliases miss short name %q", short)
		}
		if _, ok := managed[pinned]; !ok {
			t.Errorf("managed aliases miss pinned id %q", pinned)
		}
	}
	for key, target := range managed {
		if target != "group/yansu-chat" && target != "group/yansu-light" {
			t.Errorf("alias %q routes to %q, want a managed group", key, target)
		}
	}
	if got := managedBackgroundModelAliases()["sonnet"]; got != "group/yansu-background" {
		t.Errorf("background sonnet alias = %q", got)
	}
}

func TestModelAccountsStatusWithoutSidecar(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{}

	status, err := a.GetModelAccountsStatus()
	if err != nil {
		t.Fatalf("GetModelAccountsStatus: %v", err)
	}
	if status.Mode != "managed" {
		t.Errorf("Mode = %q, want managed for a fresh install", status.Mode)
	}
	if status.Accounts == nil || status.Groups == nil {
		t.Error("accounts and groups must be empty slices, not nil")
	}
	if status.Consent.Accepted || status.Consent.Required {
		t.Errorf("consent = %+v; nothing is required without subscriptions", status.Consent)
	}
	if a.AIReady() {
		t.Error("managed mode without a running gateway must not report ready")
	}
}

func TestSetAIProviderModeRejectsUnknownMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{}
	if err := a.SetAIProviderMode("sideways"); err == nil {
		t.Fatal("unknown mode must be rejected before anything is saved")
	}
}

func TestSidecarActiveCoversManagedAndExternalProviders(t *testing.T) {
	for provider, want := range map[string]bool{"": true, "managed": true, "yansu": true, "codex": true, "claude-code": true, "anthropic": false, "openai": false} {
		cfg := AIProviderConfig{Provider: provider}
		if got := cfg.sidecarActive(); got != want {
			t.Errorf("provider %q: sidecarActive = %v, want %v", provider, got, want)
		}
	}
	if (AIProviderConfig{Provider: "codex"}).managedActive() {
		t.Fatal("an external provider is not managed mode")
	}
}

func TestSetManagedAccountOrderReordersTheBundledIsoformGroup(t *testing.T) {
	s := startFakeSidecar(t)
	a := &App{magpie: s}
	if err := a.bootstrapManagedModels(context.Background()); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	isoform := func(status ModelAccountsStatus) string {
		for _, group := range status.Groups {
			if group.ID == magpieIsoformGroupID {
				return strings.Join(group.Members, ",")
			}
		}
		return ""
	}
	// Every member at Isoform's default effort, High.
	if got := isoform(a.modelAccountsStatus()); got != "claude/claude-sonnet-5-5:high,codex/gpt-5.6-sol:high" {
		t.Fatalf("isoform group before a choice = %q", got)
	}

	steps := []struct {
		order []string
		want  string
	}{
		// Change: Codex answers first, Claude Code is the backup.
		{[]string{"codex", "claude"}, "codex/gpt-5.6-sol:high,claude/claude-sonnet-5-5:high"},
		// Backup off: only Codex is asked.
		{[]string{"codex"}, "codex/gpt-5.6-sol:high"},
		// Backup back on: Claude Code rejoins with the model Isoform planned for it.
		{[]string{"codex", "claude"}, "codex/gpt-5.6-sol:high,claude/claude-sonnet-5-5:high"},
		// Claude Code first again.
		{[]string{"claude", "codex"}, "claude/claude-sonnet-5-5:high,codex/gpt-5.6-sol:high"},
	}
	for _, step := range steps {
		status, err := a.SetManagedAccountOrder(step.order)
		if err != nil {
			t.Fatalf("SetManagedAccountOrder(%v): %v", step.order, err)
		}
		if got := isoform(status); got != step.want {
			t.Fatalf("after %v the status shows %q, want %q", step.order, got, step.want)
		}
		if got := groupsOnDisk(t, s)[magpieIsoformGroupID].Models; got != step.want {
			t.Fatalf("after %v the gateway has %q, want %q", step.order, got, step.want)
		}
		if got := a.magpieRouteTargets().Chat; got != "group/"+magpieIsoformGroupID {
			t.Fatalf("chat routes to %q, want Isoform's group", got)
		}
	}
	if got := strings.Count(callsOnDisk(t, s), "group add Isoform id=isoform"); got != 1 {
		t.Fatalf("the group was created %d times, want 1", got)
	}
}
