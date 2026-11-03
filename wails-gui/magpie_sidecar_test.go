package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake magpie is built once per test binary and shared by every test in
// this package: a full parallel run used to start one `go build` per test,
// and under that load some builds failed with a bare "exit status 1". The
// binary is only ever executed, so one shared build in a temp dir (removed by
// TestMain) is safe; each test keeps its own HOME and sidecar state through
// testSidecar.
var (
	fakeMagpieOnce sync.Once
	fakeMagpieBin  string
	fakeMagpieErr  error
)

// errGoToolchainMissing marks the no-go-toolchain case so the build helpers
// skip instead of fail, as before.
var errGoToolchainMissing = errors.New("go toolchain not on PATH")

// buildFakeMagpie returns the shared fake magpie binary, building it on first
// use.
func buildFakeMagpie(t *testing.T) string {
	t.Helper()
	bin, err := sharedFakeMagpie()
	if errors.Is(err, errGoToolchainMissing) {
		t.Skipf("%v", err)
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	return bin
}

func sharedFakeMagpie() (string, error) {
	fakeMagpieOnce.Do(func() {
		fakeMagpieBin, fakeMagpieErr = compileFakeMagpie()
	})
	return fakeMagpieBin, fakeMagpieErr
}

// compileFakeMagpie runs the one `go build` of testdata/fakemagpie and
// reports the compiler's stderr when the build fails.
func compileFakeMagpie() (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errGoToolchainMissing, err)
	}
	dir, err := os.MkdirTemp("", "fakemagpie-")
	if err != nil {
		return "", fmt.Errorf("create fake magpie build dir: %w", err)
	}
	out := filepath.Join(dir, "magpie-cli")
	cmd := exec.Command(goBin, "build", "-o", out, "./testdata/fakemagpie")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = "(compiler printed nothing)"
		}
		return "", fmt.Errorf("build fake magpie: %v\ncompiler stderr:\n%s", err, detail)
	}
	return out, nil
}

// removeSharedFakeMagpie deletes the shared build directory; TestMain calls
// it once the run is done.
func removeSharedFakeMagpie() {
	if fakeMagpieBin != "" {
		_ = os.RemoveAll(filepath.Dir(fakeMagpieBin))
	}
}

func TestBuildFakeMagpieIsSharedAcrossCallers(t *testing.T) {
	first := buildFakeMagpie(t)
	if got := buildFakeMagpie(t); got != first {
		t.Fatalf("fake magpie rebuilt per call: %q then %q", first, got)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("shared binary missing: %v", err)
	}
}

// testSidecar returns a sidecar that runs bin under a temp HOME, with fast
// timings so restart and degraded behaviour shows up in milliseconds.
func testSidecar(t *testing.T, bin string) *magpieSidecar {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := newMagpieSidecar(filepath.Join(home, configDirName, magpieStateDirName))
	s.binaryPath = bin
	// Never look for stale gateways at the real bundled locations: a test
	// must not stop magpie processes that belong to an app running here.
	s.bundled = func() []string { return nil }
	s.startDeadline = 10 * time.Second
	s.healthInterval = 100 * time.Millisecond
	s.backoffBase = 20 * time.Millisecond
	s.backoffMax = 200 * time.Millisecond
	s.stopGrace = 2 * time.Second
	t.Cleanup(s.Stop)
	return s
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func persistedPort(t *testing.T, s *magpieSidecar) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.stateDir, magpiePortFileName))
	if err != nil {
		t.Fatalf("read magpie.json: %v", err)
	}
	var record struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode magpie.json: %v", err)
	}
	return record.Port
}

func TestMagpieSidecarStartHealthAndStop(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.Healthy() {
		t.Fatal("expected healthy after Start")
	}
	if base := s.BaseURL(); !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("BaseURL = %q", base)
	}
	snap := s.Status()
	if !snap.Available || !snap.Running || !snap.Healthy || snap.Degraded {
		t.Fatalf("status = %+v", snap)
	}
	if snap.Version != "0.0.0-fake" {
		t.Fatalf("version = %q, want 0.0.0-fake", snap.Version)
	}
	pid := s.childProcessID()
	if pid == 0 {
		t.Fatal("no child pid")
	}

	s.Stop()
	if s.BaseURL() != "" || s.Healthy() || s.Status().Running {
		t.Fatalf("after Stop: base=%q healthy=%v status=%+v", s.BaseURL(), s.Healthy(), s.Status())
	}
	if s.childProcessID() != 0 {
		t.Fatalf("child pid still recorded after Stop")
	}
}

func TestMagpieSidecarStopIsSafeWhenNotStarted(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	s.Stop()
	s.Stop()
}

func TestMagpieSidecarRestartsAfterKill(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := s.childProcessID()
	if first == 0 {
		t.Fatal("no child pid")
	}
	process, err := os.FindProcess(first)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}

	waitFor(t, 10*time.Second, "restart after kill", func() bool {
		pid := s.childProcessID()
		return pid != 0 && pid != first && s.Healthy()
	})
	if s.Status().Degraded {
		t.Fatal("one restart must not mark the sidecar degraded")
	}
}

func TestMagpieSidecarKeepsPersistedPortAcrossRestarts(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	first := s.Status().Port
	s.Stop()

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(s.Stop)
	if got := s.Status().Port; got != first {
		t.Fatalf("port changed across restart: %d -> %d", first, got)
	}
	if got := persistedPort(t, s); got != first {
		t.Fatalf("magpie.json port = %d, want %d", got, first)
	}
}

