//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// startFakeWorker starts a plain sleep in its own process group, the way
// Isoform's real Claude Code spawns are set up, and kills it when the test
// ends. The janitor tests stand in for Claude Code workers with these sleeps:
// they never start a real claude process and never read ~/.claude.
func startFakeWorker(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake worker: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	})
	return cmd
}

func workerAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func waitWorkerGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for workerAlive(pid) {
		if !time.Now().Before(deadline) {
			t.Fatalf("worker pid=%d still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func workerTracked(pid int) bool {
	_, ok := trackedIsoformClaudeWorkers()[pid]
	return ok
}

// The janitor reaps a stale worker Isoform started, and in the same pass
// leaves a process it did not start alone — even one that would have matched
// the old janitor's heuristics (it matched session ids and cmdlines from
// ~/.claude/sessions, so a user's own worker sharing an Isoform session id
// could be killed). The tracker is the only source now.
func TestClaudeCleanupReapsOnlyIsoformSpawnedStaleWorkers(t *testing.T) {
	own := startFakeWorker(t)
	stranger := startFakeWorker(t)

	trackIsoformClaudeWorker(own.Process.Pid, time.Now())
	t.Cleanup(func() { forgetIsoformClaudeWorker(own.Process.Pid) })

	// A zero max age makes every tracked worker stale-eligible.
	reapTrackedClaudeWorkers(time.Now(), 0)

	waitWorkerGone(t, own.Process.Pid)
	if workerTracked(own.Process.Pid) {
		t.Error("reaped worker is still tracked")
	}
	if !workerAlive(stranger.Process.Pid) {
		t.Error("janitor signaled a process Isoform did not start")
	}
}

// A worker Isoform started whose run is still within bounds must never be
// signaled: that is a live chat or research run.
func TestClaudeCleanupLeavesYoungIsoformWorker(t *testing.T) {
	cmd := startFakeWorker(t)
	trackIsoformClaudeWorker(cmd.Process.Pid, time.Now())
	t.Cleanup(func() { forgetIsoformClaudeWorker(cmd.Process.Pid) })

	reapTrackedClaudeWorkers(time.Now(), claudeWorkerMaxAge)

	if !workerAlive(cmd.Process.Pid) {
		t.Error("janitor signaled a worker whose run is still fresh")
	}
	if !workerTracked(cmd.Process.Pid) {
		t.Error("young worker should stay tracked")
	}
}

// A pid whose recorded start time does not match the process ps sees was
// recycled after the worker Isoform started exited: it belongs to someone
// else now and is never signaled, only dropped from the tracker.
func TestClaudeCleanupSparesRecycledPID(t *testing.T) {
	cmd := startFakeWorker(t)
	trackIsoformClaudeWorker(cmd.Process.Pid, time.Now().Add(-2*claudeWorkerMaxAge))
	t.Cleanup(func() { forgetIsoformClaudeWorker(cmd.Process.Pid) })

	reapTrackedClaudeWorkers(time.Now(), 0)

	if !workerAlive(cmd.Process.Pid) {
		t.Error("janitor signaled a pid whose start time does not match what Isoform recorded")
	}
	if workerTracked(cmd.Process.Pid) {
		t.Error("recycled pid should be dropped from the tracker")
	}
}

// A tracked worker that already exited is dropped without signaling anything.
func TestClaudeCleanupForgetsExitedWorker(t *testing.T) {
	cmd := startFakeWorker(t)
	pid := cmd.Process.Pid
	trackIsoformClaudeWorker(pid, time.Now())
	t.Cleanup(func() { forgetIsoformClaudeWorker(pid) })
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	waitWorkerGone(t, pid)

	reapTrackedClaudeWorkers(time.Now(), 0)

	if workerTracked(pid) {
		t.Error("exited worker should be dropped from the tracker")
	}
}
