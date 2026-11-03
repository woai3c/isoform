package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"wails-gui/storage"
)

const testBridgePort = 34999
const testBridgeToken = "test-bridge-token"

func newTestBridge(t *testing.T) *browserServer {
	t.Helper()
	t.Setenv("VITE_PORT", "")
	t.Setenv(browserBridgeOriginsEnv, "")
	return newBrowserServer(&App{}, NewEventBroadcaster(), testBridgePort, testBridgeToken)
}

// bridgeRequest builds a request that passes every check; each test then
// breaks the one it is about.
func bridgeRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Host = "127.0.0.1:" + strconv.Itoa(testBridgePort)
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set(browserBridgeTokenHeader, testBridgeToken)
	return r
}

func serveBridge(bs *browserServer, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	bs.routes().ServeHTTP(rec, r)
	return rec
}

const getVersionCall = `{"method":"GetVersion","args":[]}`

func TestBridgeCallSucceedsWithEveryCheckMet(t *testing.T) {
	bs := newTestBridge(t)
	for _, host := range []string{"127.0.0.1", "localhost", "LOCALHOST"} {
		r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
		r.Host = host + ":" + strconv.Itoa(testBridgePort)
		rec := serveBridge(bs, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("host %s: status %d, body %s", host, rec.Code, rec.Body.String())
		}
		var resp browserAPIResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK || resp.Data != Version {
			t.Fatalf("host %s: unexpected response %s", host, rec.Body.String())
		}
	}
}

func TestBridgeRejectsForeignHost(t *testing.T) {
	bs := newTestBridge(t)
	for _, host := range []string{
		"attacker.example:" + strconv.Itoa(testBridgePort), // DNS rebinding
		"127.0.0.1:1",
		"127.0.0.1",
		"[::1]:" + strconv.Itoa(testBridgePort),
		"",
	} {
		for _, path := range []string{"/api/call", "/api/bridge-token", "/api/health", "/", "/ws"} {
			method := http.MethodGet
			if path == "/api/call" {
				method = http.MethodPost
			}
			r := bridgeRequest(method, path, getVersionCall)
			r.Header.Set(browserBridgeTokenRequestHeader, "1")
			r.Host = host
			if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
				t.Errorf("host %q path %s: status %d, want 403", host, path, rec.Code)
			}
		}
	}
}

func TestBridgeRejectsNonLoopbackRemote(t *testing.T) {
	bs := newTestBridge(t)
	r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
	r.RemoteAddr = "192.0.2.10:40000"
	if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestBridgeOriginChecks(t *testing.T) {
	bs := newTestBridge(t)
	allowed := []string{
		"", // same-origin GETs and non-browser clients send none
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://localhost:4173",
		"http://127.0.0.1:4173",
		"http://localhost:" + strconv.Itoa(testBridgePort),
		"http://127.0.0.1:" + strconv.Itoa(testBridgePort),
	}
	for _, origin := range allowed {
		r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := serveBridge(bs, r)
		if rec.Code != http.StatusOK {
			t.Errorf("origin %q: status %d, want 200", origin, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: Access-Control-Allow-Origin %q", origin, got)
		}
	}
	for _, origin := range []string{
		"https://attacker.example",
		"null",
		"http://localhost:8080",
		"https://localhost:5173",
		"http://localhost.attacker.example:5173",
	} {
		r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
		r.Header.Set("Origin", origin)
		rec := serveBridge(bs, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("origin %q: status %d, want 403", origin, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q: must not get Access-Control-Allow-Origin, got %q", origin, got)
		}
	}
}

func TestBridgeExtraOriginsFromEnvironment(t *testing.T) {
	t.Setenv("VITE_PORT", "5180")
	t.Setenv(browserBridgeOriginsEnv, " http://localhost:3000/ ,http://127.0.0.1:9000")
	bs := newBrowserServer(&App{}, NewEventBroadcaster(), testBridgePort, testBridgeToken)
	for _, origin := range []string{"http://localhost:5180", "http://127.0.0.1:5180", "http://localhost:4173", "http://localhost:3000", "http://127.0.0.1:9000"} {
		r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
		r.Header.Set("Origin", origin)
		if rec := serveBridge(bs, r); rec.Code != http.StatusOK {
			t.Errorf("origin %q: status %d, want 200", origin, rec.Code)
		}
	}
	// With VITE_PORT set, whatever else listens on 5173 is not the Vite
	// server start-wails-gui.sh launched.
	r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
	r.Header.Set("Origin", "http://localhost:5173")
	if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
		t.Errorf("origin http://localhost:5173 with VITE_PORT=5180: status %d, want 403", rec.Code)
	}
}

func TestBridgeCallRequiresJSONContentType(t *testing.T) {
	bs := newTestBridge(t)
	for _, ct := range []string{"", "text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonx"} {
		r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
		if ct == "" {
			r.Header.Del("Content-Type")
		} else {
			r.Header.Set("Content-Type", ct)
		}
		if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
			t.Errorf("Content-Type %q: status %d, want 403", ct, rec.Code)
		}
	}
	r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if rec := serveBridge(bs, r); rec.Code != http.StatusOK {
		t.Fatalf("application/json with charset: status %d", rec.Code)
	}
	if rec := serveBridge(bs, bridgeRequest(http.MethodGet, "/api/call", "")); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/call: status %d, want 405", rec.Code)
	}
}

