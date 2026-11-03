package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// mcpHubStateMu serialises load → modify → save sequences against the
// MCP hub state file. Wails dispatches each RPC handler on its own
// goroutine, so concurrent toggles / installs would otherwise race and
// clobber each other's writes via the read-modify-write cycle. Mirrors
// the skill-hub pattern.
var mcpHubStateMu sync.Mutex

// mcpHubSaveTmpSeq disambiguates the temp file used by saveMcpHubState
// across callers; combined with the mutex above, this guarantees every
// successful rename promotes a fully-written file even if a previous
// save aborted.
var mcpHubSaveTmpSeq atomic.Uint64

// mcpServerInstallLocks gives each server id its own mutex so that the
// "spawn → write origin → state mutation" sequence runs serially per
// server. Different ids still install/start in parallel; locks are
// created lazily and never deleted (the set of server ids is small and
// bounded by what the user actually configures).
var (
	mcpServerInstallLocks   = make(map[string]*sync.Mutex)
	mcpServerInstallLocksMu sync.Mutex
)

func acquireMcpServerLock(id string) func() {
	mcpServerInstallLocksMu.Lock()
	mu, ok := mcpServerInstallLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		mcpServerInstallLocks[id] = mu
	}
	mcpServerInstallLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

const (
	mcpHubStateVersion = 1

	mcpTransportStdio = "stdio"
	mcpTransportSSE   = "sse"  // rejected by validateMcpServerConfig — reserved for P2
	mcpTransportHTTP  = "http" // streamable HTTP MCP

	mcpAuthModeNone  = "none"
	mcpAuthModeOAuth = "oauth"

	mcpSourceManual  = "manual"
	mcpSourceGitHub  = "github" // stamped by registry installs
	mcpSourceLocal   = "local"  // stamped by ImportLocalMcpServer
	mcpSourceManaged = "managed"
)

// mcpServerIDPattern restricts server ids to characters that compose a
// safe filesystem path AND a safe tools.Registry group prefix AND a
// safe Anthropic tool-name segment.
//
//   - No underscores: the bridge prefix "mcp__<id>" must never contain
//     "__" inside <id>, otherwise group "mcp__a" would shadow group
//     "mcp__a__b" when one is cleared. See registry.ReplaceGroup.
//   - No dots: Anthropic's tool name regex is ^[a-zA-Z0-9_-]{1,128}$,
//     and a dot inside <id> would propagate into the exposed tool name
//     ("mcp__server.id__tool") and trip API validation.
//   - Length <= 31: the full exposed tool name is
//     "mcp__<id>__<tool>" (5 + len(id) + 2 + len(tool)). With id capped
//     at 31 chars we leave 90 chars for the tool segment after
//     sanitization, comfortably under the 128 limit.
var mcpServerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,30}$`)

// McpServerConfig is the serialisable record for one MCP server. It is
// stored verbatim in state.json (modulo SecretEnv values which live in
// the OS keychain — see secrets_*.go in P1).
type McpServerConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // mcpSource* constant
	SourceURL   string `json:"source_url,omitempty"`

	Transport string            `json:"transport"` // mcpTransport* constant
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	WorkDir   string            `json:"work_dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	SecretEnv []string          `json:"secret_env,omitempty"` // env keys whose values are read from keychain
	URL       string            `json:"url,omitempty"`        // sse/http endpoint

	AuthMode string         `json:"auth_mode,omitempty"` // mcpAuthMode* constant
	OAuth    McpOAuthConfig `json:"oauth,omitempty"`

	Enabled   bool `json:"enabled"`
	AutoStart bool `json:"auto_start"`

	InstalledAt int64 `json:"installed_at"`
	UpdatedAt   int64 `json:"updated_at"`
}

