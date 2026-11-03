//go:build !linux && !darwin

package toolserver

import (
	"errors"
	"net"
)

func verifyPeer(_ *net.UnixConn, _ int) error {
	return errors.New("assistant tool server requires Unix peer credentials")
}
