//go:build !darwin

package recentfiles

import "errors"

// Supported reports whether this platform has Spotlight. Only macOS does.
func Supported() bool { return false }

// Query is unavailable outside macOS.
func Query(text, scope string, limit int) ([]Item, error) {
	return nil, errors.New("recent files are read only on macOS")
}
