package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// cua_driver_daemon.go owns the lifecycle of the bundled cua-driver
// helper running in `daemon` mode. The helper listens on a Unix domain
// socket scoped to the current user; we speak NDJSON to it (one
// request/response object per line) per Protocol.swift.
//
// Running as a long-lived daemon is the only way snapshot-driven
// workflows work end-to-end: every fresh CLI invocation gets its own
// AppStateEngine + ElementStore, so an element `ref` returned by one
// `snap` call won't resolve in the next process. The daemon keeps that
// cache hot across calls.
//
// We launch the binary in-process via exec.Command (posix_spawn), not
// via LaunchServices (`open -n -g -a`). This keeps the daemon's TCC
// responsibility at the parent — Yansu — so the helper inherits
// Yansu's existing Accessibility + Screen Recording grants without a
// fresh first-launch prompt. See project_tcc_inheritance_posix_spawn
// in memory for the why.

// cuaSocketPathMaxLen mirrors macOS's sockaddr_un.sun_path limit. Paths
// longer than this can't bind, and the failure mode is a confusing EINVAL
// — better to surface it early. We never silently fall back to a /tmp
// socket on overflow because the yansu-agent side dials the canonical
// path verbatim; a divergence between the two sides would mean the LLM
// tool can't reach the daemon Yansu started. Surface the error instead
// so the caller can either tell the user to shorten $HOME or we can
// teach yansu-agent the same fallback (currently unnecessary — even
// pathological macOS home directories sit well under 100 bytes).
const cuaSocketPathMaxLen = 100

// cuaDriverCallTimeout caps how long any single tool call can run before
// we give up on the response. cua-driver tools are mostly snappy, but
// `launch_app` against a slow-starting target plus AX-tree warm-up can
// take a few seconds; 30s is generous without leaving a stuck handler.
var cuaDriverCallTimeout = 30 * time.Second

// Spawn-backoff knobs. Failure here is "we tried to start the daemon
// and it didn't reach the canonical good state (socket bound) within
// the boot window," or "the daemon died within earlyDeathWindow of
// being spawned." A daemon that runs healthily for minutes and then
// crashes is NOT a spawn failure — that's normal life and we restart
// without backoff so an in-flight chat session recovers immediately.
const (
	cuaDriverSpawnInitialBackoff = 1 * time.Second
	cuaDriverSpawnMaxBackoff     = 30 * time.Second
	// earlyDeathWindow is how long after spawn an exit still counts as a
	// failure (suggests "binary broken" / "TCC denied at boot"). 5s is a
	// loose superset of cua-driver's actual boot path (low hundreds of
	// ms) without false-positiving on a daemon that took an unusually
	// long time to fully come up.
	cuaDriverEarlyDeathWindow = 5 * time.Second
)

// CuaDriverDaemon manages a single `cua-driver serve` subprocess.
//
// Two socket paths are involved. The daemon binds `internalSocketPath`
// (the private path); the policy proxy binds `clientSocketPath` (the
// canonical path every external caller dials) and forwards through to
// the internal one after applying the no-foreground gate. See
// cua_driver_proxy.go for the validator and protocol details.
type CuaDriverDaemon struct {
	binaryPath         string
	clientSocketPath   string // canonical, what callers dial — proxy binds here
	internalSocketPath string // private, what cua-driver --socket binds

	proxy *cuaDriverProxy

	mu       sync.Mutex
	cmd      *exec.Cmd
	startErr error
	starting bool

	// Spawn-backoff state. All fields read / written under mu.
	backoff       time.Duration // current backoff, 0 when last spawn succeeded
	nextSpawnOK   time.Time     // earliest time at which we'll attempt another spawn
	lastSpawnAt   time.Time     // start time of the current/most-recent spawn
	socketTimeout bool          // waitForSocket gave up on the most recent spawn
	stopping      bool          // Stop() was called; reaper should not bump backoff
}

