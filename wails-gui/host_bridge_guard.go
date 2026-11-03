package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
)

// Request checks for the host bridge. It starts on every launch, release
// builds included, and drives the Mac with Isoform's own grants (typing,
// clicking, the clipboard, opening files), so the same checks as the
// browser bridge (browser_bridge_guard.go) apply to every route:
//   - the request comes from a loopback address;
//   - its Host is 127.0.0.1:<port> or localhost:<port>, which rules out DNS
//     rebinding;
//   - its Origin, when present, is the one browser page allowed on that
//     route: the Spotlight helper's webview on /api/spotlight/* and the app
//     window on /api/activity/*; every other Origin is refused, and CORS
//     preflights are answered only on /api/spotlight/* for the helper;
//   - a POST carries Content-Type application/json, which a foreign page can
//     only send after a preflight;
//   - every route except /api/health carries a scoped token.
//
// There are four token scopes, so each caller holds only what it needs:
//   - the host-bridge token (X-Yansu-Host-Bridge-Token) for the routes that
//     act on the Mac. It is generated at start and never written to disk or
//     to the environment Isoform passes to its child processes; set
//     YANSU_HOST_BRIDGE_TOKEN when launching Isoform to choose it (the
//     computer-use e2e tests send it as HOST_BRIDGE_TOKEN);
//   - the Spotlight helper's IPC token (X-Yansu-Spotlight-Token), which the
//     supervisor generates for each helper it spawns and passes as
//     --ipc-token. With no helper running there is no token and
//     /api/spotlight/* refuses every request. The helper binds its own
//     ports and reports them on stdout, so the supervisor never sends the
//     token to a port another process took first;
//   - the media token for the screenshots and recordings the activity page
//     shows. <img> and <video> cannot send headers, so it rides in the
//     "token" query parameter; the window reads it through
//     GetHostBridgeMediaToken, and so does the browser preview through the
//     browser bridge, whose token holders already read activity text. It
//     opens /api/activity/* only;
//   - persistent, separately revocable assistant read tokens open only
//     /api/mcp/read on a private Unix socket, never this TCP listener.
//     Native Settings issues them; each data request rechecks
//     the private credential file and current observation privacy gates.

const (
	hostBridgeTokenHeader = "X-Yansu-Host-Bridge-Token"
	// hostBridgeTokenEnv lets a developer choose the host-bridge token at
	// launch, for the computer-use e2e tests. Shorter values are ignored.
	hostBridgeTokenEnv      = "YANSU_HOST_BRIDGE_TOKEN"
	hostBridgeTokenMinLen   = 32
	hostBridgeMediaTokenKey = "token"
)

// hostBridgeAccess says who may call a host bridge route.
type hostBridgeAccess int

const (
	// hostAccessOpen needs no token. Only /api/health, which answers
	// {"ok":true} and nothing else.
	hostAccessOpen hostBridgeAccess = iota
	// hostAccessHelper needs the host-bridge token; no browser page may
	// call it.
	hostAccessHelper
	// hostAccessSpotlight needs the Spotlight helper's token; its webview
	// is the one browser origin allowed.
	hostAccessSpotlight
	// hostAccessMedia needs the media token in the query string; the app
	// window is the one browser origin allowed.
	hostAccessMedia
	// hostAccessMCPRead accepts only a revocable assistant read token.
	hostAccessMCPRead
)

// hostBridgeRoute is one route: its path, who may call it, the method it
// takes, and its handler. Every route is registered through guard.
type hostBridgeRoute struct {
	path    string
	access  hostBridgeAccess
	method  string // http.MethodGet or http.MethodPost
	handler http.HandlerFunc
}

// hostBridgeTokenFromEnv returns YANSU_HOST_BRIDGE_TOKEN when it is long
// enough to use, or "". It removes the variable from the environment so
// the child processes Isoform starts never inherit it.
func hostBridgeTokenFromEnv() string {
	v := strings.TrimSpace(os.Getenv(hostBridgeTokenEnv))
	_ = os.Unsetenv(hostBridgeTokenEnv)
	if len(v) < hostBridgeTokenMinLen {
		return ""
	}
	return v
}

