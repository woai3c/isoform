//go:build !darwin

package cronsecret

import (
	"os"
	"path/filepath"
)

func load(dir string, _ options) (string, error) {
	return readLegacy(dir)
}

func store(dir, secret string, _ options) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Replace atomically instead of following an existing credential symlink.
	file, err := os.CreateTemp(dir, ".cron-secret-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(secret); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "cron-secret"))
}
