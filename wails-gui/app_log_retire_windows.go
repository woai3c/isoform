//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func cleanupRetiredRotatingLogFiles(path string) error {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	// A crash between retirement's rename and deletion can leave a file behind.
	// Only this log's retirement names belong to us; retain all other entries.
	prefix := filepath.Base(path) + ".old.2.retired-"
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func retireRotatingLogFile(path string) error {
	// Windows keeps a deleted file's name reserved until its last reader closes,
	// even with FILE_SHARE_DELETE. Move it out of the rotation namespace first
	// so a Settings snapshot cannot block reuse of .old.2.
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".retired-*")
	if err != nil {
		return err
	}
	retiredPath := temporary.Name()
	defer os.Remove(retiredPath)
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(path, retiredPath); err != nil {
		return err
	}
	// Existing snapshot handles keep their content and release the retired file
	// on close; the now-free rotation name can be used immediately.
	return os.Remove(retiredPath)
}
