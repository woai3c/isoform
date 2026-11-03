package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Discovery sources are well-known MCP config files that other MCP-aware
// tools maintain (Claude Desktop, Claude Code CLI, Cursor, VS Code,
// Windsurf, Codex). We surface their entries so a user who has already
// configured an MCP server elsewhere can one-click import it into Yansu's
// hub instead of retyping command/args/env by hand.
//
// Each source is a single JSON file. We tolerate a small set of schema
// variants but the dominant shape is `{"mcpServers": {"name": {...}}}`,
// pioneered by Claude Desktop and now copied by most clients.
const (
	mcpDiscoveryMaxBytes = 1 << 20 // 1 MiB; configs are tiny in practice.
)

type mcpDiscoverySourceSpec struct {
	ID   string
	Name string
	// pathFn returns the absolute path to the config file for this source
	// on the current OS, or "" if the source is not applicable here. We
	// use a function (not a static field) so per-OS branches stay in one
	// place and the spec list can be reused across darwin/linux/windows.
	pathFn func(home string) string
}

var mcpDiscoverySources = []mcpDiscoverySourceSpec{
	{
		ID:   "claude-desktop",
		Name: "Claude Desktop",
		pathFn: func(home string) string {
			switch runtime.GOOS {
			case "darwin":
				return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
			case "windows":
				if appData := os.Getenv("APPDATA"); appData != "" {
					return filepath.Join(appData, "Claude", "claude_desktop_config.json")
				}
				return ""
			default:
				return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
			}
		},
	},
	{
		ID:   "claude-code",
		Name: "Claude Code CLI",
		pathFn: func(home string) string {
			// Claude Code stores its top-level config in ~/.claude.json
			// with an `mcpServers` key alongside other fields. We read the
			// same file and only pluck the mcpServers map.
			return filepath.Join(home, ".claude.json")
		},
	},
	{
		ID:   "cursor",
		Name: "Cursor",
		pathFn: func(home string) string {
			return filepath.Join(home, ".cursor", "mcp.json")
		},
	},
	{
		ID:   "windsurf",
		Name: "Windsurf",
		pathFn: func(home string) string {
			return filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
		},
	},
	{
		ID:   "codex",
		Name: "Codex",
		pathFn: func(home string) string {
			return filepath.Join(home, ".codex", "mcp.json")
		},
	},
	{
		ID:   "vscode",
		Name: "VS Code",
		pathFn: func(home string) string {
			return filepath.Join(home, ".vscode", "mcp.json")
		},
	},
}

// DiscoveredMcpServer is one importable entry surfaced by the local
// discovery walker. It is intentionally a flat shape (no nested
// transport struct) so the frontend can render a list row directly.
type DiscoveredMcpServer struct {
	ID          string            `json:"id"`          // hub-unique discovery id, stable across runs
	SourceID    string            `json:"source_id"`   // mcpDiscoverySources[*].ID
	SourceName  string            `json:"source_name"` // human-readable
	SourcePath  string            `json:"source_path"` // file we found it in
	Name        string            `json:"name"`        // server key from the source config
	Transport   string            `json:"transport"`   // mcpTransport* constant
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Installed   bool              `json:"installed"`              // already in our hub
	InstalledID string            `json:"installed_id,omitempty"` // matching server id when Installed
}

// DiscoveredMcpSource lets the frontend show "we looked here, found N
// servers" even when a source has zero entries (or doesn't exist).
type DiscoveredMcpSource struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Found bool   `json:"found"` // file exists and parsed cleanly
	Count int    `json:"count"`
	Error string `json:"error,omitempty"` // parse error if Found=false but file existed
}

// DiscoveryReport bundles the per-source summary with the flat server
// list, so the UI can render a single roundtrip.
type DiscoveryReport struct {
	Sources []DiscoveredMcpSource `json:"sources"`
	Servers []DiscoveredMcpServer `json:"servers"`
}

// rawMcpConfigFile mirrors the union of shapes we accept. Most clients
// follow Claude Desktop's `mcpServers` key; a few use `servers` or a
// top-level map. We try them in that order.
type rawMcpConfigFile struct {
	McpServers map[string]rawMcpServerEntry `json:"mcpServers"`
	Servers    map[string]rawMcpServerEntry `json:"servers"`
}

type rawMcpServerEntry struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Type      string            `json:"type,omitempty"`      // "stdio" | "sse" | "http"
	Transport any               `json:"transport,omitempty"` // alt: nested {"type":"sse","url":"..."}
	Disabled  bool              `json:"disabled,omitempty"`
}

