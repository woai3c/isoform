package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

var bootstrapCatalog = []string{
	"claude/claude-sonnet-5-5", "claude/claude-sonnet-5", "claude/claude-sonnet-4-6",
	"claude/claude-haiku-5-5", "claude/claude-haiku-4-5",
	"codex/gpt-6.1-sol", "codex/gpt-5.6-sol", "codex/gpt-6-luna",
	"yansu-byok/claude-sonnet-4-6", "yansu-byok/claude-haiku-4-5",
	"ollama/llama3.2:latest",
}

func without(ids []string, drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	var out []string
	for _, id := range ids {
		if !skip[id] {
			out = append(out, id)
		}
	}
	return out
}

func plansByID(plans []magpieGroupPlan) map[string]magpieGroupPlan {
	out := make(map[string]magpieGroupPlan, len(plans))
	for _, p := range plans {
		out[p.ID] = p
	}
	return out
}

func TestPlanMagpieGroupsMemberSelection(t *testing.T) {
	ollama := []string{"llama3.2:latest"}
	byokChat := "claude-sonnet-4-6"
	byokLight := "claude-haiku-4-5"

	subs := []string{
		"claude/claude-sonnet-5-5", "codex/gpt-5.6-sol",
		"yansu-byok/claude-sonnet-4-6", "ollama/llama3.2:latest",
	}
	// Light work prefers the key and the local model; subscriptions come last.
	subsLight := []string{
		"yansu-byok/claude-haiku-4-5", "ollama/llama3.2:latest",
		"claude/claude-haiku-5-5", "codex/gpt-6-luna",
	}

	cases := []struct {
		name       string
		catalog    []string
		ollama     []string
		chatModel  string
		lightModel string
		consent    bool
		wantChat   []string
		wantLight  []string
	}{
		{
			name:       "consent admits subscriptions with keys and ollama",
			catalog:    bootstrapCatalog,
			ollama:     ollama,
			chatModel:  byokChat,
			lightModel: byokLight,
			consent:    true,
			wantChat:   subs,
			wantLight:  subsLight,
		},
		{
			name:       "subscriptions join without consent (the note is informational)",
			catalog:    bootstrapCatalog,
			ollama:     ollama,
			chatModel:  byokChat,
			lightModel: byokLight,
			wantChat:   subs,
			wantLight:  subsLight,
		},
		{
			name:      "sonnet-5 is the fallback when 5-5 is missing",
			catalog:   without(bootstrapCatalog, "claude/claude-sonnet-5-5"),
			chatModel: byokChat,
			consent:   true,
			wantChat:  []string{"claude/claude-sonnet-5", "codex/gpt-5.6-sol", "yansu-byok/claude-sonnet-4-6"},
			// No light key is set, so the light key defaults to the chat model.
			wantLight: []string{"yansu-byok/claude-sonnet-4-6", "claude/claude-haiku-5-5", "codex/gpt-6-luna"},
		},
		{
			name:      "sonnet-4-6 is used when only it exists",
			catalog:   []string{"claude/claude-sonnet-4-6", "codex/gpt-6.1-sol"},
			consent:   true,
			wantChat:  []string{"claude/claude-sonnet-4-6", "codex/gpt-6.1-sol"},
			wantLight: []string{"codex/gpt-6.1-sol"},
		},
		{
			name:      "codex chat falls back to the first codex id",
			catalog:   without(bootstrapCatalog, "codex/gpt-5.6-sol"),
			consent:   true,
			wantChat:  []string{"claude/claude-sonnet-5-5", "codex/gpt-6.1-sol"},
			wantLight: []string{"claude/claude-haiku-5-5", "codex/gpt-6-luna"},
		},
		{
			name:      "codex light falls back when no -luna id exists",
			catalog:   without(bootstrapCatalog, "codex/gpt-6-luna"),
			consent:   true,
			wantChat:  []string{"claude/claude-sonnet-5-5", "codex/gpt-5.6-sol"},
			wantLight: []string{"claude/claude-haiku-5-5", "codex/gpt-6.1-sol"},
		},
		{
			name:       "light key defaults to the chat model when unset",
			catalog:    []string{"yansu-byok/m1"},
			chatModel:  "m1",
			wantChat:   []string{"yansu-byok/m1"},
			wantLight:  []string{"yansu-byok/m1"},
			lightModel: "",
		},
		{
			name:      "ollama is skipped when its first model is not listed",
			catalog:   bootstrapCatalog,
			ollama:    []string{"missing:latest", "llama3.2:latest"},
			chatModel: byokChat,
			wantChat:  []string{"claude/claude-sonnet-5-5", "codex/gpt-5.6-sol", "yansu-byok/claude-sonnet-4-6"},
			// The light key defaults to the chat model, so the light group has it too.
			wantLight: []string{"yansu-byok/claude-sonnet-4-6", "claude/claude-haiku-5-5", "codex/gpt-6-luna"},
		},
		{
			name:    "empty catalog leaves every group empty",
			catalog: nil,
			consent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plans := plansByID(planMagpieGroups(magpieBootstrapInput{
				Catalog:         tc.catalog,
				OllamaModels:    tc.ollama,
				ByokChatModel:   tc.chatModel,
				ByokLightModel:  tc.lightModel,
				ConsentAccepted: tc.consent,
			}))
			if got := plans["yansu-chat"].Members; !sameMembers(got, tc.wantChat) {
				t.Errorf("yansu-chat members = %v, want %v", got, tc.wantChat)
			}
			if got := plans["yansu-background"].Members; !sameMembers(got, tc.wantChat) {
				t.Errorf("yansu-background members = %v, want %v", got, tc.wantChat)
			}
			if got := plans["yansu-light"].Members; !sameMembers(got, tc.wantLight) {
				t.Errorf("yansu-light members = %v, want %v", got, tc.wantLight)
			}
		})
	}
}

