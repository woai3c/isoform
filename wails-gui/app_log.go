package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// rotatingAppLog keeps complete log writes together and bounds disk use for
// long-running sessions. Keep .old for compatibility with support workflows.
type rotatingAppLog struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	maxSize int64
	closed  bool
}

func newRotatingAppLog(path string, maxSize int64) (*rotatingAppLog, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("invalid app log size: %d", maxSize)
	}
	if err := cleanupRetiredRotatingLogFiles(path); err != nil {
		return nil, err
	}
	w := &rotatingAppLog{path: path, maxSize: maxSize}
	if err := w.open(); err != nil {
		return nil, err
	}
	if w.size >= w.maxSize {
		if err := w.rotate(); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	return w, nil
}

func (w *rotatingAppLog) Name() string { return w.path }

func (w *rotatingAppLog) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file, w.size = f, info.Size()
	return nil
}

func (w *rotatingAppLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if int64(len(p)) > w.maxSize {
		return 0, fmt.Errorf("app log entry exceeds %d bytes", w.maxSize)
	}
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingAppLog) rotate() error {
	if err := w.file.Close(); err != nil {
		w.file = nil
		return err
	}
	w.file = nil
	if err := retireRotatingLogFile(w.path + ".old.2"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(w.path+".old", w.path+".old.2"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(w.path, w.path+".old"); err != nil {
		return err
	}
	return w.open()
}

func (w *rotatingAppLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
