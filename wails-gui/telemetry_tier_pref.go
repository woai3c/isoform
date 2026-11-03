package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Telemetry tier is the persisted "Share diagnostic data" choice. The default
// remains detailed for compatibility; off prevents all telemetry network calls
// and enqueueing. Both tiers are still passed to the embedded agent for its
// local structural diagnostics, but the desktop PostHog sink only accepts the
// content-free product events documented in docs/product-telemetry.md.
// Upstream error telemetry contains only the status code, a stable error class
// and body length; upstream error bodies are never sent.
// Legacy "basic" values normalize to the existing detailed default on load.
const (
	TelemetryTierOff      = "off"
	TelemetryTierDetailed = "detailed"

	defaultTelemetryTier = TelemetryTierDetailed

	telemetryTierFile = "telemetry_tier.txt"
)

var (
	// Serialize the save, runtime change and notification as one transition.
	telemetryTierSaveMu sync.Mutex
	telemetryTierMu     sync.Mutex
	telemetryTier       string
)

// telemetryTierPath resolves the on-disk file the tier is persisted
// to. An empty result makes setting changes fail without changing runtime state.
func telemetryTierPath() string {
	dir := configDirOrFallback()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, telemetryTierFile)
}

// loadTelemetryTierFromDisk reads the persisted value at startup so
// the agent runner can ask currentTelemetryTier() before the React
// side has mounted. Falls back to the default when the file is
// missing or contains an unrecognised value.
func loadTelemetryTierFromDisk() {
	path := telemetryTierPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	v := strings.TrimSpace(string(b))
	if !isValidTelemetryTier(v) {
		return
	}
	telemetryTierMu.Lock()
	telemetryTier = v
	telemetryTierMu.Unlock()
}

// GetTelemetryTier is the Wails binding the Privacy settings panel
// reads on mount. Returns the on-disk value (or the default) — the
// React side never sees an empty string so it can render the radio
// group without a hydration flash.
func (a *App) GetTelemetryTier() string {
	return currentTelemetryTier()
}

// SetTelemetryTier is the Wails binding the Privacy settings panel
// calls when the user picks a different tier. Returns an error so the
// frontend Promise rejects on disk-full / permission-denied / invalid-
// tier failures and the toast can fire — silent failure here would
// leave the user thinking they'd switched tiers when the next launch
// would surprise them with the old value.
func (a *App) SetTelemetryTier(tier string) error {
	v := strings.TrimSpace(tier)
	if !isValidTelemetryTier(v) {
		return fmt.Errorf("invalid telemetry tier %q (allowed: off / detailed)", tier)
	}
	telemetryTierSaveMu.Lock()
	defer telemetryTierSaveMu.Unlock()

	path := telemetryTierPath()
	if path == "" {
		return fmt.Errorf("could not resolve user config dir for telemetry tier")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
	}
	// Replace atomically so a failed write cannot truncate a persisted opt-out.
	file, err := os.CreateTemp(filepath.Dir(path), ".telemetry-tier-*")
	if err != nil {
		return fmt.Errorf("persist telemetry tier: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return fmt.Errorf("persist telemetry tier: %w", err)
	}
	if _, err := file.WriteString(v); err != nil {
		file.Close()
		return fmt.Errorf("persist telemetry tier: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("persist telemetry tier: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("persist telemetry tier: %w", err)
	}
	telemetryTierMu.Lock()
	telemetryTier = v
	telemetryTierMu.Unlock()
	a.telemetry.SetSharingEnabled(v != TelemetryTierOff)
	// Notify the window only after the saved choice and Go client agree.
	a.emitBrowserEvent("telemetry:tier-changed", v)
	return nil
}

// currentTelemetryTier is the internal accessor used by the agent
// runner CE collector. Always returns one of the canonical tier
// strings — never empty — so collector callers don't have to
// special-case unset state.
func currentTelemetryTier() string {
	telemetryTierMu.Lock()
	defer telemetryTierMu.Unlock()
	if telemetryTier == "" {
		return defaultTelemetryTier
	}
	return telemetryTier
}

func isValidTelemetryTier(v string) bool {
	switch v {
	case TelemetryTierOff, TelemetryTierDetailed:
		return true
	default:
		return false
	}
}