// sameMembers treats nil and empty as equal.
func sameMembers(got, want []string) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

func TestPlanMagpieGroupsRoutingAndNames(t *testing.T) {
	plans := plansByID(planMagpieGroups(magpieBootstrapInput{Catalog: bootstrapCatalog, ConsentAccepted: true}))
	want := map[string]struct{ name, routing, stays string }{
		"yansu-chat":       {"yansu chat", "order", "session"},
		"yansu-background": {"yansu background", "pace", "off"},
		"yansu-light":      {"yansu light", "usage", "off"},
	}
	for id, w := range want {
		p, ok := plans[id]
		if !ok {
			t.Fatalf("missing plan %s", id)
		}
		if p.Name != w.name || p.Routing != w.routing || p.Stays != w.stays {
			t.Errorf("%s = {%q %q %q}, want {%q %q %q}", id, p.Name, p.Routing, p.Stays, w.name, w.routing, w.stays)
		}
	}
}

func TestMagpieByokArgs(t *testing.T) {
	cases := []struct {
		name      string
		cfg       AIProviderConfig
		wantArgs  []string
		wantChat  string
		wantLight string
		wantOK    bool
	}{
		{
			name:      "anthropic defaults to the public endpoint",
			cfg:       AIProviderConfig{Provider: "anthropic", APIKey: "k", Model: "m1", LightModel: "m2"},
			wantArgs:  []string{"anthropic=https://api.anthropic.com", "key=k", "models=m1,m2"},
			wantChat:  "m1",
			wantLight: "m2",
			wantOK:    true,
		},
		{
			name:      "openai appends the version segment",
			cfg:       AIProviderConfig{Provider: "openai", APIKey: "k", Model: "gpt", BaseURL: "https://proxy.example.com/"},
			wantArgs:  []string{"url=https://proxy.example.com/v1", "key=k", "models=gpt"},
			wantChat:  "gpt",
			wantLight: "gpt",
			wantOK:    true,
		},
		{
			name:      "openai keeps an existing version segment",
			cfg:       AIProviderConfig{Provider: "openai", APIKey: "k", Model: "gpt", BaseURL: "https://x.example.com/v1"},
			wantArgs:  []string{"url=https://x.example.com/v1", "key=k", "models=gpt"},
			wantChat:  "gpt",
			wantLight: "gpt",
			wantOK:    true,
		},
		{name: "managed config has no key registration", cfg: AIProviderConfig{Provider: "managed", APIKey: "k", Model: "m"}},
		{name: "missing key registers nothing", cfg: AIProviderConfig{Provider: "anthropic", Model: "m"}},
		{name: "missing model registers nothing", cfg: AIProviderConfig{Provider: "anthropic", APIKey: "k"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, chat, light, ok := magpieByokArgs(tc.cfg)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
			if chat != tc.wantChat || light != tc.wantLight {
				t.Errorf("models = %q/%q, want %q/%q", chat, light, tc.wantChat, tc.wantLight)
			}
		})
	}
}

func TestDetectOllamaModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{{"name": "llama3.2:latest"}, {"name": " qwen2.5 "}},
		})
	}))
	defer server.Close()

	got := detectOllamaModels(context.Background(), server.URL)
	if !reflect.DeepEqual(got, []string{"llama3.2:latest", "qwen2.5"}) {
		t.Fatalf("detectOllamaModels = %v", got)
	}
	if got := detectOllamaModels(context.Background(), "http://127.0.0.1:1"); got != nil {
		t.Fatalf("unreachable Ollama must report nothing, got %v", got)
	}
}

func TestMagpieConsentRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "magpie")
	if magpieConsentAccepted(dir) {
		t.Fatal("no consent file must not count as accepted")
	}
	if err := saveMagpieConsent(dir, MagpieConsent{Version: magpieConsentVersion, AcceptedAt: time.Now()}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !magpieConsentAccepted(dir) {
		t.Fatal("saved consent at the current version must be accepted")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, magpieConsentFileName))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("consent file mode = %v, want 0600", info.Mode().Perm())
		}
	}
	if err := saveMagpieConsent(dir, MagpieConsent{Version: magpieConsentVersion - 1}); err != nil {
		t.Fatalf("save old version: %v", err)
	}
	if magpieConsentAccepted(dir) {
		t.Fatal("an older consent version must not count as accepted")
	}
	if err := clearMagpieConsent(dir); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := clearMagpieConsent(dir); err != nil {
		t.Fatalf("clear twice must succeed: %v", err)
	}
	if magpieConsentAccepted(dir) {
		t.Fatal("cleared consent must not be accepted")
	}
}