// DiscoverLocalMcpServers walks the configured discovery sources and
// returns every MCP server entry we can import. SSE/HTTP servers are
// included alongside stdio.
func (a *App) DiscoverLocalMcpServers() (DiscoveryReport, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return DiscoveryReport{}, fmt.Errorf("cannot determine home directory: %w", err)
	}

	installedByImportID := map[string]McpServerConfig{}
	if state, err := loadMcpHubState(); err == nil {
		for _, cfg := range state.Servers {
			if cfg.Source == mcpSourceLocal && cfg.SourceURL != "" {
				installedByImportID[cfg.SourceURL] = cfg
			}
		}
	}

	report := DiscoveryReport{
		Sources: make([]DiscoveredMcpSource, 0, len(mcpDiscoverySources)),
		Servers: []DiscoveredMcpServer{},
	}

	for _, spec := range mcpDiscoverySources {
		path := spec.pathFn(home)
		summary := DiscoveredMcpSource{ID: spec.ID, Name: spec.Name, Path: path}
		if path == "" {
			report.Sources = append(report.Sources, summary)
			continue
		}
		entries, parseErr, exists := readMcpDiscoveryFile(path)
		if !exists {
			report.Sources = append(report.Sources, summary)
			continue
		}
		if parseErr != nil {
			summary.Error = parseErr.Error()
			report.Sources = append(report.Sources, summary)
			continue
		}
		summary.Found = true
		for name, raw := range entries {
			discovered, ok := buildDiscoveredServer(spec, path, name, raw)
			if !ok {
				continue
			}
			// We match installed-vs-discovered by the canonical SourceURL
			// stamped at import time: "mcp-discovery://<source>/<name>".
			// The discovery row id (sha1 of source+name+path) is unstable
			// across path moves, so it would falsely flip a previously-
			// imported server back to "not installed" if the user moves
			// their config file. Matching on the explicit URL avoids that.
			if cfg, exists := installedByImportID[mcpDiscoverySourceURL(spec.ID, name)]; exists {
				discovered.Installed = true
				discovered.InstalledID = cfg.ID
			}
			report.Servers = append(report.Servers, discovered)
			summary.Count++
		}
		report.Sources = append(report.Sources, summary)
	}

	sort.Slice(report.Servers, func(i, j int) bool {
		if report.Servers[i].SourceName != report.Servers[j].SourceName {
			return report.Servers[i].SourceName < report.Servers[j].SourceName
		}
		return strings.ToLower(report.Servers[i].Name) < strings.ToLower(report.Servers[j].Name)
	})
	return report, nil
}

// ImportLocalMcpServer copies a single discovered entry into the hub
// state. The freshly-built server is enabled and (when AutoStart is set)
// spawned via the same code path as a manual install.
func (a *App) ImportLocalMcpServer(sourceID string, name string, autoStart bool) (McpServerView, error) {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(name) == "" {
		return McpServerView{}, fmt.Errorf("source and name are required")
	}
	report, err := a.DiscoverLocalMcpServers()
	if err != nil {
		return McpServerView{}, err
	}
	var found *DiscoveredMcpServer
	for i := range report.Servers {
		if report.Servers[i].SourceID == sourceID && report.Servers[i].Name == name {
			found = &report.Servers[i]
			break
		}
	}
	if found == nil {
		return McpServerView{}, fmt.Errorf("discovered server %s/%s not found", sourceID, name)
	}
	if found.Installed {
		return McpServerView{}, fmt.Errorf("%s is already installed (id=%s)", name, found.InstalledID)
	}

	id, err := mcpIDFromDiscovery(found.SourceID, found.Name)
	if err != nil {
		return McpServerView{}, err
	}

	cfg := McpServerConfig{
		ID:          id,
		Name:        found.Name,
		Description: fmt.Sprintf("Imported from %s (%s)", found.SourceName, found.SourcePath),
		Source:      mcpSourceLocal,
		SourceURL:   mcpDiscoverySourceURL(found.SourceID, found.Name),
		Transport:   found.Transport,
		Command:     found.Command,
		Args:        append([]string(nil), found.Args...),
		Env:         copyStringMap(found.Env),
		URL:         found.URL,
		AutoStart:   autoStart,
	}
	if err := validateMcpServerConfig(cfg); err != nil {
		return McpServerView{}, err
	}

	release := acquireMcpServerLock(cfg.ID)
	defer release()

	now := time.Now().UnixMilli()
	cfg.InstalledAt = now
	cfg.UpdatedAt = now
	cfg.Enabled = true

	if err := withMcpHubState(func(s *mcpHubState) error {
		if _, exists := s.Servers[cfg.ID]; exists {
			return fmt.Errorf("mcp server %q already exists; rename it via Add Manually or uninstall the existing one first", cfg.ID)
		}
		s.Servers[cfg.ID] = cfg
		return nil
	}); err != nil {
		return McpServerView{}, err
	}

	if cfg.AutoStart {
		if err := a.spawnAndRegisterMcp(cfg); err != nil {
			return a.viewForServer(cfg), fmt.Errorf("imported but failed to start: %w", err)
		}
	}
	return a.viewForServer(cfg), nil
}

// readMcpDiscoveryFile loads a single config file and returns its
// `mcpServers` (or `servers`) map. Returns (entries, err, exists) where
// exists=false means the file is simply absent (not an error worth
// surfacing).
func readMcpDiscoveryFile(path string) (map[string]rawMcpServerEntry, error, bool) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil, false
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err), true
	}
	if info.Size() > mcpDiscoveryMaxBytes {
		return nil, fmt.Errorf("config file is larger than %d bytes", mcpDiscoveryMaxBytes), true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err), true
	}
	var raw rawMcpConfigFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err), true
	}
	entries := raw.McpServers
	if len(entries) == 0 {
		entries = raw.Servers
	}
	if entries == nil {
		entries = map[string]rawMcpServerEntry{}
	}
	return entries, nil, true
}

