package main

import (
	"context"
	"reflect"
	"testing"

	"yansu-agent/agent"
)

func TestExternalRejectedTaskGroupUpdatesCannotReuseCloudProviders(t *testing.T) {
	s := startFakeSidecar(t)
	useFakeExternalMagpie(t, s)
	writeFakeState(t, s, "groups.json", `{
		"isoform":{"name":"Isoform","models":"ollama/llama3.2:latest","routing":"order","stays":"session"},
		"isoform-background":{"name":"Isoform background","models":"claude/claude-sonnet-5-5","routing":"pace","stays":"off"},
		"isoform-light":{"name":"Isoform light","models":"claude/claude-haiku-5-5","routing":"usage","stays":"off"}}`)
	writeFakeState(t, s, "agents.json", `{"claude":"group/isoform"}`)
	t.Setenv("FAKE_MAGPIE_READ_ONLY_GROUPS", "1")
	before := groupsOnDisk(t, s)
	for i := 0; i < 2; i++ {
		state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
		if err != nil || !state.External {
			t.Fatalf("read-only external bootstrap lost chat: %+v, %v", state, err)
		}
		a := &App{}
		a.magpieModels.applyBootstrap(state, nil)
		routes := a.magpieRouteTargets()
		if routes.Chat != "group/isoform" || routes.Background != routes.Chat || routes.Light != routes.Chat {
			t.Fatalf("unselected stale providers remained routable: %+v", routes)
		}
		cfg := agent.NativeRunnerConfig{}
		applyManagedRunnerConfig(&cfg, "http://127.0.0.1:3425", routes)
		for alias, target := range cfg.BackgroundModelAliases {
			if target != routes.Chat {
				t.Fatalf("background alias %q can still reach a stale group: %s", alias, target)
			}
		}
	}
	if after := groupsOnDisk(t, s); !reflect.DeepEqual(after, before) {
		t.Fatal("read-only group failure changed the user's gateway config")
	}
}
