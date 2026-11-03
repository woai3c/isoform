package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yansu-agent/agent"
)

// agentHomeCalls reads the CLI calls magpie ran inside Isoform's sandboxed
// agent home (the choice migration's show commands land there). The gateway's
// own calls.log never sees them: runAgentCLI redirects the config home.
func agentHomeCalls(t *testing.T, s *magpieSidecar) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.stateDir, magpieAgentHomeDirName, "config", "magpie", "calls.log"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read agent-home calls.log: %v", err)
	}
	return string(data)
}

// gatewayCalls tolerates a gateway that never ran a CLI command yet.
func gatewayCalls(t *testing.T, s *magpieSidecar) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "calls.log"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read gateway calls.log: %v", err)
	}
	return string(data)
}

// writeFakeAgentHomeState plants the per-agent settings older versions saved
// through the magpie CLI, where the CLI reads them: the sandboxed agent
// home's own magpie config, never the gateway's.
func writeFakeAgentHomeState(t *testing.T, s *magpieSidecar, name, content string) {
	t.Helper()
	dir := filepath.Join(s.stateDir, magpieAgentHomeDirName, "config", "magpie")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create agent-home state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// The picks older versions kept in magpie's per-agent settings and in
// isoform-choice.json move into Isoform's own config, once, and every run and
// Settings row keeps reading them.
func TestMigrateAgentChoicesMovesOldStoresOnce(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), ".isoform-agent"))
	writeFakeAgentHomeState(t, s, "agent-settings.json",
		`{"claude":{"model":"claude/claude-sonnet-5","effort":"xhigh"},"codex":{"effort":"medium"}}`)
	if err := os.WriteFile(filepath.Join(s.stateDir, isoformChoiceFileName),
		[]byte(`{"model":"claude/claude-sonnet-5-5","effort":"low"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{magpie: s}
	app.migrateAgentChoices()

	cfg := loadAIProviderConfig()
	want := map[string]AgentModelChoice{
		"claude-code": {Model: "claude/claude-sonnet-5", Effort: "xhigh"},
		"codex":       {Effort: "medium"},
		"isoform":     {Model: "claude/claude-sonnet-5-5", Effort: "low"},
	}
	if !cfg.AgentChoicesMigrated || !reflect.DeepEqual(cfg.AgentChoices, want) {
		t.Fatalf("migrated config = %+v, want picks %v", cfg, want)
	}
	if _, err := os.Lstat(filepath.Join(s.stateDir, isoformChoiceFileName)); !os.IsNotExist(err) {
		t.Fatalf("the old choice file survived migration: %v", err)
	}

	// Settings shows the migrated picks, and runs get them as the agent's own
	// model id at the chosen effort.
	view, err := app.GetAssistantChoice()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, row := range view.Agents {
		got[row.Agent] = [2]string{row.Model, row.Effort}
	}
	if got["claude-code"] != [2]string{"claude/claude-sonnet-5", "xhigh"} ||
		got["codex"] != [2]string{"", "medium"} ||
		got["isoform"] != [2]string{"claude/claude-sonnet-5-5", "low"} {
		t.Fatalf("Settings rows = %v", got)
	}
	if c := app.runChoice("claude-code", &agent.TaskInput{}); c != (agent.AgentChoice{Model: "claude-sonnet-5", Effort: "xhigh"}) {
		t.Fatalf("Claude Code runs get %+v", c)
	}
	if c := app.runChoice("codex", &agent.TaskInput{}); c != (agent.AgentChoice{Effort: "medium"}) {
		t.Fatalf("Codex runs get %+v", c)
	}

	// Once: old stores written again (an older build running) never overwrite
	// the config on a later start.
	writeFakeAgentHomeState(t, s, "agent-settings.json", `{"claude":{"model":"claude/claude-haiku-4-5"}}`)
	calls := agentHomeCalls(t, s)
	app.migrateAgentChoices()
	if cfg := loadAIProviderConfig(); !reflect.DeepEqual(cfg.AgentChoices, want) {
		t.Fatalf("a second migration changed the picks: %v", cfg.AgentChoices)
	}
	if got := agentHomeCalls(t, s); got != calls {
		t.Fatalf("a marked migration ran the magpie CLI again: %q", strings.TrimPrefix(got, calls))
	}
}

// Without old stores the migration only sets the marker, so it never runs the
// magpie CLI again on later starts.
func TestMigrateAgentChoicesWithoutOldStores(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), ".isoform-agent"))
	app := &App{magpie: s}
	app.migrateAgentChoices()
	cfg := loadAIProviderConfig()
	if !cfg.AgentChoicesMigrated || len(cfg.AgentChoices) != 0 {
		t.Fatalf("fresh install: %+v", cfg)
	}
	calls := agentHomeCalls(t, s)
	app.migrateAgentChoices()
	if got := agentHomeCalls(t, s); got != calls {
		t.Fatalf("a marked migration ran again: %q", strings.TrimPrefix(got, calls))
	}
}

// When the magpie CLI cannot run, nothing is marked and the next start tries
// again.
func TestMigrateAgentChoicesRetriesAfterAFailedRead(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), ".isoform-agent"))
	// Not started, with a port record left behind: the run then fails at the
	// missing binary, as on a first start before the bundle lands.
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.stateDir, magpiePortFileName), []byte(`{"port":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.binaryPath = filepath.Join(t.TempDir(), "magpie-missing")
	app := &App{magpie: s}
	app.migrateAgentChoices()
	if loadAIProviderConfig().AgentChoicesMigrated {
		t.Fatal("a failed read was marked migrated")
	}

	// The binary shows up later: the migration then moves the old picks.
	s.binaryPath = buildFakeMagpie(t)
	writeFakeAgentHomeState(t, s, "agent-settings.json", `{"codex":{"model":"codex/gpt-6.1-sol","effort":"low"}}`)
	app.migrateAgentChoices()
	cfg := loadAIProviderConfig()
	if !cfg.AgentChoicesMigrated || cfg.agentChoice("codex") != (AgentModelChoice{Model: "codex/gpt-6.1-sol", Effort: "low"}) {
		t.Fatalf("retry: %+v", cfg)
	}
}

// A pick already in Isoform's config wins over the old stores.
func TestMigrateAgentChoicesKeepsNewerConfigValues(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), ".isoform-agent"))
	cfg := loadAIProviderConfig()
	cfg.setAgentChoice("claude-code", AgentModelChoice{Model: "claude/claude-haiku-4-5"})
	if err := saveAIProviderConfig(cfg); err != nil {
		t.Fatal(err)
	}
	writeFakeAgentHomeState(t, s, "agent-settings.json", `{"claude":{"model":"claude/claude-sonnet-5","effort":"max"}}`)
	app := &App{magpie: s}
	app.migrateAgentChoices()
	got := loadAIProviderConfig().agentChoice("claude-code")
	if got != (AgentModelChoice{Model: "claude/claude-haiku-4-5"}) {
		t.Fatalf("the old store overwrote Isoform's config: %+v", got)
	}
	if !loadAIProviderConfig().AgentChoicesMigrated {
		t.Fatal("not marked migrated")
	}
}

