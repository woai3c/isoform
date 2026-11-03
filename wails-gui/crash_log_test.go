//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCrashLogPersistsFatalDiagnostics(t *testing.T) {
	// A tiny cgo executable exercises real synchronous faults, without an
	// application bundle, native windows, permission prompts or user stores.
	dir := t.TempDir()
	for _, name := range []string{"crash_log.go", "crash_signals_unix.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(fixture, []byte(crashLogFixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bin := filepath.Join(dir, "crash-fixture")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, filepath.Join(dir, "crash_log.go"),
		filepath.Join(dir, "crash_signals_unix.go"), fixture)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, out)
	}
	for _, tc := range []struct{ mode, want string }{
		{"native-segv", "signal arrived during cgo execution"},
		{"native-abrt", "SIGABRT"},
		{"signal-segv", "SIGSEGV"},
		{"signal-abrt", "SIGABRT"},
		{"panic", "panic: crash fixture panic"},
		{"oversized-panic", "panic: oversized crash fixture"},
		{"nil-panic", "invalid memory address or nil pointer dereference"},
		// Some Go versions emit the fatal reason before enabling their
		// additional crash fd; the full traceback must still be preserved.
		{"fatal", "sync.(*Mutex).Unlock"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			runCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			cmd := exec.CommandContext(runCtx, bin, root, tc.mode)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "GOTRACEBACK=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "GOTRACEBACK=single")
			if tc.mode == "oversized-panic" {
				// Darwin's one-shot runtime writes may be interrupted by the
				// asynchronous preemption signal partway through a huge string.
				// Disable that interruption in this stress fixture so all 12 MiB
				// reaches the collector; ordinary/native cases use normal tuning.
				cmd.Env = append(cmd.Env, "GODEBUG=asyncpreemptoff=1")
			}
			stderr, err := os.CreateTemp(root, "runtime-stderr-*")
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout, cmd.Stderr = stderr, stderr
			err = cmd.Run()
			_ = stderr.Close()
			out, readErr := os.ReadFile(stderr.Name())
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err == nil || runCtx.Err() != nil {
				t.Fatalf("fixture must terminate on the fault: %v, %v\n%s", err, runCtx.Err(), out)
			}
			files := waitCrashFiles(t, root, 1)
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			wants := []string{tc.want, "main.parkedCrashWorker", "goroutine "}
			if tc.mode == "oversized-panic" {
				// An oversized panic message consumes the head. The collector
				// must drain the real fatal runtime without deadlocking, cap the
				// file, and report precisely how many bytes it discarded.
				wants = []string{tc.want, fmt.Sprintf("%d bytes omitted", len(out)-(maxCrashLogBytes-crashNoticeBytes))}
				if len(out) < 12*1024*1024 || len(data) < maxCrashLogBytes-crashNoticeBytes || len(data) > maxCrashLogBytes {
					t.Fatalf("oversized fatal: stderr=%d bytes, crash=%d bytes", len(out), len(data))
				}
				if !bytes.Equal(data[:maxCrashLogBytes-crashNoticeBytes], out[:maxCrashLogBytes-crashNoticeBytes]) {
					t.Fatal("oversized fatal lost the head of its traceback")
				}
				t.Logf("oversized fatal: %d bytes received, %d bytes persisted, %d bytes omitted", len(out), len(data), len(out)-(maxCrashLogBytes-crashNoticeBytes))
			} else if tc.mode == "panic" && !bytes.Equal(data, out) {
				t.Fatalf("ordinary fatal traceback changed: file=%d bytes, stderr=%d bytes", len(data), len(out))
			}
			for _, want := range wants {
				if !bytes.Contains(data, []byte(want)) {
					t.Fatalf("crash log missing %q:\n%s", want, data)
				}
			}
			info, err := os.Stat(files[0])
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("crash log permissions: %v, %v", info, err)
			}
			info, err = os.Stat(filepath.Dir(files[0]))
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("crash directory permissions: %v, %v", info, err)
			}
			// Simulate the next launch: it opens a new destination without
			// truncating or rotating the fatal session's diagnostic file.
			if out, err := exec.CommandContext(ctx, bin, root, "clean").CombinedOutput(); err != nil {
				t.Fatalf("relaunch fixture: %v\n%s", err, out)
			}
			waitCrashFiles(t, root, 2)
			after, err := os.ReadFile(files[0])
			if err != nil || !bytes.Equal(data, after) {
				t.Fatalf("relaunch changed crash evidence: %v", err)
			}
		})
	}
	t.Run("unwritable-directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "logs"), []byte("blocked"), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.CommandContext(ctx, bin, root, "clean").CombinedOutput()
		if err == nil || !bytes.Contains(out, []byte("crash setup failed")) {
			t.Fatalf("setup failure must remain diagnosable on stderr: %v\n%s", err, out)
		}
	})
}

