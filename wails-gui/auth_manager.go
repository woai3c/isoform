package main

import (
	"log"
	"sync"
)

// AuthManager centralizes credential fallback logic. It provides a single
// RecoverAuth entry point used by both the startup probe and per-request
// error detection, eliminating the race conditions that existed when
// probeAndGreet and handleAuthFallback operated independently.
type AuthManager struct {
	mu         sync.Mutex
	active     bool // sub2api credentials injected and active
	recovered  bool // recovery completed successfully
	recovering bool // recovery in progress
	app        *App
}

func NewAuthManager(app *App) *AuthManager {
	return &AuthManager{app: app}
}

// IsActive returns true when sub2api credentials are active.
func (am *AuthManager) IsActive() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.active
}

// RecoverAuth injects sub2api credentials and restarts the agent.
// Safe to call from multiple goroutines — only the first caller runs
// the recovery; subsequent calls return immediately.
// Returns true if recovery was performed (or already done).
//
// When the user has not opted into the Yansu AI gateway (UseYansuGateway=false),
// sub2api injection is skipped entirely — the user's local credentials are the
// only auth path and they must fix them directly (e.g. 'claude login').
func (am *AuthManager) RecoverAuth() bool {
	am.mu.Lock()
	if am.recovered || am.recovering || am.active {
		am.mu.Unlock()
		return am.recovered || am.active
	}
	am.recovering = true
	am.mu.Unlock()

	defer func() {
		am.mu.Lock()
		am.recovering = false
		am.mu.Unlock()
	}()

	// The Yansu AI Gateway has been retired, so UseYansuGateway is false for
	// everyone and this branch is now the common path. A local credential
	// failure here means the user has not configured their own AI endpoint
	// (or their key is wrong) — the retirement banner already guides them to
	// Settings → AI Provider, so emitting a "run 'claude login' / reconnect"
	// toast would be misleading (there is no gateway to reconnect to, and
	// 'claude login' only applies to the external Claude Code CLI). Stay
	// silent and let the banner own the nudge.
	// The gateway is retired and cannot be enabled, so there is no sub2api
	// fallback. Stay silent and let the banner own the nudge.
	log.Printf("[auth-manager] local credential error detected, Yansu gateway retired -- staying silent (retirement banner guides the user)")
	return false
}

// Reset clears auth state (e.g. on logout or environment switch).
func (am *AuthManager) Reset() {
	if am.app != nil {
		am.app.clearPageSnapshots()
		// Sign-out also ends the warm know-how proposal: the next read re-runs
		// the source and privacy gates. candMu is a leaf lock here.
		am.app.invalidateKnowHowCandidateCache()
	}
	am.mu.Lock()
	am.active = false
	am.recovered = false
	am.recovering = false
	am.mu.Unlock()
	clearAnthropicEnv()
}
