package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// cua_driver_proxy.go runs in front of the bundled cua-driver daemon
// as the canonical client-facing socket. Every request from any caller
// (Wails HTTP handler, yansu-agent's tool, or a raw `nc -U` from a
// shelled-out coding agent) lands here first, gets policy-checked
// against the no-foreground contract, and is then translated from the
// legacy cua-driver wire shape into the alma-derived NDJSON shape the
// bundled daemon actually speaks. Responses are translated back the
// other way before reaching the caller.
//
// The proxy is the only chokepoint that catches ALL paths. Validating
// or translating at any single Go entry point would leave a hole — the
// SKILL.md teaches the LLM to invoke cua-driver via Bash, which dials
// the canonical socket directly and bypasses Go-side validation.
// Putting validation + translation at the wire boundary is the only
// way to make either enforceable rather than aspirational.
//
// Wire contract presented to callers: line-delimited JSON,
// {method:"call",name,args} → {ok,result,error}. One request, one
// response, one connection. Internally each request is rewritten into
// alma's {id,cmd,args} and the response is rewritten back. See
// cua_translator.go for the per-tool reshaping rules.

type cuaDriverProxy struct {
	clientPath string // canonical, what every caller dials
	daemonPath string // private, where cua-driver is actually serving

	mu         sync.Mutex
	listener   net.Listener
	started    bool
	translator *cuaTranslator

	sourceLocatorMu           sync.Mutex
	sourceLocatorSecret       string
	sourceLocatorCapabilities map[string]time.Time

	// ensureDaemon, when set, is invoked from handleConn if the private
	// daemon socket can't be dialled. Lets the proxy transparently
	// respawn a dead daemon and retry once before reporting failure to
	// the caller. nil in tests that supply their own fake daemon.
	ensureMu     sync.RWMutex
	ensureDaemon func(context.Context) error
}

func newCuaDriverProxy(clientPath, daemonPath string) *cuaDriverProxy {
	return &cuaDriverProxy{
		clientPath:                clientPath,
		daemonPath:                daemonPath,
		translator:                newCuaTranslator(),
		sourceLocatorSecret:       newCuaSourceLocatorToken(),
		sourceLocatorCapabilities: map[string]time.Time{},
	}
}

const (
	cuaSourceLocatorCapabilityArg = "source_locator_capability"
	cuaSourceLocatorSecretArg     = "source_locator_secret"
	cuaIncludeElementURLsArg      = "include_element_urls"
	cuaSourceLocatorSecretEnv     = "YANSU_CUA_SOURCE_LOCATOR_SECRET"
	cuaSourceLocatorCapabilityTTL = 30 * time.Second
)