func TestBridgeCallRequiresToken(t *testing.T) {
	bs := newTestBridge(t)
	r := bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
	r.Header.Del(browserBridgeTokenHeader)
	if rec := serveBridge(bs, r); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rec.Code)
	}
	r = bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
	r.Header.Set(browserBridgeTokenHeader, testBridgeToken+"x")
	if rec := serveBridge(bs, r); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", rec.Code)
	}
	// The query parameter is for the WebSocket only.
	r = bridgeRequest(http.MethodPost, "/api/call?token="+testBridgeToken, getVersionCall)
	r.Header.Del(browserBridgeTokenHeader)
	if rec := serveBridge(bs, r); rec.Code != http.StatusUnauthorized {
		t.Errorf("query token on /api/call: status %d, want 401", rec.Code)
	}
	// A bridge without a token refuses everything.
	empty := newBrowserServer(&App{}, NewEventBroadcaster(), testBridgePort, "")
	r = bridgeRequest(http.MethodPost, "/api/call", getVersionCall)
	r.Header.Set(browserBridgeTokenHeader, "")
	if rec := serveBridge(empty, r); rec.Code != http.StatusUnauthorized {
		t.Errorf("empty bridge token: status %d, want 401", rec.Code)
	}
}

func TestBridgeCallKeepsAllowlistAndUIStateKeys(t *testing.T) {
	bs := newTestBridge(t)
	r := bridgeRequest(http.MethodPost, "/api/call", `{"method":"UpdateCronJob","args":[]}`)
	if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
		t.Errorf("UpdateCronJob: status %d, want 403", rec.Code)
	}
	r = bridgeRequest(http.MethodPost, "/api/call", `{"method":"SaveUIState","args":["`+filesPrivateFoldersKey+`","[]"]}`)
	if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
		t.Errorf("SaveUIState with a settings key: status %d, want 403", rec.Code)
	}
	// An allowed key reaches storage and persists its value.
	store, err := storage.New(filepath.Join(t.TempDir(), "ui-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	bs.app.store = store
	r = bridgeRequest(http.MethodPost, "/api/call", `{"method":"SaveUIState","args":["yansu.unreadSessionIds.v1","[]"]}`)
	if rec := serveBridge(bs, r); rec.Code != http.StatusOK {
		t.Errorf("SaveUIState with an interface key: status %d, body %s", rec.Code, rec.Body.String())
	}
	if value, err := bs.app.store.GetUIState(t.Context(), "yansu.unreadSessionIds.v1"); err != nil || value != "[]" {
		t.Errorf("saved UI state = %q, %v", value, err)
	}
}

func TestBridgePreflight(t *testing.T) {
	bs := newTestBridge(t)
	r := bridgeRequest(http.MethodOptions, "/api/call", "")
	r.Header.Del(browserBridgeTokenHeader)
	r.Header.Set("Origin", "http://localhost:5173")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "content-type, x-yansu-bridge-token")
	rec := serveBridge(bs, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight: status %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("allowed preflight: Access-Control-Allow-Origin %q", got)
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Content-Type", browserBridgeTokenHeader, browserBridgeTokenRequestHeader} {
		if !strings.Contains(allowHeaders, h) {
			t.Errorf("allowed preflight: Access-Control-Allow-Headers %q lacks %s", allowHeaders, h)
		}
	}

	for _, origin := range []string{"https://attacker.example", ""} {
		r = bridgeRequest(http.MethodOptions, "/api/call", "")
		r.Header.Del(browserBridgeTokenHeader)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Access-Control-Request-Method", "POST")
		rec = serveBridge(bs, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("preflight from %q: status %d, want 403", origin, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("preflight from %q: Access-Control-Allow-Origin %q", origin, got)
		}
	}
}

func TestBridgeTokenEndpoint(t *testing.T) {
	bs := newTestBridge(t)
	r := bridgeRequest(http.MethodGet, "/api/bridge-token", "")
	r.Header.Del(browserBridgeTokenHeader)
	if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden {
		t.Errorf("without the request header: status %d, want 403", rec.Code)
	}

	r = bridgeRequest(http.MethodGet, "/api/bridge-token", "")
	r.Header.Del(browserBridgeTokenHeader)
	r.Header.Set(browserBridgeTokenRequestHeader, "1")
	r.Header.Set("Origin", "http://localhost:5173")
	rec := serveBridge(bs, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("token request: status %d", rec.Code)
	}
	var resp browserAPIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data != testBridgeToken {
		t.Fatalf("token request: body %s", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q, want nosniff", got)
	}

	r = bridgeRequest(http.MethodGet, "/api/bridge-token", "")
	r.Header.Set(browserBridgeTokenRequestHeader, "1")
	r.Header.Set("Origin", "https://attacker.example")
	if rec := serveBridge(bs, r); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), testBridgeToken) {
		t.Errorf("foreign origin: status %d body %s", rec.Code, rec.Body.String())
	}
	r = bridgeRequest(http.MethodPost, "/api/bridge-token", "")
	r.Header.Set(browserBridgeTokenRequestHeader, "1")
	if rec := serveBridge(bs, r); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}

