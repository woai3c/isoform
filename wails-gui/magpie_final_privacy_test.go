package main

import (
	"context"
	"testing"
)

func TestExternalSelectedEmptyGroupNeverFallsBackToCloud(t *testing.T) {
	s := startFakeSidecar(t)
	useFakeExternalMagpie(t, s)
	writeFakeState(t, s, "groups.json", `{
		"isoform":{"name":"Isoform","models":"","routing":"order","stays":"session"},
		"other-cloud":{"name":"Other cloud","models":"claude/claude-sonnet-5-5","routing":"order","stays":"session"},
		"isoform-background":{"name":"Isoform background","models":"claude/claude-sonnet-5-5","routing":"pace","stays":"off"},
		"isoform-light":{"name":"Isoform light","models":"claude/claude-haiku-5-5","routing":"usage","stays":"off"}}`)
	writeFakeState(t, s, "agents.json", `{"claude":"group/other-cloud"}`)
	t.Setenv("FAKE_MAGPIE_READ_ONLY_GROUPS", "1")
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil || state.ActiveGroup != "isoform" {
		t.Fatalf("selected empty group lost during bootstrap: %+v, %v", state, err)
	}
	for _, staleTasks := range []bool{false, true} {
		if staleTasks {
			state.Groups = append(state.Groups,
				magpieGroupPlan{ID: "isoform-background", Members: []string{"claude/claude-sonnet-5-5"}},
				magpieGroupPlan{ID: "isoform-light", Members: []string{"claude/claude-haiku-5-5"}})
		}
		a := &App{magpie: s}
		a.magpieModels.applyBootstrap(state, nil)
		if routes := a.magpieRouteTargets(); routes.Chat != "group/isoform" || routes.Background != routes.Chat || routes.Light != routes.Chat {
			t.Fatalf("empty selection authorized another provider (stale tasks=%v): %+v", staleTasks, routes)
		}
		if a.managedModelsReady() {
			t.Fatalf("empty selection claimed readiness through another group (stale tasks=%v)", staleTasks)
		}
	}
}
