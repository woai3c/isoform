// Package singleinstance enforces "only one desktop app per user session"
// using two coordinated primitives:
//
//  1. An OS-level lock (Windows named mutex / Unix flock) that any second
//     launch can quickly probe. Auto-released on process exit, so a crashed
//     instance never permanently blocks subsequent launches.
//
//  2. A local IPC channel (Windows named pipe / Unix domain socket) so a
//     second launch can hand "please focus yourself" — and in the future,
//     arbitrary CLI args like deep links — to the running instance instead
//     of silently exiting.
//
// This is the same pattern Electron's app.requestSingleInstanceLock(),
// Chromium, and Slack/Discord/VS Code use.
package singleinstance

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"time"
)

// BaseDirName is the folder under the home directory that holds the lock and
// the IPC socket. The app sets it per build flavor before Acquire.
var BaseDirName = ".yansu-agent"

// ErrAlreadyRunning is returned by Acquire when another process already
// holds the lock for the given app identifier. The caller can use Send to
// forward a message (e.g. "focus") to the running instance before exiting.
var ErrAlreadyRunning = errors.New("another instance is already running")

// instanceLock is the platform-specific OS-level lock primitive.
// Implementations live in lock_windows.go and lock_unix.go.
type instanceLock interface {
	Release() error
}

// Instance represents an acquired single-instance handle. Hold it for the
// lifetime of the process and Release() on shutdown — the OS will also
// release the underlying primitives if the process dies abruptly.
type Instance struct {
	lock     instanceLock
	listener net.Listener
	msgs     chan string
}

// Release closes the IPC listener and releases the OS lock. Idempotent.
func (i *Instance) Release() error {
	if i == nil {
		return nil
	}
	if i.listener != nil {
		i.listener.Close()
		i.listener = nil
	}
	if i.lock != nil {
		err := i.lock.Release()
		i.lock = nil
		return err
	}
	return nil
}

// Messages returns a channel that yields each message sent by a second
// launch via Send. The channel is closed when Release is called. Messages
// received before the caller starts draining are buffered up to a small
// bound; further messages while the buffer is full are dropped (a single
// "focus" event is enough — there's no point queuing more).
func (i *Instance) Messages() <-chan string {
	if i == nil {
		return nil
	}
	return i.msgs
}

// Acquire takes the single-instance lock for appID and starts an IPC
// listener. If another instance already holds the lock, returns
// ErrAlreadyRunning; the caller should typically call
// Send(appID, "focus") immediately afterwards and then exit.
//
// On success the OS lock is held and the listener is accepting; second
// launches can never race past this point.
func Acquire(appID string) (*Instance, error) {
	lock, err := acquireLock(appID)
	if err != nil {
		return nil, err
	}

	listener, err := listenIPC(appID)
	if err != nil {
		lock.Release()
		return nil, fmt.Errorf("start IPC listener: %w", err)
	}

	inst := &Instance{
		lock:     lock,
		listener: listener,
		msgs:     make(chan string, 16),
	}
	go inst.acceptLoop(listener)
	return inst, nil
}

// Send delivers a single newline-terminated message to the running
// instance for appID. Errors when no instance is listening or the IPC
// channel can't be reached within a short timeout.
func Send(appID, msg string) error {
	conn, err := dialIPC(appID, 2*time.Second)
	if err != nil {
		return fmt.Errorf("dial IPC: %w", err)
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(msg + "\n")); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (i *Instance) acceptLoop(listener net.Listener) {
	defer close(i.msgs)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go i.handleConn(conn)
	}
}

func (i *Instance) handleConn(conn net.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	if !scanner.Scan() {
		return
	}
	select {
	case i.msgs <- scanner.Text():
	default:
		// Buffer full — drop. Multiple "focus" requests in quick
		// succession collapse into one anyway.
	}
}