// The REST routes that bypassed the allowlist are gone.
func TestBridgeLegacyRESTRoutesAreGone(t *testing.T) {
	bs := newTestBridge(t)
	for _, path := range []string{
		"/api/send-prompt", "/api/ui-state/save", "/api/project/delete",
		"/api/settings/environment", "/api/worktree/settings", "/api/stream-item/persist",
	} {
		r := bridgeRequest(http.MethodPost, path, `{}`)
		if rec := serveBridge(bs, r); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestBridgeRejectionLogsLeaveOutBodyAndQuery(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	bs := newTestBridge(t)
	r := bridgeRequest(http.MethodPost, "/api/call?token=QUERY-SECRET", `{"method":"SaveUIState","args":["k","BODY-SECRET"]}`)
	r.Header.Set("Content-Type", "text/plain")
	serveBridge(bs, r)
	r = bridgeRequest(http.MethodPost, "/api/call?token=QUERY-SECRET", `{"method":"SaveUIState","args":["k","BODY-SECRET"]}`)
	r.Header.Set(browserBridgeTokenHeader, "HEADER-SECRET")
	serveBridge(bs, r)

	out := buf.String()
	if !strings.Contains(out, "rejected POST /api/call") {
		t.Fatalf("expected a rejection line, got %q", out)
	}
	for _, secret := range []string{"BODY-SECRET", "QUERY-SECRET", "HEADER-SECRET", testBridgeToken} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %s: %q", secret, out)
		}
	}
}