func TestBootstrapRequiresHealthySidecar(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	if _, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{}); err != errMagpieNotHealthy {
		t.Fatalf("Bootstrap on a stopped sidecar = %v, want errMagpieNotHealthy", err)
	}
}

// groupsOnDisk reads the groups the fake magpie persisted.
func groupsOnDisk(t *testing.T, s *magpieSidecar) map[string]groupRecordForTest {
	t.Helper()
	out := map[string]groupRecordForTest{}
	data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "groups.json"))
	if os.IsNotExist(err) {
		return out
	}
	if err != nil {
		t.Fatalf("read groups.json: %v", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode groups.json: %v", err)
	}
	return out
}

type groupRecordForTest struct {
	Name    string `json:"name"`
	Models  string `json:"models"`
	Routing string `json:"routing"`
	Stays   string `json:"stays"`
}

// writeFakeState shapes one of the fake magpie's state files before a call.
func writeFakeState(t *testing.T, s *magpieSidecar, name, content string) {
	t.Helper()
	dir := filepath.Join(s.stateDir, "config", "magpie")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create fake state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func callsOnDisk(t *testing.T, s *magpieSidecar) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "calls.log"))
	if err != nil {
		t.Fatalf("read calls.log: %v", err)
	}
	return string(data)
}

func startFakeSidecar(t *testing.T) *magpieSidecar {
	t.Helper()
	s := testSidecar(t, buildFakeMagpie(t))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s
}

func TestBootstrapUpsertsGroupsIdempotently(t *testing.T) {
	s := startFakeSidecar(t)
	if err := saveMagpieConsent(s.stateDir, MagpieConsent{Version: magpieConsentVersion, AcceptedAt: time.Now()}); err != nil {
		t.Fatalf("save consent: %v", err)
	}
	opts := magpieBootstrapOptions{
		Provider: AIProviderConfig{
			Provider: "anthropic", APIKey: "sk-test-123",
			Model: "claude-sonnet-4-6", LightModel: "claude-haiku-4-5",
		},
		ConsentAccepted: magpieConsentAccepted(s.stateDir),
		OllamaBaseURL:   "http://127.0.0.1:1",
	}

	if opts.Provider.APIKey != "" {
		writeMagpieSourceConfig(t, s, opts.Provider)
	}
	state, err := s.Bootstrap(context.Background(), opts)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	plans := plansByID(state.Groups)
	wantChat := []string{"claude/claude-sonnet-5-5", "codex/gpt-5.6-sol", "yansu-byok/claude-sonnet-4-6"}
	if !sameMembers(plans["yansu-chat"].Members, wantChat) {
		t.Fatalf("yansu-chat = %v, want %v", plans["yansu-chat"].Members, wantChat)
	}
	if !state.ByokRegistered {
		t.Fatal("the BYOA key should be registered as yansu-byok")
	}

	disk := groupsOnDisk(t, s)
	chat, ok := disk["yansu-chat"]
	if !ok {
		t.Fatalf("yansu-chat missing on disk: %v", disk)
	}
	if chat.Models != strings.Join(wantChat, ",") || chat.Routing != "order" || chat.Stays != "session" {
		t.Fatalf("yansu-chat on disk = %+v", chat)
	}
	if light := disk["yansu-light"]; light.Routing != "usage" || light.Stays != "off" {
		t.Fatalf("yansu-light on disk = %+v", light)
	}

	if _, err := s.Bootstrap(context.Background(), opts); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if got := strings.Count(callsOnDisk(t, s), "group add yansu chat"); got != 2 {
		t.Fatalf("group add yansu chat ran %d times, want 2 (once per bootstrap)", got)
	}
	if after := groupsOnDisk(t, s); !reflect.DeepEqual(after, disk) {
		t.Fatalf("second bootstrap changed groups: %v -> %v", disk, after)
	}
}

func TestBootstrapKeepsSubscriptionMembersWithoutConsent(t *testing.T) {
	// The consent note is informational: the groups are populated from the
	// signed-in accounts whether or not it was accepted, so a fresh data
	// folder can think right away.
	s := startFakeSidecar(t)
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{ConsentAccepted: false})
	if err != nil {
		t.Fatalf("Bootstrap without consent: %v", err)
	}
	// The three yansu groups plus Isoform's own never-stuck group.
	if len(state.Groups) != 4 {
		t.Fatalf("expected four planned groups, got %d", len(state.Groups))
	}
	for _, plan := range state.Groups {
		if len(plan.Members) == 0 {
			t.Errorf("%s has no members without consent", plan.ID)
		}
	}
	if _, ok := groupsOnDisk(t, s)["yansu-chat"]; !ok {
		t.Fatal("yansu-chat should exist without consent")
	}
	if strings.Contains(callsOnDisk(t, s), "group rm yansu-chat") {
		t.Fatal("nothing should remove yansu-chat")
	}
}

