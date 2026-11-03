package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yansu-agent/agent"
)

func TestMagpieProfileOwnAndBundled(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "bundled", true: "own"}[external], func(t *testing.T) {
			s := startFakeSidecar(t)
			t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
			if external {
				useFakeExternalMagpie(t, s)
			}
			claude := filepath.Join(t.TempDir(), "settings.json")
			codex := filepath.Join(t.TempDir(), "config.toml")
			for _, path := range []string{claude, codex} {
				if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Dir(claude))
			t.Setenv("CODEX_HOME", filepath.Dir(codex))
			writeFakeState(t, s, "groups.json", `{"isoform":{"name":"Isoform","models":"claude/claude-sonnet-5-5,codex/gpt-6.1-sol","routing":"order","stays":"session"},"work":{"name":"Work","models":"claude/claude-haiku-5-5","routing":"order","stays":"session"}}`)
			writeFakeState(t, s, "providers.json", `{"providers":[{"id":"gemini","models":["pro"]}]}`)
			writeFakeState(t, s, "agents.json", `{"claude":"group/work"}`)
			before := groupsOnDisk(t, s)["work"]
			app := &App{magpie: s}
			state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{Provider: AIProviderConfig{}})
			if err != nil {
				t.Fatal(err)
			}
			app.magpieModels.applyBootstrap(state, nil)
			if err := app.initializeMagpieProfile(context.Background()); err != nil {
				t.Fatal(err)
			}
			profile := []AgentModelChoice{{Model: "codex/gpt-6.1-sol", Effort: "max"}, {Model: "claude/claude-sonnet-5", Effort: "low"}, {Model: "gemini/pro", Effort: "xhigh"}}
			if _, err := app.SetMagpieProfile(profile); err != nil {
				t.Fatal(err)
			}
			if got := groupsOnDisk(t, s)["isoform"].Models; got != "codex/gpt-6.1-sol:max,claude/claude-sonnet-5:low,gemini/pro:xhigh" {
				t.Fatalf("group = %q", got)
			}
			if got := groupsOnDisk(t, s)["work"]; !reflect.DeepEqual(got, before) {
				t.Fatalf("other group changed: %+v", got)
			}
			data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "agents.json"))
			if err != nil || string(data) != `{"claude":"group/work"}` {
				t.Fatalf("agent settings changed: %s %v", data, err)
			}
			for _, path := range []string{claude, codex} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "unchanged" {
					t.Fatalf("executor settings changed: %s %v", path, err)
				}
			}
			if got := loadAIProviderConfig().MagpieProfile; !reflect.DeepEqual(got, profile) {
				t.Fatalf("saved profile = %+v", got)
			}
			if got := app.runChoice("claude-code", &agent.TaskInput{Model: "sonnet"}); got != (agent.AgentChoice{Model: "claude-sonnet-5", Effort: "low"}) {
				t.Fatalf("chat choice = %+v", got)
			}
			app.backgroundUsesChoice.Store(true)
			if got := app.runChoice("codex", &agent.TaskInput{Model: "haiku", Priority: agent.PriorityBackground}); got != (agent.AgentChoice{Model: "gpt-6.1-sol", Effort: "max"}) {
				t.Fatalf("background choice = %+v", got)
			}
			if routes := app.magpieRouteTargets(); routes.Chat != "group/isoform" {
				t.Fatalf("chat route = %+v", routes)
			}
			state, err = s.Bootstrap(context.Background(), magpieBootstrapOptions{Provider: loadAIProviderConfig()})
			if err != nil {
				t.Fatal(err)
			}
			if got := plansByID(state.Groups)["isoform"].Members; !reflect.DeepEqual(got, []string{"codex/gpt-6.1-sol:max", "claude/claude-sonnet-5:low", "gemini/pro:xhigh"}) {
				t.Fatalf("restart group = %v", got)
			}
		})
	}
}

