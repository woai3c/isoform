package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

func TestParseMagpieAgentShow(t *testing.T) {
	for _, tc := range []struct {
		name, text, agent, model, effort string
	}{
		{"set", "  Claude Code  local/claude-opus-5-5[1m]  ·  effort high  ·  haiku local/claude-haiku-5-5  ~/.claude/settings.json\n", "Claude Code", "local/claude-opus-5-5[1m]", "high"},
		{"unset", "  Claude Code  —  ·  effort —  ~/.claude/settings.json\n", "Claude Code", "", ""},
		{"effort only", "  Codex        —  ·  effort xhigh  ~/.codex/config.toml\n", "Codex", "", "xhigh"},
		{"other agent", "  Codex  gpt-6-astra  ·  effort high  ~/.codex/config.toml\n", "Claude Code", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, effort := parseMagpieAgentShow(tc.text, tc.agent)
			if model != tc.model || effort != tc.effort {
				t.Fatalf("got %q %q, want %q %q", model, effort, tc.model, tc.effort)
			}
		})
	}
}

func TestParseMagpieModelList(t *testing.T) {
	text := `  Routing groups
  group/isoform               Isoform  high
  Local
  local/claude-opus-5-5   Claude Opus 5.5  low/medium/high/xhigh/max
  local/gpt-6-astra       GPT-6 Astra  low/medium/high/xhigh/ultra
  local/local-big
  claude is shown every provider's models · magpie visible claude <family>,… narrows it
  http://127.0.0.1:3499/v1
`
	models, efforts := parseMagpieModelList(text)
	if fmt.Sprint(models) != "[local/claude-opus-5-5 local/gpt-6-astra local/local-big]" {
		t.Fatalf("models = %v", models)
	}
	// Only the five steps Settings offers; a model listed without steps has
	// none.
	if fmt.Sprint(efforts) != "map[local/claude-opus-5-5:[low medium high xhigh max] local/gpt-6-astra:[low medium high xhigh] local/local-big:[]]" {
		t.Fatalf("efforts = %v", efforts)
	}
	claude, _ := findChoiceAgent("claude-code")
	if got := effortSteps(claude, "local/claude-opus-5-5[1m]", efforts); fmt.Sprint(got) != "[low medium high xhigh max]" {
		t.Fatalf("listed steps = %v", got)
	}
	if got := effortSteps(claude, "local/local-big", efforts); len(got) != 0 {
		t.Fatalf("a model listed without steps offers %v", got)
	}
	codex, _ := findChoiceAgent("codex")
	if got := effortSteps(codex, "", efforts); fmt.Sprint(got) != "[low medium high xhigh]" {
		t.Fatalf("default model steps = %v", got)
	}
	if got := effortSteps(codex, "codex/gpt-7-unlisted", efforts); fmt.Sprint(got) != "[low medium high xhigh]" {
		t.Fatalf("a model the catalog does not list gets the agent's steps: %v", got)
	}
	// Isoform's effort covers its whole group: all five, whatever its first model.
	isoform, _ := findChoiceAgent("isoform")
	if got := effortSteps(isoform, "local/local-big", efforts); fmt.Sprint(got) != "[low medium high xhigh max]" {
		t.Fatalf("Isoform steps = %v", got)
	}
}

func TestNearestEffortKeepsAnEffortAcrossModels(t *testing.T) {
	three := []string{"low", "medium", "high"}
	for _, tc := range []struct {
		steps     []string
		want, got string
	}{
		{three, "medium", "medium"},
		{three, "max", "high"},
		{three, "xhigh", "high"},
		{[]string{"medium", "max"}, "high", "medium"},
		{[]string{"low", "xhigh"}, "medium", "low"},
		{nil, "high", ""},
	} {
		if got := nearestEffort(tc.steps, tc.want); got != tc.got {
			t.Errorf("nearestEffort(%v, %q) = %q, want %q", tc.steps, tc.want, got, tc.got)
		}
	}
}

