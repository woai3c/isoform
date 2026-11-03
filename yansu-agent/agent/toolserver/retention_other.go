//go:build !linux && !darwin

package toolserver

import (
	"errors"
	"time"
)

type runLease struct{}
type ledgerRetention struct{}

var defaultLedgerRetention ledgerRetention

func lockRunDirectory(string) (*runLease, error) {
	return nil, errors.New("assistant tool server requires Unix file leases")
}
func (*runLease) close() error                                     { return nil }
func pruneRunDirectories(string, time.Time, ledgerRetention) error { return nil }
func discardRunDirectory(string)                                   {}
