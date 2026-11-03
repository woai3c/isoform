package main

import (
	"context"
	"log"
	"time"
)

// cua_cursor_overlay_config.go owns the user-facing toggle for the
// bundled cua-driver's virtual-cursor lens overlay — the animated pointer
// + click markers shown wherever Yansu acts during computer use. It is
// visual only; the lens never moves the real mouse.
//
// Two layers keep the setting honest:
//   1. Spawn-time argv: cua_driver_daemon.go reads
//      shouldEnableCuaCursorOverlay() when building the daemon command, so
//      every cold start / idle respawn / crash recovery honours the
//      setting (passes `--no-lens` when disabled).
//   2. Runtime hot-apply: SetCuaCursorOverlayEnabled sends the daemon's
//      `lens` command so a toggle takes effect on an already-running
//      daemon without waiting for the next respawn.

// shouldEnableCuaCursorOverlay decides whether the lens overlay should be
// active. Precedence: explicit config value, then the default policy.
//
// Default: enabled. The overlay is the whole point of the feature — it
// lets the user see what Yansu is clicking — so we surface it unless the
// user opts out in Settings.
func shouldEnableCuaCursorOverlay() bool {
	cfg := loadConfig()
	if cfg.CuaCursorOverlayEnabled != nil {
		return *cfg.CuaCursorOverlayEnabled
	}
	return true
}

// GetCuaCursorOverlayEnabled reports the user's effective preference. We
// return the persisted/default policy rather than probing the daemon's
// live lens state: the daemon is spawned lazily and may not be running
// when Settings opens, and the setting (not the transient daemon state)
// is what the toggle should reflect.
func (a *App) GetCuaCursorOverlayEnabled() bool {
	return shouldEnableCuaCursorOverlay()
}

// SetCuaCursorOverlayEnabled persists the choice and, when a daemon is
// already running, hot-applies it via the `lens` command so the change is
// visible immediately. Safe to call from the frontend via the RPC
// allowlist.
func (a *App) SetCuaCursorOverlayEnabled(enabled bool) error {
	if err := updateConfig(func(c *yansuConfig) {
		v := enabled
		c.CuaCursorOverlayEnabled = &v
	}); err != nil {
		return err
	}

	// Hot-apply only when a daemon is already up. Spawning one just to
	// flip the lens would be wasteful — and unnecessary, since the next
	// spawn reads the setting via shouldEnableCuaCursorOverlay() when it
	// builds its argv.
	a.mu.Lock()
	daemon := a.cuaDriver
	a.mu.Unlock()
	if daemon != nil && daemon.ownsRunningDaemon() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := daemon.Call(ctx, "lens", map[string]any{"enabled": enabled}); err != nil {
			// Best-effort: the setting is already persisted, so the next
			// daemon spawn will pick it up regardless.
			log.Printf("[cua-cursor] runtime lens toggle failed (applies on next daemon spawn): %v", err)
		}
	}
	return nil
}
