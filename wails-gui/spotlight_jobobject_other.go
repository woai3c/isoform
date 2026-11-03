//go:build !windows

package main

import "os/exec"

// bindHelperToParentLifecycle is a no-op on non-Windows platforms.
// On macOS/Linux the parent's exit doesn't propagate to children
// automatically either, but Yansu's Stop() flow + exec.CommandContext
// cancellation already cover the normal exit paths. If we ever need
// hard-crash protection there, prctl(PR_SET_PDEATHSIG) on Linux and
// kqueue NOTE_EXIT on macOS would be the equivalents.
func bindHelperToParentLifecycle(_ *exec.Cmd) {}