func TestBootstrapMakesIsoformGroupFromThePlan(t *testing.T) {
	// An explicit ID cannot replace another group with the same name.
	s := startFakeSidecar(t)
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if state.ActiveGroup != magpieIsoformGroupID {
		t.Fatalf("active group = %q, want %q", state.ActiveGroup, magpieIsoformGroupID)
	}
	disk := groupsOnDisk(t, s)
	if group, ok := disk[magpieIsoformGroupID]; !ok || group.Name != magpieIsoformGroupName {
		t.Fatalf("isoform group on disk = %+v (all: %v)", group, disk)
	}
	calls := callsOnDisk(t, s)
	if !strings.Contains(calls, "group add Isoform id=isoform") {
		t.Fatalf("expected an explicit Isoform ID, calls:\n%s", calls)
	}
	if _, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{}); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if got := strings.Count(callsOnDisk(t, s), "group add Isoform id=isoform"); got != 1 {
		t.Fatalf("group creation ran %d times, want 1", got)
	}
}

func TestBootstrapMakesIsoformGroupFromThePlannedMembersWhenTemplatesFail(t *testing.T) {
	t.Setenv("FAKE_MAGPIE_NO_TEMPLATES", "1")
	s := startFakeSidecar(t)
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if state.ActiveGroup != magpieIsoformGroupID {
		t.Fatalf("active group = %q, want %q", state.ActiveGroup, magpieIsoformGroupID)
	}
	disk := groupsOnDisk(t, s)
	group, ok := disk[magpieIsoformGroupID]
	if !ok {
		t.Fatalf("isoform group missing on disk: %v", disk)
	}
	// The yansu-chat members in order, each at Isoform's default effort.
	if want := applyIsoformChoice(strings.Split(disk["yansu-chat"].Models, ","), AgentModelChoice{}); group.Models != strings.Join(want, ",") || group.Routing != "order" {
		t.Fatalf("isoform group = %+v, want the yansu-chat members in order at High", group)
	}
}

func TestBootstrapExternalReadsTheUsersMagpie(t *testing.T) {
	s := startFakeSidecar(t)
	// Shape the fake instance like a user's: one group, Claude Code set to it.
	writeFakeState(t, s, "groups.json", `{"never-stuck":{"name":"Never stuck","models":"claude/claude-sonnet-5-5,codex/gpt-6-astra","routing":"order","stays":"session"}}`)
	agents := `{"claude":"group/never-stuck","codex":"gpt-6-astra"}`
	writeFakeState(t, s, "agents.json", agents)
	userGroups := groupsOnDisk(t, s)
	// In external mode the CLI inherits the environment instead of an XDG
	// override, so point the fake at the same state the helpers read.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(s.stateDir, "config"))
	s.mu.Lock()
	s.external = true
	s.mu.Unlock()

	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap external: %v", err)
	}
	if !state.External || state.ActiveGroup != "isoform" || len(state.Groups) != 2 {
		t.Fatalf("external state = %+v", state)
	}
	if !reflect.DeepEqual(state.AgentModels, map[string]string{"claude": "group/never-stuck", "codex": "codex/gpt-6-astra"}) {
		t.Fatalf("agent models = %v", state.AgentModels)
	}
	assertExternalBootstrapPreservesUserGroups(t, s, userGroups, agents)
	if _, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{}); err != nil {
		t.Fatal(err)
	}
	assertExternalBootstrapPreservesUserGroups(t, s, userGroups, agents)
}

func assertExternalBootstrapPreservesUserGroups(t *testing.T, s *magpieSidecar, groups map[string]groupRecordForTest, agents string) {
	t.Helper()
	disk := groupsOnDisk(t, s)
	if _, ok := disk[magpieIsoformGroupID]; !ok {
		t.Fatal("Isoform group missing")
	}
	delete(disk, magpieIsoformGroupID)
	if !reflect.DeepEqual(disk, groups) {
		t.Fatalf("user groups changed: %+v", disk)
	}
	data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "agents.json"))
	if err != nil || string(data) != agents {
		t.Fatalf("agent settings changed: %s %v", data, err)
	}
}

func TestBootstrapExternalFallsBackToTheAgentsModels(t *testing.T) {
	// A user's magpie with no never-stuck group and no template support
	// still gets an Isoform group, made from the agents' current models.
	t.Setenv("FAKE_MAGPIE_NO_TEMPLATES", "1")
	s := startFakeSidecar(t)
	writeFakeState(t, s, "groups.json", `{"work":{"name":"Work","models":"claude/claude-opus-5-5","routing":"order","stays":"session"}}`)
	writeFakeState(t, s, "agents.json", `{"claude":"claude-opus-5-5","codex":"gpt-6.1-sol"}`)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(s.stateDir, "config"))
	s.mu.Lock()
	s.external = true
	s.mu.Unlock()

	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap external: %v", err)
	}
	if state.ActiveGroup != "isoform" {
		t.Fatalf("active group = %q", state.ActiveGroup)
	}
	if groups := groupsOnDisk(t, s); len(groups) != 2 || groups["work"].Models != "claude/claude-opus-5-5" || groups["isoform"].Models != "codex/gpt-6.1-sol" {
		t.Fatalf("user groups changed: %+v", groups)
	}

}

