//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

const maxCrashStackBytes = 128 * 1024

func writeCrashStacks(w io.Writer, stack func([]byte, bool) int) {
	// One lookahead byte distinguishes an exactly fitting stack from truncation.
	buf := make([]byte, maxCrashStackBytes+1)
	n := stack(buf, true)
	_, _ = w.Write(buf[:min(n, maxCrashStackBytes)])
	if n > maxCrashStackBytes {
		// runtime.Stack reports the copied length, not the full required size.
		// Do not invent an exact omitted count or allocate an unbounded buffer.
		_, _ = fmt.Fprintf(w, "\n[Isoform goroutine traceback truncated at %d bytes; at least 1 additional byte omitted]\n", maxCrashStackBytes)
	}
}

func installCrashSignals(f *os.File) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGSEGV, syscall.SIGABRT)
	go func() {
		sig := <-signals
		// signal.Notify receives user-generated signals only for SIGSEGV.
		// Synchronous memory faults stay with Go's native runtime handler,
		// which uses SetCrashOutput without needing this goroutine to run.
		name := "SIGSEGV"
		if sig == syscall.SIGABRT {
			name = "SIGABRT"
		}
		_, _ = fmt.Fprintf(f, "Isoform received %s\n", name)
		writeCrashStacks(f, runtime.Stack)
		// This descriptor is a pipe; the independent collector syncs its bounded
		// file after EOF, including the runtime's subsequent fatal traceback.
		// Restore Go's fatal handling, then deliver the original signal again.
		// Never recover or continue executing after a fatal signal.
		signal.Stop(signals)
		if err := syscall.Kill(os.Getpid(), sig.(syscall.Signal)); err != nil {
			os.Exit(2)
		}
	}()
}
