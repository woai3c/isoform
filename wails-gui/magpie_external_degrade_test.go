package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func useFakeExternalMagpie(t *testing.T, sidecar *magpieSidecar) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(sidecar.stateDir, "config"))
	sidecar.mu.Lock()
	sidecar.external = true
	sidecar.mu.Unlock()
}

func TestExternalBootstrapGroupWriteFailurePreservesChat(t *testing.T) {
	for _, groupID := range []string{"isoform", "never-stuck"} {
		t.Run(groupID, func(t *testing.T) {
			s := startFakeSidecar(t)
			writeFakeState(t, s, "groups.json", `{"`+groupID+`":{"name":"Chat","models":"claude/claude-sonnet-5-5","routing":"order","stays":"session"}}`)
			writeFakeState(t, s, "agents.json", `{"claude":"group/`+groupID+`"}`)
			before := groupsOnDisk(t, s)
			useFakeExternalMagpie(t, s)
			t.Setenv("FAKE_MAGPIE_READ_ONLY_GROUPS", "1")
			state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
			app := &App{}
			app.magpieModels.applyBootstrap(state, err)
			if err != nil || !app.magpieModels.snapshot().External {
				t.Fatalf("optional group failure lost external state: state=%+v err=%v", state, err)
			}
			want := "group/" + groupID
			if routes := app.magpieRouteTargets(); routes.Chat != want || routes.Background != want || routes.Light != want {
				t.Fatalf("read-only installation lost its usable route: %+v, want %s", routes, want)
			}
			if after := groupsOnDisk(t, s); !reflect.DeepEqual(after, before) {
				t.Fatalf("read-only configuration changed: before=%+v after=%+v", before, after)
			}
			if len(state.Groups) != 1 {
				t.Fatalf("failed desired groups reported as installed: %+v", state.Groups)
			}
		})
	}
}

func TestExternalBootstrapPartialGroupFailureUsesOnlyInstalledRoutes(t *testing.T) {
	s := startFakeSidecar(t)
	writeFakeState(t, s, "groups.json", `{"isoform":{"name":"Isoform","models":"claude/claude-sonnet-5-5","routing":"order","stays":"session"}}`)
	useFakeExternalMagpie(t, s)
	t.Setenv("FAKE_MAGPIE_FAIL_GROUP", "isoform-light")
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{magpie: s}
	app.magpieModels.applyBootstrap(state, nil)
	if routes := app.magpieRouteTargets(); routes.Chat != "group/isoform" || routes.Background != "group/isoform" || routes.Light != "group/isoform" {
		t.Fatalf("partial failure produced an unavailable route: %+v", routes)
	}
	if plans := plansByID(state.Groups); len(plans) != 1 || len(plans["isoform-light"].Members) != 0 {
		t.Fatalf("failed light group reported as installed: %+v", state.Groups)
	}
}

func TestExternalBootstrapWithoutGroupsUsesAdvertisedModel(t *testing.T) {
	s := startFakeSidecar(t)
	writeFakeState(t, s, "groups.json", `{}`)
	writeFakeState(t, s, "agents.json", `{}`)
	useFakeExternalMagpie(t, s)
	t.Setenv("FAKE_MAGPIE_NO_TEMPLATES", "1")
	state, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{})
	if err != nil || !state.External || state.ActiveGroup != "isoform" {
		t.Fatalf("empty external bootstrap: state=%+v err=%v", state, err)
	}
	catalog, err := s.listModels(context.Background())
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %v / %v", catalog, err)
	}
	app := &App{magpie: s}
	app.magpieModels.applyBootstrap(state, nil)
	want := "group/isoform"
	if !app.managedModelsReady() {
		t.Fatal("healthy advertised fallback incorrectly marked not ready")
	}
	if routes := app.magpieRouteTargets(); routes.Chat != want || routes.Background != want || routes.Light != want {
		t.Fatalf("empty plans routed to nonexistent groups: %+v, want %s", routes, want)
	}
	if calls := callsOnDisk(t, s); strings.Contains(calls, "group rm") {
		t.Fatalf("bootstrap removed a group:\n%s", calls)
	}
}

func TestExternalTaskGroupStaysChangesAreAppliedOnce(t *testing.T) {
	s := startFakeSidecar(t)
	writeFakeState(t, s, "groups.json", `{
		"isoform":{"name":"Isoform","models":"claude/claude-sonnet-5-5","routing":"order","stays":"session"},
		"isoform-background":{"name":"Isoform background","models":"claude/claude-sonnet-5-5","routing":"pace","stays":"session"},
		"isoform-light":{"name":"Isoform light","models":"claude/claude-haiku-5-5","routing":"usage","stays":"off"}}`)
	useFakeExternalMagpie(t, s)
	for i := 0; i < 2; i++ {
		if _, err := s.Bootstrap(context.Background(), magpieBootstrapOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := groupsOnDisk(t, s)["isoform-background"].Stays; got != "session" {
		t.Fatalf("user group changed: %s", got)
	}
	if calls := callsOnDisk(t, s); strings.Contains(calls, "group add ") {
		t.Fatalf("external groups were rewritten: %s", calls)
	}

}

func TestExternalRoutesRejectMissingAndEmptyGroups(t *testing.T) {
	app := &App{}
	app.magpieModels.applyBootstrap(magpieBootstrapState{
		External: true, ActiveGroup: "missing", FallbackModel: "ollama/working",
		Groups: []magpieGroupPlan{{ID: "isoform-background"}, {ID: "isoform-light"}},
	}, nil)
	if got := app.magpieRouteTargets(); got.Chat != "ollama/working" || got.Background != got.Chat || got.Light != got.Chat {
		t.Fatalf("missing or empty group chosen: %+v", got)
	}
}