func TestMagpieProfileWriteFailureIsReportedAndRetried(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	useFakeExternalMagpie(t, s)
	writeFakeState(t, s, "groups.json", `{"isoform":{"name":"Isoform","models":"claude/claude-sonnet-5-5","routing":"order","stays":"session"}}`)
	app := &App{magpie: s}
	t.Setenv("FAKE_MAGPIE_READ_ONLY_GROUPS", "1")
	want := []AgentModelChoice{{Model: "claude/claude-sonnet-5", Effort: "max"}}
	if _, err := app.SetMagpieProfile(want); err == nil {
		t.Fatal("write failure was not reported")
	}
	if got := loadAIProviderConfig().MagpieProfile; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved profile = %+v", got)
	}
	t.Setenv("FAKE_MAGPIE_READ_ONLY_GROUPS", "")
	if _, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{Provider: loadAIProviderConfig()}); err != nil {
		t.Fatal(err)
	}
	if got := groupsOnDisk(t, s)["isoform"].Models; !strings.Contains(got, "claude/claude-sonnet-5:max") {
		t.Fatalf("retry group = %s", got)
	}
}

func TestMagpieProfileProtectsOtherGroupsWithTheSameName(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	useFakeExternalMagpie(t, s)
	writeFakeState(t, s, "groups.json", `{"other":{"name":"Isoform","models":"claude/claude-haiku-5-5","routing":"order","stays":"session"}}`)
	before := groupsOnDisk(t, s)["other"]
	profile := []AgentModelChoice{{Model: "claude/claude-sonnet-5", Effort: "max"}}
	groups, err := s.applyOwnedProfile(context.Background(), s.readGroups(context.Background()), profile)
	if err != nil || !hasGroup(groups, "isoform") {
		t.Fatalf("reserved group creation: %v, %v", groups, err)
	}
	if got := groupsOnDisk(t, s)["other"]; !reflect.DeepEqual(got, before) {
		t.Fatalf("other group changed: %+v", got)
	}
	profile[0].Effort = "low"
	if _, err := s.applyOwnedProfile(context.Background(), groups, profile); err != nil {
		t.Fatalf("updating through a name collision: %v", err)
	}
	if got := groupsOnDisk(t, s)["other"]; !reflect.DeepEqual(got, before) {
		t.Fatalf("other group changed after update: %+v", got)
	}
	if got := groupsOnDisk(t, s)["isoform"]; got.Name != "Isoform profile" || got.Models != "claude/claude-sonnet-5:low" {
		t.Fatalf("Isoform group not updated: %+v", got)
	}
}

func TestProfileOrderControlsExecutorRouting(t *testing.T) {
	cfg := AIProviderConfig{AnswerWith: "claude-code", MagpieProfile: []AgentModelChoice{{Model: "codex/gpt-6.1-sol", Effort: "high"}, {Model: "claude/claude-sonnet-5", Effort: "low"}}}
	if got := routeOrder(cfg); len(got) != 0 {
		t.Fatalf("executor skipped the first profile member: %v", got)
	}
	cfg.MagpieProfile[0], cfg.MagpieProfile[1] = cfg.MagpieProfile[1], cfg.MagpieProfile[0]
	if got := routeOrder(cfg); !reflect.DeepEqual(got, []string{"claude-code"}) {
		t.Fatalf("first executor not selected: %v", got)
	}
}

