package main

import (
	"log"
	"os"
)

// shouldEnableBrowserBridge decides whether the localhost browser bridge
// (HTTP + WebSocket server on port 34116) should start during App.startup.
//
// Precedence:
//  1. YANSU_BROWSER_BRIDGE env var ("1"/"true"/"on" or "0"/"false"/"off")
//  2. Config file (yansuConfig.BrowserBridgeEnabled)
//  3. Default: enabled when WAILS_DEV is set (i.e. `wails dev`),
//     disabled otherwise (production builds).
func shouldEnableBrowserBridge() bool {
	if v := os.Getenv("YANSU_BROWSER_BRIDGE"); v != "" {
		switch v {
		case "1", "true", "TRUE", "on", "ON", "yes", "YES":
			return true
		case "0", "false", "FALSE", "off", "OFF", "no", "NO":
			return false
		}
	}

	cfg := loadConfig()
	if cfg.BrowserBridgeEnabled != nil {
		return *cfg.BrowserBridgeEnabled
	}

	// Default policy: enabled in dev (WAILS_DEV is set by `wails dev`),
	// disabled in production builds. Users can opt in via env or config.
	return os.Getenv("WAILS_DEV") != ""
}

// GetBrowserBridgeEnabled reports whether the browser bridge is currently
// active. Preferred over reading config because the running state may differ
// from the persisted setting (e.g. when a previous startBrowserServer call
// failed).
func (a *App) GetBrowserBridgeEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.browserServer != nil
}

// SetBrowserBridgeEnabled toggles the browser bridge at runtime, persisting
// the choice to config so it survives restarts. Starts/stops the HTTP server
// synchronously; safe to call from the frontend via the RPC allowlist.
func (a *App) SetBrowserBridgeEnabled(enabled bool) error {
	if err := updateConfig(func(c *yansuConfig) {
		v := enabled
		c.BrowserBridgeEnabled = &v
	}); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if enabled {
		if a.browserServer != nil {
			return nil
		}
		if a.eventBroadcaster == nil {
			a.eventBroadcaster = NewEventBroadcaster()
		}
		bSrv, err := startBrowserServer(a, a.eventBroadcaster)
		if err != nil {
			log.Printf("[browser-server] runtime start failed: %v", err)
			return err
		}
		a.browserServer = bSrv
		log.Printf("[browser-server] started via SetBrowserBridgeEnabled")
		return nil
	}

	if a.browserServer != nil {
		a.browserServer.Stop()
		a.browserServer = nil
		a.eventBroadcaster = nil
		log.Printf("[browser-server] stopped via SetBrowserBridgeEnabled")
	}
	return nil
}

// logBrowserBridgeDecision prints the effective decision once at startup
// to make disabled-state easy to grep in logs.
func logBrowserBridgeDecision(enabled bool) {
	if enabled {
		log.Printf("[browser-server] bridge enabled")
	} else {
		log.Printf("[browser-server] disabled by config; set YANSU_BROWSER_BRIDGE=1 or toggle in Settings to enable")
	}
}