func newCuaSourceLocatorToken() string {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func (p *cuaDriverProxy) issueSourceLocatorCapability() (string, error) {
	if p == nil || p.sourceLocatorSecret == "" {
		return "", errors.New("Computer Use source-locator capability is unavailable")
	}
	token := newCuaSourceLocatorToken()
	if token == "" {
		return "", errors.New("create Computer Use source-locator capability")
	}
	now := time.Now()
	p.sourceLocatorMu.Lock()
	defer p.sourceLocatorMu.Unlock()
	for existing, expiresAt := range p.sourceLocatorCapabilities {
		if !expiresAt.After(now) {
			delete(p.sourceLocatorCapabilities, existing)
		}
	}
	if len(p.sourceLocatorCapabilities) >= 32 {
		return "", errors.New("too many pending Computer Use source-locator capabilities")
	}
	p.sourceLocatorCapabilities[token] = now.Add(cuaSourceLocatorCapabilityTTL)
	return token, nil
}

func (p *cuaDriverProxy) authorizeSourceLocatorRequest(
	tool string,
	rawArgs json.RawMessage,
) (json.RawMessage, string, bool) {
	var args map[string]any
	if len(rawArgs) == 0 || json.Unmarshal(rawArgs, &args) != nil {
		return rawArgs, "", false
	}
	includeURLs, includePresent := args[cuaIncludeElementURLsArg]
	capability, capabilityPresent := args[cuaSourceLocatorCapabilityArg]
	if !includePresent && !capabilityPresent {
		return rawArgs, "", false
	}
	include, includeOK := includeURLs.(bool)
	if !includeOK || !include {
		if capabilityPresent {
			return nil, "source-locator capability requires an element URL snapshot", false
		}
		return rawArgs, "", false
	}
	if tool != "get_window_state" {
		return nil, "element URL extraction is limited to get_window_state", false
	}
	token, ok := capability.(string)
	if !ok || strings.TrimSpace(token) == "" {
		return nil, "element URL extraction requires a private source-locator capability", false
	}
	now := time.Now()
	p.sourceLocatorMu.Lock()
	expiresAt, admitted := p.sourceLocatorCapabilities[token]
	delete(p.sourceLocatorCapabilities, token)
	p.sourceLocatorMu.Unlock()
	if !admitted || !expiresAt.After(now) {
		return nil, "element URL extraction source-locator capability is invalid or expired", false
	}
	delete(args, cuaSourceLocatorCapabilityArg)
	args[cuaSourceLocatorSecretArg] = p.sourceLocatorSecret
	sanitized, err := json.Marshal(args)
	if err != nil {
		return nil, "encode element URL extraction request", false
	}
	return sanitized, "", true
}

func rewriteCuaRequestArgs(
	method string,
	name string,
	args json.RawMessage,
) []byte {
	request := struct {
		Method string          `json:"method,omitempty"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
	}{Method: method, Name: name, Args: args}
	encoded, _ := json.Marshal(request)
	return append(encoded, '\n')
}

// SetEnsureDaemon installs the respawn callback that handleConn falls
// back to when the private daemon socket dial fails. Called once from
// NewCuaDriverDaemon after the daemon struct is built so we can capture
// its bound EnsureRunning method. Safe to call multiple times — the
// most recent callback wins.
func (p *cuaDriverProxy) SetEnsureDaemon(fn func(context.Context) error) {
	p.ensureMu.Lock()
	p.ensureDaemon = fn
	p.ensureMu.Unlock()
}

func (p *cuaDriverProxy) getEnsureDaemon() func(context.Context) error {
	p.ensureMu.RLock()
	defer p.ensureMu.RUnlock()
	return p.ensureDaemon
}

// Start binds the canonical client socket and begins accepting. Safe
// to call multiple times — the first call wins, subsequent calls are
// no-ops. We bind synchronously (so callers know the canonical socket
// is reachable as soon as Start returns) but accept asynchronously.
func (p *cuaDriverProxy) Start() error {
	p.mu.Lock()
	if p.started {
		l := p.listener
		p.mu.Unlock()
		if l != nil && cuaUnixSocketReady(p.clientPath, 200*time.Millisecond) {
			return nil
		}
		p.mu.Lock()
		if p.started {
			if p.listener != nil {
				_ = p.listener.Close()
			}
			p.listener = nil
			p.started = false
			_ = os.Remove(p.clientPath)
		}
	}
	defer p.mu.Unlock()
	if p.started {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.clientPath), 0o755); err != nil {
		return fmt.Errorf("create cua proxy socket dir: %w", err)
	}
	// A stale socket file (left behind by a crashed previous run, or by
	// a previous Yansu instance that didn't get to Stop) blocks bind.
	// The canonical path is owned exclusively by Yansu's wails-gui
	// process; no other process should be bound here, so unlinking is
	// safe.
	_ = os.Remove(p.clientPath)
	l, err := net.Listen("unix", p.clientPath)
	if err != nil {
		return fmt.Errorf("listen on cua proxy socket %q: %w", p.clientPath, err)
	}
	// Lock the socket down to the user — the cua-driver daemon does
	// the same on its private path, and we want both endpoints to
	// reject foreign processes equally. macOS doesn't enforce socket
	// permissions everywhere, but it's a defence-in-depth nudge.
	_ = os.Chmod(p.clientPath, 0o600)
	p.listener = l
	p.started = true
	go p.acceptLoop(l)
	return nil
}

func (p *cuaDriverProxy) Ready() bool {
	return cuaUnixSocketReady(p.clientPath, 200*time.Millisecond)
}

// Stop closes the listener so Accept returns and in-flight handlers
// finish naturally on their existing deadlines. Safe to call multiple
// times; the second call sees started=false and bails.
func (p *cuaDriverProxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		return
	}
	if p.listener != nil {
		_ = p.listener.Close()
		p.listener = nil
	}
	p.started = false
	// Remove the socket file so the next Start doesn't trip over a
	// stale entry. We don't remove the daemon's private path — that's
	// the daemon's responsibility (handled in the reaper goroutine in
	// cua_driver_daemon.go).
	_ = os.Remove(p.clientPath)
}

func (p *cuaDriverProxy) acceptLoop(l net.Listener) {
	defer p.markListenerStopped(l)
	for {
		conn, err := l.Accept()
		if err != nil {
			// net.ErrClosed is the expected exit signal from Stop().
			// Anything else is unexpected; log and stop accepting (the
			// listener is in an unrecoverable state).
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("[cua-proxy] accept error: %v", err)
			return
		}
		go p.handleConn(conn)
	}
}

func (p *cuaDriverProxy) markListenerStopped(l net.Listener) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != l {
		return
	}
	p.listener = nil
	p.started = false
	_ = os.Remove(p.clientPath)
}

// handleConn reads a single line-delimited JSON request, applies the
// policy gate, and either writes a denial envelope back or forwards to
// the real daemon. Either way the client connection closes after the
// response — matching upstream's one-shot semantics.
func (p *cuaDriverProxy) handleConn(client net.Conn) {
	defer client.Close()

	// The deadline covers the whole request lifecycle (read request +
	// dial daemon + write request + read response + write response).
	// We grant the full call timeout plus a little headroom so a slow
	// `get_window_state` doesn't get cut off here when it would have
	// completed against the daemon directly.
	_ = client.SetDeadline(time.Now().Add(cuaDriverCallTimeout + 10*time.Second))

	// 64 KB initial buffer is enough for any inbound request; the
	// fattest known case (set_value with a multi-KB string) still fits
	// comfortably below it. Outbound from the daemon can be 4 MB+ for
	// screenshot blobs — that buffer lives on the daemon-side reader.
	reader := bufio.NewReaderSize(client, 64<<10)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		// Read errors don't get a response — the client has gone away
		// or the deadline expired, so writing back is futile.
		return
	}
	if len(line) == 0 {
		return
	}

	// Peel just enough of the request to inspect method/name/args. We
	// keep the original `line` bytes intact for forwarding so any
	// numeric precision or field ordering the caller relied on stays
	// untouched on the wire.
	var head struct {
		Method string          `json:"method"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
	}
	parsed := json.Unmarshal(line, &head) == nil

	// Audit hook: capture the frontmost app before we hand off so we
	// can flag focus changes after the daemon round-trip. Cheap call
	// (no allocation, no AX traversal) but skip it for non-call
	// methods to keep `list-tools` etc. from spamming logs.
	auditEnabled := parsed && (head.Method == "" || head.Method == "call")
	var beforePID int32
	var beforeBundle string
	var startedAt time.Time
	if auditEnabled {
		beforePID, beforeBundle, _ = cuaFrontmost()
		startedAt = time.Now()
	}

	if parsed {
		if args, denial, rewritten := p.authorizeSourceLocatorRequest(head.Name, head.Args); denial != "" {
			writeDenial(client, "yansu policy: "+denial)
			if auditEnabled {
				logCuaProxyEvent("denied", head.Name, head.Args, beforePID, beforeBundle, 0, "", 0, denial)
			}
			return
		} else if rewritten {
			head.Args = args
			line = rewriteCuaRequestArgs(head.Method, head.Name, args)
		}
		if denial := cuaPolicyDenial(head.Method, head.Name, head.Args); denial != "" {
			writeDenial(client, denial)
			if auditEnabled {
				logCuaProxyEvent("denied", head.Name, head.Args, beforePID, beforeBundle, 0, "", 0, denial)
			}
			return
		}
	}
	// A malformed JSON request falls through to the daemon — letting
	// the daemon return its native schema error keeps the LLM looking
	// at one error format instead of two.

	// Translate the request into the alma wire shape before dialling.
	// On a synthesized result (e.g. list-tools) we short-circuit and
	// hand the caller a canned response without touching the daemon.
	translated, originalTool, terr := p.translator.translateRequest(line)
	if synth, ok := terr.(*translatorSynthetic); ok {
		if body := p.translator.synthesizedResponse(synth.tool); body != nil {
			_, _ = client.Write(body)
			if auditEnabled {
				logCuaProxyEvent("synthesized", synth.tool, head.Args, beforePID, beforeBundle, beforePID, beforeBundle, time.Since(startedAt), "")
			}
			return
		}
	}
	if terr != nil && terr != errSynthesizedListTools && terr != errSynthesizedDescribe {
		writeDenial(client, fmt.Sprintf("yansu cua-proxy: translator error: %v", terr))
		return
	}

	daemon, err := net.DialTimeout("unix", p.daemonPath, 2*time.Second)
	if err != nil {
		// Daemon may have died (crash, reaper, user kill) or never come
		// up yet (race with eager start at app launch). If we have an
		// ensureDaemon callback, try to respawn once and re-dial. Caps
		// out at ~12s in the worst case (10s respawn ctx + 2s dial)
		// which still fits under the upstream call timeout.
		if respawn := p.getEnsureDaemon(); respawn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			respawnErr := respawn(ctx)
			cancel()
			if respawnErr == nil {
				daemon, err = net.DialTimeout("unix", p.daemonPath, 2*time.Second)
			} else {
				log.Printf("[cua-proxy] daemon respawn failed: %v", respawnErr)
			}
		}
		if err != nil {
			msg := fmt.Sprintf("yansu cua-proxy: cannot reach private daemon at %s: %v", p.daemonPath, err)
			writeDenial(client, msg)
			if auditEnabled {
				logCuaProxyEvent("daemon-unreachable", head.Name, head.Args, beforePID, beforeBundle, 0, "", 0, msg)
			}
			return
		}
	}
	defer daemon.Close()
	_ = daemon.SetDeadline(time.Now().Add(cuaDriverCallTimeout + 5*time.Second))

	if _, err := daemon.Write(translated); err != nil {
		// Don't try to write back — if the daemon-side write failed,
		// the client-side write probably will too, and we don't want
		// to mask the error with our own.
		return
	}

	// Daemon responses can carry a base64-encoded screenshot, so the
	// 4 MB read buffer mirrors the wails-gui Call() reader. Hard cap on
	// the bufio Reader is 16 MB by default, which we don't override —
	// any single response above that is a daemon bug worth surfacing.
	daemonReader := bufio.NewReaderSize(daemon, 4<<20)
	response, err := daemonReader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		writeDenial(client, fmt.Sprintf("yansu cua-proxy: read from private daemon failed: %v", err))
		return
	}
	if len(response) == 0 {
		writeDenial(client, "yansu cua-proxy: private daemon returned an empty response")
		return
	}
	// Translate the response back to the legacy envelope. Pass the
	// pre-translation tool name so per-tool reshaping (e.g. injecting
	// element_index on snap elements) lands on the right shape.
	translatedResp := p.translator.translateResponse(originalTool, response)
	_, _ = client.Write(translatedResp)

	if auditEnabled {
		afterPID, afterBundle, _ := cuaFrontmost()
		latency := time.Since(startedAt)
		logCuaProxyEvent("forwarded", head.Name, head.Args, beforePID, beforeBundle, afterPID, afterBundle, latency, "")
	}
}

