package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"time"
)

const (
	maxCrashLogBytes = 256 * 1024
	crashNoticeBytes = 128
	crashMonitorArg  = "--isoform-crash-monitor"
)

// runCrashMonitorCommand runs before application startup. Only the inherited
// pipe and output destination are used; no stores, windows or user files open.
func runCrashMonitorCommand(args []string) (int, bool) {
	if len(args) != 2 || args[0] != crashMonitorArg {
		return 0, false
	}
	err := copyBoundedCrash(os.Stdout, os.Stdin)
	syncErr := os.Stdout.Sync()
	closeErr := os.Stdout.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return 1, true
	}
	// Publish only complete, flushed reports. A pending file remains available
	// for diagnosis if the collector itself is interrupted or encounters I/O loss.
	if err := os.Rename(args[1]+".pending", args[1]); err != nil {
		return 1, true
	}
	return 0, true
}

// copyBoundedCrash keeps the head, drains the rest without retaining it, and
// reserves room for an exact omitted-byte count. Small reports stay unchanged,
// including reports that fill the entire limit. Memory is independent of input
// size. Draining after the limit prevents the fatal runtime blocking on a pipe.
func copyBoundedCrash(dst io.Writer, src io.Reader) error {
	_, err := io.CopyN(dst, src, maxCrashLogBytes-crashNoticeBytes)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	tail, err := io.ReadAll(io.LimitReader(src, crashNoticeBytes+1))
	if err != nil {
		return err
	}
	if len(tail) <= crashNoticeBytes {
		_, err = dst.Write(tail)
		return err
	}
	omitted, err := io.Copy(io.Discard, src)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(dst, "\n[Isoform crash output truncated: %d bytes omitted; first %d bytes retained]\n",
		omitted+int64(len(tail)), maxCrashLogBytes-crashNoticeBytes)
	return err
}

// initCrashLog opens a separate, session-stamped runtime crash destination.
// It is never rotated by stderr setup on the next launch. Open it before a
// fault: allocating files or calling Go from a synchronous native signal
// handler is unsafe. A separate process bounds runtime output even when Go's
// scheduler is stopped by a fatal error (a collector goroutine could deadlock).
func initCrashLog() error {
	dir, err := flavorDataDir()
	if err != nil {
		return err
	}
	return configureCrashLog(filepath.Join(dir, "logs"))
}

func configureCrashLog(dir string) error {
	debug.SetTraceback("all")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	name := fmt.Sprintf("crash-%s-%d.txt", time.Now().UTC().Format("20060102T150405.000000000Z"), os.Getpid())
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		_ = f.Close()
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = f.Close()
		return err
	}
	cmd := exec.Command(exe, crashMonitorArg, path)
	// Do not inherit Go tuning or application configuration into the collector.
	cmd.Env = []string{}
	cmd.Stdin, cmd.Stdout = reader, f
	err = cmd.Start()
	_ = reader.Close()
	_ = f.Close()
	if err != nil {
		_ = writer.Close()
		return err
	}
	// Reap on normal operation. After a fatal exit the independent collector
	// drains through EOF, appends any truncation notice, syncs, and exits itself.
	go func() { _ = cmd.Wait() }()
	if err := debug.SetCrashOutput(writer, debug.CrashOptions{}); err != nil {
		_ = writer.Close()
		return err
	}
	// Unix retains this writer for its ordinary signal observer. The runtime
	// owns a separate close-on-exec duplicate for synchronous faults/panics.
	installCrashSignals(writer)
	return nil
}
