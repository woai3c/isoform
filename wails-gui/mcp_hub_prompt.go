package main

import (
	"fmt"
	"sort"
	"strings"

	internalmcp "wails-gui/internal/mcp"
	"wails-gui/internal/platform/shellenv"
)

const (
	// maxMcpContextBytes caps the total prompt injection. The bridge
	// already exposes each MCP tool to the agent under its own name —
	// this section is just a "table of contents" + the yansu-CLI hint
	// for chat-driven install. Tight budget keeps small / common MCP
	// loadouts cheap; very long tool lists get truncated rather than
	// crowding out other context (memory, skills, activity).
	maxMcpContextBytes = 4 << 10
	// maxMcpToolsListedPerServer is a per-server cap. A server exposing
	// hundreds of tools (some MCP servers are essentially API gateways)
	// shouldn't dominate the section.
	maxMcpToolsListedPerServer = 12
)

// injectMcpContext prepends a compact summary of enabled MCP servers and
// their tools to the prompt. We also emit the yansu-CLI install/manage
// hint so the agent can install/enable/disable MCP servers in chat.
//
// The agent already has the actual tools registered via the bridge — this
// is just the human-readable map so it knows what's available without
// having to call list_tools first.
func (a *App) injectMcpContext(prompt string) string {
	context := a.mcpHubContext()
	if context == "" {
		return prompt
	}
	return context + prompt
}

func (a *App) mcpHubContext() string {
	rt := a.runtimeRef()
	state, err := loadMcpHubState()
	if err != nil {
		return ""
	}
	var runtimeInfo []internalmcp.ServerInfo
	if rt != nil {
		runtimeInfo = rt.manager.Info()
	}
	enabled := mcpPromptEnabledConfigs(state, runtimeInfo)
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].ID < enabled[j].ID })

	yansuCmd := mcpHubPromptYansuCmd()

	// Always emit the install hint, even when no servers are configured
	// yet — that's exactly the moment when "tell the user how to install
	// one" is most valuable.
	var b strings.Builder
	b.WriteString("<mcp-servers>\n")
	if len(enabled) == 0 {
		b.WriteString("No MCP servers are currently enabled. To install one, use the yansu CLI:\n")
		b.WriteString("  ")
		b.WriteString(yansuCmd)
		b.WriteString(" mcp discover                       # find servers configured by Claude Desktop / Cursor / VS Code / Codex / Windsurf / Claude Code CLI\n")
		b.WriteString("  ")
		b.WriteString(yansuCmd)
		b.WriteString(" mcp install --source local --ref claude-desktop:filesystem\n")
		b.WriteString("  ")
		b.WriteString(yansuCmd)
		b.WriteString(" mcp install --source manual --id <id> --command npx --args \"-y,@scope/server,...\"\n")
		b.WriteString("</mcp-servers>\n\n")
		return b.String()
	}

	if rt != nil {
		a.syncMcpToolsForPrompt(rt, enabled)
	}

	b.WriteString("The following MCP servers are enabled. Each bullet under a server shows the EXACT tool name registered with the agent — copy the backticked name verbatim (no character substitutions, no rebuilding from the server header or any \"CLI id\"). These tools are already loaded; do not verify, restart, or reinstall the server before using them, and Do NOT use Bash, the yansu MCP CLI, npx/node, or hand-written JSON-RPC as a substitute for invoking a listed native MCP tool. Those command-line paths are only for installing servers or diagnosing health/listing failures when no native tool is listed.\n\n")
	b.WriteString("After invoking ANY MCP tool, you MUST follow up with an assistant message that synthesises the result for the user in their language. The user does not see tool inputs/outputs directly — only the assistant text you write after the calls — so a turn that ends on a bare tool call looks empty to them. For chained / multi-step tools (e.g. ones that return a `nextThoughtNeeded` flag, a `cursor`, or similar continuation hints), keep invoking the tool until the chain reports it is finished, then write a single summary message that delivers the answer.\n\n")

	written := b.Len()
	for _, cfg := range enabled {
		var toolLines []string
		var totalTools int
		if isGatewayMcpServer(cfg.ID) {
			if rt != nil {
				if tools, err := rt.manager.Tools(cfg.ID); err == nil {
					totalTools = len(tools)
				}
			}
			searchTool := internalmcp.AdapterToolName(cfg.ID, "search_tools")
			describeTool := internalmcp.AdapterToolName(cfg.ID, "describe_tool")
			callTool := internalmcp.AdapterToolName(cfg.ID, "call_tool")
			toolLines = append(toolLines,
				"    - `"+searchTool+"` — Search hidden upstream provider tools by task/query; returns lightweight names/descriptions only.",
				"    - `"+describeTool+"` — Read one searched upstream tool's exact input schema before calling it.",
				"    - `"+callTool+"` — Call one upstream tool by original tool_name with args from its described schema.",
				"    - Gateway flow: search first, describe the selected upstream tool, then call it. Do not invent upstream tool arguments without describing the schema.",
				"    - For Gmail list/search requests, prefer `include_payload=false` and a small `max_results`; fetch full message bodies only when the user explicitly asks.",
			)
		} else if rt != nil {
			if tools, err := rt.manager.Tools(cfg.ID); err == nil {
				totalTools = len(tools)
				limit := min(totalTools, maxMcpToolsListedPerServer)
				for i := range limit {
					tool := tools[i]
					// Emit the exposed (sanitised, hyphen-free) name so the
					// model sees exactly the string the registry will match.
					// MCP servers in the wild advertise tool names with
					// hyphens or dots; we normalise those for Anthropic's
					// regex AND because LLMs auto-correct hyphens to
					// underscores in tool_use blocks. Listing the raw name
					// here would make the prompt disagree with the registry,
					// which we observed causing the model to abandon MCP
					// tools and fall back to Bash.
					exposed := internalmcp.AdapterToolName(cfg.ID, tool.Name)
					line := "    - `" + exposed + "`"
					if tool.Description != "" {
						line += " — " + truncateMcpToolDescription(tool.Description)
					}
					toolLines = append(toolLines, line)
				}
				if totalTools > limit {
					toolLines = append(toolLines, fmt.Sprintf("    - ... (%d more)", totalTools-limit))
				}
			}
		}

		var entry strings.Builder
		entry.WriteString("  - ")
		// Show the underscore-normalised id so it's visually consistent
		// with the exposed tool names below; LLMs that try to rebuild a
		// tool name from this header will then produce something the
		// registry actually accepts. The raw id is shown after "CLI id:"
		// only when it differs (i.e. contained hyphens), since the user
		// still types the raw form in `yansu mcp` commands.
		exposedID := internalmcp.ExposedServerID(cfg.ID)
		entry.WriteString(exposedID)
		if cfg.Name != "" && cfg.Name != cfg.ID && cfg.Name != exposedID {
			entry.WriteString(" (")
			entry.WriteString(cfg.Name)
			entry.WriteString(")")
		}
		if exposedID != cfg.ID {
			fmt.Fprintf(&entry, " [CLI id: %s]", cfg.ID)
		}
		switch {
		case isGatewayMcpServer(cfg.ID) && totalTools > 0:
			fmt.Fprintf(&entry, " — %d hidden upstream tools via gateway", totalTools)
		case isGatewayMcpServer(cfg.ID):
			entry.WriteString(" — gateway tools")
		case totalTools == 1:
			entry.WriteString(" — 1 tool")
		case totalTools > 0:
			fmt.Fprintf(&entry, " — %d tools", totalTools)
		default:
			entry.WriteString(" — no tools reported yet")
		}
		entry.WriteString("\n")
		for _, line := range toolLines {
			entry.WriteString(line)
			entry.WriteString("\n")
		}
		chunk := entry.String()
		if written+len(chunk) > maxMcpContextBytes {
			b.WriteString("  - ... (more servers truncated)\n")
			break
		}
		b.WriteString(chunk)
		written += len(chunk)
	}

	b.WriteString("\nManage MCP servers via the yansu CLI:\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp list                                   # installed servers + health\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp discover                               # servers in Claude Desktop / Cursor / etc.\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp registry search <query>                # community catalog (modelcontextprotocol + punkpeye)\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp install --source registry --ref <slug>  # one-click install for `reference` kind only\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp install --source local --ref <id>:<name>\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp install --source manual --id <id> --command <bin> --args \"...\"\n")
	b.WriteString("  ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp enable <id> | disable <id> | restart <id> | uninstall <id>\n\n")
	b.WriteString("To install a third-party MCP from a GitHub URL the user gives you:\n")
	b.WriteString("  1. Read the repo's README. Use `curl -sL https://raw.githubusercontent.com/<owner>/<repo>/HEAD/README.md` —\n")
	b.WriteString("     the `HEAD` ref auto-resolves to the repo's default branch (don't hard-code `main`; some repos like upstash/context7 use `master`).\n")
	b.WriteString("     Fall back to WebFetch on the github.com URL itself if the raw README path 404s.\n")
	b.WriteString("  2. Find the install/run command — typically `npx -y <pkg>`, `uvx <pkg>`, `pipx run <pkg>`, or a docker invocation.\n")
	b.WriteString("  3. Pick a short id (lowercase, dashes, <=31 chars), then run\n")
	b.WriteString("     ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp install --source manual --id <id> --command <bin> --args \"<comma-separated>\"\n")
	b.WriteString("  4. Verify with ")
	b.WriteString(yansuCmd)
	b.WriteString(" mcp list — health should reach \"healthy\" within a few seconds.\n")
	b.WriteString("</mcp-servers>\n\n")
	return b.String()
}

