package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// shortSocketDir returns a directory short enough that paths inside it
// fit within sockaddr_un.sun_path (104 bytes on macOS). t.TempDir()
// hands back ~110-byte paths under /var/folders/... which trip
// `bind: invalid argument` on Unix sockets. /tmp is reliably short on
// macOS and Linux.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cua-")
	if err != nil {
		t.Fatalf("mkdir short tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestCuaDriverProxy_ForwardsAllowedCalls walks the happy path through
// the proxy. The caller speaks the legacy cua-driver shape; the fake
// daemon receives the alma-translated shape; the response coming back
// to the caller is again the legacy shape. If any of those three
// boundaries drifts the proxy is corrupting traffic.
func TestCuaDriverProxy_ForwardsAllowedCalls(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	captured := make(chan []byte, 1)
	// Mimic alma's success envelope. The translator unwraps it back
	// into the legacy {ok,result} shape before the caller sees it.
	daemonResponse := []byte(`{"id":"y1","ok":true,"data":{"echo":1}}`)
	stopFake := startFakeDaemon(t, daemonPath, captured, daemonResponse)
	defer stopFake()

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	// Pre-seed the translator's element_index cache so the legacy
	// {element_index:3} payload below resolves to an alma ref. Without
	// this the translator would emit a poison ref and the assertion
	// below would still pass (the request still reaches the daemon),
	// but the test wouldn't exercise the normal flow.
	proxy.translator.recordSnap(1, &cachedSnap{
		elements: []map[string]any{
			{"ref": "a1"}, {"ref": "a2"}, {"ref": "a3"}, {"ref": "a4"},
		},
	})

	req := []byte(`{"method":"call","name":"click","args":{"pid":1,"window_id":2,"element_index":3}}` + "\n")
	resp := dialAndExchange(t, clientPath, req)

	select {
	case forwarded := <-captured:
		// Daemon should see alma-shape: cmd=click, ref="a4" (idx=3
		// → the 4th element), no element_index, no window_id.
		var got map[string]any
		if err := json.Unmarshal(forwarded, &got); err != nil {
			t.Fatalf("daemon received un-parseable JSON %q: %v", forwarded, err)
		}
		if got["cmd"] != "click" {
			t.Errorf("daemon saw cmd=%v, want click", got["cmd"])
		}
		args, _ := got["args"].(map[string]any)
		if args["ref"] != "a4" {
			t.Errorf("daemon saw ref=%v, want a4 (translator should resolve element_index 3 from cache)", args["ref"])
		}
		if _, hasIdx := args["element_index"]; hasIdx {
			t.Errorf("daemon saw element_index=%v, want it stripped by translator", args["element_index"])
		}
		if _, hasWid := args["window_id"]; hasWid {
			t.Errorf("daemon saw window_id=%v, want it stripped by translator", args["window_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fake daemon did not receive the forwarded request within 2s")
	}

	// Response back to caller is the legacy envelope.
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("decode response: %v (raw: %s)", err, resp)
	}
	if !env.OK {
		t.Errorf("want ok=true on success, got envelope: %s", resp)
	}
	if !strings.Contains(string(env.Result), `"echo":1`) {
		t.Errorf("result %q did not carry the daemon data payload", env.Result)
	}
}

// TestCuaDriverProxy_DeniesPolicyViolations verifies the chokepoint
// actually blocks hotkey combos that the WindowServer would route to
// itself. Pixel clicks and drags are NOT in this set anymore — alma's
// bgclick makes them focus-safe.
func TestCuaDriverProxy_DeniesPolicyViolations(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	captured := make(chan []byte, 1)
	stopFake := startFakeDaemon(t, daemonPath, captured, []byte(`{"id":"_","ok":true,"data":{}}`))
	defer stopFake()

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	cases := []struct {
		name   string
		req    string
		denial string
	}{
		{
			name:   "hotkey cmd+L (omnibox)",
			req:    `{"method":"call","name":"hotkey","args":{"pid":1,"keys":["cmd","l"]}}`,
			denial: "cmd+L",
		},
		{
			name:   "hotkey cmd+Tab (app switcher)",
			req:    `{"method":"call","name":"hotkey","args":{"pid":1,"keys":["cmd","tab"]}}`,
			denial: "cmd+Tab",
		},
		{
			name:   "press cmd+1 in alma-native shape",
			req:    `{"method":"call","name":"press","args":{"pid":1,"key":"1","modifiers":["cmd"]}}`,
			denial: "cmd+1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := dialAndExchange(t, clientPath, []byte(c.req+"\n"))

			select {
			case leaked := <-captured:
				t.Fatalf("policy violation reached fake daemon: %q", leaked)
			case <-time.After(150 * time.Millisecond):
				// Expected: nothing forwarded.
			}

			var env struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(resp, &env); err != nil {
				t.Fatalf("decode response: %v (raw: %s)", err, resp)
			}
			if env.OK {
				t.Errorf("ok=true on denial; want ok=false")
			}
			if !strings.Contains(env.Error, c.denial) {
				t.Errorf("error %q does not mention %q", env.Error, c.denial)
			}
		})
	}
}

func TestCuaDriverProxy_SourceLocatorCapabilityIsPrivateAndOneUse(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	captured := make(chan []byte, 1)
	stopFake := startFakeDaemon(
		t,
		daemonPath,
		captured,
		[]byte(`{"id":"_","ok":true,"data":{"elements":[]}}`),
	)
	defer stopFake()

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	unauthorized := []byte(
		`{"method":"call","name":"get_window_state","args":{"bundle":"com.apple.Safari","include_element_urls":true}}` + "\n",
	)
	response := dialAndExchange(t, clientPath, unauthorized)
	if !strings.Contains(string(response), "private source-locator capability") {
		t.Fatalf("unexpected source-locator denial: %s", response)
	}
	select {
	case leaked := <-captured:
		t.Fatalf("unauthorized element URL request reached daemon: %s", leaked)
	case <-time.After(150 * time.Millisecond):
	}

	capability, err := proxy.issueSourceLocatorCapability()
	if err != nil {
		t.Fatal(err)
	}
	authorized := []byte(fmt.Sprintf(
		`{"method":"call","name":"get_window_state","args":{"bundle":"com.apple.Safari","include_element_urls":true,"source_locator_capability":%q}}`+"\n",
		capability,
	))
	response = dialAndExchange(t, clientPath, authorized)
	if !strings.Contains(string(response), `"ok":true`) {
		t.Fatalf("authorized source-locator request failed: %s", response)
	}
	select {
	case forwarded := <-captured:
		var request map[string]any
		if err := json.Unmarshal(forwarded, &request); err != nil {
			t.Fatal(err)
		}
		args, _ := request["args"].(map[string]any)
		if args[cuaIncludeElementURLsArg] != true ||
			args[cuaSourceLocatorSecretArg] != proxy.sourceLocatorSecret {
			t.Fatalf("daemon did not receive admitted source-locator args: %+v", args)
		}
		if _, leaked := args[cuaSourceLocatorCapabilityArg]; leaked {
			t.Fatalf("one-use public capability reached private daemon: %+v", args)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authorized source-locator request did not reach daemon")
	}

	response = dialAndExchange(t, clientPath, authorized)
	if !strings.Contains(string(response), "invalid or expired") {
		t.Fatalf("reused source-locator capability was not rejected: %s", response)
	}
	select {
	case leaked := <-captured:
		t.Fatalf("reused source-locator capability reached daemon: %s", leaked)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestCuaDriverProxy_PixelClicksAreAllowed pins the new behaviour:
// raw-coord click is no longer denied. The fake daemon should see the
// translated request reach it.
func TestCuaDriverProxy_PixelClicksAreAllowed(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	captured := make(chan []byte, 1)
	stopFake := startFakeDaemon(t, daemonPath, captured, []byte(`{"id":"_","ok":true,"data":{"clicked_at":{"x":100,"y":200}}}`))
	defer stopFake()

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	req := []byte(`{"method":"call","name":"click","args":{"pid":42,"x":100,"y":200}}` + "\n")
	resp := dialAndExchange(t, clientPath, req)

	select {
	case forwarded := <-captured:
		var got map[string]any
		if err := json.Unmarshal(forwarded, &got); err != nil {
			t.Fatalf("daemon received un-parseable JSON %q: %v", forwarded, err)
		}
		args, _ := got["args"].(map[string]any)
		if args["x"] == nil || args["y"] == nil || args["pid"] == nil {
			t.Errorf("daemon saw args=%v, want pid+x+y intact", args)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pixel click was blocked or never reached daemon")
	}

	var env struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("decode response: %v (raw: %s)", err, resp)
	}
	if !env.OK {
		t.Errorf("want ok=true for pixel click, got: %s", resp)
	}
}

func TestCuaDriverProxy_TypeTextElementIndexRewritesToRef(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	captured := make(chan []byte, 1)
	stopFake := startFakeDaemon(t, daemonPath, captured, []byte(`{"id":"_","ok":true,"data":{"typed":true}}`))
	defer stopFake()

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	proxy.translator.recordSnap(9, &cachedSnap{
		elements: []map[string]any{{"ref": "a-input"}},
	})

	req := []byte(`{"method":"call","name":"type_text","args":{"pid":9,"window_id":2,"element_index":0,"text":"hello"}}` + "\n")
	_ = dialAndExchange(t, clientPath, req)

	select {
	case forwarded := <-captured:
		var got map[string]any
		if err := json.Unmarshal(forwarded, &got); err != nil {
			t.Fatalf("daemon received un-parseable JSON %q: %v", forwarded, err)
		}
		args, _ := got["args"].(map[string]any)
		if args["ref"] != "a-input" {
			t.Errorf("daemon saw ref=%v, want a-input", args["ref"])
		}
		if _, hasIdx := args["element_index"]; hasIdx {
			t.Errorf("daemon saw element_index=%v, want it stripped", args["element_index"])
		}
		if _, hasWid := args["window_id"]; hasWid {
			t.Errorf("daemon saw window_id=%v, want it stripped", args["window_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("type_text was never forwarded")
	}
}

// TestCuaDriverProxy_ListToolsIsSynthesized verifies the synthesized
// list-tools path: the proxy returns the canned tool list without
// dialling the daemon.
func TestCuaDriverProxy_ListToolsIsSynthesized(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	captured := make(chan []byte, 1)
	// If the proxy ever reaches the daemon for list-tools, the test
	// fails loudly via the select below.
	stopFake := startFakeDaemon(t, daemonPath, captured, []byte(`{"id":"_","ok":true,"data":{}}`))
	defer stopFake()

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	req := []byte(`{"method":"list-tools"}` + "\n")
	resp := dialAndExchange(t, clientPath, req)

	select {
	case leaked := <-captured:
		t.Fatalf("list-tools reached the daemon: %q (should be synthesized in-proxy)", leaked)
	case <-time.After(150 * time.Millisecond):
		// Expected: nothing forwarded.
	}

	if !strings.Contains(string(resp), `"ok":true`) {
		t.Errorf("response %q is not a success envelope", resp)
	}
	if !strings.Contains(string(resp), `"name":"click"`) {
		t.Errorf("response %q does not include a tool list", resp)
	}
}

// TestCuaDriverProxy_StartIsIdempotent ensures multiple Start calls
// don't try to bind twice and corrupt internal state. EnsureRunning
// can call Start more than once on warm restart paths.
func TestCuaDriverProxy_StartIsIdempotent(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer proxy.Stop()
	if err := proxy.Start(); err != nil {
		t.Fatalf("second Start: %v (must be a no-op)", err)
	}
}

func TestCuaDriverProxy_StartRebindsMissingSocketPath(t *testing.T) {
	dir := shortSocketDir(t)
	daemonPath := filepath.Join(dir, "daemon.sock")
	clientPath := filepath.Join(dir, "client.sock")

	proxy := newCuaDriverProxy(clientPath, daemonPath)
	if err := proxy.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer proxy.Stop()

	if err := os.Remove(clientPath); err != nil {
		t.Fatalf("remove client socket path: %v", err)
	}
	if proxy.Ready() {
		t.Fatal("proxy should not be ready after its public socket path was removed")
	}
	if err := proxy.Start(); err != nil {
		t.Fatalf("second Start should rebind missing socket path: %v", err)
	}
	if !proxy.Ready() {
		t.Fatal("proxy socket should accept connections after rebind")
	}
}

// startFakeDaemon stands in for cua-driver: it accepts one connection,
// reads one line, captures it, and writes back the canned response.
// The returned func cleans up the listener and waits for handlers to
// exit so a leaked goroutine doesn't trip -race in a sibling test.
func startFakeDaemon(t *testing.T, sockPath string, captured chan<- []byte, response []byte) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen fake daemon: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				r := bufio.NewReaderSize(conn, 64<<10)
				line, err := r.ReadBytes('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return
				}
				select {
				case captured <- append([]byte(nil), line...):
				default:
				}
				out := append([]byte(nil), response...)
				if len(out) == 0 || out[len(out)-1] != '\n' {
					out = append(out, '\n')
				}
				_, _ = conn.Write(out)
			}()
		}
	}()
	return func() {
		_ = l.Close()
		wg.Wait()
	}
}

// dialAndExchange dials the proxy socket, writes one request line, and
// reads the single response line. Failure aborts the test with t.Fatal
// so each test stays linear and easy to read.
func dialAndExchange(t *testing.T, sockPath string, req []byte) []byte {
	t.Helper()
	// The proxy bound the listener inside Start, but the goroutine
	// running Accept may not be scheduled yet. Retry briefly so the
	// test isn't flaky on a busy CI host.
	var conn net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.Dial("unix", sockPath)
		if err == nil {
			conn = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial proxy after 2s: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	line, err := r.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read: %v", err)
	}
	return line
}