// McpOAuthConfig contains non-token OAuth settings for a remote HTTP MCP
// server. Access and refresh tokens are intentionally stored outside
// state.json by mcp_oauth.go.
type McpOAuthConfig struct {
	AuthURL  string   `json:"auth_url,omitempty"`
	TokenURL string   `json:"token_url,omitempty"`
	ClientID string   `json:"client_id,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	Resource string   `json:"resource,omitempty"`
	Issuer   string   `json:"issuer,omitempty"`
}

type mcpHubState struct {
	Version int                        `json:"version"`
	Servers map[string]McpServerConfig `json:"servers"`
}

// withMcpHubState atomically loads the state, runs fn against it, and
// saves the result back. All mutators in this package go through this
// helper so the state file can never lose updates under concurrency.
func withMcpHubState(fn func(*mcpHubState) error) error {
	mcpHubStateMu.Lock()
	defer mcpHubStateMu.Unlock()
	state, err := loadMcpHubState()
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	return saveMcpHubState(state)
}

func loadMcpHubState() (*mcpHubState, error) {
	path, err := mcpHubStatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &mcpHubState{Version: mcpHubStateVersion, Servers: map[string]McpServerConfig{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read mcp hub state: %w", err)
	}
	var state mcpHubState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse mcp hub state: %w", err)
	}
	if state.Version == 0 {
		state.Version = mcpHubStateVersion
	}
	if state.Servers == nil {
		state.Servers = map[string]McpServerConfig{}
	}
	for id, cfg := range state.Servers {
		state.Servers[id] = normalizeMcpServerConfig(cfg)
	}
	return &state, nil
}

func saveMcpHubState(state *mcpHubState) error {
	if state == nil {
		state = &mcpHubState{}
	}
	state.Version = mcpHubStateVersion
	if state.Servers == nil {
		state.Servers = map[string]McpServerConfig{}
	}

	path, err := mcpHubStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create mcp hub directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp hub state: %w", err)
	}
	data = append(data, '\n')
	// Unique suffix so two callers (or a previous aborted run) can't
	// race on the same .tmp path. The rename below is atomic, so the
	// worst case is a leftover .tmp.<seq> file we sweep on the next save.
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", path, time.Now().UnixNano(), mcpHubSaveTmpSeq.Add(1))
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write mcp hub state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace mcp hub state: %w", err)
	}
	return nil
}

func normalizeMcpServerConfig(cfg McpServerConfig) McpServerConfig {
	if cfg.ID == "" {
		// Defensive: never let an unkeyed entry survive a load. Callers
		// that build configs always set ID, so this only triggers if the
		// state file is hand-edited.
		cfg.ID = "unknown"
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	if cfg.Source == "" {
		cfg.Source = mcpSourceManual
	}
	if cfg.Transport == "" {
		cfg.Transport = mcpTransportStdio
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = mcpAuthModeNone
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	cfg.OAuth = normalizeMcpOAuthConfig(cfg.OAuth)
	return cfg
}

func composioManagedMcpConfig() McpServerConfig {
	return composioManagedMcpConfigForApp(composioGoogleSuperApp)
}

func composioManagedMcpConfigForApp(app string) McpServerConfig {
	serverID, err := composioManagedServerID(app)
	if err != nil {
		serverID = app
	}
	name := "Composio " + app
	switch app {
	case composioGoogleSuperApp:
		name = "Composio Google Super"
	case composioSlackBotApp:
		name = "Composio Slack"
	case composioNotionApp:
		name = "Composio Notion"
	case composioGitHubApp:
		name = "Composio GitHub"
	case composioLinearApp:
		name = "Composio Linear"
	}
	return McpServerConfig{
		ID:          serverID,
		Name:        name,
		Description: "Managed " + name + " MCP server",
		Source:      mcpSourceManaged,
		Transport:   mcpTransportHTTP,
		Enabled:     true,
		AutoStart:   false,
	}
}

// isSpawnableTransport reports whether a server config can currently
// be handed to the supervisor. Persisted state from older builds may
// still carry SSE entries; centralising the check here keeps the
// spawn decision identical at every entry point (autoStart on launch,
// background kick-off from the prompt path) so a single P2 land later
// flips behaviour everywhere at once.
func isSpawnableTransport(cfg McpServerConfig) bool {
	return cfg.Transport == mcpTransportStdio || cfg.Transport == mcpTransportHTTP
}

// validateMcpServerConfig enforces the minimum invariants needed before a
// config can be persisted. It does NOT spawn or probe the server — that
// happens in the supervisor in commit 3.
func validateMcpServerConfig(cfg McpServerConfig) error {
	if !mcpServerIDPattern.MatchString(cfg.ID) {
		return fmt.Errorf("invalid mcp server id: %q", cfg.ID)
	}
	switch cfg.Transport {
	case mcpTransportStdio:
		if cfg.Command == "" {
			return fmt.Errorf("stdio mcp server %q requires command", cfg.ID)
		}
	case mcpTransportHTTP:
		if cfg.URL == "" {
			return fmt.Errorf("http mcp server %q requires url", cfg.ID)
		}
	case mcpTransportSSE:
		return fmt.Errorf("mcp server %q: transport %q is not yet supported", cfg.ID, cfg.Transport)
	default:
		return fmt.Errorf("mcp server %q: unsupported transport %q", cfg.ID, cfg.Transport)
	}
	switch cfg.AuthMode {
	case "", mcpAuthModeNone:
	case mcpAuthModeOAuth:
		if cfg.Transport != mcpTransportHTTP {
			return fmt.Errorf("mcp server %q: OAuth is only supported for remote HTTP MCP servers", cfg.ID)
		}
		if cfg.OAuth.AuthURL != "" {
			if err := validateMcpOAuthEndpointURL(cfg.OAuth.AuthURL); err != nil {
				return fmt.Errorf("mcp server %q: invalid OAuth authorization endpoint: %w", cfg.ID, err)
			}
		}
		if cfg.OAuth.TokenURL != "" {
			if err := validateMcpOAuthEndpointURL(cfg.OAuth.TokenURL); err != nil {
				return fmt.Errorf("mcp server %q: invalid OAuth token endpoint: %w", cfg.ID, err)
			}
		}
	default:
		return fmt.Errorf("mcp server %q: unsupported auth mode %q", cfg.ID, cfg.AuthMode)
	}
	for _, key := range cfg.SecretEnv {
		if key == "" {
			return fmt.Errorf("mcp server %q: secret_env contains empty key", cfg.ID)
		}
	}
	return nil
}

func mcpHubBaseDir() (string, error) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(dataRoot, "mcp-hub"), nil
}

func mcpHubStatePath() (string, error) {
	base, err := mcpHubBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "state.json"), nil
}

func mcpHubServerDir(id string) (string, error) {
	if !mcpServerIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid mcp server id: %q", id)
	}
	base, err := mcpHubBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "servers", id), nil
}
