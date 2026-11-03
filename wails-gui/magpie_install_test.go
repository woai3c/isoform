package main

import (
	"reflect"
	"testing"
)

func TestParseMagpieGroupsReadsTheRealListing(t *testing.T) {
	text := `  Never stuck  group/never-stuck  order  claude/claude-sonnet-5-5 → codex/gpt-6-astra → gemini/gemini-3.1-pro-preview
  yansu chat        group/yansu-chat        order · stays session  claude/claude-sonnet-5-5 → codex/gpt-5.6-sol
  yansu light       group/yansu-light       usage · stays off      claude/claude-haiku-5-5 · codex/gpt-6-luna
  fake  group/fake  pace  claude/a,codex/b
`
	got := parseMagpieGroups(text)
	want := []magpieGroupInfo{
		{ID: "never-stuck", Name: "Never stuck", Routing: "order", Members: []string{"claude/claude-sonnet-5-5", "codex/gpt-6-astra", "gemini/gemini-3.1-pro-preview"}},
		{ID: "yansu-chat", Name: "yansu chat", Routing: "order", Stays: "session", Members: []string{"claude/claude-sonnet-5-5", "codex/gpt-5.6-sol"}},
		{ID: "yansu-light", Name: "yansu light", Routing: "usage", Stays: "off", Members: []string{"claude/claude-haiku-5-5", "codex/gpt-6-luna"}},
		{ID: "fake", Name: "fake", Routing: "pace", Members: []string{"claude/a", "codex/b"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMagpieGroups =\n%#v\nwant\n%#v", got, want)
	}
	if got := parseMagpieGroups("magpie: no group; there are none yet\n"); len(got) != 0 {
		t.Fatalf("no groups should parse to nothing, got %#v", got)
	}
}

func TestParseMagpieAgentModelsReadsTheRealListing(t *testing.T) {
	text := `  Claude Code          claude/claude-haiku-5-5[1m]  ·  effort —  ~/.claude/settings.json
  Claude Desktop       provider magpie                           ~/Library/Application Support/Claude/claude_desktop_config.json
  Codex                gpt-6-astra  ·  effort ultra              ~/.codex/config.toml
  Gemini CLI           auth —  ·  —                              ~/.gemini/settings.json
`
	got := parseMagpieAgentModels(text)
	want := map[string]string{"claude": "claude/claude-haiku-5-5", "codex": "codex/gpt-6-astra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMagpieAgentModels = %v, want %v", got, want)
	}

	unset := parseMagpieAgentModels("  Claude Code          —  ·  effort —\n  Codex                group/never-stuck  ·  effort high\n")
	if _, ok := unset["claude"]; ok {
		t.Fatalf("a dash means no model, got %v", unset)
	}
	if unset["codex"] != "group/never-stuck" {
		t.Fatalf("a group setting is kept as is, got %v", unset)
	}
}

func TestAgentCatalogModelIgnoresGroups(t *testing.T) {
	agents := map[string]string{"claude": "group/never-stuck", "codex": "codex/gpt-6-astra"}
	if got := agentCatalogModel(agents, "claude"); got != "" {
		t.Fatalf("a group is not a member candidate, got %q", got)
	}
	if got := agentCatalogModel(agents, "codex"); got != "codex/gpt-6-astra" {
		t.Fatalf("codex model = %q", got)
	}
}

func TestPlanMagpieGroupsPrefersTheAgentsCurrentModels(t *testing.T) {
	catalog := append([]string{"claude/claude-opus-4-7", "codex/gpt-6-astra"}, bootstrapCatalog...)
	plans := plansByID(planMagpieGroups(magpieBootstrapInput{
		Catalog:     catalog,
		AgentModels: map[string]string{"claude": "claude/claude-opus-4-7", "codex": "codex/gpt-6-astra"},
	}))
	want := []string{"claude/claude-opus-4-7", "codex/gpt-6-astra"}
	if got := plans["yansu-chat"].Members; !reflect.DeepEqual(got, want) {
		t.Fatalf("chat members = %v, want %v", got, want)
	}

	// A model the catalog does not serve falls back to the preference list,
	// and consent is no longer a gate.
	fallback := plansByID(planMagpieGroups(magpieBootstrapInput{
		Catalog:     bootstrapCatalog,
		AgentModels: map[string]string{"claude": "claude/claude-opus-9"},
	}))
	if got := fallback["yansu-chat"].Members; !reflect.DeepEqual(got, []string{"claude/claude-sonnet-5-5", "codex/gpt-5.6-sol"}) {
		t.Fatalf("fallback chat members = %v", got)
	}
}

func TestPickActiveGroup(t *testing.T) {
	groups := []magpieGroupInfo{{ID: "never-stuck"}, {ID: "thrifty"}}
	if got := pickActiveGroup(groups, map[string]string{"claude": "group/thrifty"}); got != "thrifty" {
		t.Fatalf("the group Claude Code is set to wins, got %q", got)
	}
	if got := pickActiveGroup(groups, map[string]string{"claude": "claude/claude-haiku-5-5"}); got != "never-stuck" {
		t.Fatalf("the first group is the default, got %q", got)
	}
	if got := pickActiveGroup(nil, nil); got != "" {
		t.Fatalf("no groups means no active group, got %q", got)
	}
}

func TestMagpieChatNeverSelectsOwnedTaskGroups(t *testing.T) {
	taskGroups := []magpieGroupInfo{{ID: "isoform-background"}, {ID: "isoform-light"}}
	groups := append(append([]magpieGroupInfo{}, taskGroups...), magpieGroupInfo{ID: "local"})
	for _, selected := range []string{"", "group/isoform-background", "group/isoform-light"} {
		agents := map[string]string{"claude": selected}
		if got := pickActiveGroup(groups, agents); got != "local" {
			t.Fatalf("task group selected for chat: %q (agent=%q)", got, selected)
		}
		if got := pickActiveGroup(taskGroups, agents); got != "" {
			t.Fatalf("task-only listing selected chat group: %q", got)
		}
	}
	app := &App{}
	for _, active := range []string{"isoform-background", "isoform-light"} {
		app.magpieModels.applyBootstrap(magpieBootstrapState{
			External: true, ActiveGroup: active, AgentModels: map[string]string{
				"claude": "group/isoform-light", "codex": "codex/gpt-6-astra",
			},
		}, nil)
		if got := app.magpieRouteTargets(); got.Chat != "codex/gpt-6-astra" || got.Background != got.Chat || got.Light != got.Chat {
			t.Fatalf("cached task group became chat: %+v", got)
		}
	}
}

func TestMagpieRouteTargetsFollowTheInstall(t *testing.T) {
	app := &App{}
	app.magpieModels.applyBootstrap(magpieBootstrapState{External: true, ActiveGroup: "never-stuck", Groups: []magpieGroupPlan{
		{ID: "never-stuck", Members: []string{"claude/claude-sonnet-5-5"}},
		{ID: "isoform-background", Members: []string{"claude/claude-sonnet-5-5"}},
		{ID: "isoform-light", Members: []string{"claude/claude-haiku-5-5"}},
	}}, nil)
	if got := app.magpieRouteTargets(); got.Chat != "group/never-stuck" || got.Light != "group/isoform-light" || got.Background != "group/isoform-background" {
		t.Fatalf("user magpie should route through its group, got %+v", got)
	}
	app.magpieModels.applyBootstrap(magpieBootstrapState{External: true, AgentModels: map[string]string{"claude": "claude/claude-haiku-5-5"}}, nil)
	if got := app.magpieRouteTargets(); got.Chat != "claude/claude-haiku-5-5" {
		t.Fatalf("without a group the Claude Code model is the route, got %+v", got)
	}
	app.magpieModels.applyBootstrap(magpieBootstrapState{}, nil)
	if got := app.magpieRouteTargets(); got.Chat != magpieChatGroupModel || got.Light != magpieLightGroupModel {
		t.Fatalf("the bundled copy routes through the yansu groups until Isoform's group exists, got %+v", got)
	}
	app.magpieModels.applyBootstrap(magpieBootstrapState{ActiveGroup: magpieIsoformGroupID}, nil)
	if got := app.magpieRouteTargets(); got.Chat != "group/isoform" || got.Background != magpieBackgroundGroupModel || got.Light != magpieLightGroupModel {
		t.Fatalf("the bundled copy thinks through Isoform's own group, got %+v", got)
	}
	app.magpieModels.applyBootstrap(magpieBootstrapState{External: true, ActiveGroup: magpieIsoformGroupID, Groups: []magpieGroupPlan{
		{ID: magpieIsoformGroupID, Members: []string{"claude/claude-sonnet-5-5"}},
		{ID: "isoform-background", Members: []string{"claude/claude-sonnet-5-5"}},
		{ID: "isoform-light", Members: []string{"claude/claude-haiku-5-5"}},
	}}, nil)
	if got := app.magpieRouteTargets(); got.Chat != "group/isoform" || got.Light != "group/isoform-light" || got.Background != "group/isoform-background" {
		t.Fatalf("the user's magpie thinks through Isoform's own group, got %+v", got)
	}
}

func TestExternalBackgroundGroupsKeepChatIntactAndUsePacedRoutes(t *testing.T) {
	groups := []magpieGroupInfo{{ID: "isoform", Members: []string{"claude/claude-opus-4-7", "codex/gpt-6-astra"}, Routing: "order"}}
	catalog := []string{"claude/claude-opus-4-7", "claude/claude-haiku-4-5", "codex/gpt-6-astra", "codex/gpt-6-luna"}
	plans := planExternalBackgroundGroups(groups, "isoform", nil, catalog)
	if len(plans) != 2 || plans[0].ID != "isoform-background" || plans[0].Routing != "pace" || plans[0].Stays != "off" {
		t.Fatalf("background route is not separately paced: %+v", plans)
	}
	if !reflect.DeepEqual(plans[0].Members, groups[0].Members) || groups[0].Routing != "order" {
		t.Fatalf("selected providers or chat route changed: %+v / %+v", plans, groups)
	}
	if plans[1].ID != "isoform-light" || plans[1].Stays != "off" || !reflect.DeepEqual(plans[1].Members, []string{"claude/claude-haiku-4-5", "codex/gpt-6-luna"}) {
		t.Fatalf("light tasks should use small models: %+v", plans[1])
	}
	// A user's private/local provider remains selected. A subscription merely
	// present in the catalog must not be admitted to unattended work.
	plans = planExternalBackgroundGroups([]magpieGroupInfo{{ID: "local", Members: []string{"ollama/my-model"}}}, "local", nil, catalog)
	if !reflect.DeepEqual(plans[0].Members, []string{"ollama/my-model"}) || !reflect.DeepEqual(plans[1].Members, plans[0].Members) {
		t.Fatalf("background work expanded the user's selected providers: %+v", plans)
	}
}

func TestExternalBackgroundGroupsResolveAliasesWithoutCycles(t *testing.T) {
	groups := []magpieGroupInfo{
		{ID: "selected", Members: []string{"group/nested", "group/isoform-background", "group/missing", "ollama/local"}},
		{ID: "nested", Members: []string{"group/selected", "claude/claude-opus-4-7", "ollama/local", "group/isoform-light"}},
		{ID: "isoform-background", Members: []string{"codex/stale-model"}},
		{ID: "isoform-light", Members: []string{"codex/stale-light"}},
	}
	plans := planExternalBackgroundGroups(groups, "selected", nil, []string{"claude/claude-haiku-4-5"})
	if !reflect.DeepEqual(plans[0].Members, []string{"claude/claude-opus-4-7", "ollama/local"}) ||
		!reflect.DeepEqual(plans[1].Members, []string{"claude/claude-haiku-4-5", "ollama/local"}) {
		t.Fatalf("aliases retained a cycle or stale provider: %+v", plans)
	}
	// An explicitly selected empty group does not admit an unrelated agent
	// provider; absent routes may use the agent's direct model as before.
	plans = planExternalBackgroundGroups([]magpieGroupInfo{{ID: "empty"}}, "empty", map[string]string{"claude": "claude/unselected"}, nil)
	if len(plans[0].Members) != 0 || len(plans[1].Members) != 0 {
		t.Fatalf("empty selected group expanded its providers: %+v", plans)
	}
}

func TestManagedModelsReadyFollowsTheActiveGroup(t *testing.T) {
	sidecar := &magpieSidecar{}
	sidecar.setHealthy(true)
	app := &App{magpie: sidecar}

	// The user's own magpie: no yansu-chat group, chat routes through the
	// Isoform group, so the app is ready.
	app.magpieModels.applyBootstrap(magpieBootstrapState{
		External:    true,
		ActiveGroup: magpieIsoformGroupID,
		Groups: []magpieGroupPlan{
			{ID: "never-stuck", Members: []string{"claude/claude-sonnet-5-5"}},
			{ID: magpieIsoformGroupID, Members: []string{"claude/claude-sonnet-5-5", "codex/gpt-6-astra"}},
		},
	}, nil)
	if !app.managedModelsReady() {
		t.Fatal("a user magpie with a populated Isoform group should be ready")
	}

	// An active group with no members is not ready, and neither is an
	// unknown active group.
	app.magpieModels.applyBootstrap(magpieBootstrapState{External: true, ActiveGroup: "empty", Groups: []magpieGroupPlan{{ID: "empty"}}}, nil)
	if app.managedModelsReady() {
		t.Fatal("an empty active group must not count as ready")
	}

	// The bundled copy with no active group falls back to yansu-chat.
	app.magpieModels.applyBootstrap(magpieBootstrapState{Groups: []magpieGroupPlan{{ID: magpieChatGroupID, Members: []string{"claude/claude-sonnet-5-5"}}}}, nil)
	if !app.managedModelsReady() {
		t.Fatal("the bundled yansu-chat fallback should be ready")
	}
}
