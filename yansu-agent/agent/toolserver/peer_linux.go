//go:build linux

package toolserver

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func verifyPeer(conn *net.UnixConn, assistantPID int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var peer *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) { peer, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	if peer == nil || peer.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("tool shim has a different user")
	}
	return verifyProcessGroup(int(peer.Pid), assistantPID)
}

func verifyProcessGroup(peerPID, assistantPID int) error {
	assistantGroup, err := unix.Getpgid(assistantPID)
	if err != nil {
		return err
	}
	peerGroup, err := unix.Getpgid(peerPID)
	if err != nil {
		return err
	}
	if peerGroup != assistantGroup {
		return fmt.Errorf("tool shim is outside the assistant process group")
	}
	return nil
}
