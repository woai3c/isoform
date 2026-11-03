package main

import (
	"context"
	"testing"

	"wails-gui/storage"
)

func TestAppBindFeishuSelfIdentityCreatesSelf(t *testing.T) {
	a := newBindingsTestApp(t)
	creds := &feishuLocalCredentials{
		OpenID:   "ou_feishu_self",
		UserName: "Alex Rowan",
	}

	if err := a.bindFeishuSelfIdentity(context.Background(), creds); err != nil {
		t.Fatalf("bindFeishuSelfIdentity: %v", err)
	}

	self, err := a.store.ResolveByAppHandle(context.Background(), feishuIdentityApp, creds.OpenID)
	if err != nil {
		t.Fatalf("ResolveByAppHandle: %v", err)
	}
	if self == nil {
		t.Fatal("Feishu handle did not resolve to a principal")
	}
	if self.Kind != storage.PrincipalKindSelf {
		t.Errorf("Kind = %q, want self", self.Kind)
	}
	if self.DisplayName != creds.UserName {
		t.Errorf("DisplayName = %q, want %q", self.DisplayName, creds.UserName)
	}
}

func TestAppBindFeishuSelfIdentityPreservesExistingSelfName(t *testing.T) {
	a := newBindingsTestApp(t)
	existing, err := a.store.EnsureSelf(context.Background(), "People Name", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	creds := &feishuLocalCredentials{
		OpenID:   "ou_existing_self",
		UserName: "Feishu Name",
	}

	if err := a.bindFeishuSelfIdentity(context.Background(), creds); err != nil {
		t.Fatalf("bindFeishuSelfIdentity: %v", err)
	}

	self, err := a.store.ResolveByAppHandle(context.Background(), feishuIdentityApp, creds.OpenID)
	if err != nil {
		t.Fatalf("ResolveByAppHandle: %v", err)
	}
	if self == nil || self.ID != existing.ID {
		t.Fatalf("Feishu handle resolved to %+v, want self %s", self, existing.ID)
	}
	if self.DisplayName != existing.DisplayName {
		t.Errorf("DisplayName = %q, want existing name %q", self.DisplayName, existing.DisplayName)
	}
	handles, err := a.store.ListAppHandlesByPrincipal(context.Background(), existing.ID)
	if err != nil {
		t.Fatalf("ListAppHandlesByPrincipal: %v", err)
	}
	if len(handles) != 1 || handles[0].DisplayHandle != creds.UserName {
		t.Errorf("handles = %+v, want Feishu display handle %q", handles, creds.UserName)
	}
}

func TestAppBindFeishuSelfIdentityReplacesStaleDisplayHandle(t *testing.T) {
	a := newBindingsTestApp(t)
	creds := &feishuLocalCredentials{OpenID: "ou_upgraded_self"}

	if err := a.bindFeishuSelfIdentity(context.Background(), creds); err != nil {
		t.Fatalf("first bindFeishuSelfIdentity: %v", err)
	}
	creds.UserName = "Updated Feishu Name"
	if err := a.bindFeishuSelfIdentity(context.Background(), creds); err != nil {
		t.Fatalf("second bindFeishuSelfIdentity: %v", err)
	}
	if err := a.bindFeishuSelfIdentity(context.Background(), creds); err != nil {
		t.Fatalf("idempotent bindFeishuSelfIdentity: %v", err)
	}

	self, err := a.store.ResolveByAppHandle(context.Background(), feishuIdentityApp, creds.OpenID)
	if err != nil {
		t.Fatalf("ResolveByAppHandle: %v", err)
	}
	if self == nil {
		t.Fatal("Feishu handle did not resolve to self")
	}
	handles, err := a.store.ListAppHandlesByPrincipal(context.Background(), self.ID)
	if err != nil {
		t.Fatalf("ListAppHandlesByPrincipal: %v", err)
	}
	if len(handles) != 1 {
		t.Fatalf("handle count = %d, want 1: %+v", len(handles), handles)
	}
	if handles[0].DisplayHandle != creds.UserName {
		t.Errorf("DisplayHandle = %q, want %q", handles[0].DisplayHandle, creds.UserName)
	}
}

func TestAppBindFeishuSelfIdentityIgnoresMissingOpenID(t *testing.T) {
	a := newBindingsTestApp(t)
	if err := a.bindFeishuSelfIdentity(context.Background(), &feishuLocalCredentials{UserName: "Name"}); err != nil {
		t.Fatalf("bindFeishuSelfIdentity: %v", err)
	}
	rows, err := a.store.ListPrincipalsByKind(context.Background(), storage.PrincipalKindSelf)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("created self without open_id: %+v", rows)
	}
}

func TestPersonalAgentDisplayName(t *testing.T) {
	cases := []struct {
		name    string
		appName string
		want    string
	}{
		{"feishu chinese template", "ar的智能助手", "ar"},
		{"feishu chinese with spaces", "  Alex Rowan的智能助手  ", "Alex Rowan"},
		{"lark english ai assistant", "Ar's AI Assistant", "Ar"},
		{"lark english curly apostrophe", "Ar’s AI Assistant", "Ar"},
		{"lark english assistant", "Maya's Assistant", "Maya"},
		{"unknown template stays empty", "Some Random Bot", ""},
		{"empty input", "", ""},
		{"suffix only yields empty", "的智能助手", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := personalAgentDisplayName(tc.appName); got != tc.want {
				t.Errorf("personalAgentDisplayName(%q) = %q, want %q", tc.appName, got, tc.want)
			}
		})
	}
}

func TestFirstRegString(t *testing.T) {
	values := map[string]any{
		"name":         "  ",
		"display_name": "Feishu User",
		"nickname":     "Nickname",
	}
	if got := firstRegString(values, "name", "display_name", "nickname"); got != "Feishu User" {
		t.Errorf("firstRegString = %q, want Feishu User", got)
	}
}
