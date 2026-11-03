package main

import (
	"context"
	"log"
	"maps"
	"sync"
	"time"

	"yansu-agent/agent"

	internalmcp "wails-gui/internal/mcp"
)

// mcpRuntime owns the per-process MCP supervisor and bridge. It is
// kept on App via the mu-guarded mcpRuntime field but isolated to its
// own type so app.go doesn't grow another giant pile of mcp-specific
// methods (skill_hub.go's restraint applies here too).
type mcpRuntime struct {
	manager *internalmcp.Manager
	bridge  *internalmcp.Bridge
	// linkedRunner records which runner the bridge is currently
	// pointing at. When StartAgent rebuilds the runner we throw the
	// old bridge away and rewire to the new registry.
	linkedRunner agent.AgentRunner

	// spawnInflight dedupes background spawns kicked off from
	// syncMcpToolsForPrompt. Without it, every user message would
	// queue a fresh spawn goroutine for the same enabled server, and a
	// genuinely-broken server would make us spawn-and-fail in a loop.
	spawnMu       sync.Mutex
	spawnInflight map[string]struct{}
}

// initMcpRuntime constructs the supervisor and kicks off auto-start
// servers. It is safe to call before the agent runner exists; the
// bridge is wired up later in linkMcpBridge after StartAgent succeeds.
// Startup blocks here only long enough to read state.json — actual
// process spawns happen on a background goroutine so a slow MCP server
// can't delay the main window.
func (a *App) initMcpRuntime() {
	rt := &mcpRuntime{spawnInflight: map[string]struct{}{}}
	// Capture the runtime in a closure so the callback always sees the
	// up-to-date bridge — initMcpRuntime runs before StartAgent, but
	// the callback may fire long after StartAgent has installed one.
	rt.manager = internalmcp.New(internalmcp.WithExitCallback(func(serverID string, reason internalmcp.ExitReason, exitErr error) {
		if reason != internalmcp.ExitUnexpected {
			return
		}
		a.mu.Lock()
		bridge := rt.bridge
		a.mu.Unlock()
		if bridge == nil {
			return
		}
		// Pull the tool group out of the registry so the model stops
		// seeing tools whose subprocess just died. Failures are logged
		// but non-fatal: the next Spawn+Register will overwrite the
		// stale group anyway.
		if err := bridge.Unregister(serverID); err != nil {
			log.Printf("[mcp] unregister after exit %q: %v", serverID, err)
		} else if exitErr != nil {
			log.Printf("[mcp] server %q tools unregistered after unexpected exit: %v", serverID, exitErr)
		}
	}))
	a.mu.Lock()
	a.mcpRuntime = rt
	a.mu.Unlock()

	// initMcpRuntime is called from startup() AFTER the StartAgent
	// goroutine is launched, so on a fast machine StartAgent's
	// linkMcpBridge can fire before mcpRuntime is set and skip with
	// "mcpRuntime not initialised yet" — leaving the bridge nil for
	// the rest of the session. Re-trigger here so whichever side wins
	// the race, the second runner converges. linkMcpBridge is
	// idempotent (registry.ReplaceGroup is the serializer); if
	// StartAgent hasn't finished yet, this call simply logs
	// "runner is nil" and StartAgent's later call links for real.
	a.linkMcpBridge()

	go a.autoStartMcpServers()
}

