package main

import (
	"reflect"
	"testing"
)

func TestNormalizeManagedOrder(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty means the default", nil, []string{"claude", "codex"}},
		{"keeps the user's order", []string{"codex", "claude"}, []string{"codex", "claude"}},
		{"a single choice admits only that provider", []string{"codex"}, []string{"codex"}},
		{"unknown and repeated entries drop", []string{" Claude ", "gemini", "claude", ""}, []string{"claude"}},
		{"only unknown entries fall back to the default", []string{"gemini", "kiro"}, []string{"claude", "codex"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeManagedOrder(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizeManagedOrder(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPlanMagpieGroupsHonorsAccountOrder(t *testing.T) {
	plans := planMagpieGroups(magpieBootstrapInput{
		Catalog:         bootstrapCatalog,
		ConsentAccepted: true,
		AccountOrder:    []string{"codex", "claude"},
	})
	byID := plansByID(plans)
	wantChat := []string{"codex/gpt-5.6-sol", "claude/claude-sonnet-5-5"}
	if got := byID["yansu-chat"].Members; !reflect.DeepEqual(got, wantChat) {
		t.Fatalf("chat members = %v, want %v", got, wantChat)
	}
	wantLight := []string{"codex/gpt-6-luna", "claude/claude-haiku-5-5"}
	if got := byID["yansu-light"].Members; !reflect.DeepEqual(got, wantLight) {
		t.Fatalf("light members = %v, want %v", got, wantLight)
	}

	only := plansByID(planMagpieGroups(magpieBootstrapInput{
		Catalog:         bootstrapCatalog,
		ConsentAccepted: true,
		AccountOrder:    []string{"claude"},
	}))
	if got := only["yansu-chat"].Members; !reflect.DeepEqual(got, []string{"claude/claude-sonnet-5-5"}) {
		t.Fatalf("a single choice should admit only that provider, got %v", got)
	}
}

func TestSetManagedAccountOrderValidatesAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_AGENT_HOME", "")

	app := &App{}
	if _, err := app.SetManagedAccountOrder([]string{"gemini"}); err == nil {
		t.Fatal("an unknown assistant must be rejected")
	}
	if _, err := app.SetManagedAccountOrder(nil); err == nil {
		t.Fatal("an empty choice must be rejected")
	}
	status, err := app.SetManagedAccountOrder([]string{"Codex", "claude", "codex"})
	if err != nil {
		t.Fatalf("SetManagedAccountOrder: %v", err)
	}
	want := []string{"codex", "claude"}
	if !reflect.DeepEqual(status.AccountOrder, want) {
		t.Fatalf("status.AccountOrder = %v, want %v", status.AccountOrder, want)
	}
	if got := loadAIProviderConfig().managedOrder(); !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted order = %v, want %v", got, want)
	}
}
