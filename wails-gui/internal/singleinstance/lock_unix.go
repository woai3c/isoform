//go:build !windows

package singleinstance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"wails-gui/internal/apphome"
)

type unixLock struct {
	f *os.File
}

func (l *unixLock) Release() error {
	if l.f == nil {
		return nil
	}
	// Best-effort unlock; closing the fd also drops the lock, so failures
	// here are not fatal.
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}

// acquireLock takes a Unix flock single-instance lock under ~/.yansu-agent/.
// Different Unix users have different home dirs and therefore different locks.
func acquireLock(appID string) (instanceLock, error) {
	dataDir, err := apphome.Dir(BaseDirName)
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	dir := dataDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	path := filepath.Join(dir, "."+appID+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("flock: %w", err)
	}

	return &unixLock{f: f}, nil
}