// autoStartMcpServers reads state.json once and spawns every server
// flagged Enabled+AutoStart. Failures are logged per-server and do
// NOT block other servers — one buggy entry shouldn't kneecap the
// whole MCP hub.
func (a *App) autoStartMcpServers() {
	state, err := loadMcpHubState()
	if err != nil {
		log.Printf("[mcp] load state: %v", err)
		return
	}
	a.mu.Lock()
	rt := a.mcpRuntime
	a.mu.Unlock()
	if rt == nil {
		return
	}

	var wg sync.WaitGroup
	for id, cfg := range state.Servers {
		if !cfg.Enabled || !cfg.AutoStart {
			continue
		}
		if _, managed := composioManagedAppForServerID(id); managed {
			log.Printf("[mcp] auto-start %q: retired managed app transport; using Computer Use", id)
			continue
		}
		if !isSpawnableTransport(cfg) {
			// Older builds let SSE entries land in state.json
			// before the validator started rejecting them. Spawning
			// would fail at Spec.Validate with a misleading
			// error; log the real reason once per launch and move on.
			log.Printf("[mcp] auto-start %q: transport %q not supported; skipping", id, cfg.Transport)
			continue
		}
		wg.Add(1)
		go func(id string, cfg McpServerConfig) {
			defer wg.Done()
			if cfg.AuthMode == mcpAuthModeOAuth && !mcpOAuthReadyForBackgroundStart(cfg.ID) {
				log.Printf("[mcp] auto-start %q: OAuth authorization required; skipping until authorized", id)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), mcpSpawnTimeout)
			defer cancel()
			spec, err := a.mcpSpecFromConfig(ctx, cfg)
			if err != nil {
				log.Printf("[mcp] auto-start %q: %v", id, err)
				return
			}
			if err := rt.manager.Spawn(ctx, spec); err != nil {
				log.Printf("[mcp] auto-start %q: %v", id, err)
				return
			}
			// If the bridge is already wired (rare race: agent runner
			// initialised before MCP supervisor finished), register
			// immediately. Otherwise linkMcpBridge will pick this
			// server up when the runner becomes ready.
			a.mu.Lock()
			bridge := rt.bridge
			a.mu.Unlock()
			if bridge == nil {
				return
			}
			if err := bridge.Register(id); err != nil {
				log.Printf("[mcp] auto-register %q: %v", id, err)
			}
		}(id, cfg)
	}
	wg.Wait()
}

// linkMcpBridge points the bridge at the runner's tool registry and
// registers every currently-running MCP server's tools. Called from
// StartAgent right after the runner is fully initialised.
//
// This function logs at every observable boundary (entry, early
// returns, success) because "bridge is nil at prompt time" is the
// failure mode that masks the entire MCP integration — without these
// logs the only signal is the "prompt sync skipped" line, which says
// what but not why.
func (a *App) linkMcpBridge() {
	a.mu.Lock()
	rt := a.mcpRuntime
	runner := a.runner
	a.mu.Unlock()
	if rt == nil {
		log.Printf("[mcp] linkMcpBridge skipped: mcpRuntime not initialised yet")
		return
	}
	if runner == nil {
		log.Printf("[mcp] linkMcpBridge deferred: agent runner is not ready; StartAgent links the bridge later")
		return
	}

	native, ok := agent.NativeRunnerOf(runner)
	if !ok {
		// Non-native runners don't expose their registry; the MCP
		// integration is currently native-runner-only. Document the
		// limitation rather than failing silently somewhere downstream.
		log.Printf("[mcp] linkMcpBridge: runner %T does not expose Registry(); MCP tools won't be visible to the model", runner)
		return
	}
	bridge := internalmcp.NewBridge(rt.manager, native.Registry())
	configureManagedMcpExposure(bridge)
	// Seed the bridge with the persisted "Always allow this server" set
	// so freshly-built adapters start trusted without waiting for the
	// user to re-confirm. The lookup is consulted on every Register, so
	// later toggles via SetTrusted RPC are picked up automatically when
	// a server is restarted.
	bridge.SetTrustLookup(isMcpServerTrusted)
	// Wire the "Always allow this server" prompt response into the
	// runner: persist trust + flip live adapters so subsequent calls in
	// this and future runs skip the permission prompt entirely.
	native.SetOnPersistentAllow(func(prefix string) {
		id, ok := serverIDFromPromptPrefix(prefix)
		if !ok {
			log.Printf("[mcp] OnPersistentAllow: ignoring unrecognised prefix %q", prefix)
			return
		}
		if err := setMcpServerTrusted(id, true); err != nil {
			log.Printf("[mcp] OnPersistentAllow: persist %q: %v", id, err)
			return
		}
		bridge.SetTrustedAndRefresh(id, true)
		log.Printf("[mcp] OnPersistentAllow: server %q marked trusted", id)
	})

	a.mu.Lock()
	rt.bridge = bridge
	rt.linkedRunner = runner
	a.mu.Unlock()
	log.Printf("[mcp] linkMcpBridge: bridge linked to NativeAgentRunner registry (will register healthy server tools next)")

	// Register every server the supervisor already has running. We use
	// Info() rather than re-reading state.json so a server the user
	// just disabled isn't accidentally re-registered.
	registered := 0
	for _, info := range rt.manager.Info() {
		if info.Health != internalmcp.HealthHealthy {
			continue
		}
		if err := bridge.Register(info.ID); err != nil {
			log.Printf("[mcp] link register %q: %v", info.ID, err)
			continue
		}
		registered++
	}
	log.Printf("[mcp] linkMcpBridge: registered %d healthy server(s) into runner registry", registered)
}