// logCuaProxyEvent writes one structured line per CUA call to Yansu's
// log so we can post-mortem which tool/args pulled focus. The format
// is grep-friendly — the `[cua-proxy]` prefix matches the existing
// `[cua-driver]`/`[cua-skill]` tags so a single grep over wails-gui's
// log surfaces the whole CUA flow. We deliberately avoid JSON because
// `tail -f` readers benefit from a single human-readable line.
//
// The killer field is FOCUS-CHANGE: when the frontmost pid before and
// after differ, we tag the line with !!FOCUS-CHANGE!! so the user's
// next bug report (or grep) jumps straight to the offender.
func logCuaProxyEvent(outcome, toolName string, args json.RawMessage, beforePID int32, beforeBundle string, afterPID int32, afterBundle string, latency time.Duration, denial string) {
	argsBrief := briefArgs(args)
	switch outcome {
	case "denied":
		log.Printf("[cua-proxy] denied tool=%s args=%s before=%s(pid=%d) reason=%q", toolName, argsBrief, beforeBundle, beforePID, denial)
	case "daemon-unreachable":
		log.Printf("[cua-proxy] daemon-unreachable tool=%s args=%s before=%s(pid=%d) reason=%q", toolName, argsBrief, beforeBundle, beforePID, denial)
	case "forwarded":
		focusChanged := beforePID != 0 && afterPID != 0 && beforePID != afterPID
		switch {
		case focusChanged:
			log.Printf("[cua-proxy] !!FOCUS-CHANGE!! tool=%s args=%s before=%s(pid=%d) after=%s(pid=%d) latency=%s", toolName, argsBrief, beforeBundle, beforePID, afterBundle, afterPID, latency.Round(time.Millisecond))
		default:
			log.Printf("[cua-proxy] forwarded tool=%s args=%s frontmost=%s(pid=%d) latency=%s", toolName, argsBrief, beforeBundle, beforePID, latency.Round(time.Millisecond))
		}
	}
}

