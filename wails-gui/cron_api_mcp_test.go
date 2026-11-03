package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newCronMcpTestServer(t *testing.T) (*App, *cronAPIServer, string) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "cron-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	app := &App{}
	server, err := newCronAPIServer(app)
	if err != nil {
		t.Fatalf("start real cron API: %v", err)
	}
	t.Cleanup(server.Stop)
	return app, server, home
}

func postCronMcpInstall(t *testing.T, server *cronAPIServer, secret string, input mcpInstallRequest) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/mcp/install", server.Port()), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/install: %v", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, responseBody
}

func assertCronMcpRegistryEmpty(t *testing.T, app *App) {
	t.Helper()
	state, err := loadMcpHubState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Servers) != 0 {
		t.Fatalf("refused request persisted servers: %+v", state.Servers)
	}
	servers, err := app.ListMcpServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 {
		t.Fatalf("refused request registered servers: %+v", servers)
	}
}

func TestCronAPIMcpInstallRequiresSecret(t *testing.T) {
	app, server, _ := newCronMcpTestServer(t)
	status, body := postCronMcpInstall(t, server, "", mcpInstallRequest{
		Source: "manual", ID: "manual-command", Name: "Manual command", Command: "echo", AutoStart: true,
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", status, body)
	}
	assertCronMcpRegistryEmpty(t, app)
}

func TestCronAPIMcpInstallRefusesManualStdio(t *testing.T) {
	app, server, _ := newCronMcpTestServer(t)
	cases := []struct {
		name      string
		source    string
		transport string
	}{
		{name: "explicit", source: "manual", transport: "stdio"},
		{name: "default transport", source: "manual"},
		{name: "default source", transport: "stdio"},
		{name: "both defaults"},
		{name: "trimmed and folded", source: " MaNuAl ", transport: " StDiO "},
		{name: "whitespace defaults", source: " \t", transport: " \t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := postCronMcpInstall(t, server, server.Secret(), mcpInstallRequest{
				Source: tc.source, Transport: tc.transport,
				ID: "manual-command", Name: "Manual command", Command: "echo", AutoStart: true,
			})
			if status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body: %s", status, body)
			}
			var response map[string]string
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if response["error"] != "add servers in Settings" {
				t.Fatalf("error = %q", response["error"])
			}
			assertCronMcpRegistryEmpty(t, app)
		})
	}
}

func TestCronAPIMcpInstallAllowsLocalSource(t *testing.T) {
	app, server, home := newCronMcpTestServer(t)
	writeJSONFixture(t, filepath.Join(home, ".cursor", "mcp.json"), map[string]any{
		"mcpServers": map[string]any{
			"local-fixture": map[string]any{
				"command": "echo",
				"args":    []string{"local fixture"},
			},
		},
	})
	status, body := postCronMcpInstall(t, server, server.Secret(), mcpInstallRequest{
		Source: "local", SourceID: "cursor", DiscoveredName: "local-fixture", AutoStart: false,
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", status, body)
	}
	var view McpServerView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if view.ID != "local-fixture" || view.Source != mcpSourceLocal || view.Command != "echo" {
		t.Fatalf("unexpected installed server: %+v", view)
	}
	servers, err := app.ListMcpServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].ID != view.ID || servers[0].SourceURL != "mcp-discovery://cursor/local-fixture" {
		t.Fatalf("local server not registered: %+v", servers)
	}
}

func TestCronAPIMcpGuardPreservesSettingsBinding(t *testing.T) {
	app, _, _ := newCronMcpTestServer(t)
	view, err := app.AddMcpServerManual(ManualServerInput{
		ID: "settings-command", Name: "Settings command", Transport: mcpTransportStdio,
		Command: "echo", Args: []string{"settings fixture"}, AutoStart: false,
	})
	if err != nil {
		t.Fatalf("Settings manual install: %v", err)
	}
	servers, err := app.ListMcpServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].ID != view.ID || servers[0].Source != mcpSourceManual {
		t.Fatalf("Settings server not registered: %+v", servers)
	}
}