func TestBootstrapRemovesByokWhenKeyIsGone(t *testing.T) {
	s := startFakeSidecar(t)
	withKey := magpieBootstrapOptions{Provider: AIProviderConfig{Provider: "anthropic", APIKey: "k", Model: "claude-sonnet-4-6"}}
	writeMagpieSourceConfig(t, s, withKey.Provider)
	state, err := s.Bootstrap(context.Background(), withKey)
	if err != nil || !state.ByokRegistered {
		t.Fatalf("first Bootstrap: registered=%v err=%v", state.ByokRegistered, err)
	}

	writeMagpieSourceConfig(t, s, AIProviderConfig{})
	state, err = s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap without key: %v", err)
	}
	if state.ByokRegistered {
		t.Fatal("yansu-byok must not stay registered once the key is gone")
	}
	if _, ok := magpieProvidersOnDisk(t, s)[magpieByokName]; ok {
		t.Fatal("the provider entry must be removed from disk")
	}
}

func writeMagpieSourceConfig(t *testing.T, s *magpieSidecar, cfg AIProviderConfig) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.stateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.stateDir), aiProviderConfigFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func magpieProvidersOnDisk(t *testing.T, s *magpieSidecar) map[string]map[string]any {
	t.Helper()
	var store struct {
		Providers []map[string]any `json:"providers"`
	}
	data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "providers.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	providers := make(map[string]map[string]any)
	for _, provider := range store.Providers {
		providers[provider["id"].(string)] = provider
	}
	return providers
}