// briefArgs renders the args blob in a form short enough for log lines.
// We pick out the few fields that matter for diagnosing a focus pull
// (pid, window_id, element_index presence, x/y, key combo) and drop the
// rest — `text:"..."` for type_text can be huge, and dumping it would
// bury the signal under typing payloads.
func briefArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return "<unparseable>"
	}
	parts := make([]string, 0, 6)
	for _, k := range []string{"pid", "window_id", "bundle_id", "x", "y", "from_x", "from_y", "to_x", "to_y", "element_index"} {
		if v, ok := args[k]; ok && v != nil {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if v, ok := args["keys"]; ok {
		parts = append(parts, fmt.Sprintf("keys=%v", v))
	}
	if len(parts) == 0 {
		return "{...}"
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// writeDenial sends a response with the same shape cua-driver's own
// error path uses ({"ok":false,"error":"..."}), so callers can branch
// on `ok` without a translation table. We append the trailing newline
// the daemon protocol mandates — clients call ReadBytes('\n').
func writeDenial(client net.Conn, message string) {
	envelope := map[string]any{
		"ok":    false,
		"error": message,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		// Fallback: build the envelope by hand so a marshal failure
		// (vanishingly unlikely with a fixed shape) doesn't drop the
		// client without a response.
		encoded = []byte(`{"ok":false,"error":"yansu cua-proxy: internal marshal error"}`)
	}
	encoded = append(encoded, '\n')
	_, _ = client.Write(encoded)
}
