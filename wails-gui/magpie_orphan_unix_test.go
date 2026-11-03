//go:build !windows

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// buildBundledFakeMagpie stages the shared fake magpie build at a path shaped
// like the app's bundled copy (.../magpie-bundle/magpie-cli), which is the
// only kind of binary the stale-child cleanup is allowed to stop. Like
// buildFakeMagpie it is prepared once per test binary.
var (
	bundledFakeMagpieOnce sync.Once
	bundledFakeMagpieBin  string
	bundledFakeMagpieErr  error
)

func buildBundledFakeMagpie(t *testing.T) string {
	t.Helper()
	bundledFakeMagpieOnce.Do(func() {
		var src string
		src, bundledFakeMagpieErr = sharedFakeMagpie()
		if bundledFakeMagpieErr != nil {
			return
		}
		// Stage inside the shared build dir so TestMain's cleanup removes both.
		dir := filepath.Join(filepath.Dir(src), "Isoform.app", "Contents", "Resources", magpieBundleDirName)
		if bundledFakeMagpieErr = os.MkdirAll(dir, 0o755); bundledFakeMagpieErr != nil {
			return
		}
		data, err := os.ReadFile(src)
		if err != nil {
			bundledFakeMagpieErr = err
			return
		}
		bundledFakeMagpieBin = filepath.Join(dir, "magpie-cli")
		bundledFakeMagpieErr = os.WriteFile(bundledFakeMagpieBin, data, 0o755)
	})
	if errors.Is(bundledFakeMagpieErr, errGoToolchainMissing) {
		t.Skipf("%v", bundledFakeMagpieErr)
	}
	if bundledFakeMagpieErr != nil {
		t.Fatalf("stage bundled fake magpie: %v", bundledFakeMagpieErr)
	}
	return bundledFakeMagpieBin
}

// startOrphanedMagpie runs "<bin> serve" the way an app that was killed
// leaves it: detached, with parent PID 1, listening on port.
func startOrphanedMagpie(t *testing.T, bin string, port int, extraEnv ...string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `"$0" serve >/dev/null 2>&1 & echo $!`, bin)
	cmd.Env = append(os.Environ(), "MAGPIE_ADDR="+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start orphan: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("orphan pid from %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	waitFor(t, 10*time.Second, "orphan to listen", func() bool {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if ppid, err := getProcessPPID(pid); err == nil && ppid == 1 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Skip("this system does not reparent orphans to PID 1")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func processGone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	// Container PID 1 may leave terminated orphans unreaped. A zombie has
	// exited and released its listener; it is not a running gateway.
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err == nil {
			at := strings.LastIndex(string(raw), ") ")
			return at >= 0 && len(raw) > at+2 && raw[at+2] == 'Z'
		}
	}
	return false
}