func TestChoiceAgentsOfferWhatEachCanRunWithItsOwnSignIn(t *testing.T) {
	catalog := []string{"claude/claude-opus-5-5", "anthropic/claude-haiku-5-5", "codex/gpt-6-astra", "openai/gpt-5.6", "deepseek/deepseek-chat"}
	want := map[string][]string{
		"claude-code": {"claude/claude-opus-5-5", "anthropic/claude-haiku-5-5"},
		"codex":       {"codex/gpt-6-astra", "openai/gpt-5.6"},
		"isoform":     catalog,
	}
	for _, ca := range choiceAgents {
		var got []string
		for _, model := range catalog {
			if ca.runs(model) {
				got = append(got, model)
			}
		}
		if !slices.Equal(got, want[ca.id]) {
			t.Errorf("%s offers %v, want %v", ca.id, got, want[ca.id])
		}
	}
	if modelPart("claude/claude-opus-5-5[1m]") != "claude-opus-5-5" {
		t.Fatal("modelPart must give the agent's own id")
	}
	// Runs keep the 1M-context variant: Claude Code takes "claude-opus-5-5[1m]".
	if runModel("claude/claude-opus-5-5[1m]") != "claude-opus-5-5[1m]" || runModel("gpt-6-astra") != "gpt-6-astra" {
		t.Fatal("runModel must keep the context variant and drop only the provider")
	}
}