// promptSyncRefreshTimeout caps how long a single per-server
// tools/list refresh may block the prompt path. Refreshes run in
// parallel (see syncMcpToolsForPrompt) so the wall-clock latency
// added to the prompt is bounded by this constant regardless of how
// many servers need a refresh.
const promptSyncRefreshTimeout = 1500 * time.Millisecond

// mcpSpawnTimeout is the outer deadline for a single
// manager.Spawn() call (covers process exec + initialize handshake
// + initial tools/list). It must comfortably exceed the manager's
// internal initTimeout (30s today) — otherwise we'd cancel a server
// that was about to finish its handshake. 90s gives the inner
// initialize 30s + ~60s slack for npx/uvx cold-cache fetches when
// multiple servers race the package manager lock at startup.
const mcpSpawnTimeout = 90 * time.Second

// syncMcpToolsForPrompt keeps the native runner registry aligned with
// the MCP supervisor right before prompt composition. This closes the
// gap where state.json says a server is enabled, but the current Wails
// process either has not spawned it yet or has not registered its tool
// group into the runner registry, causing the model to fall back to
// Bash/CLI diagnostics.
//
// Lifecycle semantics this function enforces:
//   - cfg.Enabled == true means "the user wants this server available"
//     — it's the on/off switch and is honoured by the prompt path.
//   - cfg.AutoStart only controls launch-time eagerness:
//     true  → autoStartMcpServers spawns at app launch (eager);
//     false → spawn lazily here on the first prompt that needs it.
//     Either way, an Enabled server becomes usable without the user
//     having to remember a manual "Restart" — installing it is enough.
//
// Latency contract: this runs on the prompt hot-path, so it must NOT
// block on subprocess spawn or per-server work in serial.
//   - Not-yet-spawned (lazy) servers are kicked off in the background
//     (deduped via rt.spawnInflight) and skipped for the current turn
//     — they'll be available to the next user message.
//   - Already-healthy servers run their refresh+register concurrently;
//     the prompt waits at most promptSyncRefreshTimeout total, not
//     N*timeout, even with many servers needing a refresh.
//   - Unhealthy servers are intentionally NOT auto-restarted here. The
//     manager's P0 design (manager.go:36) is "explicit Restart only";
//     auto-restarting on every prompt would create a retry storm on a
//     genuinely-broken server.
func (a *App) syncMcpToolsForPrompt(rt *mcpRuntime, enabled []McpServerConfig) {
	a.mu.Lock()
	bridge := rt.bridge
	a.mu.Unlock()
	if bridge == nil {
		log.Printf("[mcp] prompt sync skipped: bridge is not linked to the agent registry yet")
		return
	}

	infoByID := make(map[string]internalmcp.ServerInfo)
	for _, info := range rt.manager.Info() {
		infoByID[info.ID] = info
	}

	var wg sync.WaitGroup
	for _, cfg := range enabled {
		info, managed := infoByID[cfg.ID]
		if !isSpawnableTransport(cfg) && !managed {
			// SSE/HTTP entries from old state.json: validator now
			// rejects them, but a persisted one still surfaces here.
			// Runtime-managed HTTP servers (Composio) are allowed past
			// this gate only after the manager already owns them; prompt
			// sync must not try to spawn them from persisted config.
			continue
		}
		if !managed {
			if cfg.AuthMode == mcpAuthModeOAuth && !mcpOAuthReadyForBackgroundStart(cfg.ID) {
				continue
			}
			// Enabled but the supervisor isn't tracking it yet. This
			// covers two cases that the user reasonably expects to
			// "just work":
			//   1. cfg.AutoStart == false (lazy): the server was never
			//      spawned at launch.
			//   2. cfg.AutoStart == true but autoStartMcpServers
			//      hadn't finished by the time the first prompt
			//      arrived (cold launch race).
			// Background spawn is deduped via rt.spawnInflight so a
			// chatty user / broken server can't queue parallel spawns.
			a.kickoffBackgroundMcpSpawn(rt, bridge, cfg)
			continue
		}
		if info.Health != internalmcp.HealthHealthy {
			// See latency contract above: deliberate no-op.
			continue
		}

		wg.Add(1)
		go func(info internalmcp.ServerInfo) {
			defer wg.Done()
			if len(info.Tools) == 0 {
				// Empty tool list on a healthy server can mean
				// tools/list hasn't been re-fetched since reconnect.
				// bridge.Refresh = RefreshTools + Register, so on
				// success the registry is already up-to-date and we
				// can return without a second Register call.
				ctx, cancel := context.WithTimeout(context.Background(), promptSyncRefreshTimeout)
				err := bridge.Refresh(ctx, info.ID)
				cancel()
				if err != nil {
					log.Printf("[mcp] prompt refresh %q: %v", info.ID, err)
				}
				return
			}
			if err := bridge.Register(info.ID); err != nil {
				log.Printf("[mcp] prompt register %q: %v", info.ID, err)
			}
		}(info)
	}
	wg.Wait()
}

