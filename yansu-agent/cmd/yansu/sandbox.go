package main

import (
	"encoding/json"
	"fmt"
)

// Only reviewed local HTTP API commands may use the bundled CLI's credential
// exception. General readers can leak symlinked credentials, and installation
// APIs can launch a desktop-owned subprocess outside the calling sandbox.
func checkSandboxCommand(args []string) error {
	sandboxed, err := cliSandboxed()
	if err != nil || !sandboxed {
		return err
	}
	if !sandboxCommandAllowed(args) {
		return fmt.Errorf("Isoform sandbox allows only local API commands for cron, activity, memory, and safe skill/MCP queries")
	}
	return nil
}

func sandboxCommandAllowed(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if args[0] == "help" {
		return true
	}
	if len(args) == 1 {
		switch args[0] {
		case "--version", "-v", "--help", "-h":
			return true
		}
	}
	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		return false
	}
	switch cmd {
	case cronCmd, activityCmd, memoryCmd, skillCmd, mcpCmd, mcpRegistryCmd:
		// Parent commands only display help; do not allow their descendants
		// implicitly when new API commands are added.
		return true
	case cronListCmd, cronShowCmd, cronAddCmd, cronUpdateCmd, cronDeleteCmd, cronRunCmd,
		activityListCmd, activityShowCmd, activitySummaryCmd, activitySearchCmd,
		memoryListCmd, memoryShowCmd, memorySearchCmd,
		skillSourcesCmd, skillListCmd, skillDiscoverCmd, skillSearchCmd,
		mcpListCmd, mcpSourcesCmd, mcpToolsCmd, mcpRegistryListCmd, mcpRegistrySearchCmd:
		// Cron execution uses the desktop's guarded automation route. Skill
		// and MCP installation/activation must remain outside this exception.
		// MCP list is projected to safe status fields before output. Discovery
		// and logs expose raw environment/config output and remain blocked.
		return true
	default:
		return false
	}
}

// MCP list's desktop response embeds configuration, including plaintext env
// values. Keep useful status listing without exposing that configuration to a
// sandboxed CLI. This typed projection intentionally omits unknown fields.
func sandboxMCPListResponse(body []byte, status int) ([]byte, error) {
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("MCP listing failed (HTTP %d)", status)
	}
	type serverView struct {
		ID      string            `json:"id"`
		Name    string            `json:"name"`
		Health  string            `json:"health"`
		Enabled bool              `json:"enabled"`
		Trusted bool              `json:"trusted"`
		Tools   []json.RawMessage `json:"tools"`
	}
	type serverStatus struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Health    string `json:"health"`
		Enabled   bool   `json:"enabled"`
		Trusted   bool   `json:"trusted"`
		ToolCount int    `json:"tool_count"`
	}
	var servers []serverView
	if err := json.Unmarshal(body, &servers); err != nil {
		return nil, fmt.Errorf("invalid MCP listing response")
	}
	statuses := make([]serverStatus, 0, len(servers))
	for _, server := range servers {
		statuses = append(statuses, serverStatus{server.ID, server.Name, server.Health, server.Enabled, server.Trusted, len(server.Tools)})
	}
	return json.Marshal(statuses)
}
