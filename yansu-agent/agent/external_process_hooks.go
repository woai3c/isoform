package agent

import (
	"sync"
	"time"
)

// ProcessHooks hear the lifecycle of every assistant CLI process the external
// runners start. The desktop app registers them so its janitor reaps only the
// Claude Code workers Isoform itself spawned, never a process from the user's
// own CLI use. With no hooks registered (the standalone CLI) nothing is
// reported.
type ProcessHooks struct {
	// Started fires right after a successful Start, with the pid and when the
	// process started; the start time lets the host guard against pid reuse.
	Started func(pid int, startedAt time.Time)
	// Exited fires when the run that started the process is done with it: the
	// process was waited on or is being torn down, so the host stops tracking
	// it. It can fire while the process is still finishing its exit.
	Exited func(pid int)
}

var processHooks struct {
	sync.Mutex
	hooks ProcessHooks
}

// SetProcessHooks registers the process lifecycle hooks; a zero ProcessHooks
// clears them.
func SetProcessHooks(hooks ProcessHooks) {
	processHooks.Lock()
	processHooks.hooks = hooks
	processHooks.Unlock()
}

func reportProcessStarted(pid int) {
	processHooks.Lock()
	started := processHooks.hooks.Started
	processHooks.Unlock()
	if started != nil && pid > 0 {
		started(pid, time.Now())
	}
}

func reportProcessExited(pid int) {
	processHooks.Lock()
	exited := processHooks.hooks.Exited
	processHooks.Unlock()
	if exited != nil && pid > 0 {
		exited(pid)
	}
}