// kickoffBackgroundMcpSpawn launches a spawn+register goroutine for a
// server that the supervisor isn't currently tracking. Concurrent
// callers for the same id collapse to a single in-flight goroutine via
// rt.spawnInflight so a chatty user can't queue dozens of spawns for
// the same broken server. The current prompt turn does NOT wait for
// the result — see syncMcpToolsForPrompt's latency contract.
func (a *App) kickoffBackgroundMcpSpawn(rt *mcpRuntime, bridge *internalmcp.Bridge, cfg McpServerConfig) {
	rt.spawnMu.Lock()
	if _, busy := rt.spawnInflight[cfg.ID]; busy {
		rt.spawnMu.Unlock()
		return
	}
	rt.spawnInflight[cfg.ID] = struct{}{}
	rt.spawnMu.Unlock()

	go func() {
		defer func() {
			rt.spawnMu.Lock()
			delete(rt.spawnInflight, cfg.ID)
			rt.spawnMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), mcpSpawnTimeout)
		defer cancel()
		spec, err := a.mcpSpecFromConfig(ctx, cfg)
		if err != nil {
			log.Printf("[mcp] background autostart %q: spec failed: %v", cfg.ID, err)
			return
		}
		if err := rt.manager.Spawn(ctx, spec); err != nil {
			log.Printf("[mcp] background autostart %q: spawn failed: %v", cfg.ID, err)
			return
		}
		if err := bridge.Register(cfg.ID); err != nil {
			log.Printf("[mcp] background autostart %q: register failed: %v", cfg.ID, err)
			return
		}
		tools, _ := rt.manager.Tools(cfg.ID)
		log.Printf("[mcp] background autostart %q: %d tool(s) ready for next turn", cfg.ID, len(tools))
	}()
}

// shutdownMcpRuntime is called from App.shutdown to terminate every
// managed subprocess. It runs with a hard deadline so a hung child
// process can't keep the desktop app alive.
func (a *App) shutdownMcpRuntime() {
	a.mu.Lock()
	rt := a.mcpRuntime
	a.mcpRuntime = nil
	a.mu.Unlock()
	if rt == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rt.manager.ShutdownAll(ctx)
}

// mcpSpecFromConfig translates the persisted McpServerConfig into the
// supervisor's narrow Spec view. SecretEnv-keyed values are NOT yet
// resolved here — that lands in P1 with the keychain integration; for
// P0 the user puts them straight into Env.
func (a *App) mcpSpecFromConfig(ctx context.Context, cfg McpServerConfig) (internalmcp.Spec, error) {
	env := make(map[string]string, len(cfg.Env))
	maps.Copy(env, cfg.Env)
	spec := internalmcp.Spec{
		ID:        cfg.ID,
		Transport: cfg.Transport,
		Command:   cfg.Command,
		Args:      append([]string(nil), cfg.Args...),
		URL:       cfg.URL,
		Env:       env,
		WorkDir:   cfg.WorkDir,
	}
	if cfg.AuthMode == mcpAuthModeOAuth {
		token, err := a.mcpOAuthAccessToken(ctx, cfg)
		if err != nil {
			return internalmcp.Spec{}, err
		}
		spec.Headers = map[string]string{"Authorization": "Bearer " + token}
	}
	return spec, nil
}