// buildDiscoveredServer normalises a raw entry into our flat shape. The
// boolean is false when the entry can't sensibly be imported (e.g. no
// command and no url) — we silently drop those rather than poison the
// list with broken rows.
func buildDiscoveredServer(spec mcpDiscoverySourceSpec, path string, name string, raw rawMcpServerEntry) (DiscoveredMcpServer, bool) {
	transport := strings.ToLower(strings.TrimSpace(raw.Type))
	url := strings.TrimSpace(raw.URL)
	if transport == "" {
		// Some clients nest transport: { "transport": {"type":"sse","url":"..."} }.
		if obj, ok := raw.Transport.(map[string]any); ok {
			if t, ok := obj["type"].(string); ok {
				transport = strings.ToLower(strings.TrimSpace(t))
			}
			if u, ok := obj["url"].(string); ok && url == "" {
				url = strings.TrimSpace(u)
			}
		} else if s, ok := raw.Transport.(string); ok {
			transport = strings.ToLower(strings.TrimSpace(s))
		}
	}
	if transport == "" {
		if url != "" {
			transport = mcpTransportSSE
		} else {
			transport = mcpTransportStdio
		}
	}
	switch transport {
	case mcpTransportStdio:
		if strings.TrimSpace(raw.Command) == "" {
			return DiscoveredMcpServer{}, false
		}
	default:
		// SSE/HTTP and any unknown transport are reserved for P2. Showing
		// them in the discovery list would invite users to click Import,
		// only to hit a "transport not yet supported" error from
		// validateMcpServerConfig. Drop them silently here so the list
		// only contains rows that can actually be installed today.
		return DiscoveredMcpServer{}, false
	}

	id := discoveredMcpServerID(spec.ID, name, path)
	return DiscoveredMcpServer{
		ID:         id,
		SourceID:   spec.ID,
		SourceName: spec.Name,
		SourcePath: path,
		Name:       name,
		Transport:  transport,
		Command:    strings.TrimSpace(raw.Command),
		Args:       append([]string(nil), raw.Args...),
		Env:        copyStringMap(raw.Env),
		URL:        url,
	}, true
}

// discoveredMcpServerID is stable across discovery runs so the frontend
// can keep a row's "Import" button disabled after install without doing
// a second roundtrip.
func discoveredMcpServerID(sourceID string, name string, path string) string {
	sum := sha1.Sum([]byte(sourceID + "|" + name + "|" + path))
	return sourceID + ":" + sanitiseMcpDiscoveryName(name) + ":" + hex.EncodeToString(sum[:4])
}

// mcpDiscoverySourceURL is the canonical "where did this come from"
// pointer we stamp on imported configs. Recording it lets future
// discovery runs match installed servers back to their source row even
// if the user later renames the server in our hub.
func mcpDiscoverySourceURL(sourceID string, name string) string {
	return "mcp-discovery://" + sourceID + "/" + name
}

// mcpIDFromDiscovery derives an mcpServerIDPattern-compatible id from a
// discovered name. Most config keys (e.g. "filesystem", "github") fit
// the pattern as-is; we sanitise the rest and append a short hash so two
// keys that collapse to the same sanitised form (e.g. "my_server" and
// "my-server") still get distinct ids.
func mcpIDFromDiscovery(sourceID string, name string) (string, error) {
	sanitised := sanitiseMcpDiscoveryName(name)
	if sanitised == "" {
		return "", fmt.Errorf("cannot derive id from name %q", name)
	}
	candidate := sanitised
	if mcpServerIDPattern.MatchString(candidate) {
		return candidate, nil
	}
	// Append a short hash and re-truncate. The pattern caps total length
	// at 31, so reserve 5 chars for the suffix.
	sum := sha1.Sum([]byte(sourceID + "|" + name))
	hash := hex.EncodeToString(sum[:2])
	if len(sanitised) > 26 {
		sanitised = sanitised[:26]
	}
	candidate = strings.TrimRight(sanitised, "-") + "-" + hash
	if !mcpServerIDPattern.MatchString(candidate) {
		// Last resort: prefix with the source so we always start with a
		// legal first character.
		candidate = sourceID + "-" + hash
	}
	if !mcpServerIDPattern.MatchString(candidate) {
		return "", fmt.Errorf("cannot derive valid id from name %q", name)
	}
	return candidate, nil
}

// sanitiseMcpDiscoveryName lowercases, swaps disallowed characters for
// hyphens, and trims leading/trailing hyphens. Empty result is possible
// for pathological inputs (e.g. "___") and callers must handle it.
func sanitiseMcpDiscoveryName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if len(out) > 31 {
		out = strings.TrimRight(out[:31], "-")
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
