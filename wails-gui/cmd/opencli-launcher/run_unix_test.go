//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestLauncherExecHelper(t *testing.T) {
	if os.Getenv("ISOFORM_OPENCLI_EXEC_HELPER") != "1" {
		return
	}
	cmd := exec.Command("/bin/sh", "-c", "printf '%s' \"$$\"; exit 7")
	cmd.Env = os.Environ()
	if err := run(cmd); err != nil {
		t.Fatal(err)
	}
}

func TestLauncherReplacesProcessAndPreservesExitCode(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestLauncherExecHelper$")
	cmd.Env = append(os.Environ(), "ISOFORM_OPENCLI_EXEC_HELPER=1")
	output, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v output=%s", err, output)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid != cmd.Process.Pid {
		t.Fatalf("Bun replacement PID=%s launcher PID=%d", output, cmd.Process.Pid)
	}
}