func startTestBridgeServer(t *testing.T) (*browserServer, *httptest.Server) {
	t.Helper()
	t.Setenv("VITE_PORT", "")
	t.Setenv(browserBridgeOriginsEnv, "")
	ts := httptest.NewUnstartedServer(nil)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	bs := newBrowserServer(&App{}, NewEventBroadcaster(), port, testBridgeToken)
	ts.Config.Handler = bs.routes()
	ts.Start()
	t.Cleanup(ts.Close)
	return bs, ts
}

func dialBridgeWS(ts *httptest.Server, query string, header http.Header) (*websocket.Conn, *http.Response, error) {
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws" + query
	return websocket.DefaultDialer.Dial(url, header)
}

func TestBridgeWebSocketChecks(t *testing.T) {
	bs, ts := startTestBridgeServer(t)
	port := strconv.Itoa(ts.Listener.Addr().(*net.TCPAddr).Port)
	viteOrigin := http.Header{"Origin": {"http://localhost:5173"}}

	cases := []struct {
		name   string
		query  string
		header http.Header
		status int
	}{
		{"no token", "", viteOrigin, http.StatusUnauthorized},
		{"wrong token", "?token=nope", viteOrigin, http.StatusUnauthorized},
		{"foreign origin", "?token=" + testBridgeToken, http.Header{"Origin": {"https://attacker.example"}}, http.StatusForbidden},
		{"foreign host", "?token=" + testBridgeToken, http.Header{"Origin": {"http://localhost:5173"}, "Host": {"attacker.example:" + port}}, http.StatusForbidden},
	}
	for _, tc := range cases {
		conn, resp, err := dialBridgeWS(ts, tc.query, tc.header)
		if err == nil {
			conn.Close()
			t.Errorf("%s: upgrade succeeded", tc.name)
			continue
		}
		if resp == nil || resp.StatusCode != tc.status {
			got := 0
			if resp != nil {
				got = resp.StatusCode
			}
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.status)
		}
	}

	for _, tc := range []struct {
		name   string
		query  string
		header http.Header
	}{
		{"query token", "?token=" + testBridgeToken, viteOrigin},
		{"header token", "", http.Header{"Origin": {"http://localhost:5173"}, browserBridgeTokenHeader: {testBridgeToken}}},
	} {
		conn, _, err := dialBridgeWS(ts, tc.query, tc.header)
		if err != nil {
			t.Fatalf("%s: dial: %v", tc.name, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for bs.broadcaster.ClientCount() == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		bs.broadcaster.Broadcast("bridge:test", "hello")
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil || !strings.Contains(string(msg), "bridge:test") {
			t.Fatalf("%s: read %q, %v", tc.name, msg, err)
		}
		conn.Close()
		deadline = time.Now().Add(2 * time.Second)
		for bs.broadcaster.ClientCount() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestBridgeTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", browserBridgeTokenFileName)
	if err := writeBrowserBridgeToken(path, "first"); err != nil {
		t.Fatal(err)
	}
	if err := writeBrowserBridgeToken(path, "second"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode %o, want 600", perm)
	}
	if data, _ := os.ReadFile(path); string(data) != "second" {
		t.Fatalf("token file holds %q", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}

	// Another launch's token stays; this launch's token goes.
	removeBrowserBridgeToken(path, "first")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed a token this bridge did not write: %v", err)
	}
	removeBrowserBridgeToken(path, "second")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file still present: %v", err)
	}
}

func TestBridgeTokensAreRandom(t *testing.T) {
	a, err := newBrowserBridgeToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newBrowserBridgeToken()
	if a == b || len(a) != 64 {
		t.Fatalf("tokens %q and %q", a, b)
	}
}
