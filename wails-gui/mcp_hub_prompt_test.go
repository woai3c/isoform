package main

import (
	"strings"
	"testing"

	internalmcp "wails-gui/internal/mcp"
)

func TestMcpHubContextEmptyStateStillTeachesCLI(t *testing.T) {
	// Empty state must still emit the install hint so the agent can pick
	// up the chat-driven install path even before the user has any
	// servers configured.
	fakeHomeDirT(t)
	app := &App{}
	got := app.mcpHubContext()
	if got == "" {
		t.Fatalf("expected non-empty context for empty state")
	}
	if !strings.Contains(got, "<mcp-servers>") {
		t.Fatalf("missing opening tag: %q", got)
	}
	if !strings.Contains(got, "yansu") || !strings.Contains(got, "mcp install") {
		t.Fatalf("install hint missing: %q", got)
	}
}

func TestMcpHubContextHyphenatedIDShownAsUnderscoreHeader(t *testing.T) {
	// Regression: when an MCP server id contains hyphens (e.g.
	// `upstash-context7`) the prompt must surface the underscore-
	// normalised form in the section header, otherwise the model sees
	// `upstash-context7` next to underscore-prefixed tool names and
	// sometimes "rebuilds" a hyphenated tool name that fails to
	// dispatch — observed empirically as a stray
	// `Called upstash-context7 · resolve-library-id` after every
	// successful underscore call.
	fakeHomeDirT(t)
	if err := withMcpHubState(func(s *mcpHubState) error {
		s.Servers["upstash-context7"] = McpServerConfig{
			ID:        "upstash-context7",
			Name:      "Upstash Context7",
			Source:    mcpSourceManual,
			Transport: mcpTransportStdio,
			Command:   "npx",
			Enabled:   true,
		}
		return nil
	}); err != nil {
		t.Fatalf("withMcpHubState: %v", err)
	}

	app := &App{}
	got := app.mcpHubContext()

	// The header must contain the underscore form.
	if !strings.Contains(got, "upstash_context7") {
		t.Fatalf("expected normalised id in header: %q", got)
	}
	// And the raw id should appear ONLY behind the "CLI id:" tag —
	// never as a bare token next to tool names, which is what fooled
	// the model previously.
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "upstash-context7") &&
			!strings.Contains(line, "CLI id:") {
			t.Fatalf("hyphenated id leaked into a non-CLI context line: %q", line)
		}
	}
}

func TestMcpHubContextListsEnabledServers(t *testing.T) {
	fakeHomeDirT(t)

	if err := withMcpHubState(func(s *mcpHubState) error {
		s.Servers["filesystem"] = McpServerConfig{
			ID:        "filesystem",
			Name:      "Filesystem",
			Source:    mcpSourceManual,
			Transport: mcpTransportStdio,
			Command:   "npx",
			Enabled:   true,
		}
		s.Servers["disabled-one"] = McpServerConfig{
			ID:        "disabled-one",
			Source:    mcpSourceManual,
			Transport: mcpTransportStdio,
			Command:   "echo",
			Enabled:   false,
		}
		return nil
	}); err != nil {
		t.Fatalf("withMcpHubState: %v", err)
	}

	app := &App{}
	got := app.mcpHubContext()
	if !strings.Contains(got, "filesystem") {
		t.Fatalf("expected enabled server in context: %q", got)
	}
	if strings.Contains(got, "disabled-one") {
		t.Fatalf("disabled server should not appear: %q", got)
	}
	if !strings.Contains(got, "EXACT tool name") {
		t.Fatalf("expected the verbatim-name guidance in context: %q", got)
	}
	if !strings.Contains(got, "Do NOT use Bash") {
		t.Fatalf("expected native MCP invocation guidance in context: %q", got)
	}
	if !strings.Contains(got, "synthesises the result for the user") {
		t.Fatalf("expected the post-call summarise rule in context: %q", got)
	}
}

func TestMcpPromptEnabledConfigsExcludesRetiredManagedComposio(t *testing.T) {
	state := &mcpHubState{Servers: map[string]McpServerConfig{}}
	enabled := mcpPromptEnabledConfigs(state, []internalmcp.ServerInfo{
		{
			ID:     composioGoogleSuperServerID,
			Health: internalmcp.HealthHealthy,
		},
		{
			ID:     composioSlackBotServerID,
			Health: internalmcp.HealthHealthy,
		},
		{
			ID:     composioNotionServerID,
			Health: internalmcp.HealthHealthy,
		},
		{
			ID:     composioGitHubServerID,
			Health: internalmcp.HealthHealthy,
		},
		{
			ID:     composioLinearServerID,
			Health: internalmcp.HealthHealthy,
		},
	})
	if len(enabled) != 0 {
		t.Fatalf("managed Composio must not be prompt-enabled: %+v", enabled)
	}
}

func TestMcpPromptEnabledConfigsSkipsUnhealthyComposio(t *testing.T) {
	state := &mcpHubState{Servers: map[string]McpServerConfig{}}
	enabled := mcpPromptEnabledConfigs(state, []internalmcp.ServerInfo{
		{ID: composioGoogleSuperServerID, Health: internalmcp.HealthUnhealthy},
	})
	if len(enabled) != 0 {
		t.Fatalf("unhealthy managed Composio should not be prompt-enabled: %+v", enabled)
	}
}

func TestMcpHubContextHidesPersistedManagedComposio(t *testing.T) {
	fakeHomeDirT(t)
	if err := withMcpHubState(func(s *mcpHubState) error {
		cfg := composioManagedMcpConfig()
		cfg.Enabled = true
		s.Servers[composioGoogleSuperServerID] = cfg
		return nil
	}); err != nil {
		t.Fatalf("withMcpHubState: %v", err)
	}

	app := &App{}
	got := app.mcpHubContext()
	if strings.Contains(got, "composio") || strings.Contains(got, "GOOGLESUPER") {
		t.Fatalf("retired managed app transport leaked into context: %q", got)
	}
}

func TestTruncateMcpToolDescription(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"single sentence.", "single sentence."},
		{"first sentence. second sentence.", "first sentence."},
		{"first\nsecond", "first"},
		// Long without sentence terminator gets ellipsised.
		{strings.Repeat("a", 150), strings.Repeat("a", 120) + "..."},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := truncateMcpToolDescription(tc.in)
			if got != tc.want {
				t.Fatalf("truncateMcpToolDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