func TestApplyIsoformChoice(t *testing.T) {
	members := []string{"claude/claude-sonnet-5-5", "codex/gpt-6-astra:high", "local/big"}
	ollama := []string{"ollama/llama3.2:latest", "claude/claude-sonnet-5-5:high"}
	for _, tc := range []struct {
		choice AgentModelChoice
		want   []string
	}{
		{AgentModelChoice{}, []string{"ollama/llama3.2:latest:high", "claude/claude-sonnet-5-5:high"}},
		{AgentModelChoice{Model: "ollama/llama3.2:latest", Effort: "low"}, []string{"ollama/llama3.2:latest:low", "claude/claude-sonnet-5-5:low"}},
	} {
		if got := applyIsoformChoice(ollama, tc.choice); !slices.Equal(got, tc.want) {
			t.Fatalf("ollama members %+v: got %v, want %v", tc.choice, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name   string
		choice AgentModelChoice
		want   []string
	}{
		{"the default is High on every member", AgentModelChoice{}, []string{"claude/claude-sonnet-5-5:high", "codex/gpt-6-astra:high", "local/big:high"}},
		{"chosen member first", AgentModelChoice{Model: "local/big"}, []string{"local/big:high", "claude/claude-sonnet-5-5:high", "codex/gpt-6-astra:high"}},
		{"new model joins first", AgentModelChoice{Model: "deepseek/deepseek-chat"}, []string{"deepseek/deepseek-chat:high", "claude/claude-sonnet-5-5:high", "codex/gpt-6-astra:high", "local/big:high"}},
		{"effort on every member", AgentModelChoice{Model: "codex/gpt-6-astra", Effort: "medium"}, []string{"codex/gpt-6-astra:medium", "claude/claude-sonnet-5-5:medium", "local/big:medium"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyIsoformChoice(members, tc.choice); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func assistantChoiceTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(home, ".isoform-agent"))
	if err := os.MkdirAll(filepath.Join(home, ".isoform-agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// One pick, with Isoform as the backup: the pick is saved in Isoform's own
// routing config, and the subscription order Isoform thinks with stays.
func TestSetAnsweringAgentIsOnePickWithIsoformAsBackup(t *testing.T) {
	assistantChoiceTestHome(t)
	cfg := loadAIProviderConfig()
	cfg.ManagedOrder = []string{"codex", "claude"}
	if err := saveAIProviderConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := answeringAgent(loadAIProviderConfig()); got != "claude-code" {
		t.Fatalf("before a pick, onboarding's first pickable assistant answers: %q", got)
	}
	// A choice can be saved while the gateway is starting.
	if view, err := (&App{}).SetAnsweringAgent("isoform"); err != nil || view.Answering != "isoform" {
		t.Fatalf("Isoform pick while starting: %+v %v", view, err)
	}
	// The gateway runs, but its models cannot be read: the pick still saves
	// and every row still shows.
	app := &App{magpie: &magpieSidecar{healthy: true}}
	for _, tc := range []struct {
		agent   string
		route   []string
		isoform bool
	}{
		{"isoform", nil, true},
		{"claude-code", []string{"claude-code"}, false},
	} {
		view, err := app.SetAnsweringAgent(tc.agent)
		cfg := loadAIProviderConfig()
		if err != nil || view.Answering != tc.agent || cfg.AnswerWith != tc.agent || len(view.Agents) != 4 || !view.IsoformReady {
			t.Fatalf("%s: err=%v view=%+v saved=%q", tc.agent, err, view, cfg.AnswerWith)
		}
		for _, row := range view.Agents {
			if row.CanAnswer != (row.Agent != "codex" && row.Agent != "deepseek") {
				t.Fatalf("%s row can answer: %v", row.Agent, row.CanAnswer)
			}
		}
		if !slices.Equal(routeOrder(cfg), tc.route) || (answeringAgent(cfg) == answeringIsoform) != tc.isoform {
			t.Fatalf("%s: route=%v", tc.agent, routeOrder(cfg))
		}
		if !slices.Equal(cfg.managedOrder(), []string{"codex", "claude"}) {
			t.Fatalf("%s changed the subscription order: %v", tc.agent, cfg.managedOrder())
		}
	}
	if _, err := app.SetAnsweringAgent("nobody"); err == nil {
		t.Fatal("an unknown pick was saved")
	}
	// Codex cannot answer in Isoform yet: refused, and the saved pick stays.
	if view, err := app.SetAnsweringAgent("codex"); err == nil || view.Answering != "claude-code" || loadAIProviderConfig().AnswerWith != "claude-code" {
		t.Fatalf("a Codex pick was saved: %+v %v", view, err)
	}
	// Onboarding's order picker means its first assistant answers again.
	if _, err := app.SetManagedAccountOrder([]string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if got := answeringAgent(loadAIProviderConfig()); got != "claude-code" {
		t.Fatalf("after onboarding's picker: %q", got)
	}
}

// The binding-level regression for pr-desk's finding: a Codex pick through
// the Settings binding is refused and changes nothing: no saved pick, no
// subscription order, no magpie group, no magpie command at all.
func TestSetAnsweringAgentRefusesCodexAndChangesNothing(t *testing.T) {
	s := startFakeSidecar(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), ".isoform-agent"))
	cfg := loadAIProviderConfig()
	cfg.ManagedOrder = []string{"claude", "codex"}
	if err := saveAIProviderConfig(cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{magpie: s}
	if err := app.bootstrapManagedModels(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	before := loadAIProviderConfig()
	groups, calls := groupsOnDisk(t, s), callsOnDisk(t, s)
	view, err := app.SetAnsweringAgent("codex")
	if err == nil || !strings.Contains(err.Error(), "can't use Isoform's tools") {
		t.Fatalf("Codex pick: %v", err)
	}
	after := loadAIProviderConfig()
	if view.Answering != "claude-code" || after.AnswerWith != before.AnswerWith || !slices.Equal(after.managedOrder(), before.managedOrder()) {
		t.Fatalf("the refused pick changed the config: view %q, %+v -> %+v", view.Answering, before, after)
	}
	if got := groupsOnDisk(t, s); fmt.Sprint(got) != fmt.Sprint(groups) {
		t.Fatalf("the refused pick changed magpie's groups:\n%v\n%v", groups, got)
	}
	if got := callsOnDisk(t, s); got != calls {
		t.Fatalf("the refused pick ran magpie: %q", strings.TrimPrefix(got, calls))
	}
}

// A Codex pick saved before Codex became unpickable (#68, refused since #72)
// is no pick: the normal default answers, Settings shows the answerer that
// really answers, and the saved value is cleaned once.
func TestASavedCodexPickIsNoPickAndIsCleanedOnce(t *testing.T) {
	assistantChoiceTestHome(t)
	cfg := loadAIProviderConfig()
	cfg.ManagedOrder = []string{"codex", "claude"}
	cfg.AnswerWith = "codex"
	if err := saveAIProviderConfig(cfg); err != nil {
		t.Fatal(err)
	}
	read := loadAIProviderConfig()
	if read.AnswerWith != "" {
		t.Fatalf("the stale Codex pick stayed saved: %+v", read)
	}
	if got := answeringAgent(read); got != "claude-code" {
		t.Fatalf("the stale pick still answers: %q", got)
	}
	if got := routeOrder(read); !slices.Equal(got, []string{"claude-code"}) {
		t.Fatalf("the stale pick still routes: %v", got)
	}
	// Cleaned once: the file itself no longer carries the pick.
	path, err := aiProviderConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "answerWith") {
		t.Fatalf("the pick stayed on disk: %s", raw)
	}
	// Settings shows the answerer that really answers, and Codex stays
	// unpickable.
	app := &App{magpie: &magpieSidecar{healthy: true}}
	view, err := app.GetAssistantChoice()
	if err != nil || view.Answering != "claude-code" {
		t.Fatalf("Settings shows %q, %v", view.Answering, err)
	}
	for _, row := range view.Agents {
		if row.CanAnswer != (row.Agent != "codex" && row.Agent != "deepseek") {
			t.Fatalf("%s row can answer: %v", row.Agent, row.CanAnswer)
		}
	}
}

func TestRunChoiceKeepsBackgroundWorkLight(t *testing.T) {
	app := &App{}
	app.agentChoices.set(map[string]agent.AgentChoice{"claude-code": {Model: "claude-opus-5-5", Effort: "max"}})
	chat := &agent.TaskInput{Model: "sonnet"}
	background := &agent.TaskInput{Model: "sonnet", Priority: agent.PriorityBackground}
	extraction := &agent.TaskInput{Model: "haiku", Priority: agent.PriorityBackground}
	title := &agent.TaskInput{Model: "haiku"}
	if c := app.runChoice("claude-code", chat); c != (agent.AgentChoice{Model: "claude-opus-5-5", Effort: "max"}) {
		t.Fatalf("chat: %+v", c)
	}
	if c := app.runChoice("claude-code", background); c != (agent.AgentChoice{Model: "claude-sonnet-5-5", Effort: "medium"}) {
		t.Fatalf("background, switch off: %+v", c)
	}
	if c := app.runChoice("codex", background); c != (agent.AgentChoice{Effort: "medium"}) {
		t.Fatalf("Codex background, switch off: %+v", c)
	}
	// Interactive light tasks stay cheap; background light tasks follow the switch.
	for _, on := range []bool{false, true} {
		app.backgroundUsesChoice.Store(on)
		if c := app.runChoice("claude-code", title); c != (agent.AgentChoice{}) {
			t.Fatalf("interactive light task (switch %v) got %+v", on, c)
		}
		want := agent.AgentChoice{}
		if on {
			want = agent.AgentChoice{Model: "claude-opus-5-5", Effort: "max"}
		}
		if c := app.runChoice("claude-code", extraction); c != want {
			t.Fatalf("background light task (switch %v) got %+v", on, c)
		}
	}
	if c := app.runChoice("claude-code", background); c != (agent.AgentChoice{Model: "claude-opus-5-5", Effort: "max"}) {
		t.Fatalf("background, switch on: %+v", c)
	}
}

// Before Isoform reads an agent's choice, its runs use the default High, but
// in every gateway state.
func TestRunChoiceDefaultsToHighUntilRead(t *testing.T) {
	assistantChoiceTestHome(t)
	chat := &agent.TaskInput{Model: "sonnet"}
	app := &App{magpie: &magpieSidecar{stateDir: t.TempDir()}}
	if c := app.runChoice("claude-code", chat); c != (agent.AgentChoice{Effort: "high"}) {
		t.Fatalf("unread choice: %+v", c)
	}
	app.agentChoices.set(map[string]agent.AgentChoice{"claude-code": {Model: "claude-haiku-4-5"}})
	if c := app.runChoice("claude-code", chat); c != (agent.AgentChoice{Model: "claude-haiku-4-5"}) {
		t.Fatalf("a model without effort steps: %+v", c)
	}
	own := &App{magpie: &magpieSidecar{stateDir: t.TempDir(), external: true}}
	if c := own.runChoice("claude-code", chat); c != (agent.AgentChoice{Effort: "high"}) {
		t.Fatalf("the user's own magpie: %+v", c)
	}
}

func TestSetBackgroundUsesChoiceSavesAndApplies(t *testing.T) {
	assistantChoiceTestHome(t)
	app := &App{}
	view, err := app.SetBackgroundUsesChoice(true)
	if err != nil || !view.BackgroundUsesChoice || !loadAIProviderConfig().BackgroundUsesChoice || !app.backgroundUsesChoice.Load() {
		t.Fatalf("on: %+v, %v", view, err)
	}
	view, err = app.SetBackgroundUsesChoice(false)
	if err != nil || view.BackgroundUsesChoice || loadAIProviderConfig().BackgroundUsesChoice || app.backgroundUsesChoice.Load() {
		t.Fatalf("off: %+v, %v", view, err)
	}
}

func TestOpenAssistantSignInRunsTheAgentsOwnLogin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Terminal sign-in is macOS only")
	}
	var opened string
	previous := openSignInTerminal
	openSignInTerminal = func(script string) error { opened = script; return nil }
	t.Cleanup(func() { openSignInTerminal = previous })
	app := &App{}
	for agentID, want := range map[string]string{"claude-code": "'auth' 'login'", "claude": "'auth' 'login'", "codex": "'login'"} {
		if err := app.OpenAssistantSignIn(agentID); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(opened)
		if err != nil || !strings.HasPrefix(string(data), "#!/bin/sh\n") || !strings.Contains(string(data), want) {
			t.Fatalf("%s script: %q %v", agentID, data, err)
		}
		_ = os.RemoveAll(filepath.Dir(opened))
	}
	if err := app.OpenAssistantSignIn("isoform"); err == nil {
		t.Fatal("Isoform has no sign-in")
	}
}

func magpieBinaryForTest(t *testing.T) string {
	t.Helper()
	bin := strings.TrimSpace(os.Getenv(magpieOverrideEnv))
	if bin == "" {
		bin = filepath.Join("magpie-bundle", "magpie-cli-"+runtime.GOOS+"-"+runtime.GOARCH)
	}
	if abs, err := filepath.Abs(bin); err == nil {
		bin = abs
	}
	if !isExecutableFile(bin) {
		t.Skipf("no magpie binary at %s; set %s or run scripts/download-magpie.sh", bin, magpieOverrideEnv)
	}
	return bin
}

// TestAssistantChoiceThroughRealMagpie runs the bundled magpie in a temporary
// HOME that stands in for the user's. Every choice goes through magpie into
// Isoform's own state and reaches runs, and the user's own Claude Code and
// Codex settings files stay byte-for-byte unchanged.
func TestAssistantChoiceThroughRealMagpie(t *testing.T) {
	bin := magpieBinaryForTest(t)
	home := assistantChoiceTestHome(t)
	userFiles := map[string]string{
		filepath.Join(home, ".claude", "settings.json"): "{\n  \"model\": \"opus\",\n  \"theme\": \"dark\"\n}\n",
		filepath.Join(home, ".codex", "config.toml"):    "model = \"gpt-6-astra\"\nmodel_reasoning_effort = \"high\"\n",
	}
	for path, content := range userFiles {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s := newMagpieSidecar(filepath.Join(home, ".isoform-agent", magpieStateDirName))
	s.binaryPath = bin
	s.startDeadline = 30 * time.Second
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A provider that never answers: its model list is typed in, nothing is
	// fetched and no request leaves the machine.
	if _, err := s.runCLI(ctx, "provider", "add", "Local", "url=http://127.0.0.1:9/v1", "key=test",
		"models=claude-opus-5-5,claude-haiku-5-5,gpt-6-astra,local-big"); err != nil {
		t.Fatalf("add provider: %v", err)
	}
	// Bootstrap made Isoform's group earlier, as it does on every computer
	// with a signed-in subscription.
	if _, err := s.runCLI(ctx, "group", "add", magpieIsoformGroupName, "models=local/claude-haiku-5-5,local/gpt-6-astra", "routing=order", "stays=session"); err != nil {
		t.Fatalf("add isoform group: %v", err)
	}
	app := &App{magpie: s}

	view, err := app.GetAssistantChoice()
	if err != nil || !view.Editable {
		t.Fatalf("choice = %+v, %v", view, err)
	}
	offered := map[string][]string{}
	for _, row := range view.Agents {
		offered[row.Agent] = row.Models
		// High until the user changes it.
		if row.Effort != "high" {
			t.Fatalf("%s default effort %q, want high", row.Agent, row.Effort)
		}
	}
	if !slices.Equal(offered["claude-code"], []string{"local/claude-opus-5-5", "local/claude-haiku-5-5"}) ||
		!slices.Equal(offered["codex"], []string{"local/gpt-6-astra"}) || len(offered["isoform"]) != 4 {
		t.Fatalf("offered %v", offered)
	}
	if c := app.agentChoices.get("claude-code"); c != (agent.AgentChoice{Effort: "high"}) {
		t.Fatalf("Claude Code runs before any change: %+v", c)
	}

	// The pick changes no magpie group; a Codex pick is refused.
	groupsBefore := fmt.Sprint(s.readGroups(ctx))
	for _, agentID := range []string{"isoform", "claude-code"} {
		if view, err := app.SetAnsweringAgent(agentID); err != nil || view.Answering != agentID {
			t.Fatalf("pick %s: %+v, %v", agentID, view, err)
		}
	}
	if _, err := app.SetAnsweringAgent("codex"); err == nil {
		t.Fatal("Codex was picked")
	}
	if after := fmt.Sprint(s.readGroups(ctx)); after != groupsBefore {
		t.Fatalf("the pick changed magpie's groups:\n%s\n%s", groupsBefore, after)
	}

	steps := []struct{ agent, field, value string }{
		{"claude-code", "model", "local/claude-opus-5-5"},
		{"claude-code", "effort", "xhigh"},
		{"codex", "model", "local/gpt-6-astra"},
		{"codex", "effort", "medium"},
		{"isoform", "model", "local/local-big"},
		{"isoform", "effort", "medium"},
	}
	for _, step := range steps {
		set := app.SetAgentModel
		if step.field == "effort" {
			set = app.SetAgentEffort
		}
		if _, err := set(step.agent, step.value); err != nil {
			t.Fatalf("set %s %s: %v", step.agent, step.field, err)
		}
	}
	if _, err := app.SetAgentModel("claude-code", "local/local-big"); err == nil {
		t.Fatal("Claude Code accepted a model it cannot run with its own sign-in")
	}
	if _, err := app.SetAgentEffort("codex", "ultra-plus"); err == nil {
		t.Fatal("Codex accepted an effort its model does not offer")
	}
	if view, err := app.SetBackgroundUsesChoice(true); err != nil || !view.BackgroundUsesChoice {
		t.Fatalf("background switch: %+v, %v", view, err)
	}

	view, err = app.GetAssistantChoice()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, row := range view.Agents {
		got[row.Agent] = [2]string{modelPart(row.Model), row.Effort}
		if row.Agent == "claude-code" && fmt.Sprint(row.Efforts) != "[low medium high xhigh max]" {
			t.Fatalf("Claude Opus effort steps from magpie: %v", row.Efforts)
		}
	}
	want := map[string][2]string{"claude-code": {"claude-opus-5-5", "xhigh"}, "codex": {"gpt-6-astra", "medium"}, "isoform": {"local-big", "medium"}}
	if fmt.Sprint(got) != fmt.Sprint(want) || view.Answering != "claude-code" || !view.BackgroundUsesChoice {
		t.Fatalf("read back %v (%+v), want %v", got, view, want)
	}
	// Runs read the cached choice, with the agent's own model id; background
	// work follows the pick while the switch is on.
	background := &agent.TaskInput{Priority: agent.PriorityBackground}
	if c := app.runChoice("claude-code", &agent.TaskInput{}); c != (agent.AgentChoice{Model: "claude-opus-5-5", Effort: "xhigh"}) {
		t.Fatalf("Claude Code chat runs get %+v", c)
	}
	if c := app.runChoice("claude-code", background); c != (agent.AgentChoice{Model: "claude-opus-5-5", Effort: "xhigh"}) {
		t.Fatalf("Claude Code background runs with the switch on get %+v", c)
	}
	if c := app.runChoice("codex", &agent.TaskInput{}); c != (agent.AgentChoice{Model: "gpt-6-astra", Effort: "medium"}) {
		t.Fatalf("Codex runs get %+v", c)
	}
	// Isoform's runs think through the isoform group: the pick first, every
	// member at the chosen effort.
	if group := isoformGroupMembers(t, s, ctx); len(group) == 0 || group[0] != "local/local-big:medium" {
		t.Fatalf("isoform group %v", group)
	} else {
		for _, member := range group {
			if !strings.HasSuffix(member, ":medium") {
				t.Fatalf("member %q lacks the chosen effort: %v", member, group)
			}
		}
	}
	// The choice lives in Isoform's own config, not in magpie's per-agent
	// settings or the user's folders.
	if cfg := loadAIProviderConfig(); cfg.agentChoice("claude-code") != (AgentModelChoice{Model: "local/claude-opus-5-5", Effort: "xhigh"}) {
		t.Fatalf("Isoform's saved Claude Code choice: %+v", cfg.AgentChoices)
	}
	agentHome := filepath.Join(s.stateDir, magpieAgentHomeDirName)
	if _, err := os.Lstat(filepath.Join(agentHome, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("magpie's per-agent settings were written: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.stateDir, isoformChoiceFileName)); !os.IsNotExist(err) {
		t.Fatalf("isoform-choice.json was written: %v", err)
	}

	// Back to defaults: the saved picks clear from Isoform's config, effort is
	// High.
	for _, agentID := range []string{"claude-code", "codex", "isoform"} {
		if _, err := app.SetAgentModel(agentID, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := app.SetAgentEffort(agentID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.SetBackgroundUsesChoice(false); err != nil {
		t.Fatal(err)
	}
	if c := app.agentChoices.get("claude-code"); c != (agent.AgentChoice{Effort: "high"}) {
		t.Fatalf("defaults left %+v", c)
	}
	if group := isoformGroupMembers(t, s, ctx); len(group) == 0 {
		t.Fatal("isoform group lost its members")
	} else {
		for _, member := range group {
			if !strings.HasSuffix(member, ":high") {
				t.Fatalf("after defaults, member %q is not at High: %v", member, group)
			}
		}
	}

	for path, content := range userFiles {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatalf("the user's %s changed:\n%s (%v)", path, data, err)
		}
	}
}

func isoformGroupMembers(t *testing.T, s *magpieSidecar, ctx context.Context) []string {
	t.Helper()
	for _, group := range s.readGroups(ctx) {
		if group.ID == magpieIsoformGroupID {
			return group.Members
		}
	}
	return nil
}

// TestMigrateAgentChoicesThroughRealMagpie seeds the pre-E2 stores the way
// older versions did — the real magpie CLI writing agent settings inside
// Isoform's sandboxed agent home, plus isoform-choice.json in the magpie state
// dir — then migrates and checks every value landed in Isoform's own config
// and the old file is gone.
func TestMigrateAgentChoicesThroughRealMagpie(t *testing.T) {
	bin := magpieBinaryForTest(t)
	home := assistantChoiceTestHome(t)
	s := newMagpieSidecar(filepath.Join(home, ".isoform-agent", magpieStateDirName))
	s.binaryPath = bin
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// runCLIEnv resolves the gateway port from the last run's record.
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.stateDir, magpiePortFileName), []byte(`{"port":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runAgentCLI(ctx, "claude", "claude/claude-opus-5-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runAgentCLI(ctx, "claude", "effort", "xhigh"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runAgentCLI(ctx, "codex", "effort", "medium"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.stateDir, isoformChoiceFileName), []byte(`{"model":"local/local-big","effort":"low"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := &App{magpie: s}
	app.migrateAgentChoices()
	cfg := loadAIProviderConfig()
	want := map[string]AgentModelChoice{
		"claude-code": {Model: "claude/claude-opus-5-5", Effort: "xhigh"},
		"codex":       {Effort: "medium"},
		"isoform":     {Model: "local/local-big", Effort: "low"},
	}
	if !cfg.AgentChoicesMigrated || !reflect.DeepEqual(cfg.AgentChoices, want) {
		t.Fatalf("migrated config = %+v, want %v", cfg.AgentChoices, want)
	}
	if _, err := os.Lstat(filepath.Join(s.stateDir, isoformChoiceFileName)); !os.IsNotExist(err) {
		t.Fatalf("the old choice file survived migration: %v", err)
	}
	// The user's own assistant folders never existed in this home.
	for _, dir := range []string{".claude", ".codex"} {
		if _, err := os.Lstat(filepath.Join(home, dir)); !os.IsNotExist(err) {
			t.Fatalf("the user's %s was created: %v", dir, err)
		}
	}
}

// An old onboarding order is not permission to pick a blocked executor.
func TestAssistantChoiceDefaultSkipsUnpickableAgents(t *testing.T) {
	for _, order := range [][]string{{"codex", "claude"}, {"codex"}} {
		for _, saved := range []string{"", "codex", "deepseek"} {
			cfg := AIProviderConfig{ManagedOrder: order, AnswerWith: saved}
			if got := answeringAgent(cfg); got != "claude-code" {
				t.Errorf("order=%v saved=%q: unpickable default %q", order, saved, got)
			}
			if got := routeOrder(cfg); !slices.Equal(got, []string{"claude-code"}) {
				t.Errorf("order=%v saved=%q: unpickable route %v", order, saved, got)
			}
		}
	}
}

func TestAssistantChoiceEditableAcrossGatewayStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		external bool
		healthy  bool
		reason   string
	}{
		{"own magpie", "managed", true, true, "own_magpie"},
		{"bundled", "managed", false, true, ""},
		{"starting", "managed", false, false, ""},
		{"own key", "anthropic", false, false, "own_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assistantChoiceTestHome(t)
			catalog := []string{"claude/claude-opus-5-5", "codex/gpt-6-astra", "local/model"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Errorf("unexpected request %s", r.URL.Path)
					return
				}
				_, _ = io.WriteString(w, `{"data":[{"id":"claude/claude-opus-5-5"},{"id":"codex/gpt-6-astra"},{"id":"local/model"},{"id":"group/test"}]}`)
			}))
			defer server.Close()
			port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
			if err != nil {
				t.Fatal(err)
			}
			cfg := AIProviderConfig{Provider: tc.provider}
			if err := saveAIProviderConfig(cfg); err != nil {
				t.Fatal(err)
			}
			s := &magpieSidecar{stateDir: t.TempDir(), external: tc.external, healthy: tc.healthy, port: port}
			if tc.name == "bundled" {
				bin := filepath.Join(t.TempDir(), "magpie")
				script := "#!/bin/sh\nprintf '%s\\n' 'claude/claude-opus-5-5 Opus low/medium/high/xhigh/max' 'codex/gpt-6-astra GPT low/medium/high/xhigh' 'local/model Local'\n"
				if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				s.binaryPath = bin
			}
			app := &App{magpie: s}
			app.agentChoices.setCatalog(catalog, map[string][]string{})
			view, err := app.GetAssistantChoice()
			if err != nil || !view.Editable || view.Reason != tc.reason {
				t.Fatalf("view %+v: %v", view, err)
			}
			for _, row := range view.Agents {
				if row.Agent == "isoform" && (!row.CanAnswer || !slices.Contains(row.Models, "local/model") || slices.Contains(row.Models, "group/test")) {
					t.Fatalf("isoform row: %+v", row)
				}
			}
			if _, err := app.SetAnsweringAgent("isoform"); err != nil {
				t.Fatal(err)
			}
			for _, choice := range []struct{ id, model, effort string }{{"claude-code", catalog[0], "medium"}, {"codex", catalog[1], "low"}, {"isoform", catalog[2], "xhigh"}} {
				if _, err := app.SetAgentModel(choice.id, choice.model); err != nil {
					t.Fatal(err)
				}
				if _, err := app.SetAgentEffort(choice.id, choice.effort); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := app.SetBackgroundUsesChoice(true); err != nil {
				t.Fatal(err)
			}
			if got := app.runChoice("claude-code", &agent.TaskInput{}); got != (agent.AgentChoice{Model: "claude-opus-5-5", Effort: "medium"}) {
				t.Fatalf("Claude run: %+v", got)
			}
			if got := app.runChoice("codex", &agent.TaskInput{Priority: agent.PriorityBackground}); got != (agent.AgentChoice{Model: "gpt-6-astra", Effort: "low"}) {
				t.Fatalf("Codex run: %+v", got)
			}
			if got := app.externalIsoformModel(); got != "local/model" {
				t.Fatalf("request model %q", got)
			}
			if got := loadAIProviderConfig().agentChoice("isoform"); got.Effort != "xhigh" {
				t.Fatalf("saved Isoform effort: %+v", got)
			}
		})
	}
}

// DeepSeek Harness is the third choice, after Claude Code and Codex: listed,
// shown as not installed until dsh is on PATH, never pickable, never routed,
// and with no model or effort to save.
func TestDeepSeekHarnessIsListedThirdAndNeverPicked(t *testing.T) {
	assistantChoiceTestHome(t)
	installed := true
	previous := lookPathCLI
	lookPathCLI = func(name string) (string, error) {
		if name == "dsh" && installed {
			return "/usr/local/bin/dsh", nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { lookPathCLI = previous })

	app := &App{magpie: &magpieSidecar{healthy: true}}
	view, err := app.GetAssistantChoice()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, row := range view.Agents {
		order = append(order, row.Agent)
	}
	if !slices.Equal(order, []string{"claude-code", "codex", "deepseek", "isoform"}) {
		t.Fatalf("rows %v", order)
	}
	row := view.Agents[2]
	if row.CanAnswer || row.NotInstalled || len(row.Models) != 0 || len(row.Efforts) != 0 || row.Effort != "" {
		t.Fatalf("installed DeepSeek row: %+v", row)
	}
	installed = false
	if view, err = app.GetAssistantChoice(); err != nil || !view.Agents[2].NotInstalled || view.Agents[2].CanAnswer {
		t.Fatalf("DeepSeek row without dsh: %+v %v", view.Agents[2], err)
	}

	view, err = app.SetAnsweringAgent("deepseek")
	if err == nil || !strings.Contains(err.Error(), "locked down to Isoform's tools") {
		t.Fatalf("DeepSeek pick: %v", err)
	}
	if cfg := loadAIProviderConfig(); cfg.AnswerWith == "deepseek" || view.Answering == "deepseek" {
		t.Fatalf("a DeepSeek pick was saved: %+v", cfg)
	}
	for _, set := range []func(string, string) (AssistantChoiceView, error){app.SetAgentModel, app.SetAgentEffort} {
		if _, err := set("deepseek", ""); err == nil {
			t.Fatal("a DeepSeek model or effort was saved")
		}
	}
	if _, ok := loadAIProviderConfig().AgentChoices["deepseek"]; ok {
		t.Fatal("DeepSeek got a saved choice")
	}
	// A pick written to disk by hand is no pick and never routes.
	cfg := AIProviderConfig{ManagedOrder: []string{"claude"}, AnswerWith: "deepseek"}
	if got := answeringAgent(cfg); got != "claude-code" || !slices.Equal(routeOrder(cfg), []string{"claude-code"}) {
		t.Fatalf("a saved DeepSeek pick answers %q, routes %v", got, routeOrder(cfg))
	}
	if _, ok := app.agentChoices.lookup("deepseek"); ok {
		t.Fatal("runs got a DeepSeek choice")
	}
}