// New picks are saved in Isoform's own config only: no magpie agent command
// runs and nothing is written into the sandboxed agent home anymore.
func TestSetAgentChoiceStaysInIsoformConfig(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), ".isoform-agent"))
	app := &App{magpie: s}
	app.migrateAgentChoices()
	agentCalls := agentHomeCalls(t, s)

	if _, err := app.SetAgentModel("claude-code", "claude/claude-sonnet-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetAgentEffort("claude-code", "max"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetAgentEffort("codex", "low"); err != nil {
		t.Fatal(err)
	}
	cfg := loadAIProviderConfig()
	if cfg.agentChoice("claude-code") != (AgentModelChoice{Model: "claude/claude-sonnet-5", Effort: "max"}) ||
		cfg.agentChoice("codex") != (AgentModelChoice{Effort: "low"}) {
		t.Fatalf("picks = %+v", cfg.AgentChoices)
	}
	// The catalog listing ("models") still runs; agent show/set commands
	// must not run again, against either magpie home.
	if got := agentHomeCalls(t, s); !strings.HasPrefix(got, agentCalls) {
		t.Fatalf("agent-home calls lost entries: %q -> %q", agentCalls, got)
	} else {
		for _, line := range strings.Split(strings.TrimPrefix(got, agentCalls), "\n") {
			if line == "claude" || line == "codex" || strings.HasPrefix(line, "claude ") || strings.HasPrefix(line, "codex ") {
				t.Fatalf("a pick write ran magpie agent command %q", line)
			}
		}
	}
	for _, line := range strings.Split(gatewayCalls(t, s), "\n") {
		if line == "claude" || line == "codex" || strings.HasPrefix(line, "claude ") || strings.HasPrefix(line, "codex ") {
			t.Fatalf("a pick write ran magpie agent command %q", line)
		}
	}
	if _, err := os.Lstat(filepath.Join(s.stateDir, magpieAgentHomeDirName, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("magpie's per-agent settings were written: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.stateDir, isoformChoiceFileName)); !os.IsNotExist(err) {
		t.Fatalf("isoform-choice.json was written: %v", err)
	}
	// Runs see the saved pick without waiting on any CLI.
	if c := app.runChoice("claude-code", &agent.TaskInput{}); c != (agent.AgentChoice{Model: "claude-sonnet-5", Effort: "max"}) {
		t.Fatalf("runs get %+v", c)
	}
	// Clearing goes back to the defaults and drops the entry.
	if _, err := app.SetAgentModel("claude-code", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetAgentEffort("claude-code", ""); err != nil {
		t.Fatal(err)
	}
	if cfg := loadAIProviderConfig(); cfg.agentChoice("claude-code") != (AgentModelChoice{}) {
		t.Fatalf("cleared pick = %+v", cfg.AgentChoices)
	}
	if c := app.runChoice("claude-code", &agent.TaskInput{}); c != (agent.AgentChoice{Effort: "high"}) {
		t.Fatalf("defaults give runs %+v", c)
	}
}