// appWindowOrigin is the Origin of Isoform's own window: Wails serves it
// from wails://wails, except on Windows (http://wails.localhost).
func appWindowOrigin() string {
	switch goruntime.GOOS {
	case "windows":
		return "http://wails.localhost"
	default:
		return "wails://wails"
	}
}

// allowedOrigin is the one browser Origin allowed on routes with this
// access, or "" when none is.
func (hb *hostBridge) allowedOrigin(access hostBridgeAccess) string {
	switch access {
	case hostAccessSpotlight:
		if hb.app != nil && hb.app.spotlight != nil {
			return hb.app.spotlight.WebviewOrigin()
		}
		return ""
	case hostAccessMedia:
		return appWindowOrigin()
	default:
		return ""
	}
}

// hasToken reports whether the request carries the token its route needs.
// An unset expected token refuses every request.
func (hb *hostBridge) hasToken(access hostBridgeAccess, r *http.Request) bool {
	var got, want string
	switch access {
	case hostAccessOpen:
		return true
	case hostAccessHelper:
		got, want = r.Header.Get(hostBridgeTokenHeader), hb.token
	case hostAccessSpotlight:
		got, want = r.Header.Get(spotlightTokenHeader), hb.spotlightToken()
	case hostAccessMedia:
		got, want = r.URL.Query().Get(hostBridgeMediaTokenKey), hb.mediaToken
	case hostAccessMCPRead:
		return validMCPReadRequest(r)
	default:
		return false
	}
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// spotlightToken is the running helper's IPC token, or "" when no helper
// runs.
func (hb *hostBridge) spotlightToken() string {
	if hb.app == nil || hb.app.spotlight == nil {
		return ""
	}
	return hb.app.spotlight.Token()
}

// reject answers with the status and logs the method, path and reason only:
// never a token, the body or the query string.
func (hb *hostBridge) reject(w http.ResponseWriter, r *http.Request, status int, reason string) {
	log.Printf("[host-bridge] rejected %s %s: %s", r.Method, r.URL.Path, reason)
	hb.writeError(w, status, reason)
}

// guard applies the loopback, Host, Origin, method, Content-Type and token
// checks for the route, and answers the Spotlight helper's CORS preflights.
func (hb *hostBridge) guard(rt hostBridgeRoute, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rt.access == hostAccessMCPRead {
			if r.Context().Value(mcpReadSocketContextKey{}) != hb || r.Host != "isoform" {
				hb.reject(w, r, http.StatusForbidden, "private Isoform socket required")
				return
			}
		} else {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				hb.reject(w, r, http.StatusForbidden, "invalid remote address")
				return
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				hb.reject(w, r, http.StatusForbidden, "localhost only")
				return
			}
			if !hb.allowedHosts[strings.ToLower(r.Host)] {
				hb.reject(w, r, http.StatusForbidden, fmt.Sprintf("host %s not allowed", quoteHeaderForLog(r.Host)))
				return
			}
		}
		origin := r.Header.Get("Origin")
		cors := false
		if origin != "" {
			allowed := hb.allowedOrigin(rt.access)
			if allowed == "" || !strings.EqualFold(origin, allowed) {
				hb.reject(w, r, http.StatusForbidden, fmt.Sprintf("origin %s not allowed", quoteHeaderForLog(origin)))
				return
			}
			cors = rt.access == hostAccessSpotlight
		}
		if cors {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+spotlightTokenHeader)
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			if !cors {
				hb.reject(w, r, http.StatusForbidden, "preflight not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != rt.method {
			hb.writeError(w, http.StatusMethodNotAllowed, rt.method+" required")
			return
		}
		if rt.method == http.MethodPost {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				hb.reject(w, r, http.StatusForbidden, "Content-Type application/json required")
				return
			}
		}
		if !hb.hasToken(rt.access, r) {
			hb.reject(w, r, http.StatusUnauthorized, "token missing or wrong")
			return
		}
		next(w, r)
	}
}