// gatewayAnswers reports whether something accepts connections on port. Unlike
// signalling a pid, it cannot be fooled by a zombie: a killed child that its
// parent never waited on still answers kill(pid, 0) but no longer listens.
func gatewayAnswers(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForGateway(t *testing.T, port int) {
	t.Helper()
	waitFor(t, 10*time.Second, "gateway to listen", func() bool { return gatewayAnswers(port) })
}

func TestIsOrphanedMagpieServeRequiresDetachedExactBundledBinary(t *testing.T) {
	const binary = "/Applications/Isoform.app/Contents/Resources/magpie-bundle/magpie-cli"
	const userBinary = "/Users/me/.local/bin/magpie"
	tests := []struct {
		name       string
		currentPID int
		pid        int
		parentPID  int
		command    string
		binary     string
		want       bool
	}{
		{name: "detached bundled gateway", currentPID: 100, pid: 200, parentPID: 1, command: binary + " serve", binary: binary, want: true},
		{name: "still has a live parent", currentPID: 100, pid: 200, parentPID: 100, command: binary + " serve", binary: binary},
		{name: "other subcommand", currentPID: 100, pid: 200, parentPID: 1, command: binary + " groups", binary: binary},
		{name: "extra arguments", currentPID: 100, pid: 200, parentPID: 1, command: binary + " serve --port 1", binary: binary},
		{name: "different binary", currentPID: 100, pid: 200, parentPID: 1, command: "/tmp/magpie-bundle/magpie-cli serve", binary: binary},
		{name: "user installed magpie", currentPID: 100, pid: 200, parentPID: 1, command: userBinary + " serve", binary: userBinary},
		{name: "current process", currentPID: 100, pid: 100, parentPID: 1, command: binary + " serve", binary: binary},
		{name: "init", currentPID: 100, pid: 1, parentPID: 0, command: binary + " serve", binary: binary},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isOrphanedMagpieServe(test.currentPID, test.pid, test.parentPID, test.command, test.binary)
			if got != test.want {
				t.Fatalf("orphan = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIsOurMagpieBinary(t *testing.T) {
	for path, want := range map[string]bool{
		"/Applications/Isoform.app/Contents/Resources/magpie-bundle/magpie-cli": true,
		"/repo/wails-gui/magpie-bundle/magpie-cli-darwin-arm64":                 true,
		"/Users/me/.local/bin/magpie":                                           false,
		"/Applications/magpie.app/Contents/MacOS/magpie":                        false,
		"/opt/homebrew/bin/magpie":                                              false,
		"":                                                                      false,
	} {
		if got := isOurMagpieBinary(path); got != want {
			t.Errorf("isOurMagpieBinary(%q) = %v, want %v", path, got, want)
		}
	}

	// A binary picked with YANSU_MAGPIE_BIN is not a shipped gateway.
	t.Setenv(magpieOverrideEnv, "/tmp/dev/magpie")
	if isOurMagpieBinary("/tmp/dev/magpie") {
		t.Error("the YANSU_MAGPIE_BIN override must not count as ours")
	}
}

func TestMagpieSidecarRecordsItsChildInItsOwnProcessGroup(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	s := testSidecar(t, bin)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := s.childProcessID()

	record, ok := s.readChildRecord()
	if !ok || record.PID != pid || record.Binary != bin || record.Port != s.Status().Port {
		t.Fatalf("child record = %+v (ok=%v), want pid %d binary %s port %d", record, ok, pid, bin, s.Status().Port)
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("child pgid = %d (err %v), want its own group %d", pgid, err, pid)
	}

	s.Stop()
	if _, err := os.Stat(s.childRecordPath()); !os.IsNotExist(err) {
		t.Fatalf("child record must be removed once the child is stopped, stat err = %v", err)
	}
	if !processGone(pid) {
		t.Fatalf("child %d still running after Stop", pid)
	}
}

func TestMagpieSidecarRestartReplacesChildRecord(t *testing.T) {
	s := testSidecar(t, buildBundledFakeMagpie(t))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := s.childProcessID()
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	waitFor(t, 10*time.Second, "restarted child recorded", func() bool {
		record, ok := s.readChildRecord()
		return ok && record.PID != first && record.PID == s.childProcessID() && s.Healthy()
	})
}

func TestMagpieSidecarStopsStaleChildFromRecord(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	s := testSidecar(t, bin)
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	orphan := startOrphanedMagpie(t, bin, port)

	// What the killed app left in its state directory.
	if err := s.ensureStateDirs(); err != nil {
		t.Fatalf("ensureStateDirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.stateDir, magpiePortFileName), []byte(`{"port":`+strconv.Itoa(port)+`}`), 0o600); err != nil {
		t.Fatalf("write magpie.json: %v", err)
	}
	if err := s.writeChildRecord(magpieChildRecord{PID: orphan, Binary: bin, Port: port}); err != nil {
		t.Fatalf("write child record: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !processGone(orphan) {
		t.Fatalf("stale gateway %d survived Start", orphan)
	}
	if got := s.Status().Port; got != port {
		t.Fatalf("port = %d, want the freed port %d", got, port)
	}
	child := s.childProcessID()
	if child == 0 || child == orphan {
		t.Fatalf("child pid = %d, want a new gateway", child)
	}
	if record, ok := s.readChildRecord(); !ok || record.PID != child {
		t.Fatalf("child record = %+v (ok=%v), want pid %d", record, ok, child)
	}
}

func TestMagpieSidecarStopsStaleChildWithoutRecord(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	s := testSidecar(t, bin)
	first, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	second, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	// Older builds recorded nothing, and several can pile up.
	orphans := []int{startOrphanedMagpie(t, bin, first), startOrphanedMagpie(t, bin, second)}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, orphan := range orphans {
		if !processGone(orphan) {
			t.Fatalf("stale gateway %d survived Start", orphan)
		}
	}
	if pids := pgrepMagpieServe(bin); len(pids) != 1 || pids[0] != s.childProcessID() {
		t.Fatalf("running gateways = %v, want only the new child %d", pids, s.childProcessID())
	}
}

func TestMagpieSidecarNeverStopsAUserInstalledMagpie(t *testing.T) {
	bundled := buildBundledFakeMagpie(t)
	userBin := buildFakeMagpie(t) // outside any magpie-bundle directory

	t.Run("sidecar runs the bundled copy", func(t *testing.T) {
		s := testSidecar(t, bundled)
		port, err := freeLoopbackPort()
		if err != nil {
			t.Fatalf("freeLoopbackPort: %v", err)
		}
		user := startOrphanedMagpie(t, userBin, port)
		// Even a record that names the user's gateway must not get it stopped.
		if err := s.ensureStateDirs(); err != nil {
			t.Fatalf("ensureStateDirs: %v", err)
		}
		if err := s.writeChildRecord(magpieChildRecord{PID: user, Binary: userBin, Port: port}); err != nil {
			t.Fatalf("write child record: %v", err)
		}

		if err := s.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if processGone(user) {
			t.Fatal("a magpie the user installed was stopped")
		}
	})

	t.Run("sidecar runs a binary outside the bundle", func(t *testing.T) {
		s := testSidecar(t, userBin)
		port, err := freeLoopbackPort()
		if err != nil {
			t.Fatalf("freeLoopbackPort: %v", err)
		}
		user := startOrphanedMagpie(t, userBin, port)

		if err := s.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if processGone(user) {
			t.Fatal("a detached gateway at a non-bundle path was stopped by the scan")
		}
	})
}

func TestMagpieSidecarKeepsAGatewayThatStillHasItsParent(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	s := testSidecar(t, bin)
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	live := exec.Command(bin, "serve")
	live.Env = append(os.Environ(), "MAGPIE_ADDR="+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err := live.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() {
		_ = live.Process.Kill()
		_ = live.Wait()
	})
	if err := s.ensureStateDirs(); err != nil {
		t.Fatalf("ensureStateDirs: %v", err)
	}
	if err := s.writeChildRecord(magpieChildRecord{PID: live.Process.Pid, Binary: bin, Port: port}); err != nil {
		t.Fatalf("write child record: %v", err)
	}

	waitForGateway(t, port)

	s.reapStaleChild(bin)
	if !gatewayAnswers(port) {
		t.Fatal("gateway with a live parent was stopped")
	}
}

func TestMagpieSidecarDropsRecordOfAGatewayThatIsGone(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	s := testSidecar(t, bin)
	if err := s.ensureStateDirs(); err != nil {
		t.Fatalf("ensureStateDirs: %v", err)
	}
	// A pid nothing runs under, as after a reboot.
	if err := s.writeChildRecord(magpieChildRecord{PID: 999999, Binary: bin, Port: 1}); err != nil {
		t.Fatalf("write child record: %v", err)
	}
	s.reapStaleChild(bin)
	if _, ok := s.readChildRecord(); ok {
		t.Fatal("record of a gateway that no longer runs must be dropped")
	}
}

func TestMagpieSidecarKillNowStopsTheChildWithoutWaiting(t *testing.T) {
	s := testSidecar(t, buildBundledFakeMagpie(t))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := s.childProcessID()

	s.KillNow()
	waitFor(t, 5*time.Second, "child to die", func() bool { return processGone(pid) })
}

func TestMagpieSidecarStopsBundledLeftoversWhileUsingTheUsersMagpie(t *testing.T) {
	bundled := buildBundledFakeMagpie(t)
	userBin := buildFakeMagpie(t) // outside any magpie-bundle directory

	s := testSidecar(t, userBin)
	s.external = true
	s.bundled = func() []string { return []string{bundled} }
	if err := s.ensureStateDirs(); err != nil {
		t.Fatalf("ensureStateDirs: %v", err)
	}

	recordedPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	scannedPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	userPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	recorded := startOrphanedMagpie(t, bundled, recordedPort)
	scanned := startOrphanedMagpie(t, bundled, scannedPort)
	user := startOrphanedMagpie(t, userBin, userPort)
	if err := s.writeChildRecord(magpieChildRecord{PID: recorded, Binary: bundled, Port: recordedPort}); err != nil {
		t.Fatalf("write child record: %v", err)
	}

	s.reapStaleChild(userBin)

	for name, pid := range map[string]int{"recorded": recorded, "scanned": scanned} {
		if !processGone(pid) {
			t.Errorf("%s bundled leftover %d survived while the user's magpie is in use", name, pid)
		}
	}
	if processGone(user) {
		t.Error("the user's own magpie was stopped")
	}
}

func TestMagpieSidecarForceKillsStaleChildrenThatIgnoreTermOnOneDeadline(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	s := testSidecar(t, bin)
	s.stopGrace = 1500 * time.Millisecond

	var orphans []int
	for range 3 {
		port, err := freeLoopbackPort()
		if err != nil {
			t.Fatalf("freeLoopbackPort: %v", err)
		}
		orphans = append(orphans, startOrphanedMagpie(t, bin, port, "FAKE_MAGPIE_IGNORE_TERM=1"))
	}

	started := time.Now()
	s.reapStaleChild(bin)
	elapsed := time.Since(started)

	for _, orphan := range orphans {
		if !processGone(orphan) {
			t.Fatalf("stale gateway %d that ignores SIGTERM survived", orphan)
		}
	}
	// Three gateways asked one after another would take three grace periods.
	if elapsed >= 3*s.stopGrace {
		t.Fatalf("reaping took %s, want about one grace period (%s)", elapsed, s.stopGrace)
	}
}

func TestStopStaleMagpiesSparesAPidThatIsNoLongerAStaleGateway(t *testing.T) {
	bin := buildBundledFakeMagpie(t)
	// A live gateway with a live parent: what a recycled pid looks like.
	cmd := exec.Command(bin, "serve")
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("freeLoopbackPort: %v", err)
	}
	cmd.Env = append(os.Environ(), "MAGPIE_ADDR="+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), "FAKE_MAGPIE_IGNORE_TERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Listening means the fake has already set up its SIGTERM handling.
	waitForGateway(t, port)

	if isStaleMagpie(staleMagpie{pid: cmd.Process.Pid, binary: bin}) {
		t.Fatal("a gateway that still has its parent must not count as stale")
	}
	// Even handed over as if it had been judged stale and asked to exit, it
	// is checked again before the SIGKILL and survives.
	stopStaleMagpies([]staleMagpie{{pid: cmd.Process.Pid, binary: bin}}, 200*time.Millisecond)
	if !gatewayAnswers(port) {
		t.Fatal("gateway with a live parent was killed")
	}
}