func mcpPromptEnabledConfigs(state *mcpHubState, infos []internalmcp.ServerInfo) []McpServerConfig {
	enabled := make([]McpServerConfig, 0, len(state.Servers))
	seen := map[string]bool{}
	for _, cfg := range state.Servers {
		if _, managed := composioManagedAppForServerID(cfg.ID); managed {
			continue
		}
		if cfg.Enabled {
			enabled = append(enabled, cfg)
			seen[cfg.ID] = true
		}
	}
	return enabled
}

// truncateMcpToolDescription keeps the per-tool blurb short. Some MCP
// servers ship multi-paragraph descriptions; one sentence is enough as a
// hint, and the agent can always call list_tools for the full schema.
func truncateMcpToolDescription(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Take everything up to the first sentence terminator or newline,
	// whichever comes first. Dot-period sentences are the most common
	// shape; we accept "." "!" "?" and a bare newline.
	stop := -1
	for i, r := range s {
		switch r {
		case '\n':
			stop = i
		case '.', '!', '?':
			// Treat the punctuation as part of the sentence.
			stop = i + 1
		}
		if stop != -1 {
			break
		}
	}
	if stop > 0 && stop < len(s) {
		s = strings.TrimSpace(s[:stop])
	}
	const maxLen = 120
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	return s
}

// mcpHubPromptYansuCmd resolves the absolute path to the yansu CLI when
// possible, falling back to the bare "yansu" name. We mirror the same
// trick streaming.go uses for cron-related prompts so the agent can
// invoke the CLI from a Bash subshell whose PATH was scrubbed by shell
// profile loading.
func mcpHubPromptYansuCmd() string {
	if p := installedYansuCLIPath(); p != "" {
		return shellenv.ShellQuote(p)
	}
	return "yansu"
}