func TestMigrateProfileKeepsAvailableLegacyChoices(t *testing.T) {
	members := []AgentModelChoice{{Model: "claude/sonnet", Effort: "high"}, {Model: "codex/astra", Effort: "high"}}
	cfg := AIProviderConfig{AnswerWith: "isoform", AgentChoices: map[string]AgentModelChoice{
		"claude-code": {Model: "claude/opus", Effort: "max"},
		"codex":       {Model: "codex/missing", Effort: "low"},
		"isoform":     {Model: "gemini/pro", Effort: "xhigh"},
	}}
	got := migrateProfileChoices(members, cfg, []string{"claude/opus", "codex/astra", "gemini/pro"})
	want := []AgentModelChoice{{Model: "gemini/pro", Effort: "xhigh"}, {Model: "claude/opus", Effort: "max"}, {Model: "codex/astra", Effort: "low"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated profile = %+v, want %+v", got, want)
	}
	if members[0].Model != "claude/sonnet" {
		t.Fatal("migration mutated the source group")
	}
}

func TestInitializeProfileWaitsForOlderChoiceMigration(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	useFakeExternalMagpie(t, s)
	writeFakeState(t, s, "groups.json", `{"isoform":{"name":"Isoform","models":"claude/claude-sonnet-5-5,codex/gpt-6.1-sol","routing":"order","stays":"session"}}`)
	if err := os.WriteFile(filepath.Join(s.stateDir, isoformChoiceFileName), []byte(`{"model":"codex/gpt-6.1-sol","effort":"max"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveAIProviderConfig(AIProviderConfig{AnswerWith: "isoform"}); err != nil {
		t.Fatal(err)
	}
	app := &App{magpie: s}
	if err := app.initializeMagpieProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := loadAIProviderConfig()
	if !cfg.AgentChoicesMigrated || len(cfg.MagpieProfile) == 0 || cfg.MagpieProfile[0] != (AgentModelChoice{Model: "codex/gpt-6.1-sol", Effort: "max"}) {
		t.Fatalf("profile after old migration: %+v", cfg.MagpieProfile)
	}
}

func TestOnboardingOrderKeepsOtherProfileProviders(t *testing.T) {
	assistantChoiceTestHome(t)
	profile := []AgentModelChoice{{Model: "claude/sonnet", Effort: "low"}, {Model: "codex/astra", Effort: "max"}, {Model: "gemini/pro", Effort: "xhigh"}}
	if err := saveAIProviderConfig(AIProviderConfig{MagpieProfile: profile}); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if _, err := app.SetManagedAccountOrder([]string{"codex", "claude"}); err != nil {
		t.Fatal(err)
	}
	want := []AgentModelChoice{profile[1], profile[0], profile[2]}
	if got := loadAIProviderConfig().MagpieProfile; !reflect.DeepEqual(got, want) {
		t.Fatalf("profile after onboarding order = %+v, want %+v", got, want)
	}
}

func TestExternalProfileBackgroundUsesOnlyProfileProviders(t *testing.T) {
	assistantChoiceTestHome(t)
	if err := saveAIProviderConfig(AIProviderConfig{MagpieProfile: []AgentModelChoice{{Model: "codex/gpt-6.1-sol", Effort: "max"}}}); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.agentChoices.setCatalog([]string{"claude/claude-haiku-5-5", "codex/gpt-6-luna", "codex/gpt-6.1-sol"}, nil)
	if got := app.externalProfileBackgroundModel(); got != "codex/gpt-6-luna" {
		t.Fatalf("background model = %q", got)
	}
	app.agentChoices.setCatalog([]string{"claude/claude-haiku-5-5"}, nil)
	if got := app.externalProfileBackgroundModel(); got != "codex/gpt-6.1-sol" {
		t.Fatalf("background fallback = %q", got)
	}
}

func TestProfileRoutesActualNativeChatAndBackgroundRequests(t *testing.T) {
	assistantChoiceTestHome(t)
	if err := saveAIProviderConfig(AIProviderConfig{MagpieProfile: []AgentModelChoice{{Model: "claude/claude-sonnet-5", Effort: "max"}}}); err != nil {
		t.Fatal(err)
	}
	var received struct {
		Model        string `json:"model"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"test","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer server.Close()
	app := &App{magpie: &magpieSidecar{external: true}}
	app.magpieModels.applyBootstrap(magpieBootstrapState{External: true, ActiveGroup: "isoform", Groups: []magpieGroupPlan{{ID: "isoform", Members: []string{"claude/claude-sonnet-5:max"}}}}, nil)
	app.agentChoices.setCatalog([]string{"claude/claude-haiku-5-5", "claude/claude-sonnet-5"}, nil)
	cfg := agent.NativeRunnerConfig{}
	applyManagedRunnerConfig(&cfg, server.URL, app.magpieRouteTargets())
	cfg.DefaultModel = "sonnet"
	app.configureMagpieRunChoices(&cfg)
	runner := agent.NewNativeAgentRunner(cfg)
	if err := runner.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		priority      string
		useChoice     bool
		model, effort string
	}{
		{agent.PriorityInteractive, false, "group/isoform", ""},
		{agent.PriorityBackground, true, "group/isoform", ""},
		{agent.PriorityBackground, false, "claude/claude-haiku-5-5", "medium"},
	} {
		app.backgroundUsesChoice.Store(tc.useChoice)
		received.Model, received.OutputConfig.Effort = "", ""
		_, err := runner.ExecuteTask(context.Background(), &agent.TaskInput{Prompt: "hello", Model: "sonnet", Priority: tc.priority, NoTools: true, NoSystem: true}, func(*agent.StreamEvent) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if received.Model != tc.model || received.OutputConfig.Effort != tc.effort {
			t.Fatalf("request = %+v; want %s, %s", received, tc.model, tc.effort)
		}
	}
}
