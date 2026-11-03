package main

import (
	"context"
	"testing"

	"wails-gui/storage"
)

func TestAppBindIntegrationSelfIdentityLinksMultipleAppsToSelf(t *testing.T) {
	a := newBindingsTestApp(t)
	self, err := a.store.EnsureSelf(context.Background(), "Alex Rowan", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}

	bindings := []struct {
		app           string
		handleID      string
		displayHandle string
	}{
		{app: "slack", handleID: "U123"},
		{app: "discord", handleID: "discord-self-synthetic", displayHandle: "alex_r"},
		{app: "telegram", handleID: "telegram-self-synthetic", displayHandle: "alexrowan_tg"},
		{app: "wechat", handleID: "wxid_self"},
	}
	for _, binding := range bindings {
		if err := a.bindIntegrationSelfIdentity(
			context.Background(),
			binding.app,
			binding.handleID,
			binding.displayHandle,
		); err != nil {
			t.Fatalf("bindIntegrationSelfIdentity(%s): %v", binding.app, err)
		}
	}

	for _, binding := range bindings {
		resolved, err := a.store.ResolveByAppHandle(context.Background(), binding.app, binding.handleID)
		if err != nil {
			t.Fatalf("ResolveByAppHandle(%s): %v", binding.app, err)
		}
		if resolved == nil || resolved.ID != self.ID {
			t.Errorf("%s handle resolved to %+v, want self %s", binding.app, resolved, self.ID)
		}
	}
}

func TestAppBindIntegrationSelfIdentityCreatesSingleSelf(t *testing.T) {
	a := newBindingsTestApp(t)
	if err := a.bindIntegrationSelfIdentity(context.Background(), "discord", "disc-1", "Discord Name"); err != nil {
		t.Fatalf("bind discord: %v", err)
	}
	if err := a.bindIntegrationSelfIdentity(context.Background(), "telegram", "tg-1", "Telegram Name"); err != nil {
		t.Fatalf("bind telegram: %v", err)
	}

	rows, err := a.store.ListPrincipalsByKind(context.Background(), storage.PrincipalKindSelf)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("self count = %d, want 1", len(rows))
	}
	if rows[0].DisplayName != "Discord Name" {
		t.Errorf("DisplayName = %q, want first integration name", rows[0].DisplayName)
	}
}

func TestAppBindIntegrationSelfIdentityIgnoresMissingHandle(t *testing.T) {
	a := newBindingsTestApp(t)
	if err := a.bindIntegrationSelfIdentity(context.Background(), "slack", "", ""); err != nil {
		t.Fatalf("bindIntegrationSelfIdentity: %v", err)
	}
	rows, err := a.store.ListPrincipalsByKind(context.Background(), storage.PrincipalKindSelf)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("created self without handle ID: %+v", rows)
	}
}

func TestAppBindIntegrationSelfIdentityWithoutAHandleNamesNobody(t *testing.T) {
	a := newBindingsTestApp(t)
	if err := a.bindIntegrationSelfIdentity(context.Background(), "wechat", "wx-1", ""); err != nil {
		t.Fatalf("bind wechat: %v", err)
	}
	rows, err := a.store.ListPrincipalsByKind(context.Background(), storage.PrincipalKindSelf)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(rows) != 1 || rows[0].DisplayName != "" {
		t.Fatalf("self = %+v, want one self with no name", rows)
	}
	// A handle bound later still names the self principal.
	if err := a.bindIntegrationSelfIdentity(context.Background(), "discord", "disc-1", "Discord Name"); err != nil {
		t.Fatalf("bind discord: %v", err)
	}
	rows, _ = a.store.ListPrincipalsByKind(context.Background(), storage.PrincipalKindSelf)
	if len(rows) != 1 || rows[0].DisplayName != "Discord Name" {
		t.Fatalf("self after discord = %+v", rows)
	}
}