// Atomic publication is the completion signal, not a sleep or file-size guess.
func waitCrashFiles(t *testing.T, root string, count int) []string {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		files, err := filepath.Glob(filepath.Join(root, "logs", "crash-*.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == count {
			return files
		}
		select {
		case <-timer.C:
			t.Fatalf("collector did not publish %d complete crash files; found %v", count, files)
		case <-ticker.C:
		}
	}
}

func TestCrashOutputBound(t *testing.T) {
	for _, size := range []int{0, 4096, maxCrashLogBytes - 1, maxCrashLogBytes, maxCrashLogBytes + 1, 12 * 1024 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), size)
			copy(payload, "panic: fixture\n\ngoroutine 1 [running]:\nmain.main()\n")
			var dst bytes.Buffer
			if err := copyBoundedCrash(&dst, bytes.NewReader(payload)); err != nil {
				t.Fatal(err)
			}
			if size <= maxCrashLogBytes {
				if !bytes.Equal(dst.Bytes(), payload) {
					t.Fatal("ordinary traceback must survive byte for byte, including the exact size limit")
				}
				return
			}
			head := maxCrashLogBytes - crashNoticeBytes
			want := append(append([]byte{}, payload[:head]...), []byte(fmt.Sprintf(
				"\n[Isoform crash output truncated: %d bytes omitted; first %d bytes retained]\n", size-head, head))...)
			if dst.Len() > maxCrashLogBytes || !bytes.Equal(dst.Bytes(), want) {
				t.Fatalf("bounded traceback: got %d bytes, want %d with preserved head and exact omitted count", dst.Len(), len(want))
			}
		})
	}
}

func TestCrashSignalStackBound(t *testing.T) {
	for _, size := range []int{4096, maxCrashStackBytes, maxCrashStackBytes + 1, 12 * 1024 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload := bytes.Repeat([]byte("s"), size)
			var dst bytes.Buffer
			calls := 0
			writeCrashStacks(&dst, func(buf []byte, all bool) int {
				calls++
				if len(buf) != maxCrashStackBytes+1 || !all || calls != 1 {
					t.Fatalf("stack capture must use one capped all-goroutine buffer: len=%d all=%v calls=%d", len(buf), all, calls)
				}
				return copy(buf, payload)
			})
			if size <= maxCrashStackBytes {
				if !bytes.Equal(dst.Bytes(), payload) {
					t.Fatal("ordinary signal stack changed")
				}
				return
			}
			want := append(append([]byte{}, payload[:maxCrashStackBytes]...), []byte(fmt.Sprintf(
				"\n[Isoform goroutine traceback truncated at %d bytes; at least 1 additional byte omitted]\n", maxCrashStackBytes))...)
			if !bytes.Equal(dst.Bytes(), want) {
				t.Fatalf("stack capture must retain only %d bytes and its quantified truncation notice; got %d bytes", maxCrashStackBytes, dst.Len())
			}
		})
	}
}

const crashLogFixture = `package main
/*
#include <stdlib.h>
static void native_segv(void) { volatile int *p = 0; *p = 1; }
*/
import "C"
import (
    "fmt"
    "os"
    "strings"
    "sync"
    "syscall"
)
func flavorDataDir() (string, error) { return os.Args[1], nil }
//go:noinline
func parkedCrashWorker(ready chan struct{}, wait chan struct{}) {
    close(ready)
    <-wait
}
func main() {
    if code, handled := runCrashMonitorCommand(os.Args[1:]); handled { os.Exit(code) }
    if err := initCrashLog(); err != nil {
        fmt.Fprintln(os.Stderr, "crash setup failed:", err)
        os.Exit(1)
    }
    ready, wait := make(chan struct{}), make(chan struct{})
    go parkedCrashWorker(ready, wait)
    <-ready
    switch os.Args[2] {
    case "clean": return
    case "native-segv": C.native_segv()
    case "native-abrt": C.abort()
    case "signal-segv": syscall.Kill(os.Getpid(), syscall.SIGSEGV); select {}
    case "signal-abrt": syscall.Kill(os.Getpid(), syscall.SIGABRT); select {}
    case "panic": panic("crash fixture panic")
    case "oversized-panic": panic("oversized crash fixture" + strings.Repeat("x", 12*1024*1024))
    case "nil-panic": var p *int; *p = 1
    case "fatal": var mu sync.Mutex; mu.Unlock()
    }
}
`