// CuaDriverResponse is the envelope cua-driver returns on the daemon
// socket. Successful calls fill Result; failures fill Error + ExitCode.
//
// Error is a RawMessage rather than a string because the on-wire shape
// is "string when the policy proxy translated it, object when the bare
// alma daemon returned it directly" (the proxy flattens
// {code,message,hint} into a string in cua_translator.go's
// translateResponse). RawMessage handles both; callers should read it
// through ErrorMessage() rather than format it themselves.
type CuaDriverResponse struct {
	OK       bool            `json:"ok"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    json.RawMessage `json:"error,omitempty"`
	ExitCode int             `json:"exitCode,omitempty"`
}

// ErrorMessage decodes whatever shape Error carries into a flat string
// suitable for log lines and user-visible errors. Empty when the
// response carries no error. The string form lands as-is; the alma
// object form ({code, message, hint}) is joined into "message (hint)
// [code]" so all three signals survive.
func (r *CuaDriverResponse) ErrorMessage() string {
	if r == nil || len(r.Error) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(r.Error, &s); err == nil {
		return s
	}
	var obj struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal(r.Error, &obj); err == nil {
		out := obj.Message
		if obj.Hint != "" {
			if out != "" {
				out += " (" + obj.Hint + ")"
			} else {
				out = obj.Hint
			}
		}
		if obj.Code != "" {
			if out != "" {
				out += " [" + obj.Code + "]"
			} else {
				out = obj.Code
			}
		}
		return out
	}
	return string(r.Error)
}

// NewCuaDriverDaemon creates an unstarted daemon handle pointing at the
// bundled cua-driver binary. Call EnsureRunning before invoking tools.
func NewCuaDriverDaemon() (*CuaDriverDaemon, error) {
	paths, err := LocateBundledCuaDriver()
	if err != nil {
		return nil, err
	}

	clientSock, err := cuaDaemonSocketPath()
	if err != nil {
		return nil, err
	}
	internalSock, err := cuaDaemonInternalSocketPath()
	if err != nil {
		return nil, err
	}

	d := &CuaDriverDaemon{
		binaryPath:         paths.BinaryPath,
		clientSocketPath:   clientSock,
		internalSocketPath: internalSock,
	}
	// Build the proxy after the daemon struct exists so we can wire its
	// auto-recovery callback to d.EnsureRunning. Without this, a daemon
	// that dies between calls leaves the proxy bound but unable to bring
	// the daemon back — every subsequent call returns "cannot reach
	// private daemon" until something else triggers EnsureRunning.
	d.proxy = newCuaDriverProxy(clientSock, internalSock)
	d.proxy.SetEnsureDaemon(d.EnsureRunning)
	return d, nil
}

// EnsureRunning starts the daemon if it's not already running and waits
// up to ctx's deadline for the socket to become connectable. Subsequent
// calls are cheap — when the socket already accepts connections we
// short-circuit without touching the lock.
//
// When recent spawns have failed (binary corrupt, TCC denied at boot,
// etc.) we apply exponential backoff so a flood of incoming calls
// can't fork-bomb us against a broken binary. A long-running daemon
// that finally dies of natural causes is NOT a spawn failure — see
// the reaper goroutine for the early-death detection.
func (d *CuaDriverDaemon) EnsureRunning(ctx context.Context) error {
	if d.socketReady() {
		if d.ownsRunningDaemon() {
			return d.ensureProxyRunning(ctx)
		}
		if err := d.shutdownExistingDaemon(ctx); err != nil {
			log.Printf("[cua-driver] existing daemon at %s was not owned by this Yansu process and did not shut down cleanly: %v", d.internalSocketPath, err)
		}
	}

	d.mu.Lock()
	if d.cmd != nil && d.cmd.Process != nil {
		// Another goroutine started it while we were waiting on the lock.
		d.mu.Unlock()
		return d.waitForSocketAndProxy(ctx)
	}
	if d.starting {
		d.mu.Unlock()
		return d.waitForSocketAndProxy(ctx)
	}
	if backoff := d.remainingBackoffLocked(); backoff > 0 {
		d.mu.Unlock()
		// Surface the wait window so callers (and the user) can tell a
		// real outage from a transient blip. The error mirrors what an
		// HTTP 503 should carry.
		return fmt.Errorf("cua-driver daemon is in spawn backoff for %s after recent failures (binary broken or TCC denied?)", backoff.Round(100*time.Millisecond))
	}
	d.starting = true
	d.socketTimeout = false
	// Clear stopping in case a previous Stop() left it set and the
	// caller is now spawning a fresh daemon. Without this the new
	// daemon's reaper would never bump backoff on early death,
	// because Stop's "exit was requested" sticky flag would still
	// suppress it. Today the only Stop call site is app shutdown
	// (so this branch is theoretical), but tests and future restart
	// flows depend on Stop() being non-permanent.
	d.stopping = false
	d.lastSpawnAt = time.Now()
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.starting = false
		d.mu.Unlock()
	}()

	// A stale socket file (left behind by a crashed previous run) blocks
	// the new daemon from binding. Remove it before spawning so we never
	// fight a ghost. Same goes for the proxy's canonical path — the
	// proxy's own Start() also unlinks it, but doing it here too keeps
	// EnsureRunning's contract self-contained.
	_ = os.Remove(d.internalSocketPath)
	_ = os.Remove(d.clientSocketPath)
	if err := os.MkdirAll(filepath.Dir(d.internalSocketPath), 0o755); err != nil {
		return fmt.Errorf("create cua-driver run dir: %w", err)
	}

	// Match Alma's daemon lifetime: keep the helper warm for normal
	// interaction bursts, but let it idle-exit instead of leaving a
	// never-ending orphan after a crash. The proxy respawns it on demand.
	//
	// The virtual-cursor lens overlay is gated by the user's setting
	// (shouldEnableCuaCursorOverlay, persisted in config). We bake the
	// decision into argv at spawn time so cold starts, idle respawns, and
	// crash recoveries all honour the current setting. Runtime toggles
	// additionally hot-apply via the `lens` command — see
	// SetCuaCursorOverlayEnabled in cua_cursor_overlay_config.go.
	daemonArgs := []string{"daemon", "--socket", d.internalSocketPath, "--idle-seconds", "900"}
	if !shouldEnableCuaCursorOverlay() {
		daemonArgs = append(daemonArgs, "--no-lens")
	}
	cmd := exec.Command(d.binaryPath, daemonArgs...)
	if d.proxy != nil && d.proxy.sourceLocatorSecret != "" {
		cmd.Env = make([]string, 0, len(os.Environ())+1)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, cuaSourceLocatorSecretEnv+"=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(
			cmd.Env,
			cuaSourceLocatorSecretEnv+"="+d.proxy.sourceLocatorSecret,
		)
	}
	// Detach stdin (the daemon doesn't read it; leaving it tied to ours
	// confuses some shells when Yansu is launched from a terminal).
	cmd.Stdin = nil
	// Pipe stderr through our logger so cua-driver crash messages and
	// permission warnings show up in Yansu's log file. We discard stdout
	// because cua-driver's only stdout output is the "daemon started"
	// banner — useful at boot, noisy thereafter.
	stderr, _ := cmd.StderrPipe()
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		// exec.Start failures are pure spawn failures (e.g. binary
		// missing or unexecutable). Always count toward backoff.
		d.mu.Lock()
		d.bumpBackoffLocked()
		d.mu.Unlock()
		return fmt.Errorf("start cua-driver daemon: %w", err)
	}

	d.mu.Lock()
	d.cmd = cmd
	d.startErr = nil
	d.mu.Unlock()

	if stderr != nil {
		go d.pumpStderr(stderr)
	}

	// Reap the process when it exits so we don't leak a zombie. Two
	// distinct exit shapes to differentiate:
	//
	//  1. EARLY DEATH — exit happens within earlyDeathWindow of spawn,
	//     OR the spawn never reached "socket bound" status (waitForSocket
	//     timed out and set d.socketTimeout). This is a spawn failure;
	//     bump backoff so the next call can't immediately re-fork.
	//
	//  2. NORMAL EXIT — exit happens after the daemon ran healthily for
	//     a while. Could be a real crash hours in, or somebody SIGKILL'd
	//     it from outside. Don't bump backoff — let the next call spawn
	//     a fresh daemon immediately so the user's chat session isn't
	//     punished for a one-off issue.
	//
	// Stop() bypasses both paths via d.stopping, since we never want a
	// graceful shutdown to discourage the next launch.
	go func() {
		err := cmd.Wait()
		d.mu.Lock()
		d.cmd = nil
		if err != nil && !errors.Is(err, exec.ErrNotFound) {
			d.startErr = err
		}
		earlyDeath := time.Since(d.lastSpawnAt) < cuaDriverEarlyDeathWindow
		stoppedExplicitly := d.stopping
		switch {
		case stoppedExplicitly:
			// Don't touch backoff; we asked for this exit.
		case d.socketTimeout || earlyDeath:
			d.bumpBackoffLocked()
		default:
			// Healthy daemon dying of natural causes — reset so the next
			// call spawns immediately without waiting on a stale backoff.
			d.backoff = 0
			d.nextSpawnOK = time.Time{}
		}
		d.mu.Unlock()
		switch {
		case err != nil:
			log.Printf("[cua-driver] daemon exited: %v", err)
		default:
			log.Printf("[cua-driver] daemon exited cleanly")
		}
		_ = os.Remove(d.internalSocketPath)
	}()

	if err := d.waitForSocket(ctx); err != nil {
		// Tell the reaper this counts as an early death even if the
		// process keeps running past the deadline (it'll see the flag
		// when cmd.Wait returns). Also kill the process so the reaper
		// doesn't sit waiting indefinitely on a half-up daemon.
		d.mu.Lock()
		d.socketTimeout = true
		proc := d.cmd
		d.mu.Unlock()
		if proc != nil && proc.Process != nil {
			_ = proc.Process.Kill()
		}
		return err
	}
	// Socket is up — the spawn succeeded. Clear backoff so any prior
	// failure history doesn't follow us into a future retry that the
	// daemon might hit naturally.
	d.mu.Lock()
	d.backoff = 0
	d.nextSpawnOK = time.Time{}
	d.mu.Unlock()

	// Stand up the policy proxy on the canonical client path. Doing it
	// AFTER waitForSocket succeeded guarantees the daemon is reachable
	// before any caller can dial through us — otherwise a fast-running
	// LLM tool could hit the proxy in between bind and daemon-up and
	// see a confusing "cannot reach private daemon" denial. proxy.Start
	// is idempotent so it's safe to call on every successful spawn.
	if d.proxy != nil {
		if err := d.ensureProxyRunning(ctx); err != nil {
			// Proxy failure is fatal for our policy contract — without
			// the proxy, callers would be talking to the bare daemon
			// and the no-foreground rules wouldn't be enforced. Tear
			// down the spawned daemon so EnsureRunning's failure mode
			// is "nothing is listening" rather than "policy bypassed".
			d.mu.Lock()
			proc := d.cmd
			d.mu.Unlock()
			if proc != nil && proc.Process != nil {
				_ = proc.Process.Kill()
			}
			return err
		}
	}
	return nil
}

func (d *CuaDriverDaemon) ownsRunningDaemon() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cmd != nil && d.cmd.Process != nil
}

func (d *CuaDriverDaemon) waitForSocketAndProxy(ctx context.Context) error {
	if err := d.waitForSocket(ctx); err != nil {
		return err
	}
	return d.ensureProxyRunning(ctx)
}

func (d *CuaDriverDaemon) ensureProxyRunning(ctx context.Context) error {
	if d.proxy == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.proxy.Ready() {
		return nil
	}
	if err := d.proxy.Start(); err != nil {
		return fmt.Errorf("start cua policy proxy: %w", err)
	}
	if d.proxy.Ready() {
		return nil
	}
	return fmt.Errorf("start cua policy proxy: socket %s did not accept connections", d.clientSocketPath)
}

// remainingBackoffLocked returns how long the caller still has to wait
// before another spawn attempt is allowed. Caller must hold d.mu.
func (d *CuaDriverDaemon) remainingBackoffLocked() time.Duration {
	if d.nextSpawnOK.IsZero() {
		return 0
	}
	wait := time.Until(d.nextSpawnOK)
	if wait < 0 {
		return 0
	}
	return wait
}

// bumpBackoffLocked grows the backoff window after a failed spawn.
// Caller must hold d.mu. Doubles the previous backoff (or starts from
// the initial value) and caps at the max so a wedged daemon eventually
// settles into a steady "try every 30s" rhythm rather than growing
// unboundedly.
func (d *CuaDriverDaemon) bumpBackoffLocked() {
	switch {
	case d.backoff <= 0:
		d.backoff = cuaDriverSpawnInitialBackoff
	default:
		d.backoff *= 2
		if d.backoff > cuaDriverSpawnMaxBackoff {
			d.backoff = cuaDriverSpawnMaxBackoff
		}
	}
	d.nextSpawnOK = time.Now().Add(d.backoff)
}

func (d *CuaDriverDaemon) pumpStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		log.Printf("[cua-driver:stderr] %s", scanner.Text())
	}
}

// waitForSocket polls the socket path until it accepts a connection or
// the context expires. We poll because cua-driver doesn't write a
// ready-marker file on its own — but `cmd.Start` returns before the
// socket is bound, so callers would otherwise race against the daemon's
// boot.
func (d *CuaDriverDaemon) waitForSocket(ctx context.Context) error {
	deadline := time.Now().Add(8 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	for time.Now().Before(deadline) {
		if d.socketReady() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	d.mu.Lock()
	startErr := d.startErr
	d.mu.Unlock()
	if startErr != nil {
		return fmt.Errorf("cua-driver daemon failed to bind socket: %w", startErr)
	}
	return fmt.Errorf("cua-driver daemon socket %s did not appear within timeout", d.internalSocketPath)
}

// socketReady probes the daemon's PRIVATE socket — that's what tells
// us cua-driver itself is up. The proxy comes up later in EnsureRunning
// after this returns true, so checking the canonical path here would
// race the proxy bind on cold boot.
func (d *CuaDriverDaemon) socketReady() bool {
	return cuaUnixSocketReady(d.internalSocketPath, 200*time.Millisecond)
}

func cuaUnixSocketReady(path string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (d *CuaDriverDaemon) shutdownExistingDaemon(ctx context.Context) error {
	conn, err := net.DialTimeout("unix", d.internalSocketPath, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
	if dl, ok := ctx.Deadline(); ok && dl.Before(time.Now().Add(1*time.Second)) {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write([]byte(`{"id":"shutdown","cmd":"shutdown"}` + "\n")); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, conn)

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !d.socketReady() {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

// SocketPath returns the canonical client-facing socket path. Diagnostic
// callers ("send me your cua-driver socket path") get the path that any
// consumer of the daemon should dial — i.e. the proxy endpoint, not the
// private daemon path. Treating the proxy as the public endpoint keeps
// the no-foreground policy enforced even when an LLM bash-shells into
// the socket directly.
func (d *CuaDriverDaemon) SocketPath() string {
	return d.clientSocketPath
}

// Call invokes a tool against the daemon. Each call opens a fresh
// connection — cua-driver's protocol is one-request-per-connection, so
// pooling would just complicate failure modes (a stale conn carries no
// useful state because element-index cache lives on the daemon, not the
// wire).
func (d *CuaDriverDaemon) Call(ctx context.Context, name string, args any) (*CuaDriverResponse, error) {
	if err := d.EnsureRunning(ctx); err != nil {
		return nil, err
	}

	// Default args to an empty object — cua-driver tools that accept zero
	// arguments still require args to be present per the schema.
	if args == nil {
		args = map[string]any{}
	}

	req := struct {
		Method string `json:"method"`
		Name   string `json:"name"`
		Args   any    `json:"args"`
	}{
		Method: "call",
		Name:   name,
		Args:   args,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal cua-driver request: %w", err)
	}
	payload = append(payload, '\n')

	deadline := time.Now().Add(cuaDriverCallTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	// Dial the canonical client socket so this in-process call goes
	// through the policy proxy too. Skipping the proxy for in-process
	// calls would leave a hole — wails-gui's HTTP handlers and any
	// renderer-side bindings would all bypass the no-foreground gate.
	conn, err := net.DialTimeout("unix", d.clientSocketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial cua-driver: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("write cua-driver request: %w", err)
	}

	// Reader buffer is generous on purpose — `get_window_state` returns a
	// JSON-encoded screenshot (PNG → base64 → JSON) plus the AX tree, so
	// a 4 MB working buffer with 16 MB hard cap keeps the LLM happy on
	// big windows like full-screen browsers.
	reader := bufio.NewReaderSize(conn, 4<<20)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read cua-driver response: %w", err)
	}
	if len(line) == 0 {
		return nil, fmt.Errorf("cua-driver returned empty response")
	}

	var resp CuaDriverResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode cua-driver response: %w (raw: %.200s)", err, string(line))
	}
	return &resp, nil
}

// CallWithSourceLocators is the only in-process path that can request AX URL
// attributes. A one-use proxy capability is exchanged for the daemon-only
// secret at the public socket, so ordinary cua_driver and raw socket callers
// cannot widen a normal accessibility snapshot.
func (d *CuaDriverDaemon) CallWithSourceLocators(
	ctx context.Context,
	name string,
	args any,
) (*CuaDriverResponse, error) {
	if d == nil || d.proxy == nil {
		return nil, errors.New("Computer Use source-locator proxy is unavailable")
	}
	if name != "get_window_state" {
		return nil, errors.New("Computer Use source locators require get_window_state")
	}
	if err := d.EnsureRunning(ctx); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal source-locator args: %w", err)
	}
	arguments := map[string]any{}
	if len(encoded) > 0 && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &arguments); err != nil {
			return nil, fmt.Errorf("decode source-locator args: %w", err)
		}
	}
	capability, err := d.proxy.issueSourceLocatorCapability()
	if err != nil {
		return nil, err
	}
	arguments[cuaIncludeElementURLsArg] = true
	arguments[cuaSourceLocatorCapabilityArg] = capability
	return d.Call(ctx, name, arguments)
}

// issueToolSourceLocatorCapability hands the cua_driver tool a one-use
// capability for element addresses in one get_window_state snapshot. The
// tool asks only for unattended runs, whose browser reads are page-checked.
func (d *CuaDriverDaemon) issueToolSourceLocatorCapability() (string, error) {
	if d == nil || d.proxy == nil {
		return "", errors.New("Computer Use source-locator proxy is unavailable")
	}
	return d.proxy.issueSourceLocatorCapability()
}

// Stop attempts a graceful shutdown via the protocol's `shutdown` method,
// then SIGTERMs the process if it doesn't exit within the grace window.
// Safe to call multiple times.
func (d *CuaDriverDaemon) Stop(ctx context.Context) {
	defer func() {
		if d.proxy != nil {
			d.proxy.Stop()
		}
	}()

	d.mu.Lock()
	cmd := d.cmd
	// Tell the reaper this exit was requested — without the flag, the
	// reaper would (correctly) interpret a sub-earlyDeathWindow exit
	// as a spawn failure and trip backoff for any future EnsureRunning,
	// e.g. on the next launch where lastSpawnAt is stale-from-disk.
	d.stopping = true
	d.mu.Unlock()
	if cmd == nil {
		return
	}

	// Best-effort graceful shutdown. We don't care about the response —
	// the daemon may have already torn down the socket by the time we
	// connect, in which case we fall through to SIGTERM. Speak to the
	// private daemon path directly so a half-stopped proxy doesn't
	// swallow the shutdown message en route. Protocol.swift expects
	// {"id":...,"cmd":"shutdown"} NDJSON; the id is opaque to the
	// daemon side but required so the response can be correlated.
	if conn, err := net.DialTimeout("unix", d.internalSocketPath, 500*time.Millisecond); err == nil {
		_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
		_, _ = conn.Write([]byte(`{"id":"shutdown","cmd":"shutdown"}` + "\n"))
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}

	// Wait briefly for the daemon to exit on its own.
	done := make(chan struct{})
	go func() {
		// cmd.Wait is already pending in the goroutine spawned by
		// EnsureRunning; observe d.cmd flipping to nil instead.
		for {
			d.mu.Lock()
			alive := d.cmd != nil
			d.mu.Unlock()
			if !alive {
				close(done)
				return
			}
			select {
			case <-ctx.Done():
				close(done)
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()

	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
	}

	// Fallback — the daemon ignored our shutdown message. Send SIGTERM
	// so we don't leak a process across Yansu restarts.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	// The deferred proxy stop runs last so any in-flight handlers finish
	// writing to the daemon (or to the client) before the listener disappears.
}

func cuaDaemonSocketPath() (string, error) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	// Must stay byte-identical with yansu-agent/agent/tools/cua_driver.go's
	// cuaDaemonSocketPath — the LLM tool dials this exact path. If you
	// ever need to relocate the socket, change both files in the same
	// commit, or thread the path through an env var so they share a
	// source of truth.
	//
	// This is the *canonical* client-facing path: the policy proxy
	// listens here. The real daemon binds the sibling path returned by
	// cuaDaemonInternalSocketPath. From the LLM's perspective nothing
	// changes — they dial the same path they always have.
	path := filepath.Join(dataRoot, "run", "cua-driver.sock")
	if len(path) > cuaSocketPathMaxLen {
		return "", fmt.Errorf("cua-driver socket path %q exceeds the %d-byte sockaddr_un limit; shorten your home directory or report this", path, cuaSocketPathMaxLen)
	}
	return path, nil
}

// cuaDaemonInternalSocketPath is where the cua-driver subprocess
// actually binds. Only the policy proxy in cua_driver_proxy.go dials
// this — every other caller (LLM tools, Bash shell-outs, the Wails
// HTTP handler) goes through the canonical path so the
// no-foreground policy is enforced at the wire boundary. We rename
// the file (rather than putting it in a sibling directory) so the
// `.sock` suffix and ~/.yansu-agent/run prefix stay consistent for
// debugging — `lsof` lists both endpoints next to each other.
func cuaDaemonInternalSocketPath() (string, error) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	path := filepath.Join(dataRoot, "run", "cua-driver.daemon.sock")
	if len(path) > cuaSocketPathMaxLen {
		return "", fmt.Errorf("cua-driver internal socket path %q exceeds the %d-byte sockaddr_un limit; shorten your home directory or report this", path, cuaSocketPathMaxLen)
	}
	return path, nil
}
