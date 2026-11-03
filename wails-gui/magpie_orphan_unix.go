//go:build !windows

package main

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// setMagpieProcessGroup makes the gateway the leader of its own process
// group, so signalling it also reaches anything it spawned and a terminal
// Ctrl-C aimed at the app does not hit it behind the app's back.
func setMagpieProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalMagpieChild signals the child's process group. When that fails it
// falls back to the child alone, which os.Process refuses once the child has
// been waited on, so a recycled pid is never signalled.
func signalMagpieChild(cmd *exec.Cmd, sig syscall.Signal) error {
	if syscall.Kill(-cmd.Process.Pid, sig) == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}

// killMagpiePID force-kills the process group of a gateway this app started,
// without waiting. It is for exit paths that cannot wait, such as the
// shutdown watchdog. It never falls back to the bare pid, which may by now
// belong to another process; the gateway leads its own group.
func killMagpiePID(pid int) {
	if pid > 1 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// signalMagpiePID signals pid's process group first and falls back to pid
// alone: a gateway left by an older build shares the app's group, so there
// is no group of its own to signal. Only for processes that were just
// verified to be stale gateways.
func signalMagpiePID(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return syscall.EINVAL
	}
	if err := syscall.Kill(-pid, sig); err == nil {
		return nil
	}
	return syscall.Kill(pid, sig)
}

// isOrphanedMagpieServe reports whether the process is a detached `serve` of
// exactly our gateway binary: parent PID 1, the whole command line equal to
// "<binary> serve", never the current process, and never a magpie the user
// installed themselves.
func isOrphanedMagpieServe(currentPID, pid, parentPID int, cmdline, binary string) bool {
	if currentPID <= 0 || pid <= 1 || pid == currentPID || parentPID != 1 {
		return false
	}
	binary = strings.TrimSpace(binary)
	return isOurMagpieBinary(binary) && strings.TrimSpace(cmdline) == binary+" serve"
}

func liveMagpieServe(pid int, binary string) bool {
	cmdline, err := getProcessCmdline(pid)
	return err == nil && strings.TrimSpace(cmdline) == binary+" serve"
}

// staleMagpie is a detached gateway that an earlier run left behind.
type staleMagpie struct {
	pid    int
	binary string
}

// isStaleMagpie reads the process's command line and parent now, so it can be
// asked again right before a SIGKILL.
func isStaleMagpie(m staleMagpie) bool {
	cmdline, err := getProcessCmdline(m.pid)
	if err != nil {
		return false
	}
	parentPID, err := getProcessPPID(m.pid)
	if err != nil {
		return false
	}
	return isOrphanedMagpieServe(os.Getpid(), m.pid, parentPID, cmdline, m.binary)
}

func (s *magpieSidecar) bundledBinaries() []string {
	if s.bundled != nil {
		return s.bundled()
	}
	return magpieBundleCandidates()
}

// reapStaleChild stops gateways that an earlier run of the app started and
// never stopped, which happens when the app dies without running its
// shutdown (SIGKILL, a crash, os.Exit). Without this every such run adds a
// gateway with parent PID 1 that keeps its loopback port for good.
//
// Two sources find them: the pid recorded when the app launched one, and a
// scan for this app's bundled binary, which also catches gateways left by
// builds that recorded nothing. The scan covers the bundled locations and the
// binary this sidecar runs itself, never the user's own magpie: when the
// sidecar has adopted that one, bin is skipped, so leftovers of the bundled
// copy are still cleaned. Only detached processes whose command line is
// exactly "<bundled binary> serve" are touched, and a user-installed magpie
// is never one.
func (s *magpieSidecar) reapStaleChild(bin string) {
	binaries := s.bundledBinaries()
	if !s.external {
		binaries = append(binaries, bin)
	}

	candidates := map[int]string{}
	record, haveRecord := s.readChildRecord()
	if haveRecord {
		candidates[record.PID] = record.Binary
	}
	for _, binary := range binaries {
		for _, pid := range pgrepMagpieServe(binary) {
			if _, seen := candidates[pid]; !seen {
				candidates[pid] = binary
			}
		}
	}

	var stale []staleMagpie
	for pid, binary := range candidates {
		if m := (staleMagpie{pid: pid, binary: binary}); isStaleMagpie(m) {
			stale = append(stale, m)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].pid < stale[j].pid })
	stopStaleMagpies(stale, s.stopGrace)

	if haveRecord && !liveMagpieServe(record.PID, record.Binary) {
		s.clearChildRecord(record.PID)
	}
}

// stopStaleMagpies asks every stale gateway to exit, waits once for all of
// them, and force-kills the ones still running, so a pile of stuck gateways
// costs one grace period rather than one each. A process is checked again
// before the SIGKILL, so a pid that was recycled in the meantime is spared.
func stopStaleMagpies(stale []staleMagpie, grace time.Duration) {
	var asked []staleMagpie
	for _, m := range stale {
		if err := signalMagpiePID(m.pid, syscall.SIGTERM); err == nil {
			asked = append(asked, m)
		} else if !errors.Is(err, syscall.ESRCH) {
			log.Printf("[magpie] could not signal stale gateway pid=%d: %v", m.pid, err)
		}
	}

	var stuck []staleMagpie
	spared := map[int]bool{}
	for _, m := range waitMagpiesGone(asked, grace) {
		if isStaleMagpie(m) {
			_ = signalMagpiePID(m.pid, syscall.SIGKILL)
			stuck = append(stuck, m)
		} else {
			spared[m.pid] = true
		}
	}
	waitMagpiesGone(stuck, time.Second)

	for _, m := range stale {
		if spared[m.pid] {
			log.Printf("[magpie] pid=%d is no longer a stale gateway, left alone", m.pid)
		} else if magpieGone(m.pid) {
			log.Printf("[magpie] stopped stale gateway pid=%d left by an earlier run", m.pid)
		} else {
			log.Printf("[magpie] could not stop stale gateway pid=%d", m.pid)
		}
	}
}

func magpieGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// waitMagpiesGone polls until every process is gone or the timeout passes,
// and returns the ones still running.
func waitMagpiesGone(list []staleMagpie, timeout time.Duration) []staleMagpie {
	deadline := time.Now().Add(timeout)
	for {
		var running []staleMagpie
		for _, m := range list {
			if !magpieGone(m.pid) {
				running = append(running, m)
			}
		}
		if len(running) == 0 || !time.Now().Before(deadline) {
			return running
		}
		list = running
		time.Sleep(20 * time.Millisecond)
	}
}

// pgrepMagpieServe lists this user's processes whose command line mentions
// "<bin> serve". It returns nothing unless bin is our own gateway binary.
func pgrepMagpieServe(bin string) []int {
	if !isOurMagpieBinary(bin) {
		return nil
	}
	out, err := exec.Command("pgrep", "-u", strconv.Itoa(os.Getuid()), "-f", regexp.QuoteMeta(bin+" serve")).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