func TestMagpieSidecarReplacesOccupiedPersistedPort(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	occupied := listener.Addr().(*net.TCPAddr).Port

	if err := s.ensureStateDirs(); err != nil {
		t.Fatalf("ensureStateDirs: %v", err)
	}
	data := []byte(`{"port":` + strconv.Itoa(occupied) + `}`)
	if err := os.WriteFile(filepath.Join(s.stateDir, magpiePortFileName), data, 0o600); err != nil {
		t.Fatalf("write magpie.json: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := s.Status().Port; got == occupied {
		t.Fatalf("sidecar reused the occupied port %d", occupied)
	}
	if got := persistedPort(t, s); got == occupied {
		t.Fatalf("magpie.json still holds the occupied port %d", occupied)
	}
}

func TestMagpieSidecarNeverChoosesReservedPort(t *testing.T) {
	s := testSidecar(t, "")
	if err := s.ensureStateDirs(); err != nil {
		t.Fatalf("ensureStateDirs: %v", err)
	}
	data := []byte(`{"port":` + strconv.Itoa(magpieReservedPort) + `}`)
	if err := os.WriteFile(filepath.Join(s.stateDir, magpiePortFileName), data, 0o600); err != nil {
		t.Fatalf("write magpie.json: %v", err)
	}
	port, err := s.choosePort()
	if err != nil {
		t.Fatalf("choosePort: %v", err)
	}
	if port == magpieReservedPort || port <= 0 {
		t.Fatalf("choosePort = %d, want a free port other than %d", port, magpieReservedPort)
	}
	if got := persistedPort(t, s); got != port {
		t.Fatalf("persisted port = %d, want %d", got, port)
	}
}

func TestMagpieSidecarDegradedAfterRepeatedExits(t *testing.T) {
	t.Setenv("FAKE_MAGPIE_EXIT", "1")
	s := testSidecar(t, buildFakeMagpie(t))
	s.startDeadline = 300 * time.Millisecond

	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start should report that the gateway never answered")
	}
	waitFor(t, 10*time.Second, "degraded after repeated exits", func() bool {
		return s.Status().Degraded
	})
	if s.Healthy() {
		t.Fatal("a sidecar that keeps exiting must not report healthy")
	}
	if s.Status().LastError == "" {
		t.Fatal("expected the last error to be recorded")
	}
}

func TestMagpieSidecarMissingBinaryIsTypedError(t *testing.T) {
	s := testSidecar(t, filepath.Join(t.TempDir(), "absent-magpie"))
	err := s.Start(context.Background())
	var notFound *MagpieNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Start error = %v, want *MagpieNotFoundError", err)
	}
	snap := s.Status()
	if snap.Available || snap.Running || s.BaseURL() != "" {
		t.Fatalf("missing binary must leave the sidecar unavailable: %+v base=%q", snap, s.BaseURL())
	}
	if snap.LastError == "" {
		t.Fatal("expected LastError to describe the missing binary")
	}
}

func TestLocateBundledMagpieHonorsOverride(t *testing.T) {
	bin := buildFakeMagpie(t)
	t.Setenv(magpieOverrideEnv, bin)
	got, err := LocateBundledMagpie()
	if err != nil || got != bin {
		t.Fatalf("LocateBundledMagpie() = %q, %v; want %q", got, err, bin)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv(magpieOverrideEnv, missing)
	_, err = LocateBundledMagpie()
	var notFound *MagpieNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("override to a missing file must be a typed error, got %v", err)
	}
}

func TestMagpieCommandEnvOverridesInheritedValues(t *testing.T) {
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:1")
	t.Setenv("DO_NOT_TRACK", "0")
	s := newMagpieSidecar(filepath.Join(t.TempDir(), "magpie"))

	env := s.commandEnv(4567)
	counts := map[string][]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			counts[kv[:i]] = append(counts[kv[:i]], kv[i+1:])
		}
	}
	want := map[string]string{
		"MAGPIE_ADDR":     "127.0.0.1:4567",
		"DO_NOT_TRACK":    "1",
		"MAGPIE_NO_STATS": "1",
	}
	for key, value := range want {
		values := counts[key]
		if len(values) != 1 || values[0] != value {
			t.Fatalf("%s = %v, want exactly [%s]", key, values, value)
		}
	}
	if values := counts["XDG_CONFIG_HOME"]; len(values) != 1 || !strings.HasSuffix(values[0], "config") {
		t.Fatalf("XDG_CONFIG_HOME = %v", values)
	}
	if values := counts["XDG_CACHE_HOME"]; len(values) != 1 || !strings.HasSuffix(values[0], "cache") {
		t.Fatalf("XDG_CACHE_HOME = %v", values)
	}
}

func TestMaskMagpieArgsHidesKeys(t *testing.T) {
	got := maskMagpieArgs([]string{"provider", "add", "yansu-byok", "anthropic=https://x", "key=sk-secret", "models=m"})
	if strings.Contains(got, "sk-secret") {
		t.Fatalf("key leaked into %q", got)
	}
	if !strings.Contains(got, "key=***") || !strings.Contains(got, "models=m") {
		t.Fatalf("masked args = %q", got)
	}
}