func TestMagpieByokSaveWhileDown(t *testing.T) {
	for _, change := range []struct {
		name string
		cfg  AIProviderConfig
	}{
		{name: "removed"},
		{name: "changed", cfg: AIProviderConfig{Provider: "openai", APIKey: "new-key", Model: "new-model", BaseURL: "https://new.example"}},
	} {
		t.Run(change.name, func(t *testing.T) {
			s := startFakeSidecar(t)
			t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
			old := AIProviderConfig{Provider: "anthropic", APIKey: "old-key", Model: "old-model"}
			if err := saveAIProviderConfig(old); err != nil {
				t.Fatal(err)
			}
			if got := magpieProvidersOnDisk(t, s)[magpieByokName]["key"]; got != old.APIKey {
				t.Fatal("initial credential was not registered")
			}
			s.Stop()
			sourceTemp := filepath.Join(filepath.Dir(s.stateDir), ".isoform-provider-interrupted")
			if err := os.WriteFile(sourceTemp, []byte(`{"apiKey":"old-key"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := s.binaryPath
			s.binaryPath = filepath.Join(t.TempDir(), "missing-magpie")
			// A failed/down executor cannot delay the config save or cleanup.
			if err := saveAIProviderConfig(change.cfg); err != nil {
				t.Fatal(err)
			}
			assertCredential := func() {
				t.Helper()
				provider, exists := magpieProvidersOnDisk(t, s)[magpieByokName]
				if change.cfg.APIKey == "" {
					if exists {
						t.Fatal("key removal left a provider entry")
					}
				} else if provider["key"] != change.cfg.APIKey || provider["chat"] != "https://new.example/v1" || provider["anthropic"] != nil {
					t.Fatal("replacement did not replace the old credential and endpoint")
				}
			}
			assertCredential()
			if _, err := os.Stat(sourceTemp); !os.IsNotExist(err) {
				t.Fatal("source save retained an interrupted credential write")
			}
			// A bootstrap queued before Save must not restore its old snapshot.
			if _, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{Provider: old}); err != errMagpieNotHealthy {
				t.Fatalf("stopped Bootstrap: %v", err)
			}
			assertCredential()
			s.binaryPath = bin
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertCredential()
		})
	}
}

func TestMagpieByokStartReconcilesBeforeAnyCommand(t *testing.T) {
	for _, source := range []string{"missing", "removed", "changed", "invalid", "recovered"} {
		t.Run(source, func(t *testing.T) {
			s := testSidecar(t, buildFakeMagpie(t))
			t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
			// Seed a legacy copy, without a successful bootstrap receipt. Also
			// preserve unrelated provider fields, routing groups and settings.
			legacy := `{"providers":[{"id":"yansu-byok","key":"old-key","models":["old"]},{"id":"yansu-byok-2","key":"another-old-key"},{"id":"local","custom":true}],"groups":[{"id":"keep"}],"future":true}`
			writeFakeState(t, s, "providers.json", legacy)
			writeFakeState(t, s, "providers.json.bak", legacy)
			writeFakeState(t, s, "providers.json.bad-20261020-000000", legacy)
			writeFakeState(t, s, ".isoform-provider-interrupted", legacy)
			sourceTemp := filepath.Join(filepath.Dir(s.stateDir), ".isoform-provider-interrupted")
			if err := os.WriteFile(sourceTemp, []byte(legacy), 0o600); err != nil {
				t.Fatal(err)
			}
			if source == "recovered" {
				writeFakeState(t, s, "providers.json", `{"providers":`)
			}
			switch source {
			case "removed":
				writeMagpieSourceConfig(t, s, AIProviderConfig{})
			case "changed":
				writeMagpieSourceConfig(t, s, AIProviderConfig{Provider: "anthropic", APIKey: "new-key", Model: "new"})
			case "invalid":
				if err := os.WriteFile(filepath.Join(filepath.Dir(s.stateDir), aiProviderConfigFileName), []byte(`{"apiKey":"partial"`), 0o600); err != nil {
					t.Fatal(err)
				}
				if loadAIProviderConfig().APIKey != "" {
					t.Fatal("a partially decoded source authorized a credential")
				}
			}
			// Even resolution failure must happen after credential cleanup.
			bin := s.binaryPath
			s.binaryPath = filepath.Join(t.TempDir(), "missing-magpie")
			if err := s.Start(context.Background()); err == nil {
				t.Fatal("expected missing binary error")
			}
			providers := magpieProvidersOnDisk(t, s)
			if _, err := os.Stat(sourceTemp); !os.IsNotExist(err) {
				t.Fatal("startup retained an interrupted source config write")
			}
			if _, exists := providers["yansu-byok-2"]; exists {
				t.Fatal("startup retained a numbered legacy credential")
			}
			if source == "changed" {
				if providers[magpieByokName]["key"] != "new-key" {
					t.Fatal("startup retained the old credential")
				}
			} else if _, exists := providers[magpieByokName]; exists {
				t.Fatal("startup retained a provider without a valid source key")
			}
			if providers["local"]["custom"] != true {
				t.Fatal("unrelated provider changed")
			}
			data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "providers.json"))
			if err != nil || !strings.Contains(string(data), `"future": true`) || !strings.Contains(string(data), `"id": "keep"`) {
				t.Fatal("unrelated store fields changed")
			}
			for _, suffix := range []string{".bak", ".bad-20261020-000000"} {
				if _, err := os.Stat(filepath.Join(s.stateDir, "config", "magpie", "providers.json"+suffix)); !os.IsNotExist(err) {
					t.Fatalf("provider recovery copy survived: %s, %v", suffix, err)
				}
			}
			if _, err := os.Stat(filepath.Join(s.stateDir, "config", "magpie", ".isoform-provider-interrupted")); !os.IsNotExist(err) {
				t.Fatal("interrupted credential write survived startup")
			}
			// Start the fixture gateway and verify reconciliation survives it.
			s.binaryPath = bin
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if after := magpieProvidersOnDisk(t, s); !reflect.DeepEqual(providers, after) {
				t.Fatal("next start changed the reconciled providers")
			}
		})
	}
}

func TestMagpieByokSaveFailsBeforeSourceRemovalWhenCleanupFails(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	old := AIProviderConfig{Provider: "anthropic", APIKey: "old-key", Model: "old"}
	if err := saveAIProviderConfig(old); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory deterministically fails removal, even as root.
	backup := filepath.Join(s.stateDir, "config", "magpie", "providers.json.bak")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "block"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveAIProviderConfig(AIProviderConfig{}); err == nil {
		t.Fatal("source removal must fail when cleanup fails")
	}
	if loadAIProviderConfig().APIKey != old.APIKey {
		t.Fatal("source changed despite failed credential cleanup")
	}
	if err := os.RemoveAll(filepath.Join(s.stateDir, "config", "magpie", "providers.json.bak")); err != nil {
		t.Fatal(err)
	}
	if err := saveAIProviderConfig(AIProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, exists := magpieProvidersOnDisk(t, s)[magpieByokName]; exists {
		t.Fatal("retry did not remove the copy")
	}
}

func TestMagpieByokNeverTouchesUserStore(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	userConfig := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", userConfig)
	userPath := filepath.Join(userConfig, "magpie", "providers.json")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := `{"providers":[{"id":"yansu-byok","key":"users-own-key"}]}`
	if err := os.WriteFile(userPath, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	s.external = true
	for _, cfg := range []AIProviderConfig{
		{Provider: "anthropic", APIKey: "fixture-key", Model: "m"},
		{},
	} {
		if err := saveAIProviderConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if err := s.reconcileByok(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(userPath)
		if err != nil || string(data) != sentinel {
			t.Fatal("reconciliation changed the user's magpie store")
		}
	}
	if _, exists := magpieProvidersOnDisk(t, s)[magpieByokName]; exists {
		t.Fatal("no key must leave no provider entry in the bundled store")
	}
}

func TestMagpieByokMigrationReconcilesBeforeStart(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	writeMagpieSourceConfig(t, s, AIProviderConfig{})
	writeFakeState(t, s, "providers.json", `{"providers":[{"id":"yansu-byok","key":"old-key"}]}`)
	if err := os.WriteFile(filepath.Join(s.stateDir, magpiePortFileName), []byte(`{"port":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MAGPIE_NO_BYOK_STORE", filepath.Join(s.stateDir, "config", "magpie", "providers.json"))
	a := &App{magpie: s}
	// StartAgent is never run. The fake refuses the migration's first CLI
	// command if the legacy gateway copy still exists at that instant.
	a.migrateAgentChoices()
	if !loadAIProviderConfig().AgentChoicesMigrated {
		t.Fatal("migration did not reconcile before its first command")
	}
	if calls := agentHomeCalls(t, s); !strings.Contains(calls, "claude") || !strings.Contains(calls, "codex") {
		t.Fatal("migration did not run both fixture commands")
	}
	if _, exists := magpieProvidersOnDisk(t, s)[magpieByokName]; exists {
		t.Fatal("migration retained the withdrawn key")
	}
}

func TestMagpieByokRecoversEmptyProviderStore(t *testing.T) {
	for _, corrupt := range []string{"", " \n\t"} {
		t.Run(fmt.Sprintf("bytes=%d", len(corrupt)), func(t *testing.T) {
			s := testSidecar(t, buildFakeMagpie(t))
			writeFakeState(t, s, "providers.json", corrupt)
			writeFakeState(t, s, "providers.json.bak", `{"providers":[{"id":"yansu-byok","key":"old-key"}]}`)
			if err := s.reconcileByok(); err != nil {
				t.Fatal(err)
			}
			if providers := magpieProvidersOnDisk(t, s); len(providers) != 0 {
				t.Fatal("recovered store retained a provider without a source key")
			}
			data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "providers.json"))
			if err != nil || !json.Valid(data) {
				t.Fatal("recovery deleted the backup without repairing the primary store")
			}
		})
	}
}

func TestMagpieByokCorruptStoreStillCleansInterruptedKeys(t *testing.T) {
	for _, primary := range []string{`{"providers":`, `{"providers":{}}`, `{"providers":[42]}`} {
		t.Run(primary, func(t *testing.T) {
			s := testSidecar(t, buildFakeMagpie(t))
			writeMagpieSourceConfig(t, s, AIProviderConfig{})
			writeFakeState(t, s, "providers.json", primary)
			// An unusable backup cannot recover the primary, but it and the
			// interrupted writes must not retain a withdrawn credential.
			writeFakeState(t, s, "providers.json.bak", `{"key":"old-key"`)
			writeFakeState(t, s, "providers.json.bad-20261020", `{"key":"old-key"}`)
			writeFakeState(t, s, ".isoform-provider-interrupted", `{"key":"old-key"}`)
			sourceTemp := filepath.Join(filepath.Dir(s.stateDir), ".isoform-provider-interrupted")
			if err := os.WriteFile(sourceTemp, []byte(`{"apiKey":"old-key"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := s.Start(context.Background()); err == nil {
				t.Fatal("unrecoverable provider store must prevent startup")
			}
			for _, path := range []string{
				sourceTemp,
				filepath.Join(s.stateDir, "config", "magpie", ".isoform-provider-interrupted"),
				filepath.Join(s.stateDir, "config", "magpie", "providers.json.bad-20261020"),
				filepath.Join(s.stateDir, "config", "magpie", "providers.json.bak"),
			} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("failed startup retained credential copy: %s, %v", path, err)
				}
			}
		})
	}
}

func TestMagpieByokPartialUpdateCannotRestoreRemovedKey(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	t.Setenv("YANSU_AGENT_HOME", filepath.Dir(s.stateDir))
	if err := saveAIProviderConfig(AIProviderConfig{Provider: "anthropic", APIKey: "old-key", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	read := make(chan struct{})
	release := make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		_, err := updateAIProviderConfig(func(cfg *AIProviderConfig) error {
			close(read)
			<-release
			cfg.CachedModels = []string{"m"}
			return nil
		})
		updated <- err
	}()
	<-read
	// Deterministically check the dangerous boundary: the old-key snapshot
	// has been read, but its partial update has not been published yet.
	if magpieByokMu.TryLock() {
		magpieByokMu.Unlock()
		close(release)
		<-updated
		t.Fatal("partial update left its read and publication open to a concurrent key removal")
	}
	removing := make(chan struct{})
	removed := make(chan error, 1)
	go func() {
		close(removing)
		removed <- saveAIProviderConfig(AIProviderConfig{})
	}()
	<-removing
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	// Exercise the actual formerly unguarded partial setter after removal too.
	if err := (&App{}).SetCachedAIProviderModels([]string{"new"}); err != nil {
		t.Fatal(err)
	}
	if cfg := loadAIProviderConfig(); cfg.APIKey != "" || !slices.Equal(cfg.CachedModels, []string{"new"}) {
		t.Fatal("partial update restored the withdrawn source credential or lost its own update")
	}
	if _, exists := magpieProvidersOnDisk(t, s)[magpieByokName]; exists {
		t.Fatal("partial update restored the executor credential")
	}
}

func TestBootstrapRegistersOllamaWhenDetected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "llama3.2:latest"}}})
	}))
	defer server.Close()

	s := startFakeSidecar(t)
	if err := saveMagpieConsent(s.stateDir, MagpieConsent{Version: magpieConsentVersion, AcceptedAt: time.Now()}); err != nil {
		t.Fatalf("save consent: %v", err)
	}
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{ConsentAccepted: true, OllamaBaseURL: server.URL})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !state.Ollama {
		t.Fatal("Ollama should be reported as present")
	}
	if !strings.Contains(callsOnDisk(t, s), "provider add ollama") {
		t.Fatal("expected provider add ollama")
	}
	plans := plansByID(state.Groups)
	if got := plans["yansu-light"].Members; len(got) == 0 || got[0] != "ollama/llama3.2:latest" {
		t.Fatalf("yansu-light should start with the Ollama member when no key is set, got %v", got)
	}
}

func TestOrderIsoformMembers(t *testing.T) {
	template := []string{"claude/claude-sonnet-5-5", "codex/gpt-6-astra"}
	planned := []string{"codex/gpt-5.6-sol", "claude/claude-sonnet-4-6", "yansu-byok/claude-sonnet-4-6"}
	cases := []struct {
		name    string
		current []string
		planned []string
		order   []string
		want    []string
	}{
		{
			name:    "already in order",
			current: template, planned: planned, order: []string{"claude", "codex"},
			want: template,
		},
		{
			name:    "Codex first",
			current: template, planned: planned, order: []string{"codex", "claude"},
			want: []string{"codex/gpt-6-astra", "claude/claude-sonnet-5-5"},
		},
		{
			name:    "backup turned off drops the other assistant",
			current: template, planned: planned, order: []string{"codex"},
			want: []string{"codex/gpt-6-astra"},
		},
		{
			name:    "backup turned back on rejoins with its planned model",
			current: []string{"codex/gpt-6-astra"}, planned: planned, order: []string{"codex", "claude"},
			want: []string{"codex/gpt-6-astra", "claude/claude-sonnet-4-6"},
		},
		{
			name:    "the key and local models keep their place after the assistants",
			current: []string{"yansu-byok/claude-sonnet-4-6", "codex/gpt-6-astra", "ollama/llama3.2:latest", "claude/claude-sonnet-5-5"},
			planned: planned, order: []string{"claude", "codex"},
			want: []string{"claude/claude-sonnet-5-5", "codex/gpt-6-astra", "yansu-byok/claude-sonnet-4-6", "ollama/llama3.2:latest"},
		},
		{
			name:    "nothing chosen is available, so the plan stands",
			current: nil, planned: []string{"yansu-byok/claude-sonnet-4-6"}, order: []string{"claude"},
			want: []string{"yansu-byok/claude-sonnet-4-6"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderIsoformMembers(tc.current, tc.planned, tc.order); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("orderIsoformMembers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBootstrapPutsTheBundledIsoformGroupInTheAssistantOrder(t *testing.T) {
	s := startFakeSidecar(t)
	codexFirst := magpieBootstrapOptions{Provider: AIProviderConfig{ManagedOrder: []string{"codex", "claude"}}}
	state, err := s.Bootstrap(context.Background(), codexFirst)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	// The plan follows the selected order, every member at the default effort.
	want := "codex/gpt-5.6-sol:high,claude/claude-sonnet-5-5:high"
	if got := groupsOnDisk(t, s)[magpieIsoformGroupID].Models; got != want {
		t.Fatalf("isoform group on disk = %q, want %q", got, want)
	}
	if got := plansByID(state.Groups)[magpieIsoformGroupID].Members; strings.Join(got, ",") != want {
		t.Fatalf("isoform group in the state = %v, want %s", got, want)
	}
	if _, err := s.Bootstrap(context.Background(), codexFirst); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if got := strings.Count(callsOnDisk(t, s), "group add Isoform id=isoform"); got != 1 {
		t.Fatalf("Isoform's group was created %d times, want 1", got)
	}
}

func TestBootstrapExternalKeepsTheUsersIsoformGroupOrder(t *testing.T) {
	s := startFakeSidecar(t)
	writeFakeState(t, s, "groups.json", `{"isoform":{"name":"Isoform","models":"claude/claude-sonnet-5-5,codex/gpt-6-astra","routing":"order","stays":"session"}}`)
	agents := `{"claude":"group/isoform","codex":"gpt-6-astra"}`
	writeFakeState(t, s, "agents.json", agents)
	userGroups := groupsOnDisk(t, s)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(s.stateDir, "config"))
	s.mu.Lock()
	s.external = true
	s.mu.Unlock()

	opts := magpieBootstrapOptions{Provider: AIProviderConfig{ManagedOrder: []string{"codex"}}}
	state, err := s.Bootstrap(context.Background(), opts)
	if err != nil {
		t.Fatalf("Bootstrap external: %v", err)
	}
	if !state.External || state.ActiveGroup != magpieIsoformGroupID {
		t.Fatalf("external chat route = %+v", state)
	}
	if disk := groupsOnDisk(t, s); !reflect.DeepEqual(disk, userGroups) {
		t.Fatalf("existing group changed without a saved profile: %+v", disk)
	}
	data, err := os.ReadFile(filepath.Join(s.stateDir, "config", "magpie", "agents.json"))
	if err != nil || string(data) != agents {
		t.Fatalf("agent settings changed: %s %v", data, err)
	}
	if got := plansByID(state.Groups)[magpieIsoformGroupID]; got.Routing != "order" || strings.Join(got.Members, ",") != userGroups[magpieIsoformGroupID].Models {
		t.Fatalf("the user's chat plan changed: %+v", got)
	}
	if !reflect.DeepEqual(state.AgentModels, map[string]string{"claude": "group/isoform", "codex": "codex/gpt-6-astra"}) {
		t.Fatalf("agent model routes changed: %v", state.AgentModels)
	}
	calls := callsOnDisk(t, s)
	if strings.Contains(calls, "group copy") || strings.Contains(calls, "group rm") || strings.Contains(calls, "group template") {
		t.Fatalf("existing chat group must stay intact, calls:\n%s", calls)
	}
}
