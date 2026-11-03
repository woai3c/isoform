package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// host_bridge_cua.go exposes the bundled cua-driver daemon over the
// localhost-only host bridge as `/api/cua/*`. The route surface is
// intentionally minimal: a single passthrough that lets the LLM (or any
// other client speaking the host-bridge protocol) invoke any
// cua-driver tool by name. We don't enumerate per-tool endpoints because
// the cua-driver protocol is already self-describing — `list-tools`,
// `describe`, and the SKILL.md cover the surface.
//
// We keep `/api/computer-use/*` (backed by activitymon) intact alongside
// this. activitymon predates cua-driver and has slightly different
// invariants — single-line click/type without element_index — so it
// stays available for callers that don't want a full snapshot loop.
// The bundled SKILL.md teaches the LLM to prefer /api/cua for
// snapshot-driven workflows.

func (hb *hostBridge) cuaRoutes() []hostBridgeRoute {
	if hb.app == nil || hb.app.cuaDriver == nil {
		// cua-driver not available on this platform / not bundled —
		// don't register stubs; callers see a 404 and can degrade
		// gracefully against /api/computer-use/* or back off entirely.
		return nil
	}
	return []hostBridgeRoute{
		{"/api/cua/call", hostAccessHelper, http.MethodPost, hb.limitBody(hb.handleCuaCall)},
		{"/api/cua/status", hostAccessHelper, http.MethodGet, hb.handleCuaStatus},
	}
}

// handleCuaCall proxies a tool invocation to the cua-driver daemon.
// Body shape: {"name": "<tool>", "args": {...}}
//
// We surface the daemon's response shape verbatim under "result" so
// callers can branch on `ok` without a translation table — the upstream
// JSON is already stable enough to be a public API.
func (hb *hostBridge) handleCuaCall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		hb.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		hb.writeError(w, http.StatusBadRequest, "tool name required")
		return
	}

	// `args` is forwarded as-is. RawMessage means we don't pay the cost
	// of a re-marshal and we preserve any field ordering / numeric
	// precision the caller sent.
	var args any
	if len(body.Args) == 0 {
		args = map[string]any{}
	} else {
		args = body.Args
	}

	// Cap the per-call deadline at the daemon's own ceiling so we never
	// outlive its read deadline — that would surface as a confusing
	// "EOF" instead of the daemon's actual error message.
	ctx, cancel := context.WithTimeout(r.Context(), cuaDriverCallTimeout+5*time.Second)
	defer cancel()

	resp, err := hb.app.cuaDriver.Call(ctx, body.Name, args)
	if err != nil {
		hb.writeError(w, http.StatusBadGateway, fmt.Sprintf("cua-driver call failed: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"result": resp,
	})
}

// handleCuaStatus reports whether the daemon handle exists and where
// its socket lives. Cheap: doesn't probe the socket. The frontend uses
// this to decide whether to surface CUA-specific UI.
func (hb *hostBridge) handleCuaStatus(w http.ResponseWriter, r *http.Request) {
	available := hb.app != nil && hb.app.cuaDriver != nil
	resp := map[string]any{
		"available": available,
	}
	if available {
		resp["socket"] = hb.app.cuaDriver.SocketPath()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// CallCuaDriver is the Wails-bound entrypoint mirror of /api/cua/call.
// Yansu's renderer (and any in-process tool wiring fed from app.go's
// Bind list) uses this to hit the daemon without dialing the host
// bridge — saving the localhost-HTTP round trip and avoiding the need
// to discover HOST_BRIDGE_URL.
//
// args is passed straight through. Pass json.RawMessage from the
// frontend if you need to preserve numeric precision; a plain map
// works for typical tool calls.
func (a *App) CallCuaDriver(name string, args any) (*CuaDriverResponse, error) {
	if a.cuaDriver == nil {
		return nil, fmt.Errorf("cua-driver not available on this platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cuaDriverCallTimeout+5*time.Second)
	defer cancel()
	return a.cuaDriver.Call(ctx, name, args)
}
