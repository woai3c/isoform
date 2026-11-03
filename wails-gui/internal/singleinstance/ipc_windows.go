//go:build windows

package singleinstance

import (
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// pipePath returns the named-pipe path for the given appID. Windows
// named pipes have their own namespace independent of the file system;
// default ACLs restrict access to the creator's user, which is what we
// want for per-user single-instance.
func pipePath(appID string) string {
	return `\\.\pipe\` + appID
}

func listenIPC(appID string) (net.Listener, error) {
	cfg := &winio.PipeConfig{
		InputBufferSize:  4096,
		OutputBufferSize: 4096,
	}
	l, err := winio.ListenPipe(pipePath(appID), cfg)
	if err != nil {
		return nil, fmt.Errorf("ListenPipe: %w", err)
	}
	return l, nil
}

func dialIPC(appID string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(pipePath(appID), &timeout)
}
