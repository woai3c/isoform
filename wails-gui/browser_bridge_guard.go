package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Request checks for the browser bridge. Any web page the user visits can
// send requests to 127.0.0.1, so the bridge accepts a request only when:
//   - it comes from a loopback address;
//   - its Host is 127.0.0.1:<port> or localhost:<port>, which rules out DNS
//     rebinding (a foreign name that resolves to 127.0.0.1 keeps its own Host);
//   - its Origin, when present, is the bridge itself or the Vite dev or
//     preview server, and CORS preflights are answered only for those;
//   - a POST carries Content-Type application/json, which a foreign page can
//     only send after a preflight;
//   - /api/call and /ws carry the per-launch bridge token.
//
// The token is generated when the bridge starts and written to a 0600 file in
// the data folder. The browser transport reads it from /api/bridge-token,
// which asks for a custom request header so a foreign page cannot read it
// without a preflight that the Origin check refuses.
//
// These checks keep web pages out. They do not authenticate local programs:
// anything on this Mac that can reach 127.0.0.1, including another user's
// processes, can ask /api/bridge-token for the token. That is why the bridge
// is off in release builds unless turned on, and why the allowlist keeps only
// what the browser preview needs.

const (
	// browserBridgeTokenHeader carries the bridge token on /api/call. The
	// WebSocket may pass it as the "token" query parameter instead, since a
	// browser cannot set headers on a WebSocket.
	browserBridgeTokenHeader = "X-Yansu-Bridge-Token"
	// browserBridgeTokenRequestHeader must be set on GET /api/bridge-token.
	browserBridgeTokenRequestHeader = "X-Yansu-Bridge-Token-Request"
	browserBridgeTokenQueryParam    = "token"
	browserBridgeTokenFileName      = "browser-bridge-token"
	// browserBridgeOriginsEnv adds comma-separated origins (for example
	// "http://localhost:3000") for unusual setups.
	browserBridgeOriginsEnv = "YANSU_BROWSER_BRIDGE_ORIGINS"

	viteDevDefaultPort     = 5173
	vitePreviewDefaultPort = 4173
)

// newBrowserBridgeToken returns 32 random bytes as hex.
func newBrowserBridgeToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("bridge token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// browserBridgeTokenPath is the token file in the data folder, or "" when the
// home folder cannot be resolved.
func browserBridgeTokenPath() string {
	dir := configDirOrFallback()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, browserBridgeTokenFileName)
}

// writeBrowserBridgeToken writes the token readable by this user only. The
// file is replaced by rename so a reader never sees a partial token.
func writeBrowserBridgeToken(path, token string) error {
	if path == "" {
		return fmt.Errorf("no data folder for the bridge token")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, browserBridgeTokenFileName+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.WriteString(token); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// removeBrowserBridgeToken deletes the token file if it still holds token, so
// a stopped bridge leaves no live credential behind.
func removeBrowserBridgeToken(path, token string) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || subtle.ConstantTimeCompare(data, []byte(token)) != 1 {
		return
	}
	_ = os.Remove(path)
}

// browserBridgeAllowedOrigins lists the origins that may call the bridge: the
// bridge itself (it serves the built UI), the Vite dev server and `vite
// preview` (4173), each on localhost and 127.0.0.1, plus
// YANSU_BROWSER_BRIDGE_ORIGINS. The dev server is the exact VITE_PORT that
// start-wails-gui.sh chose when it is set, so another project's dev server
// left on 5173 is not trusted; 5173 otherwise.
func browserBridgeAllowedOrigins(bridgePort int) map[string]bool {
	devPort := viteDevDefaultPort
	if p, err := strconv.Atoi(strings.TrimSpace(os.Getenv("VITE_PORT"))); err == nil && p > 0 {
		devPort = p
	}
	ports := []int{bridgePort, devPort, vitePreviewDefaultPort}
	origins := make(map[string]bool)
	for _, p := range ports {
		for _, host := range []string{"localhost", "127.0.0.1"} {
			origins[fmt.Sprintf("http://%s:%d", host, p)] = true
		}
	}
	for _, raw := range strings.Split(os.Getenv(browserBridgeOriginsEnv), ",") {
		if o := strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "/")); o != "" {
			origins[o] = true
		}
	}
	return origins
}

// browserBridgeAllowedHosts lists the Host values the bridge answers to.
func browserBridgeAllowedHosts(bridgePort int) map[string]bool {
	return map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", bridgePort): true,
		fmt.Sprintf("localhost:%d", bridgePort): true,
	}
}

// reject answers 403 (or the given status) and logs the method, path and
// reason only: never the body, the query string or a header value other than
// the quoted Host or Origin that failed.
func (bs *browserServer) reject(w http.ResponseWriter, r *http.Request, status int, reason string) {
	log.Printf("[browser-server] rejected %s %s: %s", r.Method, r.URL.Path, reason)
	bs.writeError(w, status, reason)
}

// guard applies the loopback, Host and Origin checks to every route and
// answers CORS preflights for the allowed origins only.
func (bs *browserServer) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			bs.reject(w, r, http.StatusForbidden, "invalid remote address")
			return
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			bs.reject(w, r, http.StatusForbidden, "localhost only")
			return
		}
		if !bs.allowedHosts[strings.ToLower(r.Host)] {
			bs.reject(w, r, http.StatusForbidden, fmt.Sprintf("host %s not allowed", quoteHeaderForLog(r.Host)))
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !bs.allowedOrigins[strings.ToLower(origin)] {
				bs.reject(w, r, http.StatusForbidden, fmt.Sprintf("origin %s not allowed", quoteHeaderForLog(origin)))
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers",
				"Content-Type, "+browserBridgeTokenHeader+", "+browserBridgeTokenRequestHeader)
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			if origin == "" {
				bs.reject(w, r, http.StatusForbidden, "preflight without origin")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// postJSON requires POST with Content-Type application/json. A foreign page
// can send text/plain or a form without a preflight, never JSON.
func (bs *browserServer) postJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			bs.writeError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			bs.reject(w, r, http.StatusForbidden, "Content-Type application/json required")
			return
		}
		next(w, r)
	}
}

// requireToken checks the bridge token from the header, or, when
// allowQuery is set (the WebSocket only), from the token query parameter.
func (bs *browserServer) requireToken(allowQuery bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(browserBridgeTokenHeader)
		if got == "" && allowQuery {
			got = r.URL.Query().Get(browserBridgeTokenQueryParam)
		}
		if bs.token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(bs.token)) != 1 {
			bs.reject(w, r, http.StatusUnauthorized, "bridge token missing or wrong")
			return
		}
		next(w, r)
	}
}

// handleBridgeToken hands the token to the browser transport. The custom
// request header forces a preflight from any other origin, and guard answers
// preflights only for the allowed origins; a plain navigation, <script> or
// <img> cannot set it.
func (bs *browserServer) handleBridgeToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		bs.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if r.Header.Get(browserBridgeTokenRequestHeader) == "" {
		bs.reject(w, r, http.StatusForbidden, browserBridgeTokenRequestHeader+" header required")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	bs.writeOK(w, bs.token)
}

// quoteHeaderForLog quotes an untrusted header value for a log line.
func quoteHeaderForLog(s string) string {
	const max = 80
	if len(s) > max {
		s = s[:max]
	}
	return strconv.Quote(s)
}
