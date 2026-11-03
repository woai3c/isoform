package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComposioProjectStatus(t *testing.T) {
	cases := map[string]string{
		"ACTIVE":       composioStatusConnected,
		"active":       composioStatusConnected,
		"INITIATED":    composioStatusConnecting,
		"INITIALIZING": composioStatusConnecting, // review: v3.1 adds this
		"PENDING":      composioStatusConnecting,
		"":             composioStatusConnecting,
		"WEIRD_NEW":    composioStatusConnecting, // unknown → not-ready, never connected
		"EXPIRED":      composioStatusDisconnected,
		"FAILED":       composioStatusDisconnected,
		"INACTIVE":     composioStatusDisconnected,
	}
	for raw, want := range cases {
		if got := composioProjectStatus(raw); got != want {
			t.Errorf("composioProjectStatus(%q)=%q want %q", raw, got, want)
		}
	}
}

func TestToolkitSlugForApp(t *testing.T) {
	if got := toolkitSlugForApp(composioSlackBotApp); got != "slack" {
		t.Errorf("slackbot toolkit slug=%q want slack", got)
	}
	if got := toolkitSlugForApp("unknownapp"); got != "unknownapp" {
		t.Errorf("unknown app slug should pass through, got %q", got)
	}
}

func TestComposioPathUsesVersionConstant(t *testing.T) {
	got := composioPath("auth_configs")
	want := composioAPIBase + "/api/" + composioAPIVersion + "/auth_configs"
	if got != want {
		t.Errorf("composioPath=%q want %q", got, want)
	}
}

func TestAddComposioMCPIdentity(t *testing.T) {
	out := addComposioMCPIdentity("https://mcp.composio.dev/v3/mcp/srv", "entity1", "ca_123")
	for _, want := range []string{"user_id=entity1", "connected_account_id=ca_123", "include_composio_helper_actions=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("identity URL %q missing %q", out, want)
		}
	}
	// Existing params must not be overwritten.
	out2 := addComposioMCPIdentity("https://x/y?user_id=keep", "entity1", "ca_1")
	if !strings.Contains(out2, "user_id=keep") {
		t.Errorf("should not overwrite existing user_id: %q", out2)
	}
}

func TestComposioAuthConfigResponseID(t *testing.T) {
	// review #2: id is nested under auth_config; accept top-level as a fallback.
	nested := composioAuthConfigCreateResponse{}
	nested.AuthConfig.ID = "ac_nested"
	if nested.id() != "ac_nested" {
		t.Errorf("nested id() = %q", nested.id())
	}
	top := composioAuthConfigCreateResponse{ID: "ac_top"}
	if top.id() != "ac_top" {
		t.Errorf("top-level id() = %q", top.id())
	}
}

// ensureServer must recover an existing remote MCP server by name when the
// create call hits a unique-name conflict (review #3) — e.g. after the user
// cleared the local cache but the remote yansu-<app> server still exists.
func TestComposioEnsureServerRecoversByName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := mutateComposioByok(func(st *composioByokState) { st.APIKey = "ck_test" }); err != nil {
		t.Fatal(err)
	}

	var sawList bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/mcp/servers"):
			// Simulate a duplicate-name conflict.
			http.Error(w, `{"error":{"code":1151,"message":"name already exists"}}`, http.StatusConflict)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/mcp/servers"):
			sawList = true
			if got := r.URL.Query().Get("name"); got != "yansu-notion" {
				t.Errorf("list by name = %q want yansu-notion", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]string{{"id": "srv_existing", "mcp_url": "https://mcp/x", "name": "yansu-notion"}},
			})
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	prev := composioAPIBase
	composioAPIBase = srv.URL
	defer func() { composioAPIBase = prev }()

	c := &directComposioClient{}
	id, err := c.ensureServer(context.Background(), composioNotionApp, "ac_1")
	if err != nil {
		t.Fatalf("ensureServer should recover, got %v", err)
	}
	if id != "srv_existing" {
		t.Errorf("recovered server id = %q want srv_existing", id)
	}
	if !sawList {
		t.Error("expected a list-by-name recovery call after the create conflict")
	}
	// The recovered id must be cached.
	if cached := loadComposioByok().Connections[composioNotionApp].ServerID; cached != "srv_existing" {
		t.Errorf("recovered server id not cached, got %q", cached)
	}
}

func TestComposioByokLegacyKeyNeverEnablesManagedAppAccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if composioByokConfigured() {
		t.Fatal("should not be configured on a fresh HOME")
	}
	if useDirectComposio() {
		t.Fatal("useDirectComposio should be false without a key")
	}

	if err := mutateComposioByok(func(st *composioByokState) { st.APIKey = "ck_test" }); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !composioByokConfigured() {
		t.Error("should be configured after saving a key")
	}
	if useDirectComposio() {
		t.Error("a legacy key must not re-enable retired managed app access")
	}
	if status := (&App{}).GetComposioApiKeyStatus(); status.Configured {
		t.Fatalf("retired key status must stay hidden, got %+v", status)
	}
	// A stable entity id is generated on first save.
	if id := loadComposioByok().EntityID; strings.TrimSpace(id) == "" {
		t.Error("entity id should be generated on save")
	}

	// Clearing the key disables direct mode.
	if err := mutateComposioByok(func(st *composioByokState) { st.APIKey = "" }); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if composioByokConfigured() || useDirectComposio() {
		t.Error("should not be configured after clearing the key")
	}
}

func TestSaveComposioApiKeyFailsClosedWithoutPersisting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	err := (&App{}).SaveComposioApiKey("ck_must_not_be_saved")
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("SaveComposioApiKey error = %v, want retired boundary", err)
	}
	if composioByokConfigured() {
		t.Fatal("retired managed app access persisted a Composio key")
	}
}

func TestDisconnectComposioAppOnlyCleansLegacyLocalState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := mutateComposioByok(func(state *composioByokState) {
		state.APIKey = "ck_legacy"
		state.Connections = map[string]composioByokConnection{}
		state.Connections[composioNotionApp] = composioByokConnection{
			ConnectedAccountID: "legacy-account",
			Status:             composioStatusConnected,
		}
	}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	if err := (&App{}).DisconnectComposioApp(composioNotionApp); err != nil {
		t.Fatalf("DisconnectComposioApp: %v", err)
	}
	state := loadComposioByok()
	if _, exists := state.Connections[composioNotionApp]; exists {
		t.Fatal("legacy managed connection was not removed")
	}
	if state.APIKey != "ck_legacy" {
		t.Fatal("single-app cleanup unexpectedly changed the legacy key")
	}
}
