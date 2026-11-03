package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/skratchdot/open-golang/open"

	internalmcp "wails-gui/internal/mcp"
)

// McpServerView is the user-facing snapshot returned by every list /
// get / mutate RPC. It folds in runtime fields (health, last error,
// tool count) that aren't persisted in state.json so the frontend can
// render a row without making a second roundtrip per server.
type McpServerView struct {
	McpServerConfig
	Health      string           `json:"health"` // "unknown" | "healthy" | "unhealthy"
	LastError   string           `json:"last_error,omitempty"`
	StartedAt   int64            `json:"started_at,omitempty"` // unix milli; zero if not running
	Tools       []McpToolSummary `json:"tools"`
	OAuthStatus string           `json:"oauth_status,omitempty"` // authorization_required | authorized | expired | token_error
	// Trusted reflects the persisted "Always allow this server" flag.
	// When true the agent skips per-call permission prompts for tools
	// exposed by this server. Toggled via SetMcpServerTrusted (or by
	// the user picking "Always allow this server" inline on a prompt).
	Trusted bool `json:"trusted"`
}

// McpToolSummary is the minimum the UI needs to render a tool row
// inside an installed-server card. Inputs schemas can be very large,
// so they are intentionally omitted here.
type McpToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ManualServerInput is the validated payload AddMcpServerManual /
// UpdateMcpServerConfig accept from the frontend. It deliberately
// does NOT include timestamps or runtime-derived fields — those are
// owned by the backend.
type ManualServerInput struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	WorkDir     string            `json:"work_dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	SecretEnv   []string          `json:"secret_env,omitempty"`
	URL         string            `json:"url,omitempty"`
	AuthMode    string            `json:"auth_mode,omitempty"`
	OAuth       McpOAuthConfig    `json:"oauth,omitempty"`
	AutoStart   bool              `json:"auto_start"`
}

func rejectRetiredManagedMcp(id string) error {
	id = strings.TrimSpace(id)
	if _, managed := composioManagedAppForServerID(id); managed {
		return fmt.Errorf("mcp server %q is retired; first-party app access is computer_use_only", id)
	}
	return nil
}

// ListMcpServers returns every persisted MCP server, with runtime
// state attached. Result is sorted by ID for stable rendering.
func (a *App) ListMcpServers() ([]McpServerView, error) {
	state, err := loadMcpHubState()
	if err != nil {
		return nil, err
	}
	views := make([]McpServerView, 0, len(state.Servers))
	seen := map[string]bool{}
	for _, cfg := range state.Servers {
		if _, managed := composioManagedAppForServerID(cfg.ID); managed {
			continue
		}
		views = append(views, a.viewForServer(cfg))
		seen[cfg.ID] = true
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views, nil
}

// GetMcpServer returns a single server's full view. Errors with
// "not found" if the id is unknown.
func (a *App) GetMcpServer(id string) (McpServerView, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return McpServerView{}, err
	}
	state, err := loadMcpHubState()
	if err != nil {
		return McpServerView{}, err
	}
	cfg, ok := state.Servers[id]
	if !ok {
		return McpServerView{}, fmt.Errorf("mcp server %q not found", id)
	}
	return a.viewForServer(cfg), nil
}

// AddMcpServerManual persists a brand-new manual server and, when
// AutoStart=true, immediately spawns it. Returns the freshly-built
// view including any spawn errors as LastError.
func (a *App) AddMcpServerManual(input ManualServerInput) (McpServerView, error) {
	cfg, err := buildConfigFromInput(input, mcpSourceManual)
	if err != nil {
		return McpServerView{}, err
	}
	if err := rejectRetiredManagedMcp(cfg.ID); err != nil {
		return McpServerView{}, err
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
			return fmt.Errorf("mcp server %q already exists; pick a different id or update it", cfg.ID)
		}
		s.Servers[cfg.ID] = cfg
		return nil
	}); err != nil {
		return McpServerView{}, err
	}

	if cfg.AutoStart {
		if cfg.AuthMode == mcpAuthModeOAuth && !mcpOAuthReadyForBackgroundStart(cfg.ID) {
			return a.viewForServer(cfg), nil
		}
		if err := a.spawnAndRegisterMcp(cfg); err != nil {
			// Persistence already succeeded; we surface the spawn
			// failure to the frontend so the user sees a toast instead
			// of a silently-broken row. The view still renders, but
			// LastError will be populated by viewForServer once the
			// supervisor records it (or empty if Spawn failed before
			// it could even register a managedClient — caller's err
			// is the source of truth in that case).
			return a.viewForServer(cfg), fmt.Errorf("server saved but failed to start: %w", err)
		}
	}
	return a.viewForServer(cfg), nil
}

// UpdateMcpServerConfig overwrites the persisted config for an
// existing server. If the server is currently running, it is
// restarted with the new spec so the change actually takes effect.
func (a *App) UpdateMcpServerConfig(id string, input ManualServerInput) (McpServerView, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return McpServerView{}, err
	}
	if input.ID == "" {
		input.ID = id
	}
	if input.ID != id {
		return McpServerView{}, fmt.Errorf("cannot rename server %q via UpdateMcpServerConfig", id)
	}
	cfg, err := buildConfigFromInput(input, "")
	if err != nil {
		return McpServerView{}, err
	}
	if err := validateMcpServerConfig(cfg); err != nil {
		return McpServerView{}, err
	}

	release := acquireMcpServerLock(id)
	defer release()

	var (
		updated    McpServerConfig
		wasRunning bool
	)
	if err := withMcpHubState(func(s *mcpHubState) error {
		existing, ok := s.Servers[id]
		if !ok {
			return fmt.Errorf("mcp server %q not found", id)
		}
		// Preserve runtime + provenance fields the user can't change here.
		cfg.InstalledAt = existing.InstalledAt
		cfg.Source = existing.Source
		cfg.SourceURL = existing.SourceURL
		cfg.Enabled = existing.Enabled
		cfg.UpdatedAt = time.Now().UnixMilli()
		s.Servers[id] = cfg
		updated = cfg
		wasRunning = existing.Enabled
		return nil
	}); err != nil {
		return McpServerView{}, err
	}

	if wasRunning && updated.Enabled {
		if err := a.restartMcp(updated); err != nil {
			// New config persisted, but the restart with the new spec
			// failed. Frontend should toast this so the user knows
			// "save succeeded, but the running instance is now stopped
			// and refusing to come back".
			return a.viewForServer(updated), fmt.Errorf("config saved but restart failed: %w", err)
		}
	}
	return a.viewForServer(updated), nil
}

// SetMcpServerEnabled flips the enabled flag and applies the
// corresponding runtime change: enabling a stopped server spawns it,
// disabling a running one tears it down + unregisters its tools.
func (a *App) SetMcpServerEnabled(id string, enabled bool) (McpServerView, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return McpServerView{}, err
	}
	release := acquireMcpServerLock(id)
	defer release()

	var updated McpServerConfig
	if err := withMcpHubState(func(s *mcpHubState) error {
		existing, ok := s.Servers[id]
		if !ok {
			return fmt.Errorf("mcp server %q not found", id)
		}
		existing.Enabled = enabled
		existing.UpdatedAt = time.Now().UnixMilli()
		s.Servers[id] = existing
		updated = existing
		return nil
	}); err != nil {
		return McpServerView{}, err
	}

	if enabled {
		if updated.AuthMode == mcpAuthModeOAuth && !mcpOAuthReadyForBackgroundStart(updated.ID) {
			return a.viewForServer(updated), nil
		}
		if err := a.spawnAndRegisterMcp(updated); err != nil {
			return a.viewForServer(updated), err
		}
	} else {
		a.unregisterAndStopMcp(id)
	}
	return a.viewForServer(updated), nil
}

// SetMcpServerTrusted toggles the persistent "Always allow this
// server" flag. Trusted servers skip the per-call permission prompt
// in the agent loop, both for already-registered adapters (refreshed
// in place) and for future Register/Refresh cycles (seeded via the
// bridge's trust lookup).
//
// The id must reference a known persisted server. Retired managed
// Composio ids are rejected even if stale state remains on disk.
func (a *App) SetMcpServerTrusted(id string, trusted bool) (McpServerView, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return McpServerView{}, err
	}
	release := acquireMcpServerLock(id)
	defer release()

	state, err := loadMcpHubState()
	if err != nil {
		return McpServerView{}, err
	}
	cfg, ok := state.Servers[id]
	if !ok {
		return McpServerView{}, fmt.Errorf("mcp server %q not found", id)
	}
	if err := setMcpServerTrusted(id, trusted); err != nil {
		return McpServerView{}, err
	}
	a.mu.Lock()
	rt := a.mcpRuntime
	var bridge *internalmcp.Bridge
	if rt != nil {
		bridge = rt.bridge
	}
	a.mu.Unlock()
	if bridge != nil {
		bridge.SetTrustedAndRefresh(id, trusted)
	}
	return a.viewForServer(cfg), nil
}

// UninstallMcpServer stops the server (if running), tears down its
// state.json entry, and removes any persisted scratch directory under
// mcp-hub/servers/<id>. Safe to call on an already-stopped server.
func (a *App) UninstallMcpServer(id string) error {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return err
	}
	release := acquireMcpServerLock(id)
	defer release()

	a.unregisterAndStopMcp(id)

	if err := withMcpHubState(func(s *mcpHubState) error {
		if _, ok := s.Servers[id]; !ok {
			return fmt.Errorf("mcp server %q not found", id)
		}
		delete(s.Servers, id)
		return nil
	}); err != nil {
		return err
	}

	// Clean up trust so a future server with the same id doesn't
	// silently inherit approval from a previous uninstall.
	_ = setMcpServerTrusted(id, false)

	if dir, err := mcpHubServerDir(id); err == nil {
		_ = os.RemoveAll(dir)
	}
	return nil
}

// RestartMcpServer tears down and re-spawns the server. If it wasn't
// running, this is equivalent to "start".
func (a *App) RestartMcpServer(id string) (McpServerView, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return McpServerView{}, err
	}
	release := acquireMcpServerLock(id)
	defer release()

	state, err := loadMcpHubState()
	if err != nil {
		return McpServerView{}, err
	}
	cfg, ok := state.Servers[id]
	if !ok {
		return McpServerView{}, fmt.Errorf("mcp server %q not found", id)
	}
	if !cfg.Enabled {
		return McpServerView{}, fmt.Errorf("mcp server %q is disabled; enable it before restarting", id)
	}
	if err := a.restartMcp(cfg); err != nil {
		return a.viewForServer(cfg), err
	}
	return a.viewForServer(cfg), nil
}

// StopMcpServer tears down the running subprocess without changing
// the persisted Enabled flag. The next AutoStart cycle (or manual
// Restart) will bring it back. Useful when a server is misbehaving
// and the user wants to free its resources without forgetting the
// configuration.
func (a *App) StopMcpServer(id string) error {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return err
	}
	release := acquireMcpServerLock(id)
	defer release()
	a.unregisterAndStopMcp(id)
	return nil
}

// GetMcpServerHealth returns the current health string for id. Errors
// when the server isn't currently running (the persisted state may
// still exist; the frontend uses this to detect "configured but not
// alive").
func (a *App) GetMcpServerHealth(id string) (string, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return "", err
	}
	rt := a.runtimeRef()
	if rt == nil {
		return "", fmt.Errorf("mcp runtime not initialised")
	}
	h, err := rt.manager.Health(id)
	if err != nil {
		return "", err
	}
	return h.String(), nil
}

// ListMcpServerTools returns the cached tool list for id. The list
// reflects whatever the supervisor saw at the last initialise/refresh
// — RestartMcpServer or a future Refresh RPC will repopulate it.
func (a *App) ListMcpServerTools(id string) ([]McpToolSummary, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return nil, err
	}
	rt := a.runtimeRef()
	if rt == nil {
		return nil, fmt.Errorf("mcp runtime not initialised")
	}
	tools, err := rt.manager.Tools(id)
	if err != nil {
		return nil, err
	}
	out := make([]McpToolSummary, 0, len(tools))
	for _, t := range tools {
		out = append(out, McpToolSummary{Name: t.Name, Description: t.Description})
	}
	return out, nil
}

// TailMcpServerLog returns the most recent up-to-n stderr lines for
// id. n<=0 returns whatever is currently in the ring buffer.
func (a *App) TailMcpServerLog(id string, n int) ([]string, error) {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return nil, err
	}
	rt := a.runtimeRef()
	if rt == nil {
		return nil, fmt.Errorf("mcp runtime not initialised")
	}
	return rt.manager.Logs(id, n)
}

// OpenMcpServerFolder reveals the per-server scratch directory in the
// OS file browser. The directory is created on demand so the user
// always sees something even on a stock install.
func (a *App) OpenMcpServerFolder(id string) error {
	if err := rejectRetiredManagedMcp(id); err != nil {
		return err
	}
	dir, err := mcpHubServerDir(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create server folder: %w", err)
	}
	return open.Start(dir)
}

// viewForServer composes a McpServerView from the persisted config
// plus whatever the supervisor knows about it right now.
func (a *App) viewForServer(cfg McpServerConfig) McpServerView {
	view := McpServerView{
		McpServerConfig: cfg,
		Health:          internalmcp.HealthUnknown.String(),
		Tools:           []McpToolSummary{},
		Trusted:         isMcpServerTrusted(cfg.ID),
	}
	if cfg.AuthMode == mcpAuthModeOAuth {
		view.OAuthStatus = mcpOAuthStatus(cfg.ID)
	}
	rt := a.runtimeRef()
	if rt == nil {
		return view
	}
	if h, err := rt.manager.Health(cfg.ID); err == nil {
		view.Health = h.String()
	}
	if tools, err := rt.manager.Tools(cfg.ID); err == nil {
		out := make([]McpToolSummary, 0, len(tools))
		for _, t := range tools {
			out = append(out, McpToolSummary{Name: t.Name, Description: t.Description})
		}
		view.Tools = out
	}
	for _, info := range rt.manager.Info() {
		if info.ID != cfg.ID {
			continue
		}
		if info.LastError != "" {
			view.LastError = info.LastError
		}
		if !info.StartedAt.IsZero() {
			view.StartedAt = info.StartedAt.UnixMilli()
		}
	}
	return view
}

func (a *App) runtimeRef() *mcpRuntime {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mcpRuntime
}

// spawnAndRegisterMcp is the canonical "start a server" path: it
// spawns the subprocess and (if the bridge is wired) registers the
// tools into the agent registry. Errors are returned to the RPC
// caller; supervisor-level state mutations are committed regardless
// so the next Health/Logs query reflects reality.
func (a *App) spawnAndRegisterMcp(cfg McpServerConfig) error {
	rt := a.runtimeRef()
	if rt == nil {
		return fmt.Errorf("mcp runtime not initialised")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spec, err := a.mcpSpecFromConfig(ctx, cfg)
	if err != nil {
		return err
	}
	if err := rt.manager.Spawn(ctx, spec); err != nil {
		return err
	}
	a.mu.Lock()
	bridge := rt.bridge
	a.mu.Unlock()
	if bridge != nil {
		configureManagedMcpExposure(bridge)
		if err := bridge.Register(cfg.ID); err != nil {
			return err
		}
	}
	return nil
}

// unregisterAndStopMcp is the inverse: pull the tools out of the
// agent registry first (so the model stops dispatching to a soon-to-
// die server), then tear down the subprocess. Both sides are
// best-effort; we never propagate errors because the user has already
// committed to "stop" by the time they hit the button.
func (a *App) unregisterAndStopMcp(id string) {
	rt := a.runtimeRef()
	if rt == nil {
		return
	}
	a.mu.Lock()
	bridge := rt.bridge
	a.mu.Unlock()
	if bridge != nil {
		_ = bridge.Unregister(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = rt.manager.Shutdown(ctx, id)
}

func (a *App) restartMcpSpec(ctx context.Context, id string, spec internalmcp.Spec) error {
	rt := a.runtimeRef()
	if rt == nil {
		return fmt.Errorf("mcp runtime not initialised")
	}
	a.mu.Lock()
	bridge := rt.bridge
	a.mu.Unlock()
	if bridge != nil {
		if err := bridge.Unregister(id); err != nil {
			log.Printf("[mcp] unregister before restart %q: %v", id, err)
		}
	}
	if err := rt.manager.Restart(ctx, spec); err != nil {
		return err
	}
	if bridge != nil {
		configureManagedMcpExposure(bridge)
		if err := bridge.Register(id); err != nil {
			return err
		}
	}
	return nil
}

// restartMcp stops then starts the server in one shot. It exists as
// a separate helper because Restart's natural shape (tear-down + spawn)
// shows up both in RestartMcpServer and inside UpdateMcpServerConfig.
func (a *App) restartMcp(cfg McpServerConfig) error {
	a.unregisterAndStopMcp(cfg.ID)
	return a.spawnAndRegisterMcp(cfg)
}

// buildConfigFromInput sanitises the frontend payload into a
// McpServerConfig. It does NOT touch timestamps; callers (Add /
// Update) own those because the rules differ between create and edit.
func buildConfigFromInput(input ManualServerInput, defaultSource string) (McpServerConfig, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return McpServerConfig{}, fmt.Errorf("server id required")
	}
	transport := strings.TrimSpace(input.Transport)
	if transport == "" {
		transport = mcpTransportStdio
	}
	args := append([]string(nil), input.Args...)
	env := map[string]string{}
	for k, v := range input.Env {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		env[k] = v
	}
	secretEnv := append([]string(nil), input.SecretEnv...)
	oauth := normalizeMcpOAuthConfig(input.OAuth)
	cfg := McpServerConfig{
		ID:          id,
		Name:        coalesce(input.Name, id),
		Description: input.Description,
		Source:      defaultSource,
		Transport:   transport,
		Command:     strings.TrimSpace(input.Command),
		Args:        args,
		WorkDir:     strings.TrimSpace(input.WorkDir),
		Env:         env,
		SecretEnv:   secretEnv,
		URL:         strings.TrimSpace(input.URL),
		AuthMode:    normalizeMcpAuthMode(input.AuthMode),
		OAuth:       oauth,
		AutoStart:   input.AutoStart,
	}
	return cfg, nil
}

func coalesce(s, fallback string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return fallback
}

// init verifies our struct tags are JSON-clean at process startup.
// Wails serialises App method results through encoding/json, so a
// silent tag typo would surface as "this field never reaches the
// frontend" — much more annoying to debug than a panic at startup.
func init() {
	probe, _ := json.Marshal(McpServerView{})
	if !strings.Contains(string(probe), "\"id\":") {
		panic("McpServerView JSON tags broken — check struct embedding order")
	}
}
