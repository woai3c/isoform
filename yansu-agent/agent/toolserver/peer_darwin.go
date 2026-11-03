//go:build darwin

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
	var peerPID int
	var credential *unix.Xucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		peerPID, controlErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if controlErr == nil {
			credential, controlErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		}
	}); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	if credential == nil || credential.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("tool shim has a different user")
	}
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
